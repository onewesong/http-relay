package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
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

	return mux, &webReporter{store: s}
}

func (s *store) handleTransactions(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.transactions())
}

// webReporter forwards captured traffic into the store, mirroring the merge by
// seq the TUI does. Each call broadcasts the updated transaction.
type webReporter struct {
	store *store
}

func (r *webReporter) RequestDump(seq uint64, head string, body []byte, _, _ string) {
	r.store.mutate(seq, func(t *Transaction) {
		t.HasReq = true
		t.ReqHead = head
		t.ReqBody = newBody(body)
	})
}

func (r *webReporter) ResponseDump(seq uint64, head string, body []byte, _ string) {
	r.store.mutate(seq, func(t *Transaction) {
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
	r.store.mutate(seq, func(t *Transaction) {
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
