package relay

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
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
	dumpRequest bool
	dumpScope   DumpScope
	maskAuth    bool
	dumpSeq     atomic.Uint64
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
	return &Handler{
		client:      client,
		logger:      logger,
		dumpRequest: dumpRequest,
		dumpScope:   dumpScope,
		maskAuth:    maskAuth,
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
				h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, "", http.StatusInternalServerError, time.Since(start).Milliseconds(), err.Error())
				return
			}
		}
	}

	targetURL, err := parseTargetURL(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, "", http.StatusBadRequest, time.Since(start).Milliseconds(), err.Error())
		return
	}

	upstreamReq, err := h.buildUpstreamRequest(r, targetURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build upstream request")
		h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, targetURL.String(), http.StatusInternalServerError, time.Since(start).Milliseconds(), err.Error())
		return
	}

	resp, err := h.client.Do(upstreamReq)
	if err != nil {
		status, msg := mapUpstreamError(err)
		writeError(w, status, msg)
		h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, targetURL.String(), status, time.Since(start).Milliseconds(), err.Error())
		return
	}
	defer resp.Body.Close()

	if h.dumpRequest && h.dumpScope.HasResp() {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			writeError(w, http.StatusBadGateway, "failed to read upstream response")
			h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, targetURL.String(), http.StatusBadGateway, time.Since(start).Milliseconds(), readErr.Error())
			return
		}
		if err := h.logUpstreamResponse(dumpID, resp, respBody); err != nil {
			h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, targetURL.String(), resp.StatusCode, time.Since(start).Milliseconds(), err.Error())
		}

		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(respBody); err != nil {
			h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, targetURL.String(), resp.StatusCode, time.Since(start).Milliseconds(), err.Error())
			return
		}
	} else {
		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, copyErr := io.Copy(w, resp.Body)
		if copyErr != nil {
			h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, targetURL.String(), resp.StatusCode, time.Since(start).Milliseconds(), copyErr.Error())
			return
		}
	}

	h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, targetURL.String(), resp.StatusCode, time.Since(start).Milliseconds(), "")
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

	target, err := url.Parse(raw)
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

func (h *Handler) buildUpstreamRequest(r *http.Request, targetURL *url.URL) (*http.Request, error) {
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), r.Body)
	if err != nil {
		return nil, err
	}

	upstreamReq.Header = cloneHeader(r.Header)
	removeHopByHopHeaders(upstreamReq.Header)
	setForwardedHeaders(upstreamReq, r)
	upstreamReq.ContentLength = r.ContentLength
	return upstreamReq, nil
}

func setForwardedHeaders(upstreamReq *http.Request, originalReq *http.Request) {
	clientIP := clientIPFromRemoteAddr(originalReq.RemoteAddr)
	if clientIP != "" {
		if prior := originalReq.Header.Get("X-Forwarded-For"); prior != "" {
			upstreamReq.Header.Set("X-Forwarded-For", prior+", "+clientIP)
		} else {
			upstreamReq.Header.Set("X-Forwarded-For", clientIP)
		}
	}
	upstreamReq.Header.Set("X-Forwarded-Proto", "http")
	upstreamReq.Header.Set("X-Forwarded-Host", originalReq.Host)
}

func clientIPFromRemoteAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return ""
	}
	return host
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
