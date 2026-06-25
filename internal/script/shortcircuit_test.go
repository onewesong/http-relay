package script

import (
	"net/http"
	"testing"
)

func TestOnRequest_ShortCircuitReturnsResponse(t *testing.T) {
	t.Parallel()

	src := `function onRequest(req){ return { status: 200, body: "ok" }; }`
	req := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	_, resp := runOnRequest(t, src, req)

	if resp == nil {
		t.Fatal("expected short-circuit response, got nil")
	}
	if resp.Status != 200 || string(resp.Body) != "ok" {
		t.Fatalf("unexpected short-circuit: status=%d body=%q", resp.Status, string(resp.Body))
	}
}

func TestOnRequest_ShortCircuitCustomStatus(t *testing.T) {
	t.Parallel()

	src := `function onRequest(req){ return { status: 403, body: "denied" }; }`
	_, resp := runOnRequest(t, src, &Request{Method: "GET", URL: "https://e/", Header: http.Header{}})

	if resp == nil || resp.Status != 403 {
		t.Fatalf("expected 403 short-circuit, got %#v", resp)
	}
}

func TestOnRequest_ShortCircuitHeaders(t *testing.T) {
	t.Parallel()

	src := `function onRequest(req){ return { status: 200, headers: { "Content-Type": "application/json" }, body: "{}" }; }`
	_, resp := runOnRequest(t, src, &Request{Method: "GET", URL: "https://e/", Header: http.Header{}})

	if resp == nil {
		t.Fatal("expected short-circuit response")
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("short-circuit header missing: %v", resp.Header)
	}
}

func TestOnRequest_ShortCircuitDefaults(t *testing.T) {
	t.Parallel()

	// Returning an object with no status defaults to 200; no body is empty.
	src := `function onRequest(req){ return {}; }`
	_, resp := runOnRequest(t, src, &Request{Method: "GET", URL: "https://e/", Header: http.Header{}})

	if resp == nil {
		t.Fatal("expected short-circuit response for empty object return")
	}
	if resp.Status != http.StatusOK {
		t.Errorf("expected default status 200, got %d", resp.Status)
	}
	if len(resp.Body) != 0 {
		t.Errorf("expected empty body, got %q", string(resp.Body))
	}
}

func TestOnRequest_ReturnUndefinedNoShortCircuit(t *testing.T) {
	t.Parallel()

	// Explicitly returning nothing must not short-circuit.
	_, resp := runOnRequest(t, `function onRequest(req){ return; }`,
		&Request{Method: "GET", URL: "https://e/", Header: http.Header{}})

	if resp != nil {
		t.Fatalf("expected no short-circuit, got %#v", resp)
	}
}
