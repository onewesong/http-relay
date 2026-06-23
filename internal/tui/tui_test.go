package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/onewesong/http-relay/internal/relay"
)

// send applies one message and returns the model, keeping the *model type.
func send(t *testing.T, m *model, msg tea.Msg) *model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(*model)
	if !ok {
		t.Fatalf("Update returned %T, want *model", next)
	}
	return got
}

func TestModelAccumulatesAndExpands(t *testing.T) {
	m := newModel("test header")
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	// One transaction reported across the three reporter calls, keyed by seq.
	m = send(t, m, reqMsg{
		seq:  1,
		head: "POST /echo HTTP/1.1\r\nHost: api.example.com\r\nContent-Type: text/plain\r\n",
		body: []byte("ping-body"),
		host: "api.example.com",
	})
	m = send(t, m, respMsg{
		seq:    1,
		head:   "HTTP/1.1 201 Created\r\nX-Upstream: ok\r\n",
		body:   []byte("pong-body"),
		status: "201 Created",
	})
	m = send(t, m, accessMsg{rec: relay.AccessRecord{
		Seq:      1,
		Method:   "POST",
		Target:   "https://api.example.com/echo",
		Status:   201,
		Duration: 12 * time.Millisecond,
		Bytes:    9,
	}})

	if len(m.txns) != 1 {
		t.Fatalf("want 1 txn merged by seq, got %d", len(m.txns))
	}

	// Collapsed: summary visible, bodies hidden.
	view := m.View()
	for _, want := range []string{"POST", "201", "api.example.com/echo"} {
		if !strings.Contains(view, want) {
			t.Fatalf("collapsed view missing %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "ping-body") || strings.Contains(view, "pong-body") {
		t.Fatalf("bodies should be hidden when collapsed\n%s", view)
	}

	// Expand the selected row.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view = m.View()
	for _, want := range []string{"ping-body", "pong-body", "X-Upstream", "request", "response"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded view missing %q\n%s", want, view)
		}
	}

	// Collapse again hides the bodies.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view = m.View()
	if strings.Contains(view, "ping-body") {
		t.Fatalf("body should be hidden after re-collapse\n%s", view)
	}
}

func TestModelFoldsJSONBody(t *testing.T) {
	m := newModel("h")
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	m = send(t, m, respMsg{
		seq:    1,
		head:   "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n",
		body:   []byte(`{"id":42,"items":[1,2,3]}`),
		status: "200 OK",
	})
	m = send(t, m, accessMsg{rec: relay.AccessRecord{
		Seq: 1, Method: "GET", Target: "https://h/x", Status: 200,
	}})

	// Expand the row: JSON is pretty-printed (indented, multi-line).
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	if !strings.Contains(view, "\"items\": [") {
		t.Fatalf("expanded JSON should be pretty-printed\n%s", view)
	}
	if !strings.Contains(view, "[J] fold") {
		t.Fatalf("expanded JSON should show the fold hint\n%s", view)
	}

	// Fold the JSON: bodies collapse to a one-line summary.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}})
	view = m.View()
	if strings.Contains(view, "\"items\": [") {
		t.Fatalf("folded JSON should hide the pretty body\n%s", view)
	}
	for _, want := range []string{"{…}", "2 keys", "[J] expand"} {
		if !strings.Contains(view, want) {
			t.Fatalf("folded view missing %q\n%s", want, view)
		}
	}

	// Unfold restores the pretty body.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}})
	if view = m.View(); !strings.Contains(view, "\"items\": [") {
		t.Fatalf("unfold should restore pretty body\n%s", view)
	}
}

func TestModelMultipleTxnsAndNavigation(t *testing.T) {
	m := newModel("h")
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	for i := uint64(1); i <= 3; i++ {
		m = send(t, m, accessMsg{rec: relay.AccessRecord{
			Seq: i, Method: "GET", Target: "https://h/" + string(rune('a'+i)), Status: 200,
		}})
	}
	if len(m.txns) != 3 {
		t.Fatalf("want 3 txns, got %d", len(m.txns))
	}

	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if m.cursor != 2 {
		t.Fatalf("G should jump to last row, cursor=%d", m.cursor)
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m.cursor != 1 {
		t.Fatalf("k should move up, cursor=%d", m.cursor)
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if m.cursor != 0 {
		t.Fatalf("g should jump to first row, cursor=%d", m.cursor)
	}
}

func TestModelQuitKey(t *testing.T) {
	m := newModel("h")
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q should return a quit command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("quit command should produce a message")
	} else if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("q should produce tea.QuitMsg, got %T", msg)
	}
}
