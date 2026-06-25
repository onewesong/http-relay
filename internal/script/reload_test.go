package script

import (
	"net/http"
	"os"
	"testing"
)

// overwrite replaces the file at path with src.
func overwrite(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("overwrite script: %v", err)
	}
}

func TestReload_NewLogicTakesEffect(t *testing.T) {
	t.Parallel()

	path := writeScript(t, `function onRequest(req){ req.headers["X-Ver"]="v1"; }`)
	e, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r1 := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	_, _ = e.OnRequest(r1)
	if r1.Header.Get("X-Ver") != "v1" {
		t.Fatalf("pre-reload wrong: %q", r1.Header.Get("X-Ver"))
	}

	overwrite(t, path, `function onRequest(req){ req.headers["X-Ver"]="v2"; }`)
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	r2 := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	_, _ = e.OnRequest(r2)
	if r2.Header.Get("X-Ver") != "v2" {
		t.Fatalf("post-reload did not take effect: %q", r2.Header.Get("X-Ver"))
	}
}

func TestReload_CompileErrorKeepsOldVersion(t *testing.T) {
	t.Parallel()

	path := writeScript(t, `function onRequest(req){ req.headers["X-Ver"]="good"; }`)
	e, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	overwrite(t, path, `function onRequest( {`) // syntax error
	if err := e.Reload(); err == nil {
		t.Fatal("expected reload error for broken script, got nil")
	}

	// Old version still serves.
	r := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	if _, err := e.OnRequest(r); err != nil {
		t.Fatalf("old version should still work: %v", err)
	}
	if r.Header.Get("X-Ver") != "good" {
		t.Fatalf("old version not retained: %q", r.Header.Get("X-Ver"))
	}
}

func TestReload_RecoversAfterFix(t *testing.T) {
	t.Parallel()

	path := writeScript(t, `function onRequest(req){ req.headers["X-Ver"]="v1"; }`)
	e, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	overwrite(t, path, `@@@ broken`)
	if err := e.Reload(); err == nil {
		t.Fatal("expected error on broken reload")
	}

	overwrite(t, path, `function onRequest(req){ req.headers["X-Ver"]="v3"; }`)
	if err := e.Reload(); err != nil {
		t.Fatalf("recovery reload failed: %v", err)
	}

	r := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	_, _ = e.OnRequest(r)
	if r.Header.Get("X-Ver") != "v3" {
		t.Fatalf("did not recover to v3: %q", r.Header.Get("X-Ver"))
	}
}

func TestReload_ChangesHookPresence(t *testing.T) {
	t.Parallel()

	path := writeScript(t, `function onRequest(req){}`)
	e, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e.HasResponseHook() {
		t.Fatal("should not have response hook initially")
	}

	overwrite(t, path, `function onRequest(req){}
		function onResponse(resp, req){}`)
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !e.HasResponseHook() {
		t.Fatal("response hook should be present after reload")
	}
}
