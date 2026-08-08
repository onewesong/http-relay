// Package script runs user-supplied JavaScript hooks that rewrite relayed
// HTTP requests and responses.
//
// A script may export two optional hook functions:
//
//	function onRequest(req)        // mutate req in place, or return a response to short-circuit
//	function onResponse(resp, req) // mutate resp in place
//
// The binding model is intentionally simple (mitmproxy/whistle style):
//   - req.method / req.url / req.host are strings.
//   - req.headers / resp.headers are plain objects keyed by canonical header
//     name; assigning a string sets the header, `delete h[k]` removes it, and
//     assigning "" keeps the header present with an empty value.
//   - req.body / resp.body are strings.
//
// onRequest may `return { status, headers, body }` to short-circuit: the relay
// skips the upstream call but still runs onResponse on the synthesized response.
//
// An Engine is safe for concurrent use and supports hot-reload: Reload swaps in
// a freshly compiled script atomically, and pooled runtimes tagged with an
// older generation are rebuilt on next use. A failed Reload keeps the previous
// version serving traffic.
package script

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// DefaultTimeout bounds a single hook invocation when Options.Timeout is zero.
const DefaultTimeout = 200 * time.Millisecond

// Options configures a script Engine.
type Options struct {
	// Path is the script file to compile, or a diagnostic label when Source is
	// set. Path and Source both empty disables scripting.
	Path string
	// Source is an optional in-memory script. When set, Path is used only as a
	// diagnostic label and the script cannot be watched for file changes.
	Source string
	// Timeout bounds a single hook invocation. Zero uses DefaultTimeout.
	Timeout time.Duration
	// Console receives output from console.log/info/warn/error/debug calls in
	// the script. Nil discards it.
	Console io.Writer
}

// Request is the mutable view of an inbound request handed to onRequest.
type Request struct {
	Method         string
	URL            string
	Host           string
	Header         http.Header
	Body           []byte
	Namespace      string
	RewriteProfile string
	OriginalPath   string
}

// Response is the mutable view of a response handed to onResponse, and the
// shape onRequest returns to short-circuit the upstream call.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// scriptVersion is an immutable compiled snapshot of a script. A new version is
// produced on every successful compile and published atomically.
type scriptVersion struct {
	program *goja.Program
	hasReq  bool
	hasResp bool
	gen     uint64
}

// pooledRuntime is a goja.Runtime tagged with the script generation it was
// initialized against, so stale runtimes can be rebuilt after a reload.
type pooledRuntime struct {
	rt  *goja.Runtime
	gen uint64
}

// Engine is a compiled script ready to run hooks.
type Engine struct {
	path    string
	source  string
	timeout time.Duration
	console io.Writer
	current atomic.Pointer[scriptVersion]
	nextGen atomic.Uint64
	pool    sync.Pool
}

// New compiles the script at opts.Path. If opts.Path is empty it returns
// (nil, nil) so callers can treat "no script" as a disabled feature. A missing
// file, a syntax error, or a non-function hook export is a fatal error.
func New(opts Options) (*Engine, error) {
	if opts.Path == "" && opts.Source == "" {
		return nil, nil
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	console := opts.Console
	if console == nil {
		console = io.Discard
	}

	if opts.Path == "" {
		opts.Path = "in-memory script"
	}
	e := &Engine{path: opts.Path, source: opts.Source, timeout: timeout, console: console}
	e.pool.New = func() any { return &pooledRuntime{} }

	ver, err := e.compile()
	if err != nil {
		return nil, err
	}
	e.current.Store(ver)
	return e, nil
}

// compile reads and compiles the script at e.path into a new version, assigning
// it the next generation number. It validates that hook exports are callable.
func (e *Engine) compile() (*scriptVersion, error) {
	src := []byte(e.source)
	if e.source == "" {
		var err error
		src, err = os.ReadFile(e.path)
		if err != nil {
			return nil, fmt.Errorf("read script %q: %w", e.path, err)
		}
	}

	program, err := goja.Compile(e.path, string(src), true)
	if err != nil {
		return nil, fmt.Errorf("compile script %q: %w", e.path, err)
	}

	// Run once in a probe runtime to surface init-time errors and to validate
	// that any exported hooks are actually functions.
	probe := goja.New()
	installConsole(probe, e.console)
	if _, err := probe.RunProgram(program); err != nil {
		return nil, fmt.Errorf("init script %q: %w", e.path, err)
	}
	hasReq, err := validateHook(probe, "onRequest")
	if err != nil {
		return nil, err
	}
	hasResp, err := validateHook(probe, "onResponse")
	if err != nil {
		return nil, err
	}

	return &scriptVersion{
		program: program,
		hasReq:  hasReq,
		hasResp: hasResp,
		gen:     e.nextGen.Add(1),
	}, nil
}

// Reload recompiles the script from disk and publishes it atomically. On a
// compile/validation error the previous version is retained and the error is
// returned, so in-flight traffic is never disrupted by a bad edit.
func (e *Engine) Reload() error {
	ver, err := e.compile()
	if err != nil {
		return err
	}
	e.current.Store(ver)
	return nil
}

// validateHook reports whether the named global is defined, returning an error
// if it is defined but not callable.
func validateHook(rt *goja.Runtime, name string) (bool, error) {
	v := rt.Get(name)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return false, nil
	}
	if _, ok := goja.AssertFunction(v); !ok {
		return false, fmt.Errorf("script export %q must be a function", name)
	}
	return true, nil
}

// HasRequestHook reports whether the current script defines onRequest.
func (e *Engine) HasRequestHook() bool { return e.current.Load().hasReq }

// HasResponseHook reports whether the current script defines onResponse.
func (e *Engine) HasResponseHook() bool { return e.current.Load().hasResp }

// OnRequest runs onRequest against req, mutating it in place. If the script
// returns a response object it is returned here (non-nil) to short-circuit the
// upstream call. A thrown error or timeout is returned as a non-nil error.
func (e *Engine) OnRequest(req *Request) (*Response, error) {
	if e == nil {
		return nil, nil
	}
	ver := e.current.Load()
	if !ver.hasReq {
		return nil, nil
	}

	pr := e.get(ver)
	defer e.put(pr)

	fn, ok := goja.AssertFunction(pr.rt.Get("onRequest"))
	if !ok {
		return nil, fmt.Errorf("onRequest is not callable")
	}

	obj := newRequestObject(pr.rt, req)
	ret, err := e.call(pr.rt, fn, obj)
	if err != nil {
		return nil, fmt.Errorf("onRequest: %w", err)
	}

	readRequestObject(pr.rt, obj, req)

	if ret != nil && !goja.IsUndefined(ret) && !goja.IsNull(ret) {
		if o, ok := ret.(*goja.Object); ok {
			return toResponse(pr.rt, o), nil
		}
	}
	return nil, nil
}

// OnResponse runs onResponse against resp, mutating it in place, with req as
// read-only context. A thrown error or timeout is returned as a non-nil error.
func (e *Engine) OnResponse(resp *Response, req *Request) error {
	if e == nil {
		return nil
	}
	ver := e.current.Load()
	if !ver.hasResp {
		return nil
	}

	pr := e.get(ver)
	defer e.put(pr)

	fn, ok := goja.AssertFunction(pr.rt.Get("onResponse"))
	if !ok {
		return fmt.Errorf("onResponse is not callable")
	}

	respObj := newResponseObject(pr.rt, resp)
	reqObj := newRequestObject(pr.rt, req)
	if _, err := e.call(pr.rt, fn, respObj, reqObj); err != nil {
		return fmt.Errorf("onResponse: %w", err)
	}

	readResponseObject(pr.rt, respObj, resp)
	return nil
}

// call invokes fn under the engine's timeout, interrupting a runaway script.
func (e *Engine) call(rt *goja.Runtime, fn goja.Callable, args ...goja.Value) (goja.Value, error) {
	timer := time.AfterFunc(e.timeout, func() {
		rt.Interrupt(fmt.Sprintf("script exceeded %s timeout", e.timeout))
	})
	defer timer.Stop()
	defer rt.ClearInterrupt()

	return fn(goja.Undefined(), args...)
}

// get draws a runtime from the pool, (re)initializing it against ver when it is
// freshly created or was tagged with an older generation.
func (e *Engine) get(ver *scriptVersion) *pooledRuntime {
	pr := e.pool.Get().(*pooledRuntime)
	if pr.rt == nil || pr.gen != ver.gen {
		pr.rt = goja.New()
		installConsole(pr.rt, e.console)
		// ver.program compiled and ran cleanly in compile(); a re-run cannot fail.
		_, _ = pr.rt.RunProgram(ver.program)
		pr.gen = ver.gen
	}
	return pr
}

func (e *Engine) put(pr *pooledRuntime) {
	if pr.rt != nil {
		pr.rt.ClearInterrupt()
	}
	e.pool.Put(pr)
}
