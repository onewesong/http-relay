package relay

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"TE":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

type Handler struct {
	client      *http.Client
	logger      *log.Logger
	palette     Palette
	targetMode  TargetMode
	headerRules []HeaderRule
	dumpRequest bool
	dumpScope   DumpScope
	maskAuth    bool
	dumpSeq     atomic.Uint64
}

type HandlerOptions struct {
	TargetMode  TargetMode
	HeaderRules []HeaderRule
	DumpRequest bool
	DumpScope   DumpScope
	MaskAuth    bool
	Palette     Palette
}

type DumpScope uint8

const (
	DumpScopeNone DumpScope = 0
	DumpScopeReq  DumpScope = 1 << iota
	DumpScopeResp
)

func (s DumpScope) HasReq() bool {
	return s&DumpScopeReq != 0
}

func (s DumpScope) HasResp() bool {
	return s&DumpScopeResp != 0
}

func (s DumpScope) String() string {
	switch {
	case s.HasReq() && s.HasResp():
		return "req,resp"
	case s.HasReq():
		return "req"
	case s.HasResp():
		return "resp"
	default:
		return "none"
	}
}

func ParseDumpScope(raw string) (DumpScope, bool) {
	if strings.TrimSpace(raw) == "" {
		return DumpScopeReq | DumpScopeResp, true
	}

	parts := strings.Split(strings.ToLower(raw), ",")
	var scope DumpScope
	valid := true
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch part {
		case "":
			continue
		case "req":
			scope |= DumpScopeReq
		case "resp":
			scope |= DumpScopeResp
		default:
			valid = false
		}
	}

	if scope == DumpScopeNone {
		return DumpScopeReq | DumpScopeResp, false
	}

	return scope, valid
}

func NewHandler(client *http.Client, logger *log.Logger, dumpRequest bool, dumpScope DumpScope, maskAuth bool) *Handler {
	return NewHandlerWithOptions(client, logger, HandlerOptions{
		TargetMode:  DefaultTargetMode(),
		DumpRequest: dumpRequest,
		DumpScope:   dumpScope,
		MaskAuth:    maskAuth,
	})
}

func NewHandlerWithOptions(client *http.Client, logger *log.Logger, opts HandlerOptions) *Handler {
	if opts.DumpScope == DumpScopeNone {
		opts.DumpScope = DumpScopeReq | DumpScopeResp
	}

	return &Handler{
		client:      client,
		logger:      logger,
		palette:     opts.Palette,
		targetMode:  opts.TargetMode,
		headerRules: append([]HeaderRule(nil), opts.HeaderRules...),
		dumpRequest: opts.DumpRequest,
		dumpScope:   opts.DumpScope,
		maskAuth:    opts.MaskAuth,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	dumpID := uint64(0)

	if h.dumpRequest {
		dumpID = h.dumpSeq.Add(1)
		if h.dumpScope.HasReq() {
			if err := h.logIncomingRequest(dumpID, r); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to read inbound request")
				h.logAccess(dumpID, r.Method, "", http.StatusInternalServerError, time.Since(start), 0, err.Error())
				return
			}
		}
	}

	targetURL, err := h.targetMode.TargetURL(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		h.logAccess(dumpID, r.Method, "", http.StatusBadRequest, time.Since(start), 0, err.Error())
		return
	}

	upstreamReq, err := h.buildUpstreamRequest(r, targetURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build upstream request")
		h.logAccess(dumpID, r.Method, targetURL.String(), http.StatusInternalServerError, time.Since(start), 0, err.Error())
		return
	}

	resp, err := h.client.Do(upstreamReq)
	if err != nil {
		status, msg := mapUpstreamError(err)
		writeError(w, status, msg)
		h.logAccess(dumpID, r.Method, targetURL.String(), status, time.Since(start), 0, err.Error())
		return
	}
	defer resp.Body.Close()

	var bytesWritten int64
	if h.dumpRequest && h.dumpScope.HasResp() {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			writeError(w, http.StatusBadGateway, "failed to read upstream response")
			h.logAccess(dumpID, r.Method, targetURL.String(), http.StatusBadGateway, time.Since(start), 0, readErr.Error())
			return
		}
		if err := h.logUpstreamResponse(dumpID, resp, respBody); err != nil {
			h.logAccess(dumpID, r.Method, targetURL.String(), resp.StatusCode, time.Since(start), 0, err.Error())
		}

		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		n, err := w.Write(respBody)
		bytesWritten = int64(n)
		if err != nil {
			h.logAccess(dumpID, r.Method, targetURL.String(), resp.StatusCode, time.Since(start), bytesWritten, err.Error())
			return
		}
	} else {
		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		n, copyErr := io.Copy(w, resp.Body)
		bytesWritten = n
		if copyErr != nil {
			h.logAccess(dumpID, r.Method, targetURL.String(), resp.StatusCode, time.Since(start), bytesWritten, copyErr.Error())
			return
		}
	}

	h.logAccess(dumpID, r.Method, targetURL.String(), resp.StatusCode, time.Since(start), bytesWritten, "")
}

// logAccess emits one access-log entry. With color enabled it renders a layered,
// colorized line; otherwise it falls back to the machine-readable key=value form.
func (h *Handler) logAccess(seq uint64, method, target string, status int, dur time.Duration, bytes int64, errMsg string) {
	p := h.palette
	if !p.Enabled() {
		h.logger.Printf("method=%s target=%q status=%d duration_ms=%d bytes=%d err=%q",
			method, target, status, dur.Milliseconds(), bytes, errMsg)
		return
	}

	var b strings.Builder
	b.WriteString(p.Timestamp(time.Now()))
	b.WriteByte(' ')
	if seq > 0 {
		b.WriteString(p.Dim(fmt.Sprintf("#%d", seq)))
		b.WriteByte(' ')
	}
	fmt.Fprintf(&b, "%s %s %s %s",
		p.Method(fmt.Sprintf("%-6s", method)),
		p.Status(status),
		p.Duration(dur),
		p.Dim(fmt.Sprintf("%8s", formatBytes(bytes))),
	)
	if target != "" {
		b.WriteByte(' ')
		b.WriteString(p.URL(target))
	}
	if errMsg != "" {
		b.WriteString("  ")
		b.WriteString(p.Error("✗ " + errMsg))
	}
	h.logger.Print(b.String())
}

func (h *Handler) logIncomingRequest(seq uint64, r *http.Request) error {
	dumpReq := new(http.Request)
	*dumpReq = *r
	dumpReq.Header = cloneHeader(r.Header)
	if h.maskAuth {
		maskAuthHeaders(dumpReq.Header)
	}

	head, err := httputil.DumpRequest(dumpReq, false)
	if err != nil {
		return fmt.Errorf("dump request headers: %w", err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	if h.palette.Enabled() {
		h.logger.Print(h.renderDump(true, seq, string(head), body,
			fmt.Sprintf("remote=%s host=%s", r.RemoteAddr, r.Host)))
		return nil
	}

	h.logger.Printf(
		"---- REQUEST DUMP BEGIN id=%d remote=%s host=%s ----\n%s%s\n---- REQUEST DUMP END id=%d body_bytes=%d ----",
		seq,
		r.RemoteAddr,
		r.Host,
		string(head),
		string(body),
		seq,
		len(body),
	)
	return nil
}

// renderDump builds a colored, indented dump block for one request or response.
func (h *Handler) renderDump(isRequest bool, seq uint64, head string, body []byte, meta string) string {
	p := h.palette
	arrow, label := p.RespArrow(), "response"
	if isRequest {
		arrow, label = p.ReqArrow(), "request"
	}

	var b strings.Builder
	b.WriteString(p.Timestamp(time.Now()))
	b.WriteByte(' ')
	b.WriteString(p.Dim(fmt.Sprintf("#%d", seq)))
	fmt.Fprintf(&b, " %s %s  %s", arrow, p.Bold(label), p.Dim(meta))

	lines := strings.Split(strings.TrimRight(head, "\r\n"), "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if i == 0 {
			b.WriteString("\n    " + p.Bold(line)) // request/status line
			continue
		}
		b.WriteString("\n    " + p.Header(line))
	}

	if len(body) > 0 {
		b.WriteString("\n    " + p.Dim(fmt.Sprintf("body=%s", formatBytes(int64(len(body))))))
		for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
			b.WriteString("\n    " + line)
		}
	}
	return b.String()
}

func maskAuthHeaders(h http.Header) {
	for _, key := range []string{"Authorization", "Proxy-Authorization"} {
		values := h.Values(key)
		if len(values) == 0 {
			continue
		}
		masked := make([]string, 0, len(values))
		for _, v := range values {
			masked = append(masked, maskAuthorizationLike(v))
		}
		h[key] = masked
	}

	for _, key := range []string{"Cookie", "X-Api-Key", "X-Auth-Token"} {
		if h.Get(key) != "" {
			h.Set(key, "<redacted>")
		}
	}
}

func maskAuthorizationLike(v string) string {
	parts := strings.Fields(v)
	if len(parts) >= 2 {
		return parts[0] + " <redacted>"
	}
	return "<redacted>"
}

func (h *Handler) logUpstreamResponse(seq uint64, resp *http.Response, body []byte) error {
	respForDump := new(http.Response)
	*respForDump = *resp
	respForDump.Body = io.NopCloser(bytes.NewReader(body))

	head, err := httputil.DumpResponse(respForDump, false)
	if err != nil {
		return fmt.Errorf("dump response headers: %w", err)
	}

	if h.palette.Enabled() {
		h.logger.Print(h.renderDump(false, seq, string(head), body,
			fmt.Sprintf("status=%s", resp.Status)))
		return nil
	}

	h.logger.Printf(
		"---- RESPONSE DUMP BEGIN id=%d status=%s ----\n%s%s\n---- RESPONSE DUMP END id=%d body_bytes=%d ----",
		seq,
		resp.Status,
		string(head),
		string(body),
		seq,
		len(body),
	)
	return nil
}

func parseTargetURL(r *http.Request) (*url.URL, error) {
	raw := strings.TrimPrefix(r.RequestURI, "/")
	if strings.TrimSpace(raw) == "" {
		raw = strings.TrimPrefix(r.URL.RequestURI(), "/")
	}
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("missing target URL in path")
	}

	target, err := url.Parse(normalizeTargetURL(raw))
	if err != nil {
		return nil, errors.New("invalid target URL")
	}

	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("target URL scheme must be http or https")
	}
	if target.Host == "" {
		return nil, errors.New("target URL host is required")
	}

	return target, nil
}

func normalizeTargetURL(raw string) string {
	lower := strings.ToLower(raw)
	for _, scheme := range []string{"http", "https"} {
		prefix := scheme + ":/"
		if strings.HasPrefix(lower, prefix) && !strings.HasPrefix(lower, prefix+"/") {
			return raw[:len(scheme)+1] + "//" + raw[len(prefix):]
		}
	}
	return raw
}

func (h *Handler) buildUpstreamRequest(r *http.Request, targetURL *url.URL) (*http.Request, error) {
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), r.Body)
	if err != nil {
		return nil, err
	}

	upstreamReq.Header = cloneHeader(r.Header)
	upstreamReq.Host = targetURL.Host
	removeHopByHopHeaders(upstreamReq.Header)
	ApplyHeaderRules(upstreamReq, h.headerRules)
	upstreamReq.ContentLength = r.ContentLength
	return upstreamReq, nil
}

func cloneHeader(h http.Header) http.Header {
	cloned := make(http.Header, len(h))
	for k, vals := range h {
		copied := make([]string, len(vals))
		copy(copied, vals)
		cloned[k] = copied
	}
	return cloned
}

func removeHopByHopHeaders(h http.Header) {
	connectionValues := h.Values("Connection")
	for _, v := range connectionValues {
		for _, token := range strings.Split(v, ",") {
			token = strings.TrimSpace(token)
			if token != "" {
				h.Del(token)
			}
		}
	}
	for key := range hopByHopHeaders {
		h.Del(key)
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, vals := range src {
		for _, v := range vals {
			dst.Add(key, v)
		}
	}
	removeHopByHopHeaders(dst)
}
