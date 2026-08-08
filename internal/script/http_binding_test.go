package script

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newHTTPBindingEngine(t *testing.T, source string, service *HTTPService, timeout time.Duration) *Engine {
	t.Helper()
	engine, err := New(Options{Path: writeScript(t, source), HTTP: service, Timeout: timeout})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine
}

func TestRelayHTTPBindingRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Multi", "a")
		w.Header().Add("X-Multi", "b")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("done"))
	}))
	defer server.Close()
	service := mustHTTPService(t, testHTTPOptions(server.URL))
	engine := newHTTPBindingEngine(t, `function onRequest(req) {
		var response = relay.http.request({url: "`+server.URL+`", timeoutMs: 500});
		req.headers["X-Result"] = response.status + "|" + response.headers["X-Multi"] + "|" + response.body + "|" + response.url;
	}`, service, time.Second)
	req := &Request{Method: "GET", URL: "https://upstream/", Header: http.Header{}}
	if _, err := engine.OnRequest(req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("X-Result"); got != "202|a, b|done|"+server.URL {
		t.Fatalf("result=%q", got)
	}
}

func TestRelayHTTPBindingDisabledAndOutsideHook(t *testing.T) {
	t.Parallel()
	disabled := mustHTTPService(t, HTTPOptions{DefaultTimeout: time.Second, MaxTimeout: time.Second, MaxRequestBodyBytes: 1, MaxResponseBodyBytes: 1, MaxCallsPerHook: 1})
	engine := newHTTPBindingEngine(t, `function onRequest(req) {
		req.headers["X-Enabled"] = String(relay.http.enabled);
		try { relay.http.request({url:"https://example.com"}); } catch (error) { req.headers["X-Error"] = error.message; }
	}`, disabled, time.Second)
	req := &Request{Header: http.Header{}}
	if _, err := engine.OnRequest(req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("X-Enabled") != "false" || !strings.Contains(req.Header.Get("X-Error"), "disabled") {
		t.Fatalf("headers=%v", req.Header)
	}

	if _, err := New(Options{Path: writeScript(t, `relay.http.request({url:"https://example.com"}); function onRequest(req){}`), HTTP: disabled}); err == nil || !strings.Contains(err.Error(), "outside a hook") {
		t.Fatalf("init error=%v", err)
	}
}

func TestRelayHTTPBindingReadOnly(t *testing.T) {
	t.Parallel()
	engine := newHTTPBindingEngine(t, `function onRequest(req) { relay = {}; }`, nil, time.Second)
	if _, err := engine.OnRequest(&Request{Header: http.Header{}}); err == nil {
		t.Fatal("expected relay global assignment to fail")
	}
	engine = newHTTPBindingEngine(t, `function onRequest(req) { relay.http.enabled = true; }`, nil, time.Second)
	if _, err := engine.OnRequest(&Request{Header: http.Header{}}); err == nil {
		t.Fatal("expected relay.http.enabled assignment to fail")
	}
	engine = newHTTPBindingEngine(t, `function onRequest(req) { relay.http.request = function() {}; }`, nil, time.Second)
	if _, err := engine.OnRequest(&Request{Header: http.Header{}}); err == nil {
		t.Fatal("expected relay.http.request assignment to fail")
	}
}

func TestRelayHTTPBindingValidatesArguments(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	service := mustHTTPService(t, testHTTPOptions(server.URL))
	engine := newHTTPBindingEngine(t, `function onRequest(req) {
		var invalid = [
			function(){ relay.http.request(); },
			function(){ relay.http.request({url: 42}); },
			function(){ relay.http.request({url:"`+server.URL+`", headers: []}); },
			function(){ relay.http.request({url:"`+server.URL+`", timeoutMs: 1.5}); },
			function(){ relay.http.request({url:"`+server.URL+`", timeoutMs: "1"}); }
		];
		var caught = 0;
		for (var i = 0; i < invalid.length; i++) { try { invalid[i](); } catch (error) { caught++; } }
		req.headers["X-Caught"] = String(caught);
	}`, service, time.Second)
	req := &Request{Header: http.Header{}}
	if _, err := engine.OnRequest(req); err != nil || req.Header.Get("X-Caught") != "5" {
		t.Fatalf("headers=%v error=%v", req.Header, err)
	}
}

func TestRelayHTTPBindingReturnsFreshObjects(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Origin", "clean")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	service := mustHTTPService(t, testHTTPOptions(server.URL))
	engine := newHTTPBindingEngine(t, `var requestHTTP = relay.http.request;
		function onRequest(req) {
			var first = requestHTTP({url:"`+server.URL+`"});
			first.headers["X-Injected"] = "yes";
			var second = requestHTTP({url:"`+server.URL+`"});
			req.headers["X-Fresh"] = String(second.headers["X-Injected"] === undefined && second.headers["X-Origin"] === "clean");
		}`, service, time.Second)
	req := &Request{Header: http.Header{}}
	if _, err := engine.OnRequest(req); err != nil || req.Header.Get("X-Fresh") != "true" {
		t.Fatalf("headers=%v error=%v", req.Header, err)
	}
}

func TestRelayHTTPBindingCallLimitResetsPerHook(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	opts := testHTTPOptions(server.URL)
	opts.MaxCallsPerHook = 1
	service := mustHTTPService(t, opts)
	engine := newHTTPBindingEngine(t, `function onRequest(req) {
		relay.http.request({url:"`+server.URL+`"});
		try { relay.http.request({url:"`+server.URL+`"}); } catch (error) { req.headers["X-Limited"] = error.message; }
	}`, service, time.Second)
	for i := 0; i < 2; i++ {
		req := &Request{Header: http.Header{}}
		if _, err := engine.OnRequest(req); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(req.Header.Get("X-Limited"), "limit") {
			t.Fatalf("headers=%v", req.Header)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("server calls=%d", calls.Load())
	}
}

func TestRelayHTTPBindingHookTimeoutCancelsRequest(t *testing.T) {
	t.Parallel()
	canceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	defer server.Close()
	service := mustHTTPService(t, testHTTPOptions(server.URL))
	engine := newHTTPBindingEngine(t, `function onRequest(req) {
		if (req.headers["X-Hang"]) relay.http.request({url:"`+server.URL+`", timeoutMs:1000});
		req.headers["X-Ok"] = "1";
	}`, service, 50*time.Millisecond)
	h := http.Header{}
	h.Set("X-Hang", "1")
	if _, err := engine.OnRequest(&Request{Header: h}); err == nil {
		t.Fatal("expected hook timeout")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("server request context was not canceled")
	}

	req := &Request{Header: http.Header{}}
	if _, err := engine.OnRequest(req); err != nil || req.Header.Get("X-Ok") != "1" {
		t.Fatalf("runtime not reusable: headers=%v error=%v", req.Header, err)
	}
}

func TestRelayHTTPBindingWorksInOnResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("external")) }))
	defer server.Close()
	service := mustHTTPService(t, testHTTPOptions(server.URL))
	engine := newHTTPBindingEngine(t, `function onResponse(resp, req) { resp.body = relay.http.request({url:"`+server.URL+`"}).body; }`, service, time.Second)
	response := &Response{Status: 200, Header: http.Header{}, Body: []byte("upstream")}
	if err := engine.OnResponse(response, &Request{Header: http.Header{}}); err != nil || string(response.Body) != "external" {
		t.Fatalf("response=%+v error=%v", response, err)
	}
}
