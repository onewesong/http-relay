package script

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestResponseStream_LifecycleAndState(t *testing.T) {
	t.Parallel()
	e := mustEngine(t, `
		function onResponseStart(resp, req) {
			resp.headers["X-Stream"] = "yes";
			return { count: 0 };
		}
		function onResponseEvent(event, state, req) {
			state.count++;
			return { event: "out", data: state.count + ":" + event.data };
		}
		function onResponseEnd(state, req) { return { data: "end:" + state.count }; }
	`)
	resp := &Response{Status: http.StatusOK, Header: http.Header{}}
	req := &Request{Method: "GET", URL: "https://example.test/", Header: http.Header{}}
	stream, err := e.BeginResponseStream(context.Background(), resp, req)
	if err != nil {
		t.Fatalf("BeginResponseStream: %v", err)
	}
	if got := resp.Header.Get("X-Stream"); got != "yes" {
		t.Fatalf("start hook header = %q", got)
	}
	first, err := stream.OnEvent(SSEEvent{Data: "one"}, req)
	if err != nil || len(first) != 1 || first[0].Event != "out" || first[0].Data != "1:one" {
		t.Fatalf("first event = %#v, %v", first, err)
	}
	second, err := stream.OnEvent(SSEEvent{Data: "two"}, req)
	if err != nil || len(second) != 1 || second[0].Data != "2:two" {
		t.Fatalf("second event = %#v, %v", second, err)
	}
	end, err := stream.End(req)
	if err != nil || len(end) != 1 || end[0].Data != "end:2" {
		t.Fatalf("end event = %#v, %v", end, err)
	}
}

func TestResponseStream_AllowsMixedBufferedAndEventHooks(t *testing.T) {
	t.Parallel()
	e, err := New(Options{Path: writeScript(t, `
		function onResponse(resp, req) {}
		function onResponseEvent(event, state, req) {}
	`)})
	if err != nil || !e.HasResponseHook() || !e.HasResponseEventHook() {
		t.Fatalf("engine=%#v err=%v", e, err)
	}
}

func TestResponseStream_RequiresEventHookForStartAndEnd(t *testing.T) {
	t.Parallel()
	_, err := New(Options{Path: writeScript(t, `function onResponseStart(resp, req) {}`)})
	if err == nil || !strings.Contains(err.Error(), "require onResponseEvent") {
		t.Fatalf("New error = %v", err)
	}
}
