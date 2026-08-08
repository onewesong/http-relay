package script

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testHTTPOptions(origin string) HTTPOptions {
	return HTTPOptions{
		Enabled: true, AllowedOrigins: []string{origin},
		DefaultTimeout: time.Second, MaxTimeout: 2 * time.Second,
		MaxRequestBodyBytes: 1024, MaxResponseBodyBytes: 1024,
		MaxCallsPerHook: 3, AllowPrivateNetworks: true,
	}
}

func mustHTTPService(t *testing.T, opts HTTPOptions) *HTTPService {
	t.Helper()
	service, err := NewHTTPService(opts)
	if err != nil {
		t.Fatalf("NewHTTPService: %v", err)
	}
	t.Cleanup(service.transport.CloseIdleConnections)
	return service
}

func TestHTTPServiceRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Add("X-Multi", "a")
		w.Header().Add("X-Multi", "b")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, "%s|%s|%s", r.Method, r.Header.Get("X-Test"), body)
	}))
	defer server.Close()
	service := mustHTTPService(t, testHTTPOptions(server.URL))

	response, err := service.Request(context.Background(), HTTPRequest{
		URL: server.URL + "/v1?q=1", Method: "post", Headers: map[string]string{"X-Test": "yes"},
		Body: "payload", HasBody: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != http.StatusCreated || response.Body != "POST|yes|payload" || response.Headers["X-Multi"] != "a, b" || response.URL != server.URL+"/v1?q=1" {
		t.Fatalf("response=%+v", response)
	}
}

func TestHTTPServicePolicyAndLimits(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "12345")
	}))
	defer server.Close()
	opts := testHTTPOptions(server.URL)
	opts.MaxRequestBodyBytes = 4
	opts.MaxResponseBodyBytes = 4
	service := mustHTTPService(t, opts)

	tests := []HTTPRequest{
		{URL: "https://example.com/"},
		{URL: server.URL, Method: "TRACE"},
		{URL: server.URL, Headers: map[string]string{"Host": "evil"}},
		{URL: server.URL, Body: "12345", HasBody: true},
	}
	for _, request := range tests {
		if _, err := service.Request(context.Background(), request); err == nil {
			t.Errorf("Request(%+v) succeeded, want error", request)
		}
	}
	if _, err := service.Request(context.Background(), HTTPRequest{URL: server.URL}); err == nil || !strings.Contains(err.Error(), "response body") {
		t.Fatalf("response limit error=%v", err)
	}
}

func TestHTTPServiceTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	opts := testHTTPOptions(server.URL)
	opts.DefaultTimeout = 40 * time.Millisecond
	service := mustHTTPService(t, opts)

	started := time.Now()
	_, err := service.Request(context.Background(), HTTPRequest{URL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestHTTPServicePrivateNetworkPolicy(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	opts := testHTTPOptions(server.URL)
	opts.AllowPrivateNetworks = false
	service := mustHTTPService(t, opts)
	if _, err := service.Request(context.Background(), HTTPRequest{URL: server.URL}); err == nil {
		t.Fatal("expected loopback target to be rejected")
	}

	for _, raw := range []string{"0.0.0.0", "127.0.0.1", "10.0.0.1", "169.254.169.254", "224.0.0.1", "100.64.0.1", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1"} {
		if !forbiddenHTTPIP(net.ParseIP(raw)) {
			t.Errorf("%s was not prohibited", raw)
		}
	}
	if forbiddenHTTPIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address was prohibited")
	}
}

type fakeResolver struct {
	addresses []net.IPAddr
	calls     atomic.Int32
}

func (r *fakeResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	r.calls.Add(1)
	return r.addresses, nil
}

func TestHTTPServiceRejectsMixedDNSAnswers(t *testing.T) {
	t.Parallel()
	service := mustHTTPService(t, testHTTPOptions("http://allowed.test:8080"))
	service.opts.AllowPrivateNetworks = false
	resolver := &fakeResolver{addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}}
	service.resolver = resolver
	if _, err := service.dialContext(context.Background(), "tcp", "allowed.test:8080"); err == nil {
		t.Fatal("expected mixed public/private DNS answers to be rejected")
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("DNS calls=%d", resolver.calls.Load())
	}
}

func TestHTTPServiceDialsValidatedDNSAnswer(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	service := mustHTTPService(t, testHTTPOptions(fmt.Sprintf("http://allowed.test:%d", port)))
	resolver := &fakeResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	service.resolver = resolver

	response, err := service.Request(context.Background(), HTTPRequest{URL: fmt.Sprintf("http://allowed.test:%d/path", port)})
	if err != nil || response.Body != "ok" {
		t.Fatalf("response=%+v error=%v", response, err)
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("DNS calls=%d", resolver.calls.Load())
	}
}

func TestHTTPServiceClampsRequestedTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	opts := testHTTPOptions(server.URL)
	opts.DefaultTimeout = 20 * time.Millisecond
	opts.MaxTimeout = 50 * time.Millisecond
	service := mustHTTPService(t, opts)
	started := time.Now()
	_, err := service.Request(context.Background(), HTTPRequest{URL: server.URL, Timeout: time.Second, HasTimeout: true})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error=%v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("max timeout was not applied: %s", elapsed)
	}
}

func TestHTTPServiceRedirectPolicy(t *testing.T) {
	t.Parallel()
	var authorization atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/target", http.StatusFound)
	}))
	defer redirect.Close()

	opts := testHTTPOptions(redirect.URL)
	opts.AllowedOrigins = append(opts.AllowedOrigins, target.URL)
	service := mustHTTPService(t, opts)
	if _, err := service.Request(context.Background(), HTTPRequest{URL: redirect.URL}); err == nil {
		t.Fatal("expected redirects to be disabled")
	}

	opts.FollowRedirects = true
	service = mustHTTPService(t, opts)
	response, err := service.Request(context.Background(), HTTPRequest{URL: redirect.URL, Headers: map[string]string{"Authorization": "Bearer secret"}})
	if err != nil || response.Body != "ok" {
		t.Fatalf("response=%+v error=%v", response, err)
	}
	if got, _ := authorization.Load().(string); got != "" {
		t.Fatalf("authorization leaked across origin: %q", got)
	}
}

func TestHTTPServiceIgnoresEnvironmentProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer server.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	service := mustHTTPService(t, testHTTPOptions(server.URL))
	response, err := service.Request(context.Background(), HTTPRequest{URL: server.URL})
	if err != nil || response.Body != "ok" {
		t.Fatalf("response=%+v error=%v", response, err)
	}
}

func TestHTTPServiceCompressedResponseLimit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = io.WriteString(writer, strings.Repeat("x", 32))
		_ = writer.Close()
	}))
	defer server.Close()
	opts := testHTTPOptions(server.URL)
	opts.MaxResponseBodyBytes = 16
	service := mustHTTPService(t, opts)
	if _, err := service.Request(context.Background(), HTTPRequest{URL: server.URL}); err == nil || !strings.Contains(err.Error(), "response body") {
		t.Fatalf("error=%v", err)
	}
}
