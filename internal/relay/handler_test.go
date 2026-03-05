package relay

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseTargetURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "valid", path: "/https://example.com/a?x=1", wantErr: false},
		{name: "missing scheme", path: "/example.com", wantErr: true},
		{name: "invalid", path: "/https://%", wantErr: true},
		{name: "unsupported scheme", path: "/ftp://example.com", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "http://relay.local/", nil)
			req.RequestURI = tt.path
			req.URL = &url.URL{Path: tt.path}
			got, err := parseTargetURL(req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil target=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != "https://example.com/a?x=1" {
				t.Fatalf("target=%q", got.String())
			}
		})
	}
}

func TestRemoveHopByHopHeaders(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Connection", "keep-alive, X-Custom-Hop")
	h.Set("Keep-Alive", "timeout=5")
	h.Set("Transfer-Encoding", "chunked")
	h.Set("X-Custom-Hop", "1")
	h.Set("X-Keep", "ok")

	removeHopByHopHeaders(h)

	if h.Get("X-Keep") != "ok" {
		t.Fatalf("expected X-Keep to remain")
	}
	if h.Get("Connection") != "" || h.Get("Keep-Alive") != "" || h.Get("Transfer-Encoding") != "" || h.Get("X-Custom-Hop") != "" {
		t.Fatalf("hop-by-hop headers were not fully removed: %#v", h)
	}
}

func TestRelayForwardAndResponse(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "hello" {
			t.Fatalf("body=%q", string(body))
		}
		if r.URL.Path != "/echo" || r.URL.Query().Get("x") != "1" {
			t.Fatalf("url=%s", r.URL.String())
		}
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("world"))
	}))
	defer upstream.Close()

	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	handler := NewHandler(client, log.New(io.Discard, "", 0), false, DumpScopeReq|DumpScopeResp)
	relay := httptest.NewServer(handler)
	defer relay.Close()

	resp, err := http.Post(relay.URL+"/"+upstream.URL+"/echo?x=1", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if resp.Header.Get("X-Upstream") != "ok" {
		t.Fatalf("missing upstream header")
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "world" {
		t.Fatalf("body=%q", string(body))
	}
}

func TestRelayUnavailableUpstream(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	handler := NewHandler(client, log.New(io.Discard, "", 0), false, DumpScopeReq|DumpScopeResp)
	relay := httptest.NewServer(handler)
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestRelayHeadRequest(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ignore-body"))
	}))
	defer upstream.Close()

	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	handler := NewHandler(client, log.New(io.Discard, "", 0), false, DumpScopeReq|DumpScopeResp)
	relay := httptest.NewServer(handler)
	defer relay.Close()

	req, _ := http.NewRequest(http.MethodHead, relay.URL+"/"+upstream.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Fatalf("head should not return body, got=%q", string(body))
	}
}

func TestRelayDumpReqResp(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != "ping-body" {
			t.Fatalf("upstream body=%q", string(body))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	handler := NewHandler(client, logger, true, DumpScopeReq|DumpScopeResp)
	relay := httptest.NewServer(handler)
	defer relay.Close()

	req, _ := http.NewRequest(http.MethodPost, relay.URL+"/"+upstream.URL, strings.NewReader("ping-body"))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	gotLog := logs.String()
	if !strings.Contains(gotLog, "REQUEST DUMP BEGIN") ||
		!strings.Contains(gotLog, "REQUEST DUMP END") ||
		!strings.Contains(gotLog, "RESPONSE DUMP BEGIN") ||
		!strings.Contains(gotLog, "RESPONSE DUMP END") ||
		!strings.Contains(gotLog, "ping-body") ||
		!strings.Contains(gotLog, "ok") {
		t.Fatalf("dump log not found, logs=%q", gotLog)
	}
}

func TestRelayDumpReqOnly(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	handler := NewHandler(client, logger, true, DumpScopeReq)
	relay := httptest.NewServer(handler)
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/" + upstream.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	gotLog := logs.String()
	if !strings.Contains(gotLog, "REQUEST DUMP BEGIN") {
		t.Fatalf("request dump missing, logs=%q", gotLog)
	}
	if strings.Contains(gotLog, "RESPONSE DUMP BEGIN") {
		t.Fatalf("response dump should be disabled in req scope, logs=%q", gotLog)
	}
}

func TestRelayDumpRespOnly(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	handler := NewHandler(client, logger, true, DumpScopeResp)
	relay := httptest.NewServer(handler)
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/" + upstream.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	gotLog := logs.String()
	if strings.Contains(gotLog, "REQUEST DUMP BEGIN") {
		t.Fatalf("request dump should be disabled in resp scope, logs=%q", gotLog)
	}
	if !strings.Contains(gotLog, "RESPONSE DUMP BEGIN") {
		t.Fatalf("response dump missing, logs=%q", gotLog)
	}
}

func TestParseDumpScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		want   DumpScope
		wantOK bool
	}{
		{name: "empty default", input: "", want: DumpScopeReq | DumpScopeResp, wantOK: true},
		{name: "req", input: "req", want: DumpScopeReq, wantOK: true},
		{name: "resp", input: "resp", want: DumpScopeResp, wantOK: true},
		{name: "req,resp", input: "req,resp", want: DumpScopeReq | DumpScopeResp, wantOK: true},
		{name: "resp,req", input: "resp,req", want: DumpScopeReq | DumpScopeResp, wantOK: true},
		{name: "invalid", input: "abc", want: DumpScopeReq | DumpScopeResp, wantOK: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseDumpScope(tt.input)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("input=%q got=(%s,%v) want=(%s,%v)", tt.input, got.String(), ok, tt.want.String(), tt.wantOK)
			}
		})
	}
}
