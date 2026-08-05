// Package tui renders relayed traffic in an interactive, collapsible
// full-screen terminal UI built on bubbletea.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/onewesong/http-relay/internal/relay"
)

// maxBodyRender caps how many bytes of a body are shown when expanded, so a
// huge payload can't blow up rendering. The full size is always reported.
const maxBodyRender = 64 * 1024

// New builds the bubbletea program and the relay.Reporter that feeds it.
// The reporter is safe to call from request-handling goroutines.
func New(header string) (*tea.Program, relay.Reporter) {
	m := newModel(header)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	return prog, &reporter{prog: prog}
}

// reporter forwards captured traffic to the bubbletea program. Send is
// goroutine-safe, so handler goroutines may call these directly.
type reporter struct {
	prog *tea.Program
}

func (r *reporter) RequestDump(seq uint64, _ string, head string, body []byte, remote, host string) {
	r.prog.Send(reqMsg{seq: seq, head: head, body: append([]byte(nil), body...), remote: remote, host: host})
}

func (r *reporter) ResponseDump(seq uint64, _ string, head string, body []byte, status string) {
	r.prog.Send(respMsg{seq: seq, head: head, body: append([]byte(nil), body...), status: status})
}

func (r *reporter) Access(rec relay.AccessRecord) {
	r.prog.Send(accessMsg{rec: rec})
}

type reqMsg struct {
	seq          uint64
	head         string
	body         []byte
	remote, host string
}

type respMsg struct {
	seq    uint64
	head   string
	body   []byte
	status string
}

type accessMsg struct {
	rec relay.AccessRecord
}

// txn accumulates everything known about one relayed request, keyed by seq.
type txn struct {
	seq      uint64
	at       time.Time
	method   string
	target   string
	status   int
	duration time.Duration
	bytes    int64
	err      string

	reqHead  string
	reqBody  []byte
	respHead string
	respBody []byte

	hasReq  bool
	hasResp bool
	done    bool
}

type model struct {
	header   string
	txns     []*txn
	index    map[uint64]*txn
	cursor   int
	expanded map[uint64]bool
	// jsonFolded collapses a row's JSON bodies to a one-line summary when set.
	// Default (unset) shows the pretty-printed form.
	jsonFolded map[uint64]bool

	viewport viewport.Model
	ready    bool
	width    int
	height   int

	// txnLine[i] is the line offset of txn i within the rendered content.
	txnLine []int
}

func newModel(header string) *model {
	return &model{
		header:     header,
		index:      make(map[uint64]*txn),
		expanded:   make(map[uint64]bool),
		jsonFolded: make(map[uint64]bool),
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) upsert(seq uint64) *txn {
	if t, ok := m.index[seq]; ok {
		return t
	}
	t := &txn{seq: seq, at: time.Now()}
	m.index[seq] = t
	m.txns = append(m.txns, t)
	return t
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerH := lipgloss.Height(m.headerView())
		footerH := lipgloss.Height(m.footerView())
		bodyH := msg.Height - headerH - footerH
		if bodyH < 1 {
			bodyH = 1
		}
		if !m.ready {
			m.viewport = viewport.New(msg.Width, bodyH)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = bodyH
		}
		m.refresh(true)
		return m, nil

	case reqMsg:
		t := m.upsert(msg.seq)
		t.hasReq = true
		t.reqHead = msg.head
		t.reqBody = msg.body
		m.refresh(false)
		return m, nil

	case respMsg:
		t := m.upsert(msg.seq)
		t.hasResp = true
		t.respHead = msg.head
		t.respBody = msg.body
		m.refresh(false)
		return m, nil

	case accessMsg:
		rec := msg.rec
		var t *txn
		if rec.Seq > 0 {
			t = m.upsert(rec.Seq)
		} else {
			t = &txn{at: time.Now()}
			m.txns = append(m.txns, t)
		}
		t.method = rec.Method
		t.target = rec.Target
		t.status = rec.Status
		t.duration = rec.Duration
		t.bytes = rec.Bytes
		t.err = rec.Err
		t.done = true
		m.refresh(false)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.refresh(false)
		}
	case "down", "j":
		if m.cursor < len(m.txns)-1 {
			m.cursor++
			m.refresh(false)
		}
	case "g", "home":
		m.cursor = 0
		m.refresh(false)
	case "G", "end":
		if len(m.txns) > 0 {
			m.cursor = len(m.txns) - 1
		}
		m.refresh(false)
	case "enter", " ", "tab":
		if len(m.txns) > 0 {
			key := keyFor(m.txns[m.cursor])
			m.expanded[key] = !m.expanded[key]
			m.refresh(false)
		}
	case "J":
		// Toggle JSON-body folding for the selected row. Only visible while the
		// row is expanded, but harmless to flip otherwise.
		if len(m.txns) > 0 {
			key := keyFor(m.txns[m.cursor])
			m.jsonFolded[key] = !m.jsonFolded[key]
			m.refresh(false)
		}
	case "pgdown", "ctrl+f":
		m.viewport.PageDown()
	case "pgup", "ctrl+b":
		m.viewport.PageUp()
	}
	return m, nil
}

// keyFor returns a stable expand key. Access entries without a dump seq (seq==0)
// can't be keyed by seq, so fall back to a synthetic per-row key.
func keyFor(t *txn) uint64 {
	if t.seq > 0 {
		return t.seq
	}
	return 0
}

func (m *model) View() string {
	if !m.ready {
		return "initializing..."
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.headerView(),
		m.viewport.View(),
		m.footerView(),
	)
}

// refresh re-renders content into the viewport and keeps the cursor visible.
// When sizeChanged is false and the user was at the bottom, follow new entries.
func (m *model) refresh(sizeChanged bool) {
	if !m.ready {
		return
	}
	if m.cursor >= len(m.txns) {
		m.cursor = len(m.txns) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}

	content := m.renderContent()
	m.viewport.SetContent(content)
	m.ensureCursorVisible()
}

func (m *model) ensureCursorVisible() {
	if m.cursor < 0 || m.cursor >= len(m.txnLine) {
		return
	}
	top := m.txnLine[m.cursor]
	bottom := top
	if m.cursor+1 < len(m.txnLine) {
		bottom = m.txnLine[m.cursor+1] - 1
	} else {
		bottom = m.viewport.TotalLineCount() - 1
	}

	if top < m.viewport.YOffset {
		m.viewport.SetYOffset(top)
		return
	}
	if bottom >= m.viewport.YOffset+m.viewport.Height {
		// Prefer showing the selection's top edge; clamp handled by viewport.
		m.viewport.SetYOffset(bottom - m.viewport.Height + 1)
	}
}

func (m *model) renderContent() string {
	m.txnLine = m.txnLine[:0]
	if len(m.txns) == 0 {
		return dimStyle.Render("  waiting for traffic...")
	}

	var b strings.Builder
	line := 0
	for i, t := range m.txns {
		m.txnLine = append(m.txnLine, line)
		selected := i == m.cursor
		summary := m.renderSummary(t, selected)
		b.WriteString(summary)
		b.WriteByte('\n')
		line += lipgloss.Height(summary)

		if m.expanded[keyFor(t)] {
			detail := m.renderDetail(t)
			if detail != "" {
				b.WriteString(detail)
				b.WriteByte('\n')
				line += lipgloss.Height(detail)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) renderSummary(t *txn, selected bool) string {
	caret := "▸"
	if m.expanded[keyFor(t)] {
		caret = "▾"
	}

	seq := "  -"
	if t.seq > 0 {
		seq = fmt.Sprintf("#%-3d", t.seq)
	}

	status := "  ···"
	if t.done {
		status = methodStatusColor(t.status).Render(fmt.Sprintf("%3d", t.status))
	}

	dur := "       "
	if t.done {
		dur = durationStyle(t.duration).Render(fmt.Sprintf("%7s", formatDuration(t.duration)))
	}

	target := t.target
	if target == "" && t.hasReq {
		target = firstLine(t.reqHead)
	}

	line := fmt.Sprintf("%s %s %s %s %s  %s",
		caret,
		dimStyle.Render(seq),
		methodColor(t.method).Render(fmt.Sprintf("%-6s", orDash(t.method))),
		status,
		dur,
		urlStyle.Render(truncate(target, max(10, m.width-32))),
	)

	if t.err != "" {
		line += "  " + errStyle.Render("✗ "+t.err)
	}

	if selected {
		return selectedStyle.Width(m.width).Render(line)
	}
	return line
}

func (m *model) renderDetail(t *txn) string {
	var b strings.Builder
	wrote := false

	emit := func(label, head string, body []byte, arrow string) {
		if head == "" && len(body) == 0 {
			return
		}
		if wrote {
			b.WriteByte('\n')
		}
		wrote = true
		b.WriteString("    " + arrowStyle.Render(arrow) + " " + boldStyle.Render(label))
		for i, l := range splitLines(head) {
			if l == "" {
				continue
			}
			if i == 0 {
				b.WriteString("\n      " + boldStyle.Render(l))
			} else {
				b.WriteString("\n      " + headerLineStyle(l))
			}
		}
		if len(body) > 0 {
			m.emitBody(&b, body, m.jsonFolded[keyFor(t)])
		}
	}

	emit("request", t.reqHead, t.reqBody, "▶")
	emit("response", t.respHead, t.respBody, "◀")

	if !wrote {
		return "    " + dimStyle.Render("(no captured body — request still in flight)")
	}
	return b.String()
}

// emitBody renders a body block. JSON objects/arrays are pretty-printed, or
// collapsed to a one-line summary when folded; everything else is shown raw.
// The output is capped at maxBodyRender bytes regardless of form.
func (m *model) emitBody(b *strings.Builder, body []byte, folded bool) {
	meta := dimStyle.Render(fmt.Sprintf("body=%s", formatBytes(int64(len(body)))))

	if isFoldableJSON(body) {
		hint := "[J] fold"
		if folded {
			hint = "[J] expand"
		}
		b.WriteString("\n      " + meta + "  " + dimStyle.Render(hint))
		if folded {
			if summary, ok := jsonSummary(body); ok {
				b.WriteString("\n      " + dimStyle.Render(summary))
			}
			return
		}
		if len(body) <= maxBodyRender {
			if hl, ok := highlightJSON(body); ok {
				for _, l := range splitLines(hl) {
					b.WriteString("\n      " + l)
				}
				return
			}
		}
		// Valid JSON but too large to highlight safely; show raw, truncated.
		b.WriteString("\n      " + dimStyle.Render("(large body — highlighting skipped)"))
		writeBodyLines(b, string(body))
		return
	}

	b.WriteString("\n      " + meta)
	writeBodyLines(b, string(body))
}

// writeBodyLines appends each line of s under the detail indent, truncating the
// content at maxBodyRender bytes.
func writeBodyLines(b *strings.Builder, s string) {
	truncated := false
	if len(s) > maxBodyRender {
		s = s[:maxBodyRender]
		truncated = true
	}
	for _, l := range splitLines(s) {
		b.WriteString("\n      " + l)
	}
	if truncated {
		b.WriteString("\n      " + dimStyle.Render("... (truncated)"))
	}
}

func (m *model) headerView() string {
	title := titleStyle.Render(" http-relay · TUI ")
	info := dimStyle.Render(m.header)
	bar := lipgloss.JoinHorizontal(lipgloss.Left, title, "  ", info)
	return lipgloss.NewStyle().Width(m.width).Render(bar)
}

func (m *model) footerView() string {
	count := fmt.Sprintf(" %d reqs ", len(m.txns))
	help := "↑/↓ select · enter expand · J fold json · pgup/pgdn scroll · g/G top/bottom · q quit"
	left := footerCountStyle.Render(count)
	right := dimStyle.Render(help)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right + " "
}
