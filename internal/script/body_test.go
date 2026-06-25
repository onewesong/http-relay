package script

import (
	"net/http"
	"testing"
)

func TestOnRequest_ReadBody(t *testing.T) {
	t.Parallel()

	req := &Request{Method: "POST", URL: "https://e/", Header: http.Header{}, Body: []byte("hello")}
	got, _ := runOnRequest(t, `function onRequest(req){ req.headers["X-Body"]=req.body; }`, req)

	if got.Header.Get("X-Body") != "hello" {
		t.Fatalf("body not visible to script: %q", got.Header.Get("X-Body"))
	}
}

func TestOnRequest_RewriteBody(t *testing.T) {
	t.Parallel()

	req := &Request{Method: "POST", URL: "https://e/", Header: http.Header{}, Body: []byte("old")}
	got, _ := runOnRequest(t, `function onRequest(req){ req.body="new-and-longer"; }`, req)

	if string(got.Body) != "new-and-longer" {
		t.Fatalf("body not rewritten: %q", string(got.Body))
	}
}

func TestOnRequest_ClearBody(t *testing.T) {
	t.Parallel()

	req := &Request{Method: "POST", URL: "https://e/", Header: http.Header{}, Body: []byte("data")}
	got, _ := runOnRequest(t, `function onRequest(req){ req.body=""; }`, req)

	if len(got.Body) != 0 {
		t.Fatalf("body not cleared: %q", string(got.Body))
	}
}

func TestOnRequest_JSONInjection(t *testing.T) {
	t.Parallel()

	req := &Request{Method: "POST", URL: "https://e/", Header: http.Header{}, Body: []byte(`{"a":1}`)}
	src := `function onRequest(req){
		var data = JSON.parse(req.body);
		data.injected = true;
		req.body = JSON.stringify(data);
	}`
	got, _ := runOnRequest(t, src, req)

	if string(got.Body) != `{"a":1,"injected":true}` {
		t.Fatalf("json injection failed: %q", string(got.Body))
	}
}

func TestOnRequest_EmptyBodyNoError(t *testing.T) {
	t.Parallel()

	req := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	got, _ := runOnRequest(t, `function onRequest(req){ req.headers["X-Len"]=String(req.body.length); }`, req)

	if got.Header.Get("X-Len") != "0" {
		t.Fatalf("empty body should be zero-length string, got %q", got.Header.Get("X-Len"))
	}
}
