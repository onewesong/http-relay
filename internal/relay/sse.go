package relay

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/onewesong/http-relay/internal/script"
)

// sseCapture copies a bounded prefix for diagnostics without making client
// delivery wait for an in-memory response buffer.
type sseCapture struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *sseCapture) Write(p []byte) (int, error) {
	if c.limit <= c.buf.Len() {
		c.truncated = true
		return len(p), nil
	}
	remaining := c.limit - c.buf.Len()
	if len(p) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	_, _ = c.buf.Write(p)
	return len(p), nil
}

// sseFrame preserves comment lines separately because they are transport
// keepalives rather than data events presented to a rewrite script.
type sseFrame struct {
	comments []string
	event    *script.SSEEvent
}

type sseReader struct {
	r       *bufio.Reader
	maxSize int
}

func newSSEReader(r io.Reader, maxSize int) *sseReader {
	return &sseReader{r: bufio.NewReader(r), maxSize: maxSize}
}

// Next returns the next complete SSE frame. A trailing frame at EOF is valid.
func (r *sseReader) Next() (sseFrame, error) {
	var frame sseFrame
	var data []string
	var event script.SSEEvent
	var hasField bool
	size := 0
	for {
		line, err := r.r.ReadString('\n')
		if len(line) > 0 {
			size += len(line)
			if size > r.maxSize {
				return sseFrame{}, fmt.Errorf("SSE event exceeds %d bytes", r.maxSize)
			}
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				if hasField {
					event.Data = strings.Join(data, "\n")
					frame.event = &event
				}
				return frame, nil
			}
			if strings.HasPrefix(line, ":") {
				frame.comments = append(frame.comments, strings.TrimPrefix(line, ":"))
			} else {
				name, value, found := strings.Cut(line, ":")
				if found && strings.HasPrefix(value, " ") {
					value = strings.TrimPrefix(value, " ")
				}
				if !found {
					value = ""
				}
				switch name {
				case "data":
					data = append(data, value)
					hasField = true
				case "event":
					event.Event = value
					hasField = true
				case "id":
					if !strings.Contains(value, "\x00") {
						event.ID = value
						hasField = true
					}
				case "retry":
					event.Retry = value
					hasField = true
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if hasField || len(frame.comments) > 0 {
					if hasField {
						event.Data = strings.Join(data, "\n")
						frame.event = &event
					}
					return frame, nil
				}
				return sseFrame{}, io.EOF
			}
			return sseFrame{}, err
		}
	}
}

func writeSSEComment(w io.Writer, comment string) (int, error) {
	return io.WriteString(w, ":"+comment+"\n\n")
}

func writeSSEEvent(w io.Writer, event script.SSEEvent) (int, error) {
	var b strings.Builder
	if event.Event != "" {
		b.WriteString("event: ")
		b.WriteString(event.Event)
		b.WriteByte('\n')
	}
	if event.ID != "" {
		b.WriteString("id: ")
		b.WriteString(event.ID)
		b.WriteByte('\n')
	}
	if event.Retry != "" {
		b.WriteString("retry: ")
		b.WriteString(event.Retry)
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(event.Data, "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return io.WriteString(w, b.String())
}

func sseEventSize(event script.SSEEvent) int {
	return len(event.Event) + len(event.Data) + len(event.ID) + len(event.Retry)
}
