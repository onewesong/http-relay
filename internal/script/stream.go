package script

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/dop251/goja"
)

// SSEEvent is a complete server-sent event. Relay parses and serializes the
// wire format so scripts never need to reason about arbitrary read boundaries.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
	Retry string
}

// ResponseStream owns one pooled JavaScript runtime for the lifetime of a
// single response stream. It must be closed exactly once by the caller.
type ResponseStream struct {
	engine  *Engine
	version *scriptVersion
	pooled  *pooledRuntime
	ctx     context.Context
	state   goja.Value
	closed  bool
}

// BeginResponseStream runs onResponseStart, if present, and returns a stream
// session that preserves its returned JavaScript value as private state.
func (e *Engine) BeginResponseStream(ctx context.Context, resp *Response, req *Request) (*ResponseStream, error) {
	if e == nil || !e.HasResponseEventHook() {
		return nil, fmt.Errorf("onResponseEvent is not configured")
	}
	ver := e.current.Load()
	pr := e.get(ver)
	s := &ResponseStream{engine: e, version: ver, pooled: pr, ctx: ctx, state: goja.Undefined()}
	if !ver.hasRespStart {
		return s, nil
	}
	fn, ok := goja.AssertFunction(pr.rt.Get("onResponseStart"))
	if !ok {
		s.Close()
		return nil, fmt.Errorf("onResponseStart is not callable")
	}
	respObj := newResponseObject(pr.rt, resp)
	ret, err := e.callContext(pr, ctx, fn, respObj, newRequestObject(pr.rt, req))
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("onResponseStart: %w", err)
	}
	readResponseObject(pr.rt, respObj, resp)
	if ret != nil && !goja.IsUndefined(ret) && !goja.IsNull(ret) {
		s.state = ret
	}
	return s, nil
}

// OnEvent runs onResponseEvent and converts its event-object result.
func (s *ResponseStream) OnEvent(event SSEEvent, req *Request) ([]SSEEvent, error) {
	if s == nil || s.closed {
		return nil, fmt.Errorf("response stream is closed")
	}
	fn, ok := goja.AssertFunction(s.pooled.rt.Get("onResponseEvent"))
	if !ok {
		return nil, fmt.Errorf("onResponseEvent is not callable")
	}
	ret, err := s.engine.callContext(s.pooled, s.ctx, fn, sseEventToJS(s.pooled.rt, event), s.state, newRequestObject(s.pooled.rt, req))
	if err != nil {
		return nil, fmt.Errorf("onResponseEvent: %w", err)
	}
	return sseEventsFromJS(s.pooled.rt, ret)
}

// End runs onResponseEnd, if present, and always releases the runtime.
func (s *ResponseStream) End(req *Request) ([]SSEEvent, error) {
	if s == nil || s.closed {
		return nil, fmt.Errorf("response stream is closed")
	}
	defer s.Close()
	if !s.version.hasRespEnd {
		return nil, nil
	}
	fn, ok := goja.AssertFunction(s.pooled.rt.Get("onResponseEnd"))
	if !ok {
		return nil, fmt.Errorf("onResponseEnd is not callable")
	}
	ret, err := s.engine.callContext(s.pooled, s.ctx, fn, s.state, newRequestObject(s.pooled.rt, req))
	if err != nil {
		return nil, fmt.Errorf("onResponseEnd: %w", err)
	}
	return sseEventsFromJS(s.pooled.rt, ret)
}

// Close returns the private runtime to the pool. It is safe to call repeatedly.
func (s *ResponseStream) Close() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	s.engine.put(s.pooled)
}

func sseEventToJS(rt *goja.Runtime, event SSEEvent) *goja.Object {
	obj := rt.NewObject()
	_ = obj.Set("data", event.Data)
	if event.Event != "" {
		_ = obj.Set("event", event.Event)
	}
	if event.ID != "" {
		_ = obj.Set("id", event.ID)
	}
	if event.Retry != "" {
		_ = obj.Set("retry", event.Retry)
	}
	return obj
}

func sseEventsFromJS(rt *goja.Runtime, value goja.Value) ([]SSEEvent, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	obj := value.ToObject(rt)
	if obj.ClassName() == "Array" {
		length := int(obj.Get("length").ToInteger())
		if length > 1024 {
			return nil, fmt.Errorf("stream hook returned too many events")
		}
		result := make([]SSEEvent, 0, length)
		for i := 0; i < length; i++ {
			event, err := sseEventFromObject(rt, obj.Get(fmt.Sprintf("%d", i)))
			if err != nil {
				return nil, err
			}
			result = append(result, event)
		}
		return result, nil
	}
	event, err := sseEventFromObject(rt, value)
	if err != nil {
		return nil, err
	}
	return []SSEEvent{event}, nil
}

func sseEventFromObject(rt *goja.Runtime, value goja.Value) (SSEEvent, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return SSEEvent{}, fmt.Errorf("stream hook event must be an object")
	}
	obj := value.ToObject(rt)
	if obj.ClassName() != "Object" {
		return SSEEvent{}, fmt.Errorf("stream hook event must be an object")
	}
	data, err := requiredString(obj.Get("data"), "stream hook event data")
	if err != nil {
		return SSEEvent{}, err
	}
	event := SSEEvent{Data: data}
	for _, field := range []struct {
		name string
		to   *string
	}{{"event", &event.Event}, {"id", &event.ID}, {"retry", &event.Retry}} {
		value := obj.Get(field.name)
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			continue
		}
		if value.ExportType() == nil || value.ExportType().Kind() != reflect.String {
			return SSEEvent{}, fmt.Errorf("stream hook event %s must be a string", field.name)
		}
		*field.to = value.String()
		if strings.ContainsAny(*field.to, "\r\n") {
			return SSEEvent{}, fmt.Errorf("stream hook event %s must not contain a newline", field.name)
		}
	}
	return event, nil
}
