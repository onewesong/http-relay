package script

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dop251/goja"
)

// installConsole exposes a minimal console object (log/info/warn/error/debug)
// that writes space-joined arguments to out. Without it, a script calling
// console.log would throw a ReferenceError and fail the hook.
func installConsole(rt *goja.Runtime, out io.Writer) {
	console := rt.NewObject()
	write := func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for i, arg := range call.Arguments {
			parts[i] = arg.String()
		}
		fmt.Fprintln(out, strings.Join(parts, " "))
		return goja.Undefined()
	}
	for _, name := range []string{"log", "info", "warn", "error", "debug"} {
		_ = console.Set(name, write)
	}
	_ = rt.Set("console", console)
}

// newRequestObject builds the JS object handed to onRequest from a Go Request.
func newRequestObject(rt *goja.Runtime, req *Request) *goja.Object {
	obj := rt.NewObject()
	_ = obj.Set("method", req.Method)
	_ = obj.Set("url", req.URL)
	_ = obj.Set("host", req.Host)
	_ = obj.Set("headers", headersToJS(rt, req.Header))
	_ = obj.Set("body", string(req.Body))
	return obj
}

// readRequestObject copies mutations from the JS request object back into req.
func readRequestObject(rt *goja.Runtime, obj *goja.Object, req *Request) {
	req.Method = stringField(obj, "method")
	req.URL = stringField(obj, "url")
	req.Host = stringField(obj, "host")
	req.Body = []byte(stringField(obj, "body"))
	req.Header = jsToHeaders(rt, obj.Get("headers"))
}

// newResponseObject builds the JS object handed to onResponse from a Response.
func newResponseObject(rt *goja.Runtime, resp *Response) *goja.Object {
	obj := rt.NewObject()
	_ = obj.Set("status", resp.Status)
	_ = obj.Set("headers", headersToJS(rt, resp.Header))
	_ = obj.Set("body", string(resp.Body))
	return obj
}

// readResponseObject copies mutations from the JS response object back into resp.
func readResponseObject(rt *goja.Runtime, obj *goja.Object, resp *Response) {
	if s := obj.Get("status"); s != nil && !goja.IsUndefined(s) && !goja.IsNull(s) {
		resp.Status = int(s.ToInteger())
	}
	resp.Body = []byte(stringField(obj, "body"))
	resp.Header = jsToHeaders(rt, obj.Get("headers"))
}

// toResponse converts a value returned by onRequest into a short-circuit
// Response. Missing fields fall back to sensible defaults (status 200, empty
// headers/body).
func toResponse(rt *goja.Runtime, obj *goja.Object) *Response {
	resp := &Response{Status: http.StatusOK, Header: http.Header{}}
	readResponseObject(rt, obj, resp)
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	return resp
}

// headersToJS renders an http.Header as a plain JS object keyed by canonical
// name. Multi-value headers are joined with ", ".
func headersToJS(rt *goja.Runtime, h http.Header) *goja.Object {
	obj := rt.NewObject()
	for key, vals := range h {
		_ = obj.Set(key, strings.Join(vals, ", "))
	}
	return obj
}

// jsToHeaders reconstructs an http.Header from a JS headers object. Each own
// key becomes a single header value; deleted keys disappear, and an empty
// string keeps the header present with an empty value.
func jsToHeaders(rt *goja.Runtime, v goja.Value) http.Header {
	h := http.Header{}
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return h
	}
	obj := v.ToObject(rt)
	for _, key := range obj.Keys() {
		val := obj.Get(key)
		if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
			continue
		}
		h.Set(key, val.String())
	}
	return h
}

// stringField reads obj[name] as a string, treating undefined/null as "".
func stringField(obj *goja.Object, name string) string {
	v := obj.Get(name)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}
