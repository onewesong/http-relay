package script

import (
	"net/http"
	"testing"
)

func TestOnRequest_RewriteURL(t *testing.T) {
	t.Parallel()

	req := &Request{Method: "GET", URL: "https://old.example.com/a", Header: http.Header{}}
	got, _ := runOnRequest(t, `function onRequest(req){ req.url = "https://new.example.com/b?x=1"; }`, req)

	if got.URL != "https://new.example.com/b?x=1" {
		t.Fatalf("url not rewritten: %q", got.URL)
	}
}

func TestOnRequest_RewriteURLConditional(t *testing.T) {
	t.Parallel()

	// A script can route based on the inbound path.
	src := `function onRequest(req){
		if (req.url.indexOf("/legacy/") >= 0) {
			req.url = req.url.replace("/legacy/", "/v2/");
		}
	}`
	req := &Request{Method: "GET", URL: "https://e/legacy/users", Header: http.Header{}}
	got, _ := runOnRequest(t, src, req)

	if got.URL != "https://e/v2/users" {
		t.Fatalf("conditional url rewrite failed: %q", got.URL)
	}
}
