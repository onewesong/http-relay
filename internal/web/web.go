package web

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onewesong/http-relay/internal/relay"
)

//go:embed static
var staticFS embed.FS

// New builds the web UI handler and the relay.Reporter that feeds it. The
// reporter is safe to call from request-handling goroutines; mount the handler
// on a listener separate from the relay's proxy port.
func New(meta Meta, options ...Options) (http.Handler, relay.Reporter) {
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}
	auth := newAuthenticator(opts)
	meta.AuthEnabled = auth.enabled()
	s := newStore(meta)

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("web: embedded static assets missing: " + err.Error())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", auth.handleLogin)
	mux.HandleFunc("POST /login", auth.handleLogin)
	mux.HandleFunc("POST /logout", auth.handleLogout)
	mux.Handle("GET /", auth.protect(http.FileServerFS(static)))
	mux.Handle("GET /events", auth.protect(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/transactions", auth.protect(http.HandlerFunc(s.handleTransactions)))
	mux.Handle("DELETE /api/transactions", auth.protect(http.HandlerFunc(s.handleClearTransactions)))

	return namespaceRouter(mux), &webReporter{store: s}
}

func (s *store) handleTransactions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.transactions(namespaceFromRequest(r)))
}

func (s *store) handleClearTransactions(w http.ResponseWriter, r *http.Request) {
	s.clear(namespaceFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

type namespaceContextKey struct{}
type originalURIContextKey struct{}

func namespaceFromRequest(r *http.Request) string {
	namespace, _ := r.Context().Value(namespaceContextKey{}).(string)
	return namespace
}

func originalURIFromRequest(r *http.Request) string {
	if uri, ok := r.Context().Value(originalURIContextKey{}).(string); ok {
		return uri
	}
	return r.URL.RequestURI()
}

// namespaceRouter maps /{namespace}/... onto the existing Web UI routes while
// retaining the namespace in context for API and SSE filtering.
func namespaceRouter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if isRootWebPath(path) {
			next.ServeHTTP(w, r)
			return
		}

		trimmed := strings.TrimPrefix(path, "/")
		namespace, remainder, hasSlash := strings.Cut(trimmed, "/")
		decoded, err := url.PathUnescape(namespace)
		if err != nil || !relay.ValidNamespace(decoded) || decoded == "" {
			http.NotFound(w, r)
			return
		}
		if !hasSlash {
			location := path + "/"
			if r.URL.RawQuery != "" {
				location += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, location, http.StatusTemporaryRedirect)
			return
		}

		ctx := context.WithValue(r.Context(), namespaceContextKey{}, decoded)
		ctx = context.WithValue(ctx, originalURIContextKey{}, r.URL.RequestURI())
		clone := r.Clone(ctx)
		clone.URL.Path = "/" + remainder
		if remainder == "" {
			clone.URL.Path = "/"
		}
		next.ServeHTTP(w, clone)
	})
}

func isRootWebPath(path string) bool {
	if path == "/" || path == "/login" || path == "/logout" || path == "/events" || path == "/api/transactions" ||
		path == "/index.html" || path == "/app.js" || path == "/style.css" || path == "/conversation.mjs" {
		return true
	}
	return strings.HasPrefix(path, "/preview/")
}

// webReporter forwards captured traffic into the store, mirroring the merge by
// seq the TUI does. Each call broadcasts the updated transaction.
type webReporter struct {
	store *store
}

func (r *webReporter) RequestDump(seq uint64, namespace, head string, body []byte, _, _ string) {
	r.store.mutate(seq, namespace, func(t *Transaction) {
		t.HasReq = true
		t.ReqHead = head
		t.ReqBody = newBody(body)
	})
}

func (r *webReporter) ResponseDump(seq uint64, namespace, head string, body []byte, _ string) {
	r.store.mutate(seq, namespace, func(t *Transaction) {
		t.HasResp = true
		t.RespHead = head
		t.RespBody = newBody(body)
	})
}

func (r *webReporter) Access(rec relay.AccessRecord) {
	seq := rec.Seq
	if seq == 0 {
		seq = r.store.synthID()
	}
	r.store.mutate(seq, rec.Namespace, func(t *Transaction) {
		t.Method = rec.Method
		t.Target = rec.Target
		t.Status = rec.Status
		t.DurationMs = rec.Duration.Milliseconds()
		t.Bytes = rec.Bytes
		t.Err = rec.Err
		t.Done = true
	})
}

// keepAlive is the interval between SSE comment pings that hold the connection
// open through intermediary proxies.
const keepAlive = 25 * time.Second
