package script

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

func TestEngine_ConcurrentNoCrosstalk(t *testing.T) {
	t.Parallel()

	// Each request echoes its own unique header value into a new one. With a
	// per-runtime pool there must be no bleed between concurrent invocations.
	e := mustEngine(t, `function onRequest(req){ req.headers["X-Echo"] = req.headers["X-In"]; }`)

	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := fmt.Sprintf("val-%d", i)
			h := http.Header{}
			h.Set("X-In", want)
			req := &Request{Method: "GET", URL: "https://e/", Header: h}
			if _, err := e.OnRequest(req); err != nil {
				errs <- err
				return
			}
			if got := req.Header.Get("X-Echo"); got != want {
				errs <- fmt.Errorf("crosstalk: got %q want %q", got, want)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestEngine_RuntimeReuseNoResidualRequestState(t *testing.T) {
	t.Parallel()

	// Reusing a pooled runtime across sequential calls must not leak the
	// previous request's data: each call gets a fresh request object.
	e := mustEngine(t, `function onRequest(req){ req.headers["X-Out"] = req.headers["X-In"] || "none"; }`)

	h1 := http.Header{}
	h1.Set("X-In", "first")
	r1 := &Request{Method: "GET", URL: "https://e/", Header: h1}
	if _, err := e.OnRequest(r1); err != nil {
		t.Fatal(err)
	}

	// Second call has no X-In; must not see "first".
	r2 := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	if _, err := e.OnRequest(r2); err != nil {
		t.Fatal(err)
	}

	if r1.Header.Get("X-Out") != "first" {
		t.Errorf("first call wrong: %q", r1.Header.Get("X-Out"))
	}
	if r2.Header.Get("X-Out") != "none" {
		t.Errorf("residual state leaked into second call: %q", r2.Header.Get("X-Out"))
	}
}
