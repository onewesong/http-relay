package relay

import (
	"io"
	"strings"
	"testing"

	"github.com/onewesong/http-relay/internal/script"
)

func TestSSEReader_ParsesSplitMultiLineEvent(t *testing.T) {
	t.Parallel()
	r := newSSEReader(strings.NewReader(": keepalive\r\nevent: update\r\ndata: first\r\ndata: second\r\n\r\n"), 1024)
	frame, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(frame.comments) != 1 || frame.comments[0] != " keepalive" {
		t.Fatalf("comments = %#v", frame.comments)
	}
	if frame.event == nil || frame.event.Event != "update" || frame.event.Data != "first\nsecond" {
		t.Fatalf("event = %#v", frame.event)
	}
	_, err = r.Next()
	if err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestWriteSSEEvent(t *testing.T) {
	t.Parallel()
	var output strings.Builder
	_, err := writeSSEEvent(&output, script.SSEEvent{Event: "x", ID: "1", Data: "a\nb"})
	if err != nil {
		t.Fatalf("writeSSEEvent: %v", err)
	}
	if want := "event: x\nid: 1\ndata: a\ndata: b\n\n"; output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}
