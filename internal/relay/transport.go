package relay

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/proxy"
)

type RelayTransport struct {
	selector     func(*url.URL) (*url.URL, error)
	direct       *http.Transport
	httpByProxy  map[string]*http.Transport
	socksByProxy map[string]*http.Transport
	httpBase     *http.Transport
	socksBase    *http.Transport
	mu           sync.RWMutex
}

func NewTransportFromEnv() (*RelayTransport, string, error) {
	selector, summary := newProxySelectorFromGetenv(os.Getenv)

	directBase := newBaseTransport()
	httpBase := newBaseTransport()
	socksBase := newBaseTransport()
	socksBase.Proxy = nil

	return &RelayTransport{
		selector:     selector,
		direct:       directBase,
		httpByProxy:  make(map[string]*http.Transport),
		socksByProxy: make(map[string]*http.Transport),
		httpBase:     httpBase,
		socksBase:    socksBase,
	}, summary, nil
}

func (t *RelayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	proxyURL, err := t.selector(req.URL)
	if err != nil {
		return nil, err
	}
	if proxyURL == nil {
		return t.direct.RoundTrip(req)
	}

	scheme := strings.ToLower(proxyURL.Scheme)
	switch scheme {
	case "http", "https":
		tr := t.getOrCreateHTTPProxyTransport(proxyURL)
		return tr.RoundTrip(req)
	case "socks5", "socks5h":
		tr, err := t.getOrCreateSOCKSProxyTransport(proxyURL)
		if err != nil {
			return nil, err
		}
		return tr.RoundTrip(req)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
	}
}

func (t *RelayTransport) getOrCreateHTTPProxyTransport(proxyURL *url.URL) *http.Transport {
	key := proxyURL.String()

	t.mu.RLock()
	if tr, ok := t.httpByProxy[key]; ok {
		t.mu.RUnlock()
		return tr
	}
	t.mu.RUnlock()

	t.mu.Lock()
	defer t.mu.Unlock()
	if tr, ok := t.httpByProxy[key]; ok {
		return tr
	}

	tr := t.httpBase.Clone()
	tr.Proxy = http.ProxyURL(proxyURL)
	t.httpByProxy[key] = tr
	return tr
}

func (t *RelayTransport) getOrCreateSOCKSProxyTransport(proxyURL *url.URL) (*http.Transport, error) {
	key := proxyURL.String()

	t.mu.RLock()
	if tr, ok := t.socksByProxy[key]; ok {
		t.mu.RUnlock()
		return tr, nil
	}
	t.mu.RUnlock()

	t.mu.Lock()
	defer t.mu.Unlock()
	if tr, ok := t.socksByProxy[key]; ok {
		return tr, nil
	}

	dialContext, err := socksDialContext(proxyURL)
	if err != nil {
		return nil, err
	}

	tr := t.socksBase.Clone()
	tr.DialContext = dialContext
	tr.Proxy = nil
	t.socksByProxy[key] = tr
	return tr, nil
}

func newBaseTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

func socksDialContext(proxyURL *url.URL) (func(context.Context, string, string) (net.Conn, error), error) {
	base := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	proxyDialer, err := proxy.FromURL(proxyURL, base)
	if err != nil {
		return nil, fmt.Errorf("build socks5 dialer: %w", err)
	}

	if ctxDialer, ok := proxyDialer.(proxy.ContextDialer); ok {
		return ctxDialer.DialContext, nil
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		type result struct {
			conn net.Conn
			err  error
		}
		ch := make(chan result, 1)
		go func() {
			conn, dialErr := proxyDialer.Dial(network, addr)
			ch <- result{conn: conn, err: dialErr}
		}()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case res := <-ch:
			return res.conn, res.err
		}
	}, nil
}

func newProxySelectorFromGetenv(getenv func(string) string) (func(*url.URL) (*url.URL, error), string) {
	cfg := httpproxy.Config{
		HTTPProxy:  firstNonEmpty(getenv("HTTP_PROXY"), getenv("http_proxy")),
		HTTPSProxy: firstNonEmpty(getenv("HTTPS_PROXY"), getenv("https_proxy")),
		NoProxy:    firstNonEmpty(getenv("NO_PROXY"), getenv("no_proxy")),
	}

	allProxy := firstNonEmpty(getenv("ALL_PROXY"), getenv("all_proxy"))
	if allProxy != "" {
		cfg.HTTPProxy = allProxy
		cfg.HTTPSProxy = allProxy
	}

	summary := buildProxySummary(cfg, allProxy)
	proxyFunc := cfg.ProxyFunc()
	return proxyFunc, summary
}

func buildProxySummary(cfg httpproxy.Config, allProxy string) string {
	if allProxy != "" {
		return fmt.Sprintf("all_proxy=%s no_proxy=%s", sanitizeProxyURL(allProxy), summarizeNoProxy(cfg.NoProxy))
	}
	if cfg.HTTPProxy == "" && cfg.HTTPSProxy == "" {
		return fmt.Sprintf("direct no_proxy=%s", summarizeNoProxy(cfg.NoProxy))
	}
	return fmt.Sprintf("http_proxy=%s https_proxy=%s no_proxy=%s",
		sanitizeProxyURL(cfg.HTTPProxy),
		sanitizeProxyURL(cfg.HTTPSProxy),
		summarizeNoProxy(cfg.NoProxy),
	)
}

func summarizeNoProxy(noProxy string) string {
	if strings.TrimSpace(noProxy) == "" {
		return "<empty>"
	}
	return "<set>"
}

func sanitizeProxyURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "<empty>"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
