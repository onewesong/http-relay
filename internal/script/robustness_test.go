package script

import (
	"net/http"
	"testing"
	"time"
)

func TestOnRequest_ThrowReturnsError(t *testing.T) {
	t.Parallel()

	e := mustEngine(t, `function onRequest(req){ throw new Error("boom"); }`)
	_, err := e.OnRequest(&Request{Method: "GET", URL: "https://e/", Header: http.Header{}})
	if err == nil {
		t.Fatal("expected error when onRequest throws, got nil")
	}
}

func TestOnResponse_ThrowReturnsError(t *testing.T) {
	t.Parallel()

	e := mustEngine(t, `function onResponse(resp, req){ throw new Error("boom"); }`)
	err := e.OnResponse(&Response{Status: 200, Header: http.Header{}}, &Request{Header: http.Header{}})
	if err == nil {
		t.Fatal("expected error when onResponse throws, got nil")
	}
}

func TestOnRequest_TimeoutReturnsError(t *testing.T) {
	t.Parallel()

	e, err := New(Options{
		Path:    writeScript(t, `function onRequest(req){ while(true){} }`),
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, e2 := e.OnRequest(&Request{Method: "GET", URL: "https://e/", Header: http.Header{}})
		done <- e2
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnRequest did not return; timeout interrupt failed")
	}
}

func TestEngine_UsableAfterTimeout(t *testing.T) {
	t.Parallel()

	// After a hook times out, a subsequent (well-behaved) call must still work:
	// the interrupted runtime must be cleared/rebuilt, not left poisoned.
	e, err := New(Options{
		Path: writeScript(t, `function onRequest(req){
			if (req.headers["X-Hang"]) { while(true){} }
			req.headers["X-Ok"] = "1";
		}`),
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	hang := http.Header{}
	hang.Set("X-Hang", "1")
	if _, err := e.OnRequest(&Request{Method: "GET", URL: "https://e/", Header: hang}); err == nil {
		t.Fatal("expected timeout error on hanging request")
	}

	req := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	if _, err := e.OnRequest(req); err != nil {
		t.Fatalf("engine not usable after timeout: %v", err)
	}
	if req.Header.Get("X-Ok") != "1" {
		t.Fatalf("subsequent request not processed: %v", req.Header)
	}
}
