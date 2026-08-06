package relay

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// AccessRecord is one access-log entry produced per relayed request.
type AccessRecord struct {
	Seq            uint64
	Namespace      string
	RewriteProfile string
	Method         string
	Target         string
	Status         int
	Duration       time.Duration
	Bytes          int64
	Err            string
}

// Reporter consumes the structured output of a relayed request. The handler
// captures data and forwards it here; how it is rendered (streaming log lines,
// an interactive TUI, ...) is the reporter's concern.
type Reporter interface {
	// RequestDump reports the captured inbound request head and body.
	RequestDump(seq uint64, namespace, head string, body []byte, remote, host string)
	// ResponseDump reports the captured upstream response head and body.
	ResponseDump(seq uint64, namespace, head string, body []byte, status string)
	// Access reports the per-request access-log summary.
	Access(rec AccessRecord)
}

// logReporter renders to a *log.Logger. With color enabled it emits layered,
// colorized blocks; otherwise it falls back to the machine-readable form.
type logReporter struct {
	logger  *log.Logger
	palette Palette
}

func newLogReporter(logger *log.Logger, palette Palette) *logReporter {
	return &logReporter{logger: logger, palette: palette}
}

func (r *logReporter) RequestDump(seq uint64, namespace, head string, body []byte, remote, host string) {
	if r.palette.Enabled() {
		r.logger.Print(r.renderDump(true, seq, head, body,
			fmt.Sprintf("remote=%s host=%s", remote, host)))
		return
	}

	r.logger.Printf(
		"---- REQUEST DUMP BEGIN id=%d remote=%s host=%s ----\n%s%s\n---- REQUEST DUMP END id=%d body_bytes=%d ----",
		seq,
		remote,
		host,
		head,
		string(body),
		seq,
		len(body),
	)
}

func (r *logReporter) ResponseDump(seq uint64, namespace, head string, body []byte, status string) {
	if r.palette.Enabled() {
		r.logger.Print(r.renderDump(false, seq, head, body,
			fmt.Sprintf("status=%s", status)))
		return
	}

	r.logger.Printf(
		"---- RESPONSE DUMP BEGIN id=%d status=%s ----\n%s%s\n---- RESPONSE DUMP END id=%d body_bytes=%d ----",
		seq,
		status,
		head,
		string(body),
		seq,
		len(body),
	)
}

func (r *logReporter) Access(rec AccessRecord) {
	p := r.palette
	if !p.Enabled() {
		routeMeta := ""
		if rec.Namespace != "" {
			routeMeta += fmt.Sprintf(" namespace=%q", rec.Namespace)
		}
		if rec.RewriteProfile != "" {
			routeMeta += fmt.Sprintf(" rewrite_profile=%q", rec.RewriteProfile)
		}
		r.logger.Printf("method=%s%s target=%q status=%d duration_ms=%d bytes=%d err=%q",
			rec.Method, routeMeta, rec.Target, rec.Status, rec.Duration.Milliseconds(), rec.Bytes, rec.Err)
		return
	}

	var b strings.Builder
	b.WriteString(p.Timestamp(time.Now()))
	b.WriteByte(' ')
	if rec.Seq > 0 {
		b.WriteString(p.Dim(fmt.Sprintf("#%d", rec.Seq)))
		b.WriteByte(' ')
	}
	fmt.Fprintf(&b, "%s %s %s %s",
		p.Method(fmt.Sprintf("%-6s", rec.Method)),
		p.Status(rec.Status),
		p.Duration(rec.Duration),
		p.Dim(fmt.Sprintf("%8s", formatBytes(rec.Bytes))),
	)
	if rec.Target != "" {
		b.WriteByte(' ')
		b.WriteString(p.URL(rec.Target))
	}
	if rec.RewriteProfile != "" {
		b.WriteByte(' ')
		b.WriteString(p.Dim("@" + rec.RewriteProfile))
	}
	if rec.Err != "" {
		b.WriteString("  ")
		b.WriteString(p.Error("✗ " + rec.Err))
	}
	r.logger.Print(b.String())
}

// renderDump builds a colored, indented dump block for one request or response.
func (r *logReporter) renderDump(isRequest bool, seq uint64, head string, body []byte, meta string) string {
	p := r.palette
	arrow, label := p.RespArrow(), "response"
	if isRequest {
		arrow, label = p.ReqArrow(), "request"
	}

	var b strings.Builder
	b.WriteString(p.Timestamp(time.Now()))
	b.WriteByte(' ')
	b.WriteString(p.Dim(fmt.Sprintf("#%d", seq)))
	fmt.Fprintf(&b, " %s %s  %s", arrow, p.Bold(label), p.Dim(meta))

	lines := strings.Split(strings.TrimRight(head, "\r\n"), "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if i == 0 {
			b.WriteString("\n    " + p.Bold(line)) // request/status line
			continue
		}
		b.WriteString("\n    " + p.Header(line))
	}

	if len(body) > 0 {
		b.WriteString("\n    " + p.Dim(fmt.Sprintf("body=%s", formatBytes(int64(len(body))))))
		for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
			b.WriteString("\n    " + line)
		}
	}
	return b.String()
}
