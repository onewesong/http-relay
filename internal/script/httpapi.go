package script

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/idna"
)

// HTTPOptions configures the synchronous relay.http.request capability.
type HTTPOptions struct {
	Enabled              bool
	AllowedOrigins       []string
	DefaultTimeout       time.Duration
	MaxTimeout           time.Duration
	MaxRequestBodyBytes  int64
	MaxResponseBodyBytes int64
	MaxCallsPerHook      int
	FollowRedirects      bool
	AllowPrivateNetworks bool
}

// HTTPInfo is safe startup metadata for the script HTTP capability.
type HTTPInfo struct {
	Enabled              bool
	AllowedOrigins       int
	DefaultTimeout       time.Duration
	MaxTimeout           time.Duration
	MaxRequestBodyBytes  int64
	MaxResponseBodyBytes int64
	MaxCallsPerHook      int
	FollowRedirects      bool
	AllowPrivateNetworks bool
}

// HTTPRequest is the validated Go-side representation of one JS request.
type HTTPRequest struct {
	URL        string
	Method     string
	Headers    map[string]string
	Body       string
	Timeout    time.Duration
	HasBody    bool
	HasTimeout bool
}

// HTTPResponse is returned to JavaScript as a plain object.
type HTTPResponse struct {
	Status  int
	Headers map[string]string
	Body    string
	URL     string
}

type ipResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// HTTPService owns the shared HTTP policy and transport used by all engines.
type HTTPService struct {
	opts      HTTPOptions
	origins   map[string]struct{}
	resolver  ipResolver
	dialer    *net.Dialer
	transport *http.Transport
	client    *http.Client
}

var allowedHTTPMethods = map[string]struct{}{
	http.MethodGet: {}, http.MethodHead: {}, http.MethodPost: {},
	http.MethodPut: {}, http.MethodPatch: {}, http.MethodDelete: {},
}

var forbiddenScriptHeaders = map[string]struct{}{
	"Accept-Encoding":     {},
	"Connection":          {},
	"Content-Length":      {},
	"Host":                {},
	"Proxy-Authorization": {},
	"Proxy-Connection":    {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// NewHTTPService validates opts and creates a dedicated client that does not
// use environment proxies or a cookie jar.
func NewHTTPService(opts HTTPOptions) (*HTTPService, error) {
	if opts.DefaultTimeout <= 0 || opts.MaxTimeout < opts.DefaultTimeout {
		return nil, errors.New("script HTTP timeout configuration is invalid")
	}
	if opts.MaxRequestBodyBytes <= 0 || opts.MaxResponseBodyBytes <= 0 || opts.MaxCallsPerHook <= 0 {
		return nil, errors.New("script HTTP limits must be greater than zero")
	}
	if opts.Enabled && len(opts.AllowedOrigins) == 0 {
		return nil, errors.New("script HTTP allowed origins must not be empty when enabled")
	}

	s := &HTTPService{
		opts:     opts,
		origins:  make(map[string]struct{}, len(opts.AllowedOrigins)),
		resolver: net.DefaultResolver,
		dialer:   &net.Dialer{},
	}
	for _, raw := range opts.AllowedOrigins {
		origin, err := canonicalHTTPOrigin(raw)
		if err != nil {
			return nil, err
		}
		s.origins[origin] = struct{}{}
	}
	s.transport = &http.Transport{
		Proxy:                 nil,
		DialContext:           s.dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		MaxConnsPerHost:       8,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: opts.MaxTimeout,
	}
	s.client = &http.Client{Transport: s.transport, CheckRedirect: s.checkRedirect}
	return s, nil
}

func (s *HTTPService) Info() HTTPInfo {
	if s == nil {
		return HTTPInfo{}
	}
	return HTTPInfo{
		Enabled: s.opts.Enabled, AllowedOrigins: len(s.origins),
		DefaultTimeout: s.opts.DefaultTimeout, MaxTimeout: s.opts.MaxTimeout,
		MaxRequestBodyBytes: s.opts.MaxRequestBodyBytes, MaxResponseBodyBytes: s.opts.MaxResponseBodyBytes,
		MaxCallsPerHook: s.opts.MaxCallsPerHook, FollowRedirects: s.opts.FollowRedirects,
		AllowPrivateNetworks: s.opts.AllowPrivateNetworks,
	}
}

func (s *HTTPService) Enabled() bool { return s != nil && s.opts.Enabled }

func (s *HTTPService) maxCallsPerHook() int {
	if s == nil {
		return 0
	}
	return s.opts.MaxCallsPerHook
}

// Request executes one policy-checked request under the current Hook context.
func (s *HTTPService) Request(ctx context.Context, request HTTPRequest) (*HTTPResponse, error) {
	if s == nil || !s.opts.Enabled {
		return nil, errors.New("relay.http is disabled")
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodGet
	}
	if _, ok := allowedHTTPMethods[method]; !ok {
		return nil, errors.New("HTTP method is not allowed")
	}
	u, err := s.validateURL(request.URL)
	if err != nil {
		return nil, err
	}
	if int64(len(request.Body)) > s.opts.MaxRequestBodyBytes {
		return nil, errors.New("HTTP request body exceeds configured limit")
	}

	timeout := s.opts.DefaultTimeout
	if request.HasTimeout {
		timeout = request.Timeout
	}
	if timeout <= 0 {
		return nil, errors.New("HTTP timeout must be greater than zero")
	}
	if timeout > s.opts.MaxTimeout {
		timeout = s.opts.MaxTimeout
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if request.HasBody {
		body = strings.NewReader(request.Body)
	}
	httpRequest, err := http.NewRequestWithContext(requestContext, method, u.String(), body)
	if err != nil {
		return nil, errors.New("invalid HTTP request")
	}
	for name, value := range request.Headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if canonical == "" || !httpguts.ValidHeaderFieldName(canonical) || !httpguts.ValidHeaderFieldValue(value) {
			return nil, errors.New("invalid HTTP header")
		}
		if _, forbidden := forbiddenScriptHeaders[canonical]; forbidden {
			return nil, fmt.Errorf("HTTP header %s is not allowed", canonical)
		}
		httpRequest.Header.Set(canonical, value)
	}

	response, err := s.client.Do(httpRequest)
	if err != nil {
		if httpRequestTimedOut(err, requestContext, ctx) {
			return nil, errors.New("HTTP request timed out")
		}
		if requestContext.Err() != nil || ctx.Err() != nil {
			return nil, errors.New("HTTP request canceled")
		}
		return nil, errors.New("HTTP request failed")
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, s.opts.MaxResponseBodyBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		if httpRequestTimedOut(err, requestContext, ctx) {
			return nil, errors.New("HTTP request timed out")
		}
		if requestContext.Err() != nil || ctx.Err() != nil {
			return nil, errors.New("HTTP request canceled")
		}
		return nil, errors.New("HTTP response body read failed")
	}
	if int64(len(data)) > s.opts.MaxResponseBodyBytes {
		return nil, errors.New("HTTP response body exceeds configured limit")
	}
	headers := make(map[string]string, len(response.Header))
	for name, values := range response.Header {
		headers[name] = strings.Join(values, ", ")
	}
	return &HTTPResponse{Status: response.StatusCode, Headers: headers, Body: string(data), URL: response.Request.URL.String()}, nil
}

func httpRequestTimedOut(err error, requestContext, parent context.Context) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) || errors.Is(parent.Err(), context.DeadlineExceeded) {
		return true
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func (s *HTTPService) validateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !u.IsAbs() || u.Host == "" || u.Opaque != "" {
		return nil, errors.New("invalid HTTP URL")
	}
	if u.User != nil {
		return nil, errors.New("HTTP URL userinfo is not allowed")
	}
	origin, err := canonicalURLOrigin(u)
	if err != nil {
		return nil, err
	}
	if _, ok := s.origins[origin]; !ok {
		return nil, errors.New("HTTP origin is not allowed")
	}
	return u, nil
}

func (s *HTTPService) checkRedirect(req *http.Request, via []*http.Request) error {
	if !s.opts.FollowRedirects {
		return errors.New("HTTP redirects are disabled")
	}
	if len(via) >= 3 {
		return errors.New("HTTP redirect limit exceeded")
	}
	if _, err := s.validateURL(req.URL.String()); err != nil {
		return err
	}
	if len(via) > 0 && requestOrigin(via[len(via)-1]) != requestOrigin(req) {
		for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
			req.Header.Del(name)
		}
	}
	return nil
}

func requestOrigin(req *http.Request) string {
	origin, _ := canonicalURLOrigin(req.URL)
	return origin
}

func (s *HTTPService) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid HTTP target address")
	}
	var addresses []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		addresses = []net.IPAddr{{IP: ip}}
	} else {
		addresses, err = s.resolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("HTTP DNS lookup failed")
		}
	}
	for _, address := range addresses {
		if !s.opts.AllowPrivateNetworks && forbiddenHTTPIP(address.IP) {
			return nil, errors.New("HTTP target resolves to a prohibited address")
		}
	}
	selected := addresses[0].IP.String()
	return s.dialer.DialContext(ctx, network, net.JoinHostPort(selected, port))
}

func forbiddenHTTPIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() {
		return true
	}
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	return cgnat.Contains(address)
}

func canonicalHTTPOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("invalid script HTTP origin %q", raw)
	}
	return canonicalURLOrigin(u)
}

func canonicalURLOrigin(u *url.URL) (string, error) {
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("HTTP URL must use http or https")
	}
	hostname := u.Hostname()
	if hostname == "" || strings.HasSuffix(hostname, ".") || strings.Contains(hostname, "*") {
		return "", errors.New("HTTP URL has an invalid hostname")
	}
	if ip := net.ParseIP(hostname); ip == nil {
		ascii, err := idna.Lookup.ToASCII(hostname)
		if err != nil || ascii == "" {
			return "", errors.New("HTTP URL has an invalid hostname")
		}
		hostname = strings.ToLower(ascii)
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil || value == 0 {
		return "", errors.New("HTTP URL has an invalid port")
	}
	return scheme + "://" + net.JoinHostPort(hostname, port), nil
}
