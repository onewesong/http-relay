package relay

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseHeaderRule(t *testing.T) {
	t.Parallel()

	rule, err := ParseHeaderRule("x-debug: 1", HeaderRuleAdd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rule.Action != HeaderRuleAdd || rule.Name != "X-Debug" || rule.Value != "1" {
		t.Fatalf("rule=%#v", rule)
	}
}

func TestParseHeaderRuleRejectsInvalidFormat(t *testing.T) {
	t.Parallel()

	if _, err := ParseHeaderRule("X-Debug", HeaderRuleAdd); err == nil {
		t.Fatalf("expected invalid header rule error")
	}
}

func TestRelayAppliesHeaderRules(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		debugValues := r.Header.Values("X-Debug")
		if strings.Join(debugValues, ",") != "client,relay" {
			t.Fatalf("X-Debug=%q", debugValues)
		}
		if r.Header.Get("User-Agent") != "http-relay" {
			t.Fatalf("User-Agent=%q", r.Header.Get("User-Agent"))
		}
		if r.Host != "upstream.example" {
			t.Fatalf("Host=%q", r.Host)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	rules, err := ParseHeaderRules(
		[]string{"X-Debug: relay"},
		[]string{"User-Agent: http-relay", "Host: upstream.example"},
	)
	if err != nil {
		t.Fatalf("parse rules: %v", err)
	}

	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	handler := NewHandlerWithOptions(client, log.New(io.Discard, "", 0), HandlerOptions{
		TargetMode:  DefaultTargetMode(),
		HeaderRules: rules,
		DumpScope:   DumpScopeReq | DumpScopeResp,
	})
	relay := httptest.NewServer(handler)
	defer relay.Close()

	req, _ := http.NewRequest(http.MethodGet, relay.URL+"/"+upstream.URL, nil)
	req.Header.Set("X-Debug", "client")
	req.Header.Set("User-Agent", "client")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHeaderRuleSummaryMasksSensitiveValues(t *testing.T) {
	t.Parallel()

	rule, err := ParseHeaderRule("Authorization: Bearer secret", HeaderRuleModify)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(rule.Summary(), "secret") {
		t.Fatalf("summary leaked sensitive value: %q", rule.Summary())
	}
}
