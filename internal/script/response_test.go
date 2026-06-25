package script

import (
	"net/http"
	"testing"
)

// runOnResponse runs the script's onResponse hook against resp (with req as
// context) and returns the mutated response, failing the test on hook error.
func runOnResponse(t *testing.T, src string, resp *Response, req *Request) *Response {
	t.Helper()
	e := mustEngine(t, src)
	if req == nil {
		req = &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	}
	if err := e.OnResponse(resp, req); err != nil {
		t.Fatalf("OnResponse: unexpected error: %v", err)
	}
	return resp
}

func TestOnResponse_ReadFields(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Content-Type", "text/plain")
	resp := &Response{Status: 201, Header: h, Body: []byte("payload")}
	src := `function onResponse(resp, req){
		resp.headers["X-Saw-Status"] = String(resp.status);
		resp.headers["X-Saw-CT"] = resp.headers["Content-Type"];
		resp.headers["X-Saw-Body"] = resp.body;
	}`
	got := runOnResponse(t, src, resp, nil)

	if got.Header.Get("X-Saw-Status") != "201" {
		t.Errorf("status not visible: %q", got.Header.Get("X-Saw-Status"))
	}
	if got.Header.Get("X-Saw-CT") != "text/plain" {
		t.Errorf("content-type not visible: %q", got.Header.Get("X-Saw-CT"))
	}
	if got.Header.Get("X-Saw-Body") != "payload" {
		t.Errorf("body not visible: %q", got.Header.Get("X-Saw-Body"))
	}
}

func TestOnResponse_RewriteStatus(t *testing.T) {
	t.Parallel()

	got := runOnResponse(t, `function onResponse(resp, req){ resp.status = 503; }`,
		&Response{Status: 200, Header: http.Header{}}, nil)

	if got.Status != 503 {
		t.Fatalf("expected status 503, got %d", got.Status)
	}
}

func TestOnResponse_HeaderAddDeleteModify(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("X-Mod", "old")
	h.Set("X-Del", "bye")
	resp := &Response{Status: 200, Header: h}
	src := `function onResponse(resp, req){
		resp.headers["X-New"] = "1";
		resp.headers["X-Mod"] = "new";
		delete resp.headers["X-Del"];
	}`
	got := runOnResponse(t, src, resp, nil)

	if got.Header.Get("X-New") != "1" {
		t.Errorf("add failed: %v", got.Header)
	}
	if got.Header.Get("X-Mod") != "new" {
		t.Errorf("modify failed: %v", got.Header)
	}
	if _, ok := got.Header["X-Del"]; ok {
		t.Errorf("delete failed: %v", got.Header)
	}
}

func TestOnResponse_RewriteBody(t *testing.T) {
	t.Parallel()

	got := runOnResponse(t, `function onResponse(resp, req){ resp.body = "replaced"; }`,
		&Response{Status: 200, Header: http.Header{}, Body: []byte("orig")}, nil)

	if string(got.Body) != "replaced" {
		t.Fatalf("body not rewritten: %q", string(got.Body))
	}
}

func TestOnResponse_SeesRequestContext(t *testing.T) {
	t.Parallel()

	req := &Request{Method: "DELETE", URL: "https://e/thing", Header: http.Header{}}
	src := `function onResponse(resp, req){ resp.headers["X-Req-Method"] = req.method; }`
	got := runOnResponse(t, src, &Response{Status: 200, Header: http.Header{}}, req)

	if got.Header.Get("X-Req-Method") != "DELETE" {
		t.Fatalf("request context not visible: %q", got.Header.Get("X-Req-Method"))
	}
}

func TestOnResponse_ConditionalByContentType(t *testing.T) {
	t.Parallel()

	src := `function onResponse(resp, req){
		var ct = resp.headers["Content-Type"] || "";
		if (ct.indexOf("json") >= 0) {
			var d = JSON.parse(resp.body); d.touched = true; resp.body = JSON.stringify(d);
		}
	}`

	jsonHdr := http.Header{}
	jsonHdr.Set("Content-Type", "application/json")
	gotJSON := runOnResponse(t, src, &Response{Status: 200, Header: jsonHdr, Body: []byte(`{"a":1}`)}, nil)
	if string(gotJSON.Body) != `{"a":1,"touched":true}` {
		t.Errorf("json branch failed: %q", string(gotJSON.Body))
	}

	txtHdr := http.Header{}
	txtHdr.Set("Content-Type", "text/plain")
	gotTxt := runOnResponse(t, src, &Response{Status: 200, Header: txtHdr, Body: []byte("plain")}, nil)
	if string(gotTxt.Body) != "plain" {
		t.Errorf("non-json should pass through: %q", string(gotTxt.Body))
	}
}

func TestOnResponse_NoMutationPassthrough(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("X-Orig", "1")
	got := runOnResponse(t, `function onResponse(resp, req){ /* noop */ }`,
		&Response{Status: 204, Header: h, Body: []byte("keep")}, nil)

	if got.Status != 204 || got.Header.Get("X-Orig") != "1" || string(got.Body) != "keep" {
		t.Fatalf("response mutated unexpectedly: %+v", got)
	}
}

func TestOnResponse_NoResponseHookIsNoop(t *testing.T) {
	t.Parallel()

	e := mustEngine(t, `function onRequest(req){}`)
	resp := &Response{Status: 200, Header: http.Header{}, Body: []byte("x")}
	if err := e.OnResponse(resp, &Request{Header: http.Header{}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != 200 || string(resp.Body) != "x" {
		t.Fatalf("response changed by no-op: %+v", resp)
	}
}
