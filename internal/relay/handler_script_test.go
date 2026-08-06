package relay

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onewesong/http-relay/internal/script"
)

// scriptEngine compiles src into an engine for handler tests.
func scriptEngine(t *testing.T, src string) *script.Engine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relay.js")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	e, err := script.New(script.Options{Path: path})
	if err != nil {
		t.Fatalf("compile script: %v", err)
	}
	return e
}

// scriptHandler builds a relay handler wired with the given engine.
func scriptHandler(t *testing.T, e *script.Engine) http.Handler {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	return NewHandlerWithOptions(client, log.New(io.Discard, "", 0), HandlerOptions{
		TargetMode:   DefaultTargetMode(),
		DumpRequest:  true,
		DumpScope:    DumpScopeReq | DumpScopeResp,
		ScriptEngine: e,
	})
}

func registryHandler(t *testing.T, defaultEngine *script.Engine, profiles map[string]string) http.Handler {
	t.Helper()
	opts := make([]script.ProfileOptions, 0, len(profiles))
	for name, source := range profiles {
		path := filepath.Join(t.TempDir(), name+".js")
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write profile %s: %v", name, err)
		}
		opts = append(opts, script.ProfileOptions{Name: name, Path: path})
	}
	registry, err := script.NewRegistry(defaultEngine, opts)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	return NewHandlerWithOptions(client, log.New(io.Discard, "", 0), HandlerOptions{
		TargetMode:     DefaultTargetMode(),
		ScriptRegistry: registry,
	})
}

func TestHandler_ScriptRewriteRequestHeader(t *testing.T) {
	t.Parallel()

	var gotHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Injected")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	e := scriptEngine(t, `function onRequest(req){ req.headers["X-Injected"]="from-script"; }`)
	relay := httptest.NewServer(scriptHandler(t, e))
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/" + upstream.URL + "/x")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if gotHeader != "from-script" {
		t.Fatalf("upstream did not see injected header, got %q", gotHeader)
	}
}

func TestHandler_ScriptRewriteResponseBody(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"a":1}`))
	}))
	defer upstream.Close()

	e := scriptEngine(t, `function onResponse(resp, req){
		var d = JSON.parse(resp.body); d.injected = true; resp.body = JSON.stringify(d);
	}`)
	relay := httptest.NewServer(scriptHandler(t, e))
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/" + upstream.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"a":1,"injected":true}` {
		t.Fatalf("response body not rewritten: %q", string(body))
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" && cl != "23" {
		t.Fatalf("Content-Length not recomputed: %q", cl)
	}
}

func TestHandler_ScriptShortCircuit(t *testing.T) {
	t.Parallel()

	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	e := scriptEngine(t, `function onRequest(req){ return { status: 418, body: "teapot" }; }`)
	relay := httptest.NewServer(scriptHandler(t, e))
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/" + upstream.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 418 {
		t.Fatalf("status=%d want 418", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "teapot" {
		t.Fatalf("body=%q want teapot", string(body))
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("upstream was hit %d times during short-circuit", n)
	}
}

func TestHandler_ShortCircuitStillRunsOnResponse(t *testing.T) {
	t.Parallel()

	e := scriptEngine(t, `
		function onRequest(req){ return { status: 200, body: "mock" }; }
		function onResponse(resp, req){ resp.status = 201; resp.headers["X-Phase"]="resp"; }
	`)
	relay := httptest.NewServer(scriptHandler(t, e))
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/http://unused.example.invalid/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Fatalf("status=%d want 201 (onResponse should run after short-circuit)", resp.StatusCode)
	}
	if resp.Header.Get("X-Phase") != "resp" {
		t.Fatalf("onResponse header missing on short-circuited response")
	}
}

func TestHandler_ScriptRewriteURL(t *testing.T) {
	t.Parallel()

	var bHit int32
	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&bHit, 1)
		_, _ = w.Write([]byte("from-B"))
	}))
	defer upstreamB.Close()
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream A should not be hit after URL rewrite")
	}))
	defer upstreamA.Close()

	e := scriptEngine(t, `function onRequest(req){ req.url = "`+upstreamB.URL+`/"; }`)
	relay := httptest.NewServer(scriptHandler(t, e))
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/" + upstreamA.URL + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "from-B" || atomic.LoadInt32(&bHit) != 1 {
		t.Fatalf("URL rewrite did not route to B: body=%q hits=%d", string(body), bHit)
	}
}

func TestHandler_ScriptHookErrorReturns500(t *testing.T) {
	t.Parallel()

	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer upstream.Close()

	e := scriptEngine(t, `function onRequest(req){ throw new Error("boom"); }`)
	relay := httptest.NewServer(scriptHandler(t, e))
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/" + upstream.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", resp.StatusCode)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("upstream hit %d times despite hook error", n)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "script hook failed") {
		t.Fatalf("error body missing reason: %q", string(body))
	}
}

func TestHandler_ScriptRewriteMethod(t *testing.T) {
	t.Parallel()

	var gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
	}))
	defer upstream.Close()

	e := scriptEngine(t, `function onRequest(req){ req.method = "DELETE"; }`)
	relay := httptest.NewServer(scriptHandler(t, e))
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/" + upstream.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if gotMethod != "DELETE" {
		t.Fatalf("method not rewritten upstream: %q", gotMethod)
	}
}

func TestHandler_NilEngineUnchanged(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain"))
	}))
	defer upstream.Close()

	relay := httptest.NewServer(scriptHandler(t, nil))
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/" + upstream.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "plain" {
		t.Fatalf("nil engine altered response: %q", string(body))
	}
}

func TestHandler_SelectsRewriteProfileWithNamespaceContext(t *testing.T) {
	t.Parallel()

	profile := `
		function onRequest(req) {
			return {status: 200, body: req.namespace + "|" + req.rewriteProfile + "|" + req.originalPath};
		}
		function onResponse(resp, req) { resp.body = "openai:" + resp.body; }
	`
	relayServer := httptest.NewServer(registryHandler(t, nil, map[string]string{"openai": profile}))
	defer relayServer.Close()

	tests := []struct {
		path string
		want string
	}{
		{"/@openai/http://unused.example/", "openai:|openai|/@openai/http://unused.example/"},
		{"/team-a/@openai/http://unused.example/", "openai:team-a|openai|/team-a/@openai/http://unused.example/"},
	}
	for _, tc := range tests {
		resp, err := http.Get(relayServer.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != tc.want {
			t.Fatalf("GET %s: status=%d body=%q want=%q", tc.path, resp.StatusCode, body, tc.want)
		}
	}
}

func TestHandler_UnknownRewriteProfileDoesNotUseDefaultEngine(t *testing.T) {
	t.Parallel()

	defaultEngine := scriptEngine(t, `function onRequest(req){ return {status: 200, body: "default"}; }`)
	relayServer := httptest.NewServer(registryHandler(t, defaultEngine, nil))
	defer relayServer.Close()

	resp, err := http.Get(relayServer.URL + "/@missing/http://unused.example/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound || strings.Contains(string(body), "default") {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
}

func TestHandler_ProfileURLRewriteKeepsSelectedEngine(t *testing.T) {
	t.Parallel()

	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("upstream-b"))
	}))
	defer upstreamB.Close()
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("original upstream should not be reached")
	}))
	defer upstreamA.Close()

	profileA := `
		function onRequest(req) { req.url = "` + upstreamB.URL + `/"; }
		function onResponse(resp, req) { resp.headers["X-Engine"] = "a"; }
	`
	profileB := `function onResponse(resp, req) { resp.headers["X-Engine"] = "b"; }`
	relayServer := httptest.NewServer(registryHandler(t, nil, map[string]string{"a": profileA, "b": profileB}))
	defer relayServer.Close()

	resp, err := http.Get(relayServer.URL + "/@a/" + upstreamA.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Engine"); got != "a" {
		t.Fatalf("onResponse engine=%q, want a", got)
	}
}

func TestHandler_ProfileWithoutHooksUsesPlainRelay(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain"))
	}))
	defer upstream.Close()
	relayServer := httptest.NewServer(registryHandler(t, nil, map[string]string{"noop": `var enabled = true;`}))
	defer relayServer.Close()

	resp, err := http.Get(relayServer.URL + "/@noop/" + upstream.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "plain" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
}
