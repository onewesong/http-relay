package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/onewesong/http-relay/internal/authjwt"
)

// handleEvents streams transactions to one browser over Server-Sent Events. It
// first sends the relay meta and retained history newest-first, then live
// updates until the client disconnects.
func (s *store) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (e.g. nginx)

	ch, replay, meta, cancel := s.subscribe(namespaceFromRequest(r))
	defer cancel()
	auth := authFromRequest(r)
	meta.AuthEnabled = auth.enabled

	if data, err := json.Marshal(event{Type: "meta", Meta: &meta}); err == nil {
		writeSSE(w, data)
	}
	for _, data := range replay {
		writeSSE(w, data)
	}
	flusher.Flush()

	ticker := time.NewTicker(keepAlive)
	defer ticker.Stop()
	var expiry <-chan time.Time
	var expiryTimer *time.Timer
	if auth.expires != nil {
		delay := time.Until(auth.expires.Add(authjwt.ClockSkew))
		if delay <= 0 {
			return
		}
		expiryTimer = time.NewTimer(delay)
		expiry = expiryTimer.C
		defer expiryTimer.Stop()
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-expiry:
			return
		case data, open := <-ch:
			if !open {
				return // dropped as a slow consumer
			}
			writeSSE(w, data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// writeSSE emits one SSE data frame. JSON payloads never contain newlines, so a
// single data: line is sufficient.
func writeSSE(w http.ResponseWriter, data []byte) {
	fmt.Fprintf(w, "data: %s\n\n", data)
}
