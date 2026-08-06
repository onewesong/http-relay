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
	s := newStore(meta)
	if opts.MaxTransactionsPerNamespace > 0 {
		s.maxTxnsPerNamespace = opts.MaxTransactionsPerNamespace
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("web: embedded static assets missing: " + err.Error())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", auth.handleLogin)
	mux.HandleFunc("POST /login", auth.handleLogin)
	mux.HandleFunc("POST /logout", auth.handleLogout)
	mux.Handle("GET /", existingStatic(static, auth.protect(http.FileServerFS(static))))
	mux.Handle("GET /events", auth.protect(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/transactions", auth.protect(http.HandlerFunc(s.handleTransactions)))
	mux.Handle("DELETE /api/transactions", auth.protect(http.HandlerFunc(s.handleClearTransactions)))
	newAdminServer(s, auth, static, opts.Logger).routes(mux)

	return namespaceRouter(mux, auth), &webReporter{store: s}
}

func existingStatic(static fs.FS, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "."
		}
		if _, err := fs.Stat(static, name); err != nil {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *store) handleTransactions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.transactions(namespaceFromRequest(r)))
}

func (s *store) handleClearTransactions(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r, authFromRequest(r).trustForwardedHeaders) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	if !discardLimitedBody(w, r, maxWriteBodyBytes) {
		return
	}
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

func originalPathFromRequest(r *http.Request) string {
	uri := originalURIFromRequest(r)
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		return uri[:i]
	}
	return uri
}

// namespaceRouter maps /namespace/{namespace}/... onto the canonical Web UI
// routes while retaining the namespace in context for filtering and auth.
func namespaceRouter(next http.Handler, auth *authenticator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped := r.URL.EscapedPath()
		if (escaped == "/admin" || strings.HasPrefix(escaped, "/admin/")) && (!auth.jwtEnabled() || !auth.jwt.AdminEnabled) {
			http.NotFound(w, r)
			return
		}
		const prefix = "/namespace/"
		if !strings.HasPrefix(escaped, prefix) {
			next.ServeHTTP(w, r)
			return
		}
		rest := strings.TrimPrefix(escaped, prefix)
		segment, remainder, hasSlash := strings.Cut(rest, "/")
		decoded, err := url.PathUnescape(segment)
		if err != nil || !relay.ValidNamespace(decoded) || decoded == "" {
			http.NotFound(w, r)
			return
		}
		if !hasSlash {
			location := r.URL.Path + "/"
			if r.URL.RawQuery != "" {
				location += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, location, http.StatusTemporaryRedirect)
			return
		}

		ctx := context.WithValue(r.Context(), namespaceContextKey{}, decoded)
		ctx = context.WithValue(ctx, originalURIContextKey{}, r.URL.RequestURI())
		clone := r.Clone(ctx)
		decodedRemainder, err := url.PathUnescape(remainder)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		clone.URL.Path = "/" + decodedRemainder
		clone.URL.RawPath = ""
		if remainder == "" {
			clone.URL.Path = "/"
		}
		next.ServeHTTP(w, clone)
	})
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
