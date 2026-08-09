package relay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestProxySelectorAllProxyOverride(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		switch key {
		case "ALL_PROXY":
			return "socks5://127.0.0.1:1080"
		case "HTTPS_PROXY":
			return "http://127.0.0.1:7890"
		default:
			return ""
		}
	}

	selector, _ := newProxySelectorFromGetenv(getenv)
	target, _ := url.Parse("https://example.com")
	proxyURL, err := selector(target)
	if err != nil {
		t.Fatalf("selector error: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "socks5://127.0.0.1:1080" {
		t.Fatalf("proxy=%v", proxyURL)
	}
}

func TestProxySelectorByScheme(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		switch key {
		case "HTTP_PROXY":
			return "http://127.0.0.1:8081"
		case "HTTPS_PROXY":
			return "http://127.0.0.1:8082"
		default:
			return ""
		}
	}

	selector, _ := newProxySelectorFromGetenv(getenv)

	httpTarget, _ := url.Parse("http://example.com")
	httpsTarget, _ := url.Parse("https://example.com")

	httpProxy, err := selector(httpTarget)
	if err != nil {
		t.Fatalf("http selector error: %v", err)
	}
	httpsProxy, err := selector(httpsTarget)
	if err != nil {
		t.Fatalf("https selector error: %v", err)
	}

	if httpProxy == nil || httpProxy.String() != "http://127.0.0.1:8081" {
		t.Fatalf("http proxy=%v", httpProxy)
	}
	if httpsProxy == nil || httpsProxy.String() != "http://127.0.0.1:8082" {
		t.Fatalf("https proxy=%v", httpsProxy)
	}
}

func TestProxySelectorNoProxy(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		switch key {
		case "HTTPS_PROXY":
			return "http://127.0.0.1:8082"
		case "NO_PROXY":
			return "example.com"
		default:
			return ""
		}
	}

	selector, _ := newProxySelectorFromGetenv(getenv)
	target, _ := url.Parse("https://example.com")
	proxyURL, err := selector(target)
	if err != nil {
		t.Fatalf("selector error: %v", err)
	}
	if proxyURL != nil {
		t.Fatalf("expected direct connection, got proxy=%v", proxyURL)
	}
}

func TestProtectedRequestBypassesProxy(t *testing.T) {
	t.Parallel()
	direct := newBaseTransport()
	direct.DialContext = func(_ context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			buf := make([]byte, 4096)
			_, _ = server.Read(buf)
			_, _ = server.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
		}()
		return client, nil
	}
	transport := &RelayTransport{
		selector: func(*url.URL) (*url.URL, error) { return nil, errors.New("proxy selector should not run") },
		direct:   direct,
	}
	policy := &TargetPolicy{resolver: staticResolver{"public.test": {{IP: net.ParseIP("8.8.8.8")}}}}
	ctx := withTargetPolicy(context.Background(), policy)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://public.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestProtectedDialUsesValidatedIP(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			close(accepted)
			_ = conn.Close()
		}
	}()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), targetDialInfoContextKey{}, targetDialInfo{
		host: "public.test", port: port, addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}},
	})
	transport := &RelayTransport{}
	conn, err := transport.dialContext(ctx, "tcp", fmt.Sprintf("public.test:%s", port))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	<-accepted
}
