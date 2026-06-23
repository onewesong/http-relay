// Package web renders relayed traffic in a browser. It implements a
// relay.Reporter that accumulates transactions and streams them to connected
// pages over Server-Sent Events; the static front-end does JSON pretty-printing
// and folding client-side.
package web

import (
	"encoding/base64"
	"time"
	"unicode/utf8"
)

// maxBodyBytes caps how much of a body is sent to the browser, so a huge payload
// can't bloat the event stream. The full size is always reported via Body.Size.
const maxBodyBytes = 256 * 1024

// Meta is the relay configuration shown in the page header.
type Meta struct {
	Addr    string `json:"addr"`
	Mode    string `json:"mode"`
	Proxy   string `json:"proxy"`
	Timeout string `json:"timeout"`
	Version string `json:"version"`
}

// Body is a body captured for display. Text is set for valid UTF-8 payloads;
// otherwise Base64 holds the raw bytes. Either way the content is capped at
// maxBodyBytes and Truncated records whether anything was dropped.
type Body struct {
	Size      int    `json:"size"`
	Text      string `json:"text,omitempty"`
	Base64    string `json:"base64,omitempty"`
	Truncated bool   `json:"truncated"`
}

// newBody encodes raw body bytes for transport, capping at maxBodyBytes.
func newBody(raw []byte) *Body {
	if len(raw) == 0 {
		return nil
	}
	b := &Body{Size: len(raw)}
	shown := raw
	if len(shown) > maxBodyBytes {
		shown = shown[:maxBodyBytes]
		b.Truncated = true
	}
	if utf8.Valid(shown) {
		b.Text = string(shown)
	} else {
		b.Base64 = base64.StdEncoding.EncodeToString(shown)
	}
	return b
}

// Transaction accumulates everything known about one relayed request, keyed by
// seq. It mirrors the TUI's txn but is JSON-serializable for the browser.
type Transaction struct {
	Seq        uint64    `json:"seq"`
	At         time.Time `json:"at"`
	Method     string    `json:"method"`
	Target     string    `json:"target"`
	Status     int       `json:"status"`
	DurationMs int64     `json:"durationMs"`
	Bytes      int64     `json:"bytes"`
	Err        string    `json:"err,omitempty"`

	ReqHead  string `json:"reqHead,omitempty"`
	ReqBody  *Body  `json:"reqBody,omitempty"`
	RespHead string `json:"respHead,omitempty"`
	RespBody *Body  `json:"respBody,omitempty"`

	HasReq  bool `json:"hasReq"`
	HasResp bool `json:"hasResp"`
	Done    bool `json:"done"`
}

// event is the envelope for one SSE message. Exactly one payload field is set.
type event struct {
	Type string       `json:"type"` // "meta" | "txn"
	Meta *Meta        `json:"meta,omitempty"`
	Txn  *Transaction `json:"txn,omitempty"`
}
