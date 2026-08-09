package script

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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

func TestRewriteAnthropicMessagesToResponses(t *testing.T) {
	t.Parallel()
	source, err := builtinplugins.ReadBuiltIn("rewrite.anthropic-messages-to-responses.js")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{Path: "anthropic compatibility", Source: string(source)})
	if err != nil {
		t.Fatal(err)
	}
	req := &Request{
		Method:       "POST",
		URL:          "https://api.example.com/v1/messages",
		OriginalPath: "/@anthropic-compat/https://api.example.com/v1/messages",
		Header: http.Header{
			"X-Api-Key":         {"test-key"},
			"Anthropic-Version": {"2023-06-01"},
		},
		Body: []byte(`{"model":"gpt-test","max_tokens":64,"stream":true,"system":"Be concise.","messages":[{"role":"user","content":[{"type":"text","text":"Look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YWJj"}}]},{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"weather","input":{"city":"Paris"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"sunny"}]}],"tools":[{"name":"weather","input_schema":{"type":"object"}}]}`),
	}
	if _, err := engine.OnRequest(req); err != nil {
		t.Fatal(err)
	}
	if req.URL != "https://api.example.com/v1/responses" || !req.StreamResponse {
		t.Fatalf("request URL=%q stream=%t", req.URL, req.StreamResponse)
	}
	if req.Header.Get("Authorization") != "Bearer test-key" || req.Header.Get("X-Api-Key") != "" || req.Header.Get("Anthropic-Version") != "" {
		t.Fatalf("headers=%v", req.Header)
	}
	if !strings.Contains(string(req.Body), `"input_image"`) || !strings.Contains(string(req.Body), `"function_call_output"`) || !strings.Contains(string(req.Body), `"max_output_tokens":64`) {
		t.Fatalf("converted request=%s", req.Body)
	}

	resp := &Response{Status: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp_1","model":"gpt-test","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hi"}]},{"type":"function_call","call_id":"call_2","name":"weather","arguments":"{\"city\":\"Paris\"}"}],"usage":{"input_tokens":4,"output_tokens":2}}`)}
	if err := engine.OnResponse(resp, req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resp.Body), `"type":"message"`) || !strings.Contains(string(resp.Body), `"type":"tool_use"`) || !strings.Contains(string(resp.Body), `"stop_reason":"tool_use"`) {
		t.Fatalf("converted response=%s", resp.Body)
	}
	failure := &Response{Status: http.StatusBadGateway, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"error":{"type":"invalid_request_error","message":"upstream rejected request"}}`)}
	if err := engine.OnResponse(failure, req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(failure.Body), `"type":"error"`) || !strings.Contains(string(failure.Body), "upstream rejected request") {
		t.Fatalf("converted error=%s", failure.Body)
	}

	streamResp := &Response{Status: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}
	stream, err := engine.BeginResponseStream(context.Background(), streamResp, req)
	if err != nil {
		t.Fatal(err)
	}
	started, err := stream.OnEvent(SSEEvent{Event: "response.created", Data: `{"response":{"id":"resp_stream","model":"gpt-test","usage":{"input_tokens":3,"output_tokens":0}}}`}, req)
	if err != nil || len(started) != 1 || !strings.Contains(started[0].Data, `"message_start"`) {
		t.Fatalf("message start=%#v err=%v", started, err)
	}
	text, err := stream.OnEvent(SSEEvent{Event: "response.output_text.delta", Data: `{"item_id":"item_1","delta":"Hi"}`}, req)
	if err != nil || len(text) != 2 || !strings.Contains(text[1].Data, `"text_delta"`) {
		t.Fatalf("text delta=%#v err=%v", text, err)
	}
	completed, err := stream.OnEvent(SSEEvent{Event: "response.completed", Data: `{"response":{"usage":{"input_tokens":3,"output_tokens":1}}}`}, req)
	if err != nil || len(completed) < 2 || !strings.Contains(completed[len(completed)-1].Data, `"message_stop"`) {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	if _, err := stream.End(req); err != nil {
		t.Fatal(err)
	}
}

func TestRewriteAnthropicMessagesRejectsUnsupportedInput(t *testing.T) {
	t.Parallel()
	source, err := builtinplugins.ReadBuiltIn("rewrite.anthropic-messages-to-responses.js")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{Path: "anthropic compatibility", Source: string(source)})
	if err != nil {
		t.Fatal(err)
	}
	req := &Request{Method: "POST", URL: "https://api.example.com/v1/messages", Header: http.Header{}, Body: []byte(`{"model":"gpt","max_tokens":1,"stop_sequences":["END"],"messages":[]}`)}
	shortCircuit, err := engine.OnRequest(req)
	if err != nil || shortCircuit == nil || shortCircuit.Status != http.StatusBadRequest || !strings.Contains(string(shortCircuit.Body), "invalid_request_error") {
		t.Fatalf("short circuit=%#v err=%v", shortCircuit, err)
	}
}

func TestRewriteAnthropicMessagesToChatCompletions(t *testing.T) {
	t.Parallel()
	source, err := builtinplugins.ReadBuiltIn("rewrite.anthropic-messages-to-chat-completions.js")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{Path: "anthropic chat compatibility", Source: string(source)})
	if err != nil {
		t.Fatal(err)
	}
	req := &Request{
		Method:       "POST",
		URL:          "https://api.example.com/v1/messages",
		OriginalPath: "/@anthropic-chat-compat/https://api.example.com/v1/messages",
		Header:       http.Header{"X-Api-Key": {"test-key"}, "Anthropic-Version": {"2023-06-01"}},
		Body:         []byte(`{"model":"gpt-test","max_tokens":64,"stream":true,"system":"Be concise.","messages":[{"role":"user","content":"Hello"},{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"weather","input":{"city":"Paris"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"sunny"}]}],"tools":[{"name":"weather","input_schema":{"type":"object"}}]}`),
	}
	if _, err := engine.OnRequest(req); err != nil {
		t.Fatal(err)
	}
	if req.URL != "https://api.example.com/v1/chat/completions" || !req.StreamResponse || req.Header.Get("Authorization") != "Bearer test-key" || req.Header.Get("X-Api-Key") != "" {
		t.Fatalf("request=%+v headers=%v", req, req.Header)
	}
	if !strings.Contains(string(req.Body), `"role":"system"`) || !strings.Contains(string(req.Body), `"tool_call_id":"call_1"`) || !strings.Contains(string(req.Body), `"stream_options":{"include_usage":true}`) {
		t.Fatalf("converted request=%s", req.Body)
	}

	resp := &Response{Status: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"message":{"role":"assistant","content":"Hi","tool_calls":[{"id":"call_2","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`)}
	if err := engine.OnResponse(resp, req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resp.Body), `"type":"tool_use"`) || !strings.Contains(string(resp.Body), `"stop_reason":"tool_use"`) {
		t.Fatalf("converted response=%s", resp.Body)
	}

	streamResp := &Response{Status: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}
	stream, err := engine.BeginResponseStream(context.Background(), streamResp, req)
	if err != nil {
		t.Fatal(err)
	}
	textEvents, err := stream.OnEvent(SSEEvent{Data: `{"id":"chatcmpl_2","model":"gpt-test","choices":[{"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`}, req)
	if err != nil || len(textEvents) != 3 || !strings.Contains(textEvents[2].Data, `"text_delta"`) {
		t.Fatalf("text events=%#v err=%v", textEvents, err)
	}
	finished, err := stream.OnEvent(SSEEvent{Data: `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`}, req)
	if err != nil || len(finished) < 3 || !strings.Contains(finished[len(finished)-1].Data, `"message_stop"`) {
		t.Fatalf("finished=%#v err=%v", finished, err)
	}
	if _, err := stream.End(req); err != nil {
		t.Fatal(err)
	}
}
