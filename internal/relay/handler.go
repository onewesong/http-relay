package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/onewesong/http-relay/internal/script"
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
	client               *http.Client
	targetPolicy         *TargetPolicy
	reporter             Reporter
	targetMode           TargetMode
	headerRules          []HeaderRule
	dumpRequest          bool
	dumpScope            DumpScope
	maskAuth             bool
	scripts              *script.Registry
	maxSSEEventBytes     int
	maxSSEEventsResponse int
	dumpSeq              atomic.Uint64
}

type HandlerOptions struct {
	TargetMode TargetMode
	// TargetPolicy restricts client-selected regular-mode targets. Nil retains
	// the unrestricted behavior for embedders that do not opt in.
	TargetPolicy *TargetPolicy
	HeaderRules  []HeaderRule
	DumpRequest  bool
	DumpScope    DumpScope
	MaskAuth     bool
	Palette      Palette
	// Reporter overrides where captured traffic is rendered. When nil, a
	// log-based reporter is built from the logger and Palette.
	Reporter Reporter
	// ScriptEngine, when non-nil, runs onRequest/onResponse hooks that may
	// rewrite or short-circuit traffic. Nil disables scripting entirely.
	ScriptEngine *script.Engine
	// ScriptRegistry selects named path-bound rewrite profiles. When nil, a
	// registry containing only ScriptEngine is created for compatibility.
	ScriptRegistry *script.Registry
	// MaxSSEEventBytes caps a complete upstream SSE frame before it reaches a
	// streaming response hook. Zero uses DefaultMaxSSEEventBytes.
	MaxSSEEventBytes int
	// MaxSSEEventsResponse caps the number of input SSE frames handled for one
	// response. Zero uses DefaultMaxSSEEventsResponse.
	MaxSSEEventsResponse int
}

const DefaultMaxSSEEventBytes = 1 << 20
const DefaultMaxSSEEventsResponse = 100000

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
	if opts.MaxSSEEventBytes <= 0 {
		opts.MaxSSEEventBytes = DefaultMaxSSEEventBytes
	}
	if opts.MaxSSEEventsResponse <= 0 {
		opts.MaxSSEEventsResponse = DefaultMaxSSEEventsResponse
	}

	reporter := opts.Reporter
	if reporter == nil {
		reporter = newLogReporter(logger, opts.Palette)
	}

	registry := opts.ScriptRegistry
	if registry == nil {
		registry, _ = script.NewRegistry(opts.ScriptEngine, nil)
	}
	configuredClient := *client
	previousRedirect := configuredClient.CheckRedirect
	configuredClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if opts.TargetPolicy != nil {
			if err := opts.TargetPolicy.Validate(req.Context(), req.URL); err != nil {
				return err
			}
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	return &Handler{
		client:               &configuredClient,
		targetPolicy:         opts.TargetPolicy,
		reporter:             reporter,
		targetMode:           opts.TargetMode,
		headerRules:          append([]HeaderRule(nil), opts.HeaderRules...),
		dumpRequest:          opts.DumpRequest,
		dumpScope:            opts.DumpScope,
		maskAuth:             opts.MaskAuth,
		scripts:              registry,
		maxSSEEventBytes:     opts.MaxSSEEventBytes,
		maxSSEEventsResponse: opts.MaxSSEEventsResponse,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resolved, err := h.targetMode.Resolve(r)
	if err != nil {
		h.reject(w, 0, "", "", r.Method, "", http.StatusBadRequest, err.Error())
		return
	}
	if h.targetPolicy != nil && h.targetMode.kind == TargetModeAbsoluteURL {
		if err := h.targetPolicy.Validate(r.Context(), resolved.URL); err != nil {
			status, msg := mapUpstreamError(err)
			h.reject(w, 0, resolved.Namespace, resolved.RewriteProfile, r.Method, resolved.URL.String(), status, msg)
			return
		}
		r = r.WithContext(withTargetPolicy(r.Context(), h.targetPolicy))
	}
	engine := h.scripts.Default()
	if resolved.RewriteProfile != "" {
		var ok bool
		engine, ok = h.scripts.Lookup(resolved.RewriteProfile)
		if !ok {
			h.reject(w, 0, resolved.Namespace, resolved.RewriteProfile, r.Method, resolved.URL.String(), http.StatusNotFound, "rewrite profile not found")
			return
		}
	}
	if engine != nil && (engine.HasRequestHook() || engine.HasResponseHook() || engine.HasResponseEventHook()) {
		h.serveScripted(w, r, resolved, engine)
		return
	}
	h.servePlain(w, r, resolved)
}

// reject writes a relay-generated error and, when dumping is enabled, exposes
// the exact response body through the configured reporter as well.
func (h *Handler) reject(w http.ResponseWriter, seq uint64, namespace, rewriteProfile, method, target string, status int, msg string) {
	if h.dumpRequest && seq == 0 {
		seq = h.dumpSeq.Add(1)
	}
	body := errorBody(msg)
	if h.dumpRequest && h.dumpScope.HasResp() {
		h.dumpScriptedResponse(seq, namespace, status, fmt.Sprintf("%d %s", status, http.StatusText(status)), http.Header{"Content-Type": {"text/plain; charset=utf-8"}}, body)
	}
	writeError(w, status, msg)
	h.logAccess(seq, namespace, rewriteProfile, method, target, status, 0, int64(len(body)), msg)
}

// servePlain relays a request with no script hooks active. It is the original,
// streaming-friendly path.
func (h *Handler) servePlain(w http.ResponseWriter, r *http.Request, resolved ResolvedTarget) {
	start := time.Now()
	dumpID := uint64(0)

	if h.dumpRequest {
		dumpID = h.dumpSeq.Add(1)
		if h.dumpScope.HasReq() {
			if err := h.logIncomingRequest(dumpID, resolved.Namespace, r); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to read inbound request")
				h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, "", http.StatusInternalServerError, time.Since(start), 0, err.Error())
				return
			}
		}
	}

	targetURL := resolved.URL

	upstreamReq, err := h.buildUpstreamRequest(r, targetURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build upstream request")
		h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), http.StatusInternalServerError, time.Since(start), 0, err.Error())
		return
	}

	resp, err := h.client.Do(upstreamReq)
	if err != nil {
		status, msg := mapUpstreamError(err)
		writeError(w, status, msg)
		h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), status, time.Since(start), 0, err.Error())
		return
	}
	defer resp.Body.Close()

	var bytesWritten int64
	if h.dumpRequest && h.dumpScope.HasResp() {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			writeError(w, http.StatusBadGateway, "failed to read upstream response")
			h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), http.StatusBadGateway, time.Since(start), 0, readErr.Error())
			return
		}
		if err := h.logUpstreamResponse(dumpID, resolved.Namespace, resp, respBody); err != nil {
			h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), resp.StatusCode, time.Since(start), 0, err.Error())
		}

		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		n, err := w.Write(respBody)
		bytesWritten = int64(n)
		if err != nil {
			h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), resp.StatusCode, time.Since(start), bytesWritten, err.Error())
			return
		}
	} else {
		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		n, copyErr := io.Copy(w, resp.Body)
		bytesWritten = n
		if copyErr != nil {
			h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), resp.StatusCode, time.Since(start), bytesWritten, copyErr.Error())
			return
		}
	}

	h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), resp.StatusCode, time.Since(start), bytesWritten, "")
}

// logAccess emits one access-log entry through the reporter.
func (h *Handler) logAccess(seq uint64, namespace, rewriteProfile, method, target string, status int, dur time.Duration, bytes int64, errMsg string) {
	h.reporter.Access(AccessRecord{
		Seq:            seq,
		Namespace:      namespace,
		RewriteProfile: rewriteProfile,
		Method:         method,
		Target:         target,
		Status:         status,
		Duration:       dur,
		Bytes:          bytes,
		Err:            errMsg,
	})
}

func (h *Handler) logIncomingRequest(seq uint64, namespace string, r *http.Request) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	return h.dumpIncomingRequest(seq, namespace, r, body)
}

// dumpIncomingRequest reports an already-buffered inbound request to the
// reporter. The caller owns reading/resetting r.Body.
func (h *Handler) dumpIncomingRequest(seq uint64, namespace string, r *http.Request, body []byte) error {
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

	h.reporter.RequestDump(seq, namespace, string(head), body, r.RemoteAddr, r.Host)
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

func (h *Handler) logUpstreamResponse(seq uint64, namespace string, resp *http.Response, body []byte) error {
	respForDump := new(http.Response)
	*respForDump = *resp
	respForDump.Body = io.NopCloser(bytes.NewReader(body))

	head, err := httputil.DumpResponse(respForDump, false)
	if err != nil {
		return fmt.Errorf("dump response headers: %w", err)
	}

	h.reporter.ResponseDump(seq, namespace, string(head), body, resp.Status)
	return nil
}

func parseTargetURL(r *http.Request) (*url.URL, error) {
	resolved, err := parseNamespacedTargetURL(r)
	if err != nil {
		return nil, err
	}
	return resolved.URL, nil
}

func parseNamespacedTargetURL(r *http.Request) (ResolvedTarget, error) {
	raw := strings.TrimPrefix(r.RequestURI, "/")
	if strings.TrimSpace(raw) == "" {
		raw = strings.TrimPrefix(r.URL.RequestURI(), "/")
	}
	if strings.TrimSpace(raw) == "" {
		return ResolvedTarget{}, errors.New("missing target URL in path")
	}

	namespace := ""
	profile := ""
	if !hasHTTPPrefix(raw) {
		first, remainder, ok := strings.Cut(raw, "/")
		if !ok {
			return ResolvedTarget{}, errors.New("missing target URL in path")
		}
		if strings.HasPrefix(first, "@") {
			var err error
			profile, err = parseRewriteProfileSegment(first)
			if err != nil {
				return ResolvedTarget{}, err
			}
			raw = remainder
		} else {
			decoded, err := url.PathUnescape(first)
			if err != nil || !ValidNamespace(decoded) || decoded == "" {
				return ResolvedTarget{}, errors.New("invalid namespace")
			}
			namespace = decoded
			raw = remainder
			if !hasHTTPPrefix(raw) {
				second, targetRaw, hasTarget := strings.Cut(raw, "/")
				if !hasTarget || !strings.HasPrefix(second, "@") {
					return ResolvedTarget{}, errors.New("invalid target URL")
				}
				profile, err = parseRewriteProfileSegment(second)
				if err != nil {
					return ResolvedTarget{}, err
				}
				raw = targetRaw
			}
		}
	}

	target, err := url.Parse(normalizeTargetURL(raw))
	if err != nil {
		return ResolvedTarget{}, errors.New("invalid target URL")
	}

	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return ResolvedTarget{}, errors.New("target URL scheme must be http or https")
	}
	if target.Host == "" {
		return ResolvedTarget{}, errors.New("target URL host is required")
	}

	return ResolvedTarget{URL: target, Namespace: namespace, RewriteProfile: profile, OriginalPath: r.URL.EscapedPath()}, nil
}

func parseRewriteProfileSegment(segment string) (string, error) {
	if !strings.HasPrefix(segment, "@") || strings.Contains(segment, "%") {
		return "", errors.New("invalid rewrite profile")
	}
	profile := strings.TrimPrefix(segment, "@")
	if profile == "" || !ValidNamespace(profile) {
		return "", errors.New("invalid rewrite profile")
	}
	return profile, nil
}

func hasHTTPPrefix(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.HasPrefix(lower, "http:/") || strings.HasPrefix(lower, "https:/")
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

// serveScripted relays a request through the script hooks. The inbound body is
// always buffered (a hook may read or rewrite it); onRequest may rewrite the
// method/url/host/headers/body or short-circuit with a synthesized response,
// and onResponse runs on whatever response results (upstream or short-circuit).
func (h *Handler) serveScripted(w http.ResponseWriter, r *http.Request, resolved ResolvedTarget, engine *script.Engine) {
	if engine.HasResponseEventHook() {
		h.serveStreamingScripted(w, r, resolved, engine)
		return
	}
	start := time.Now()
	dumpID := uint64(0)
	if h.dumpRequest {
		dumpID = h.dumpSeq.Add(1)
	}
	namespace := resolved.Namespace
	rewriteProfile := resolved.RewriteProfile

	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read inbound request")
		h.logAccess(dumpID, namespace, rewriteProfile, r.Method, "", http.StatusInternalServerError, time.Since(start), 0, err.Error())
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(reqBody))
	r.ContentLength = int64(len(reqBody))

	if h.dumpRequest && h.dumpScope.HasReq() {
		if err := h.dumpIncomingRequest(dumpID, namespace, r, reqBody); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read inbound request")
			h.logAccess(dumpID, namespace, rewriteProfile, r.Method, "", http.StatusInternalServerError, time.Since(start), 0, err.Error())
			return
		}
	}

	targetURL := resolved.URL

	// Build the request view handed to onRequest. Static header rules apply
	// first so the script always has the last word.
	header := cloneHeader(r.Header)
	removeHopByHopHeaders(header)
	hostOverride := applyHeaderRulesToHeader(header, targetURL.Host, h.headerRules)

	sreq := &script.Request{
		Method:         r.Method,
		URL:            targetURL.String(),
		Host:           hostOverride,
		Header:         header,
		Body:           reqBody,
		Namespace:      resolved.Namespace,
		RewriteProfile: resolved.RewriteProfile,
		OriginalPath:   resolved.OriginalPath,
		StreamResponse: engine.HasResponseEventHook() && !engine.HasResponseHook(),
	}

	var shortCircuit *script.Response
	if engine.HasRequestHook() {
		sc, err := engine.OnRequest(sreq)
		if err != nil {
			h.failHook(w, dumpID, namespace, rewriteProfile, r.Method, targetURL.String(), start, err)
			return
		}
		newTarget, perr := parseScriptTarget(sreq.URL)
		if perr != nil {
			h.failHook(w, dumpID, namespace, rewriteProfile, r.Method, sreq.URL, start, perr)
			return
		}
		targetURL = newTarget
		shortCircuit = sc
	}

	status, respHeader, respBody, statusText, ok := h.obtainResponse(w, r, dumpID, namespace, rewriteProfile, start, sreq, targetURL, shortCircuit)
	if !ok {
		return
	}

	if engine.HasResponseHook() {
		sresp := &script.Response{Status: status, Header: cloneHeader(respHeader), Body: respBody}
		if err := engine.OnResponse(sresp, sreq); err != nil {
			h.failHook(w, dumpID, namespace, rewriteProfile, r.Method, targetURL.String(), start, err)
			return
		}
		status = sresp.Status
		respHeader = sresp.Header
		if respHeader == nil {
			respHeader = http.Header{}
		}
		respBody = sresp.Body
		statusText = strconv.Itoa(status) + " " + http.StatusText(status)
	}

	if h.dumpRequest && h.dumpScope.HasResp() {
		h.dumpScriptedResponse(dumpID, namespace, status, statusText, respHeader, respBody)
	}

	bytesWritten := h.writeScriptedResponse(w, r, status, respHeader, respBody)
	h.logAccess(dumpID, namespace, rewriteProfile, r.Method, targetURL.String(), status, time.Since(start), bytesWritten, "")
}

// serveStreamingScripted applies event-level script hooks to an SSE response
// without buffering the upstream body. onRequest remains buffered because it
// may rewrite the inbound JSON body.
func (h *Handler) serveStreamingScripted(w http.ResponseWriter, r *http.Request, resolved ResolvedTarget, engine *script.Engine) {
	start := time.Now()
	dumpID := uint64(0)
	if h.dumpRequest {
		dumpID = h.dumpSeq.Add(1)
	}

	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read inbound request")
		h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, "", http.StatusInternalServerError, time.Since(start), 0, err.Error())
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(reqBody))
	r.ContentLength = int64(len(reqBody))
	if h.dumpRequest && h.dumpScope.HasReq() {
		if err := h.dumpIncomingRequest(dumpID, resolved.Namespace, r, reqBody); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read inbound request")
			h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, "", http.StatusInternalServerError, time.Since(start), 0, err.Error())
			return
		}
	}

	targetURL := resolved.URL
	header := cloneHeader(r.Header)
	removeHopByHopHeaders(header)
	hostOverride := applyHeaderRulesToHeader(header, targetURL.Host, h.headerRules)
	sreq := &script.Request{Method: r.Method, URL: targetURL.String(), Host: hostOverride, Header: header, Body: reqBody, Namespace: resolved.Namespace, RewriteProfile: resolved.RewriteProfile, OriginalPath: resolved.OriginalPath, StreamResponse: engine.HasResponseEventHook() && !engine.HasResponseHook()}
	shortCircuit, err := engine.OnRequest(sreq)
	if err != nil {
		h.failHook(w, dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), start, err)
		return
	}
	targetURL, err = parseScriptTarget(sreq.URL)
	if err != nil {
		h.failHook(w, dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, sreq.URL, start, err)
		return
	}
	if !sreq.StreamResponse {
		status, respHeader, respBody, statusText, ok := h.obtainResponse(w, r, dumpID, resolved.Namespace, resolved.RewriteProfile, start, sreq, targetURL, shortCircuit)
		if !ok {
			return
		}
		if engine.HasResponseHook() {
			sresp := &script.Response{Status: status, Header: cloneHeader(respHeader), Body: respBody}
			if err := engine.OnResponse(sresp, sreq); err != nil {
				h.failHook(w, dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), start, err)
				return
			}
			status, respHeader, respBody = sresp.Status, sresp.Header, sresp.Body
			if respHeader == nil {
				respHeader = http.Header{}
			}
			statusText = strconv.Itoa(status) + " " + http.StatusText(status)
		}
		if h.dumpRequest && h.dumpScope.HasResp() {
			h.dumpScriptedResponse(dumpID, resolved.Namespace, status, statusText, respHeader, respBody)
		}
		written := h.writeScriptedResponse(w, r, status, respHeader, respBody)
		h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), status, time.Since(start), written, "")
		return
	}

	var status int
	var headerOut http.Header
	var body io.ReadCloser
	if shortCircuit != nil {
		status = shortCircuit.Status
		if status == 0 {
			status = http.StatusOK
		}
		headerOut = shortCircuit.Header
		body = io.NopCloser(bytes.NewReader(shortCircuit.Body))
	} else {
		upstreamReq, err := buildScriptedUpstream(r.Context(), sreq, targetURL)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build upstream request")
			h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), http.StatusInternalServerError, time.Since(start), 0, err.Error())
			return
		}
		upstreamResp, err := h.client.Do(upstreamReq)
		if err != nil {
			s, msg := mapUpstreamError(err)
			writeError(w, s, msg)
			h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), s, time.Since(start), 0, err.Error())
			return
		}
		status, headerOut, body = upstreamResp.StatusCode, upstreamResp.Header, upstreamResp.Body
	}
	defer body.Close()

	mediaType, _, _ := mime.ParseMediaType(headerOut.Get("Content-Type"))
	if !strings.EqualFold(mediaType, "text/event-stream") {
		writeError(w, http.StatusBadGateway, "streaming response hook requires text/event-stream")
		h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), http.StatusBadGateway, time.Since(start), 0, "streaming response hook requires text/event-stream")
		return
	}
	sresp := &script.Response{Status: status, Header: cloneHeader(headerOut)}
	stream, err := engine.BeginResponseStream(r.Context(), sresp, sreq)
	if err != nil {
		h.failHook(w, dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), start, err)
		return
	}
	defer stream.Close()

	out := w.Header()
	copyResponseHeaders(out, sresp.Header)
	removeHopByHopHeaders(out)
	out.Del("Content-Length")
	out.Set("Content-Type", "text/event-stream")
	w.WriteHeader(sresp.Status)
	flusher, _ := w.(http.Flusher)
	streamOut := io.Writer(w)
	var capture *sseCapture
	if h.dumpRequest && h.dumpScope.HasResp() {
		capture = &sseCapture{limit: h.maxSSEEventBytes}
		streamOut = io.MultiWriter(w, capture)
		defer func() {
			head := cloneHeader(sresp.Header)
			if capture.truncated {
				head.Set("X-Relay-Capture-Truncated", "true")
			}
			statusText := strconv.Itoa(sresp.Status) + " " + http.StatusText(sresp.Status)
			h.dumpScriptedResponse(dumpID, resolved.Namespace, sresp.Status, statusText, head, capture.buf.Bytes())
		}()
	}
	reader := newSSEReader(body, h.maxSSEEventBytes)
	var written int64
	eventCount := 0
	for {
		frame, readErr := reader.Next()
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), sresp.Status, time.Since(start), written, readErr.Error())
			return
		}
		for _, comment := range frame.comments {
			n, writeErr := writeSSEComment(streamOut, comment)
			written += int64(n)
			if writeErr != nil {
				return
			}
		}
		if frame.event != nil {
			eventCount++
			if eventCount > h.maxSSEEventsResponse {
				h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), sresp.Status, time.Since(start), written, "SSE response exceeded event limit")
				return
			}
			events, hookErr := stream.OnEvent(*frame.event, sreq)
			if hookErr != nil {
				h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), sresp.Status, time.Since(start), written, hookErr.Error())
				return
			}
			for _, event := range events {
				if sseEventSize(event) > h.maxSSEEventBytes {
					h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), sresp.Status, time.Since(start), written, "stream hook output exceeded event limit")
					return
				}
				n, writeErr := writeSSEEvent(streamOut, event)
				written += int64(n)
				if writeErr != nil {
					return
				}
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	events, endErr := stream.End(sreq)
	if endErr != nil {
		h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), sresp.Status, time.Since(start), written, endErr.Error())
		return
	}
	for _, event := range events {
		if sseEventSize(event) > h.maxSSEEventBytes {
			h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), sresp.Status, time.Since(start), written, "stream hook output exceeded event limit")
			return
		}
		n, writeErr := writeSSEEvent(streamOut, event)
		written += int64(n)
		if writeErr != nil {
			return
		}
	}
	if flusher != nil {
		flusher.Flush()
	}
	h.logAccess(dumpID, resolved.Namespace, resolved.RewriteProfile, r.Method, targetURL.String(), sresp.Status, time.Since(start), written, "")
}

// obtainResponse returns the response to relay, either synthesized from a
// short-circuit or fetched from upstream. It writes an error response and
// returns ok=false if the upstream call fails.
func (h *Handler) obtainResponse(w http.ResponseWriter, r *http.Request, dumpID uint64, namespace, rewriteProfile string, start time.Time, sreq *script.Request, targetURL *url.URL, shortCircuit *script.Response) (status int, header http.Header, body []byte, statusText string, ok bool) {
	if shortCircuit != nil {
		status = shortCircuit.Status
		if status == 0 {
			status = http.StatusOK
		}
		header = shortCircuit.Header
		if header == nil {
			header = http.Header{}
		}
		return status, header, shortCircuit.Body, strconv.Itoa(status) + " " + http.StatusText(status), true
	}

	upstreamReq, err := buildScriptedUpstream(r.Context(), sreq, targetURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build upstream request")
		h.logAccess(dumpID, namespace, rewriteProfile, r.Method, targetURL.String(), http.StatusInternalServerError, time.Since(start), 0, err.Error())
		return 0, nil, nil, "", false
	}

	resp, err := h.client.Do(upstreamReq)
	if err != nil {
		s, msg := mapUpstreamError(err)
		writeError(w, s, msg)
		h.logAccess(dumpID, namespace, rewriteProfile, r.Method, targetURL.String(), s, time.Since(start), 0, err.Error())
		return 0, nil, nil, "", false
	}
	defer resp.Body.Close()

	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		writeError(w, http.StatusBadGateway, "failed to read upstream response")
		h.logAccess(dumpID, namespace, rewriteProfile, r.Method, targetURL.String(), http.StatusBadGateway, time.Since(start), 0, readErr.Error())
		return 0, nil, nil, "", false
	}

	return resp.StatusCode, resp.Header, b, resp.Status, true
}

// writeScriptedResponse writes the final response with a recomputed
// Content-Length and returns the number of body bytes written.
func (h *Handler) writeScriptedResponse(w http.ResponseWriter, r *http.Request, status int, header http.Header, body []byte) int64 {
	out := w.Header()
	for key, vals := range header {
		for _, v := range vals {
			out.Add(key, v)
		}
	}
	removeHopByHopHeaders(out)
	out.Del("Content-Length")
	if r.Method != http.MethodHead {
		out.Set("Content-Length", strconv.Itoa(len(body)))
	}

	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return 0
	}
	n, _ := w.Write(body)
	return int64(n)
}

// failHook writes a 500 for a failed script hook and records the access log.
func (h *Handler) failHook(w http.ResponseWriter, seq uint64, namespace, rewriteProfile, method, target string, start time.Time, err error) {
	writeError(w, http.StatusInternalServerError, "script hook failed: "+err.Error())
	h.logAccess(seq, namespace, rewriteProfile, method, target, http.StatusInternalServerError, time.Since(start), 0, err.Error())
}

// dumpScriptedResponse reports a (possibly synthesized) response to the reporter.
func (h *Handler) dumpScriptedResponse(seq uint64, namespace string, status int, statusText string, header http.Header, body []byte) {
	resp := &http.Response{
		StatusCode:    status,
		Status:        statusText,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        cloneHeader(header),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	head, err := httputil.DumpResponse(resp, false)
	if err != nil {
		return
	}
	h.reporter.ResponseDump(seq, namespace, string(head), body, statusText)
}

// applyHeaderRulesToHeader applies static header rules to header, returning the
// (possibly overridden) host. A "Host" rule sets the host instead of a header.
func applyHeaderRulesToHeader(header http.Header, host string, rules []HeaderRule) string {
	for _, rule := range rules {
		if strings.EqualFold(rule.Name, "Host") {
			host = rule.Value
			continue
		}
		switch rule.Action {
		case HeaderRuleAdd:
			header.Add(rule.Name, rule.Value)
		case HeaderRuleModify:
			header.Set(rule.Name, rule.Value)
		}
	}
	return host
}

// parseScriptTarget validates and parses a target URL a script wrote to req.url.
func parseScriptTarget(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("script set an empty target URL")
	}
	target, err := url.Parse(normalizeTargetURL(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid target URL %q from script", raw)
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("script target URL scheme must be http or https")
	}
	if target.Host == "" {
		return nil, errors.New("script target URL host is required")
	}
	return target, nil
}

// buildScriptedUpstream builds the upstream request from the script-mutated
// request view.
func buildScriptedUpstream(ctx context.Context, sreq *script.Request, targetURL *url.URL) (*http.Request, error) {
	upstreamReq, err := http.NewRequestWithContext(ctx, sreq.Method, targetURL.String(), bytes.NewReader(sreq.Body))
	if err != nil {
		return nil, err
	}
	upstreamReq.Header = sreq.Header
	removeHopByHopHeaders(upstreamReq.Header)
	host := sreq.Host
	if host == "" {
		host = targetURL.Host
	}
	upstreamReq.Host = host
	upstreamReq.ContentLength = int64(len(sreq.Body))
	return upstreamReq, nil
}
