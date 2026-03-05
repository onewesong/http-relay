package relay

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
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
	client *http.Client
	logger *log.Logger
}

func NewHandler(client *http.Client, logger *log.Logger) *Handler {
	return &Handler{client: client, logger: logger}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

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

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, copyErr := io.Copy(w, resp.Body)
	if copyErr != nil {
		h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, targetURL.String(), resp.StatusCode, time.Since(start).Milliseconds(), copyErr.Error())
		return
	}

	h.logger.Printf("method=%s target=%q status=%d duration_ms=%d err=%q", r.Method, targetURL.String(), resp.StatusCode, time.Since(start).Milliseconds(), "")
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
