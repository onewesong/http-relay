package script

import (
	"net/http"
	"testing"
)

// runOnRequest builds a Request from the given seed, runs the script's
// onRequest hook, and returns the mutated Request plus any short-circuit
// response. It fails the test on hook error.
func runOnRequest(t *testing.T, src string, req *Request) (*Request, *Response) {
	t.Helper()
	e := mustEngine(t, src)
	resp, err := e.OnRequest(req)
	if err != nil {
		t.Fatalf("OnRequest: unexpected error: %v", err)
	}
	return req, resp
}

// newRequest is a small constructor for a Request seed with an initialized
// header map. Tests that care about Host set it explicitly.
func newRequest(method, url string, header http.Header) *Request {
	if header == nil {
		header = http.Header{}
	}
	return &Request{Method: method, URL: url, Header: header}
}

func TestOnRequest_ReadFields(t *testing.T) {
	t.Parallel()

	src := `
		function onRequest(req) {
			req.headers["X-Seen-Method"] = req.method;
			req.headers["X-Seen-Url"] = req.url;
			req.headers["X-Seen-Host"] = req.host;
			req.headers["X-Seen-Auth"] = req.headers["Authorization"];
		}
	`
	h := http.Header{}
	h.Set("Authorization", "Bearer t")
	req := &Request{Method: "GET", URL: "https://api.example.com/v1", Host: "api.example.com", Header: h}

	got, _ := runOnRequest(t, src, req)

	if got.Header.Get("X-Seen-Method") != "GET" {
		t.Errorf("method not visible: %q", got.Header.Get("X-Seen-Method"))
	}
	if got.Header.Get("X-Seen-Url") != "https://api.example.com/v1" {
		t.Errorf("url not visible: %q", got.Header.Get("X-Seen-Url"))
	}
	if got.Header.Get("X-Seen-Host") != "api.example.com" {
		t.Errorf("host not visible: %q", got.Header.Get("X-Seen-Host"))
	}
	if got.Header.Get("X-Seen-Auth") != "Bearer t" {
		t.Errorf("auth header not visible: %q", got.Header.Get("X-Seen-Auth"))
	}
}

func TestOnRequest_AddHeader(t *testing.T) {
	t.Parallel()

	got, _ := runOnRequest(t, `function onRequest(req){ req.headers["X-Add"]="1"; }`,
		newRequest("GET", "https://e/", nil))

	if got.Header.Get("X-Add") != "1" {
		t.Fatalf("expected X-Add=1, got %q", got.Header.Get("X-Add"))
	}
}

func TestOnRequest_OverwriteHeader(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("X-Env", "dev")
	got, _ := runOnRequest(t, `function onRequest(req){ req.headers["X-Env"]="prod"; }`,
		&Request{Method: "GET", URL: "https://e/", Header: h})

	if v := got.Header.Values("X-Env"); len(v) != 1 || v[0] != "prod" {
		t.Fatalf("expected single X-Env=prod, got %v", v)
	}
}

func TestOnRequest_DeleteHeader(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("X-Drop", "gone")
	got, _ := runOnRequest(t, `function onRequest(req){ delete req.headers["X-Drop"]; }`,
		&Request{Method: "GET", URL: "https://e/", Header: h})

	if _, ok := got.Header["X-Drop"]; ok {
		t.Fatalf("expected X-Drop removed, still present: %v", got.Header.Values("X-Drop"))
	}
}

func TestOnRequest_SetHeaderEmptyStringKeepsHeader(t *testing.T) {
	t.Parallel()

	// Assigning "" is distinct from delete: the header stays present but empty.
	h := http.Header{}
	h.Set("X-Keep", "v")
	got, _ := runOnRequest(t, `function onRequest(req){ req.headers["X-Keep"]=""; }`,
		&Request{Method: "GET", URL: "https://e/", Header: h})

	vals, ok := got.Header["X-Keep"]
	if !ok {
		t.Fatalf("expected X-Keep to remain present after empty assignment")
	}
	if len(vals) != 1 || vals[0] != "" {
		t.Fatalf("expected X-Keep to be a single empty value, got %v", vals)
	}
}

func TestOnRequest_RewriteMethod(t *testing.T) {
	t.Parallel()

	got, _ := runOnRequest(t, `function onRequest(req){ req.method="POST"; }`,
		newRequest("GET", "https://e/", nil))

	if got.Method != "POST" {
		t.Fatalf("expected method POST, got %q", got.Method)
	}
}

func TestOnRequest_RewriteHost(t *testing.T) {
	t.Parallel()

	got, _ := runOnRequest(t, `function onRequest(req){ req.host="internal.svc"; }`,
		&Request{Method: "GET", URL: "https://e/", Host: "e"})

	if got.Host != "internal.svc" {
		t.Fatalf("expected host internal.svc, got %q", got.Host)
	}
}

func TestOnRequest_MultiValueHeaderJoinedOnRead(t *testing.T) {
	t.Parallel()

	// Design decision: multi-value request headers are presented to JS joined
	// by ", "; a write collapses them to a single value.
	h := http.Header{}
	h.Add("X-Multi", "a")
	h.Add("X-Multi", "b")
	got, _ := runOnRequest(t, `function onRequest(req){ req.headers["X-Echo"]=req.headers["X-Multi"]; }`,
		&Request{Method: "GET", URL: "https://e/", Header: h})

	if got.Header.Get("X-Echo") != "a, b" {
		t.Fatalf("expected joined multi-value 'a, b', got %q", got.Header.Get("X-Echo"))
	}
}

func TestOnRequest_NoMutationPassthrough(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("X-Orig", "1")
	got, resp := runOnRequest(t, `function onRequest(req){ /* read nothing, change nothing */ }`,
		&Request{Method: "GET", URL: "https://e/x", Host: "e", Header: h})

	if resp != nil {
		t.Fatalf("expected no short-circuit, got %#v", resp)
	}
	if got.Method != "GET" || got.URL != "https://e/x" || got.Host != "e" || got.Header.Get("X-Orig") != "1" {
		t.Fatalf("request mutated unexpectedly: %+v", got)
	}
}

func TestOnRequest_NoRequestHookIsNoop(t *testing.T) {
	t.Parallel()

	// Script defines only onResponse; OnRequest must be a no-op, no error.
	e := mustEngine(t, `function onResponse(resp, req){}`)
	req := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	resp, err := e.OnRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil short-circuit, got %#v", resp)
	}
}

func TestOnRequest_ReadOnlyRouteContext(t *testing.T) {
	t.Parallel()

	req := &Request{
		Method:         "GET",
		URL:            "https://example.com/",
		Header:         http.Header{},
		Namespace:      "team-a",
		RewriteProfile: "openai",
		OriginalPath:   "/team-a/@openai/https://example.com/",
	}
	src := `function onRequest(req) {
		req.headers["X-Context"] = req.namespace + "|" + req.rewriteProfile + "|" + req.originalPath;
		var descriptor = Object.getOwnPropertyDescriptor(req, "namespace");
		req.headers["X-Read-Only"] = String(!descriptor.writable && !descriptor.configurable);
	}`

	got, _ := runOnRequest(t, src, req)
	if want := "team-a|openai|/team-a/@openai/https://example.com/"; got.Header.Get("X-Context") != want {
		t.Fatalf("route context = %q, want %q", got.Header.Get("X-Context"), want)
	}
	if got.Header.Get("X-Read-Only") != "true" {
		t.Fatalf("namespace property is not read-only: %q", got.Header.Get("X-Read-Only"))
	}
	if got.Namespace != "team-a" || got.RewriteProfile != "openai" || got.OriginalPath != "/team-a/@openai/https://example.com/" {
		t.Fatalf("read-only route context was mutated: %+v", got)
	}
}

func TestOnRequest_RejectsRouteContextMutation(t *testing.T) {
	t.Parallel()

	req := &Request{Header: http.Header{}, Namespace: "team-a"}
	e := mustEngine(t, `function onRequest(req) { req.namespace = "changed"; }`)
	if _, err := e.OnRequest(req); err == nil {
		t.Fatal("expected assignment to read-only namespace to fail")
	}
	if req.Namespace != "team-a" {
		t.Fatalf("namespace changed to %q", req.Namespace)
	}
}
