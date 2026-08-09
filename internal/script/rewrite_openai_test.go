package script

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	builtinplugins "github.com/onewesong/http-relay/plugins"
)

func TestRewriteOpenAI_AddsWebSearchTool(t *testing.T) {
	t.Parallel()

	source, err := builtinplugins.ReadBuiltIn("rewrite.openai.js")
	if err != nil {
		t.Fatalf("ReadBuiltIn: %v", err)
	}
	engine, err := New(Options{Path: "builtin:rewrite.openai.js", Source: string(source)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		name       string
		url        string
		body       string
		wantTools  int
		wantSearch bool
	}{
		{
			name:       "adds to responses request",
			url:        "https://api.example.com/v1/responses",
			body:       `{"model":"gpt"}`,
			wantTools:  1,
			wantSearch: true,
		},
		{
			name:       "skips existing web search",
			url:        "https://api.example.com/v1/responses?stream=true",
			body:       `{"tools":[{"type":"web_search"}]}`,
			wantTools:  1,
			wantSearch: true,
		},
		{
			name:      "ignores other paths",
			url:       "https://api.example.com/v1/chat/completions",
			body:      `{"model":"gpt"}`,
			wantTools: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &Request{Method: "POST", URL: tt.url, Header: http.Header{}, Body: []byte(tt.body)}
			if _, err := engine.OnRequest(req); err != nil {
				t.Fatalf("OnRequest: %v", err)
			}

			var value struct {
				Tools []struct {
					Type string `json:"type"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(req.Body, &value); err != nil {
				t.Fatalf("updated body is not JSON: %v", err)
			}
			if len(value.Tools) != tt.wantTools {
				t.Fatalf("tools = %d, want %d; body=%s", len(value.Tools), tt.wantTools, req.Body)
			}
			found := false
			for _, tool := range value.Tools {
				if tool.Type == "web_search" {
					found = true
				}
			}
			if found != tt.wantSearch {
				t.Fatalf("web_search present = %v, want %v", found, tt.wantSearch)
			}
		})
	}
}

func TestRewriteChatCompletionsToResponsesStream(t *testing.T) {
	t.Parallel()
	source, err := builtinplugins.ReadBuiltIn("rewrite.chat-completions-to-responses.js")
	if err != nil {
		t.Fatalf("read stream example: %v", err)
	}
	engine, err := New(Options{Path: "stream example", Source: string(source)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &Request{
		Method:       "POST",
		URL:          "https://api.example.com/v1/chat/completions",
		OriginalPath: "/@stream/https://api.example.com/v1/chat/completions",
		Header:       http.Header{"Content-Type": {"application/json"}},
		Body:         []byte(`{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"Hello"}]}`),
	}
	if _, err := engine.OnRequest(req); err != nil {
		t.Fatalf("OnRequest: %v", err)
	}
	if req.URL != "https://api.example.com/v1/responses" {
		t.Fatalf("rewritten URL = %q", req.URL)
	}
	var upstreamBody struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(req.Body, &upstreamBody); err != nil || !upstreamBody.Stream {
		t.Fatalf("upstream body = %s, err=%v", req.Body, err)
	}
	resp := &Response{Status: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}
	stream, err := engine.BeginResponseStream(context.Background(), resp, req)
	if err != nil {
		t.Fatalf("BeginResponseStream: %v", err)
	}
	events, err := stream.OnEvent(SSEEvent{Event: "response.output_text.delta", Data: `{"delta":"Hi","response":{"id":"resp_1","model":"gpt-test"}}`}, req)
	if err != nil || len(events) != 2 {
		t.Fatalf("text event = %#v, err=%v", events, err)
	}
	if events[1].Data == "" {
		t.Fatal("text chunk is empty")
	}
	end, err := stream.End(req)
	if err != nil || len(end) != 1 || end[0].Data != "[DONE]" {
		t.Fatalf("end = %#v, err=%v", end, err)
	}
}
