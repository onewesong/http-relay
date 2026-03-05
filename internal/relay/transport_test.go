package relay

import (
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
