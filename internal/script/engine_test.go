package script

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// writeScript writes src to a temp .js file and returns its path.
func writeScript(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relay.js")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// mustEngine compiles src and fails the test if compilation errors.
func mustEngine(t *testing.T, src string) *Engine {
	t.Helper()
	e, err := New(Options{Path: writeScript(t, src)})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	if e == nil {
		t.Fatal("New: engine is nil for non-empty path")
	}
	return e
}

func TestNew_EmptyPathReturnsNilEngine(t *testing.T) {
	t.Parallel()

	e, err := New(Options{Path: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e != nil {
		t.Fatalf("expected nil engine for empty path, got %#v", e)
	}
}

func TestNew_ValidScript(t *testing.T) {
	t.Parallel()

	e := mustEngine(t, `
		function onRequest(req) {}
		function onResponse(resp, req) {}
	`)
	if !e.HasRequestHook() {
		t.Error("expected HasRequestHook to be true")
	}
	if !e.HasResponseHook() {
		t.Error("expected HasResponseHook to be true")
	}
}

func TestNew_FileNotFound(t *testing.T) {
	t.Parallel()

	_, err := New(Options{Path: filepath.Join(t.TempDir(), "does-not-exist.js")})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestNew_SyntaxError(t *testing.T) {
	t.Parallel()

	_, err := New(Options{Path: writeScript(t, `function onRequest( {`)})
	if err == nil {
		t.Fatal("expected compile error for invalid syntax, got nil")
	}
}

func TestNew_NoHooks(t *testing.T) {
	t.Parallel()

	// A script with no hooks loads fine and reports neither hook present.
	e := mustEngine(t, `var x = 1;`)
	if e.HasRequestHook() {
		t.Error("expected HasRequestHook to be false")
	}
	if e.HasResponseHook() {
		t.Error("expected HasResponseHook to be false")
	}
}

func TestNew_OnlyRequestHook(t *testing.T) {
	t.Parallel()

	e := mustEngine(t, `function onRequest(req) {}`)
	if !e.HasRequestHook() {
		t.Error("expected HasRequestHook to be true")
	}
	if e.HasResponseHook() {
		t.Error("expected HasResponseHook to be false")
	}
}

func TestNew_OnlyResponseHook(t *testing.T) {
	t.Parallel()

	e := mustEngine(t, `function onResponse(resp, req) {}`)
	if e.HasRequestHook() {
		t.Error("expected HasRequestHook to be false")
	}
	if !e.HasResponseHook() {
		t.Error("expected HasResponseHook to be true")
	}
}

func TestNew_HookNotFunction(t *testing.T) {
	t.Parallel()

	// Exporting onRequest as a non-function is a configuration error.
	_, err := New(Options{Path: writeScript(t, `var onRequest = 123;`)})
	if err == nil {
		t.Fatal("expected error when onRequest is not a function, got nil")
	}
}

func TestConsoleLogRoutedToWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	e, err := New(Options{
		Path:    writeScript(t, `function onRequest(req){ console.log("hello", 42); }`),
		Console: &buf,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e.OnRequest(&Request{Method: "GET", URL: "https://e/", Header: nil}); err != nil {
		t.Fatalf("OnRequest: %v", err)
	}
	if got := buf.String(); got != "hello 42\n" {
		t.Fatalf("console output = %q, want %q", got, "hello 42\n")
	}
}

func TestConsoleLogDefaultNoError(t *testing.T) {
	t.Parallel()

	// With no Console configured, console.log must be a silent no-op, not a 500.
	e := mustEngine(t, `function onRequest(req){ console.log("dropped"); }`)
	if _, err := e.OnRequest(&Request{Method: "GET", URL: "https://e/", Header: nil}); err != nil {
		t.Fatalf("console.log without Console writer should not error: %v", err)
	}
}
