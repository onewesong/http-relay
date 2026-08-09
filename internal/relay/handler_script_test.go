package relay

import (
	"encoding/json"
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
	builtinplugins "github.com/onewesong/http-relay/plugins"
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

type responseCaptureReporter struct {
	body []byte
	head string
}

func (r *responseCaptureReporter) RequestDump(uint64, string, string, []byte, string, string) {}
func (r *responseCaptureReporter) ResponseDump(_ uint64, _ string, head string, body []byte, _ string) {
	r.head = head
	r.body = append([]byte(nil), body...)
}
func (r *responseCaptureReporter) Access(AccessRecord) {}

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

func TestHandler_StreamResponseEventHook(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: one\n\ndata: two\n\n")
	}))
	defer upstream.Close()

	e := scriptEngine(t, `
		function onResponseStart(resp, req) { resp.headers["X-Stream-Hook"] = "yes"; return { n: 0 }; }
		function onResponseEvent(event, state, req) { state.n++; return { data: state.n + ":" + event.data }; }
		function onResponseEnd(state, req) { return { data: "done" }; }
	`)
	capture := &responseCaptureReporter{}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	relayServer := httptest.NewServer(NewHandlerWithOptions(client, log.New(io.Discard, "", 0), HandlerOptions{
		TargetMode: DefaultTargetMode(), DumpRequest: true, DumpScope: DumpScopeReq | DumpScopeResp, ScriptEngine: e, Reporter: capture,
	}))
	defer relayServer.Close()

	resp, err := http.Get(relayServer.URL + "/" + upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if got, want := string(body), "data: 1:one\n\ndata: 2:two\n\ndata: done\n\n"; got != want {
		t.Fatalf("stream body = %q, want %q", got, want)
	}
	if resp.Header.Get("X-Stream-Hook") != "yes" {
		t.Fatalf("start hook did not modify response headers: %v", resp.Header)
	}
	if resp.Header.Get("Content-Length") != "" {
		t.Fatalf("stream should not have Content-Length: %q", resp.Header.Get("Content-Length"))
	}
	if got, want := string(capture.body), "data: 1:one\n\ndata: 2:two\n\ndata: done\n\n"; got != want {
		t.Fatalf("captured stream = %q, want %q", got, want)
	}
	if !strings.Contains(capture.head, "Content-Type: text/event-stream") {
		t.Fatalf("captured response header = %q", capture.head)
	}
}

func TestHandler_ChatCompletionsResponsesStreamExample(t *testing.T) {
	t.Parallel()
	source, err := builtinplugins.ReadBuiltIn("rewrite.chat-completions-to-responses.js")
	if err != nil {
		t.Fatal(err)
	}
	e, err := script.New(script.Options{Path: "stream example", Source: string(source)})
	if err != nil {
		t.Fatalf("compile stream example: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Stream {
			t.Errorf("upstream stream body: %+v, err=%v", body, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hi\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-test\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-test\",\"usage\":{\"input_tokens\":2,\"output_tokens\":1,\"total_tokens\":3}}}\n\n")
	}))
	defer upstream.Close()
	relayServer := httptest.NewServer(scriptHandler(t, e))
	defer relayServer.Close()

	req, err := http.NewRequest(http.MethodPost, relayServer.URL+"/"+upstream.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"Hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"role":"assistant"`) || !strings.Contains(text, `"content":"Hi"`) || !strings.Contains(text, "data: [DONE]") {
		t.Fatalf("status=%d stream=%q", resp.StatusCode, text)
	}
}

func TestHandler_ChatCompletionsResponsesBuiltInNonStream(t *testing.T) {
	t.Parallel()
	source, err := builtinplugins.ReadBuiltIn("rewrite.chat-completions-to-responses.js")
	if err != nil {
		t.Fatal(err)
	}
	e, err := script.New(script.Options{Path: "built-in compatibility", Source: string(source)})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","model":"gpt-test","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hi"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`)
	}))
	defer upstream.Close()
	relayServer := httptest.NewServer(scriptHandler(t, e))
	defer relayServer.Close()

	req, err := http.NewRequest(http.MethodPost, relayServer.URL+"/"+upstream.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"Hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"object":"chat.completion"`) || !strings.Contains(string(body), `"content":"Hi"`) {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
}

func TestHandler_AnthropicMessagesResponsesBuiltInStream(t *testing.T) {
	t.Parallel()
	source, err := builtinplugins.ReadBuiltIn("rewrite.anthropic-messages-to-responses.js")
	if err != nil {
		t.Fatal(err)
	}
	e, err := script.New(script.Options{Path: "anthropic compatibility", Source: string(source)})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Anthropic-Version") != "" {
			t.Errorf("upstream path=%q headers=%v", r.URL.Path, r.Header)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-test\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\nevent: response.output_text.delta\ndata: {\"item_id\":\"item_1\",\"delta\":\"Hi\"}\n\nevent: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n")
	}))
	defer upstream.Close()
	capture := &responseCaptureReporter{}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	relayServer := httptest.NewServer(NewHandlerWithOptions(client, log.New(io.Discard, "", 0), HandlerOptions{
		TargetMode: DefaultTargetMode(), DumpRequest: true, DumpScope: DumpScopeReq | DumpScopeResp, ScriptEngine: e, Reporter: capture,
	}))
	defer relayServer.Close()

	req, err := http.NewRequest(http.MethodPost, relayServer.URL+"/"+upstream.URL+"/v1/messages", strings.NewReader(`{"model":"gpt-test","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"Hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", "test-key")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"message_start"`) || !strings.Contains(string(body), `"text_delta"`) || !strings.Contains(string(body), `"message_stop"`) {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	if !strings.Contains(string(capture.body), `"message_stop"`) || !strings.Contains(capture.head, "Content-Type: text/event-stream") {
		t.Fatalf("captured head=%q body=%q", capture.head, capture.body)
	}
}
