package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onewesong/http-relay/internal/relay"
)

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (t bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}

func TestMCPBearerNamespaceIsolation(t *testing.T) {
	cfg := jwtTestConfig()
	handler, reporter := New(Meta{Version: "test"}, Options{JWTAuth: cfg, MCPEnabled: true})
	reporter.Access(relay.AccessRecord{Seq: 1, Namespace: "team-a", Method: "GET", Target: "https://example.test/a", Status: 200})
	reporter.Access(relay.AccessRecord{Seq: 2, Namespace: "team-b", Method: "POST", Target: "https://example.test/b", Status: 500, Err: "boom"})
	ts := httptest.NewServer(handler)
	defer ts.Close()
	token := jwtToken(t, cfg, "team-a", false)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}, DisableStandaloneSSE: true, MaxRetries: -1}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_transactions", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool error: %v", result.GetError())
	}
	if got := string(result.Content[0].(*mcp.TextContent).Text); got == "" || !contains(got, "team-a") || contains(got, "team-b") {
		t.Fatalf("unexpected result: %s", got)
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_transactions", Arguments: map[string]any{"namespace": "team-b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected namespace denial")
	}
}

func TestMCPDisabledAndMalformedBearer(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled MCP status=%d", rec.Code)
	}
	handler, _ = New(Meta{}, Options{JWTAuth: cfg, MCPEnabled: true})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Basic abc")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("malformed bearer status=%d", rec.Code)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
