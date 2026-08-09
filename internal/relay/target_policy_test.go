package relay

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type staticResolver map[string][]net.IPAddr

func (r staticResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	addresses, ok := r[host]
	if !ok {
		return nil, errors.New("not found")
	}
	return addresses, nil
}

func TestTargetPolicyValidate(t *testing.T) {
	t.Parallel()
	policy := &TargetPolicy{resolver: staticResolver{
		"public.test":  {{IP: net.ParseIP("203.0.113.10")}},
		"mixed.test":   {{IP: net.ParseIP("203.0.113.10")}, {IP: net.ParseIP("127.0.0.1")}},
		"private.test": {{IP: net.ParseIP("10.0.0.8")}},
	}}

	tests := []struct {
		name string
		raw  string
		want error
	}{
		{name: "public literal", raw: "https://8.8.8.8/"},
		{name: "public hostname", raw: "https://public.test/"},
		{name: "loopback", raw: "http://127.0.0.1/", want: ErrProhibitedTarget},
		{name: "ipv6 loopback", raw: "http://[::1]/", want: ErrProhibitedTarget},
		{name: "private hostname", raw: "http://private.test/", want: ErrProhibitedTarget},
		{name: "mixed dns", raw: "http://mixed.test/", want: ErrProhibitedTarget},
		{name: "dns failure", raw: "http://missing.test/", want: ErrTargetDNS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			err = policy.Validate(context.Background(), u)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestHandlerTargetPolicyRejectsPathTarget(t *testing.T) {
	t.Parallel()
	policy := &TargetPolicy{resolver: staticResolver{"private.test": {{IP: net.ParseIP("127.0.0.1")}}}}
	handler := NewHandlerWithOptions(http.DefaultClient, log.New(io.Discard, "", 0), HandlerOptions{
		TargetMode: DefaultTargetMode(), TargetPolicy: policy,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://relay.local/", nil)
	req.RequestURI = "/http://private.test/"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

type redirectTransport struct{}

func (redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{"http://private.test/"}},
		Body:       io.NopCloser(http.NoBody),
		Request:    req,
	}, nil
}

func TestHandlerTargetPolicyRejectsPrivateRedirect(t *testing.T) {
	t.Parallel()
	policy := &TargetPolicy{resolver: staticResolver{
		"public.test":  {{IP: net.ParseIP("8.8.8.8")}},
		"private.test": {{IP: net.ParseIP("127.0.0.1")}},
	}}
	client := &http.Client{Transport: redirectTransport{}}
	handler := NewHandlerWithOptions(client, log.New(io.Discard, "", 0), HandlerOptions{
		TargetMode: DefaultTargetMode(), TargetPolicy: policy,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://relay.local/", nil)
	req.RequestURI = "/http://public.test/"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandlerTargetPolicyCanBeDisabled(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer upstream.Close()
	handler := NewHandlerWithOptions(&http.Client{Transport: &http.Transport{Proxy: nil}}, log.New(io.Discard, "", 0), HandlerOptions{TargetMode: DefaultTargetMode()})
	relay := httptest.NewServer(handler)
	defer relay.Close()
	resp, err := http.Get(relay.URL + "/" + upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
