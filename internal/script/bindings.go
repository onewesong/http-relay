package script

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/dop251/goja"
)

func installBindings(rt *goja.Runtime, out io.Writer, service *HTTPService, state func() *hookState) {
	installConsole(rt, out)
	installRelayHTTP(rt, service, state)
}

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

func installRelayHTTP(rt *goja.Runtime, service *HTTPService, state func() *hookState) {
	if state == nil {
		state = func() *hookState { return nil }
	}
	httpObject := rt.NewObject()
	_ = httpObject.DefineDataProperty("enabled", rt.ToValue(service != nil && service.Enabled()), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	request := func(call goja.FunctionCall) goja.Value {
		current := state()
		if current == nil || current.context == nil {
			panic(rt.NewGoError(fmt.Errorf("relay.http.request cannot be called outside a hook")))
		}
		if service == nil || !service.Enabled() {
			panic(rt.NewGoError(fmt.Errorf("relay.http is disabled")))
		}
		if current.calls >= service.maxCallsPerHook() {
			panic(rt.NewGoError(fmt.Errorf("relay.http.request call limit exceeded")))
		}
		parsed, err := parseHTTPRequest(rt, call.Argument(0))
		if err != nil {
			panic(rt.NewGoError(err))
		}
		current.calls++
		response, err := service.Request(current.context, parsed)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		return httpResponseToJS(rt, response)
	}
	_ = httpObject.DefineDataProperty("request", rt.ToValue(request), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	relay := rt.NewObject()
	_ = relay.DefineDataProperty("http", httpObject, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = rt.GlobalObject().DefineDataProperty("relay", relay, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

func parseHTTPRequest(rt *goja.Runtime, value goja.Value) (HTTPRequest, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return HTTPRequest{}, fmt.Errorf("relay.http.request options must be an object")
	}
	obj := value.ToObject(rt)
	if obj.ClassName() != "Object" {
		return HTTPRequest{}, fmt.Errorf("relay.http.request options must be an object")
	}
	urlValue, err := requiredString(obj.Get("url"), "url")
	if err != nil {
		return HTTPRequest{}, err
	}
	request := HTTPRequest{URL: urlValue, Method: http.MethodGet}
	if value := obj.Get("method"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		request.Method, err = requiredString(value, "method")
		if err != nil {
			return HTTPRequest{}, err
		}
	}
	if value := obj.Get("body"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		request.Body, err = requiredString(value, "body")
		if err != nil {
			return HTTPRequest{}, err
		}
		request.HasBody = true
	}
	if value := obj.Get("timeoutMs"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		exportType := value.ExportType()
		if exportType == nil || !isNumericKind(exportType.Kind()) {
			return HTTPRequest{}, fmt.Errorf("relay.http.request timeoutMs must be a positive integer")
		}
		milliseconds := value.ToFloat()
		maxMilliseconds := float64(math.MaxInt64 / int64(time.Millisecond))
		if math.IsNaN(milliseconds) || math.IsInf(milliseconds, 0) || milliseconds <= 0 || milliseconds > maxMilliseconds || milliseconds != math.Trunc(milliseconds) {
			return HTTPRequest{}, fmt.Errorf("relay.http.request timeoutMs must be a positive integer")
		}
		request.Timeout = time.Duration(milliseconds) * time.Millisecond
		request.HasTimeout = true
	}
	if value := obj.Get("headers"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		headers := value.ToObject(rt)
		if headers.ClassName() != "Object" {
			return HTTPRequest{}, fmt.Errorf("relay.http.request headers must be an object")
		}
		request.Headers = make(map[string]string, len(headers.Keys()))
		for _, name := range headers.Keys() {
			headerValue := headers.Get(name)
			if headerValue == nil || goja.IsUndefined(headerValue) || goja.IsNull(headerValue) {
				continue
			}
			request.Headers[name] = headerValue.String()
		}
	}
	return request, nil
}

func isNumericKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func requiredString(value goja.Value, name string) (string, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) || value.ExportType() == nil || value.ExportType().Kind() != reflect.String {
		return "", fmt.Errorf("relay.http.request %s must be a string", name)
	}
	return value.String(), nil
}

func httpResponseToJS(rt *goja.Runtime, response *HTTPResponse) goja.Value {
	obj := rt.NewObject()
	_ = obj.Set("status", response.Status)
	_ = obj.Set("headers", stringMapToJS(rt, response.Headers))
	_ = obj.Set("body", response.Body)
	_ = obj.Set("url", response.URL)
	return obj
}

func stringMapToJS(rt *goja.Runtime, values map[string]string) *goja.Object {
	obj := rt.NewObject()
	for name, value := range values {
		_ = obj.Set(name, value)
	}
	return obj
}

// newRequestObject builds the JS object handed to onRequest from a Go Request.
func newRequestObject(rt *goja.Runtime, req *Request) *goja.Object {
	obj := rt.NewObject()
	_ = obj.Set("method", req.Method)
	_ = obj.Set("url", req.URL)
	_ = obj.Set("host", req.Host)
	_ = obj.Set("headers", headersToJS(rt, req.Header))
	_ = obj.Set("body", string(req.Body))
	_ = obj.Set("streamResponse", req.StreamResponse)
	defineReadOnlyString(obj, "namespace", rt.ToValue(req.Namespace))
	defineReadOnlyString(obj, "rewriteProfile", rt.ToValue(req.RewriteProfile))
	defineReadOnlyString(obj, "originalPath", rt.ToValue(req.OriginalPath))
	return obj
}

func defineReadOnlyString(obj *goja.Object, name string, value goja.Value) {
	_ = obj.DefineDataProperty(name, value, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

// readRequestObject copies mutations from the JS request object back into req.
func readRequestObject(rt *goja.Runtime, obj *goja.Object, req *Request) {
	req.Method = stringField(obj, "method")
	req.URL = stringField(obj, "url")
	req.Host = stringField(obj, "host")
	req.Body = []byte(stringField(obj, "body"))
	if stream := obj.Get("streamResponse"); stream != nil && !goja.IsUndefined(stream) && !goja.IsNull(stream) {
		req.StreamResponse = stream.ToBoolean()
	}
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
