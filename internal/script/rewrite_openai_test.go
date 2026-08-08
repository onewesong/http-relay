package script

import (
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
