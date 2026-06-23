package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Semantic styles, mirroring the color intent of relay.Palette.
var (
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	boldStyle  = lipgloss.NewStyle().Bold(true)
	urlStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("44"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	arrowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	headerKey  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("63"))

	footerCountStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("238"))
	selectedStyle    = lipgloss.NewStyle().Background(lipgloss.Color("236"))
)

// JSON syntax-highlight styles, applied per token when a body is pretty-printed.
var (
	jsonKeyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))  // light blue
	jsonStringStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("150")) // soft green
	jsonNumberStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // orange
	jsonBoolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("170")) // magenta
	jsonNullStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245")) // grey
	jsonPunctStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // dim braces/commas
)

// methodColor colors an HTTP method by intent: read / write / delete / other.
func methodColor(method string) lipgloss.Style {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")) // green
	case "POST", "PUT", "PATCH":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // yellow/orange
	case "DELETE":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("44")) // cyan
	}
}

// methodStatusColor colors an HTTP status code by class.
func methodStatusColor(code int) lipgloss.Style {
	switch {
	case code >= 500:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	case code >= 400:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	case code >= 300:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("44"))
	case code >= 200:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	}
}

// durationStyle warns as elapsed time grows.
func durationStyle(d time.Duration) lipgloss.Style {
	switch {
	case d >= time.Second:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	case d >= 500*time.Millisecond:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	default:
		return dimStyle
	}
}

// headerLineStyle dims the key of a "Key: value" header line.
func headerLineStyle(line string) string {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return line
	}
	key := line[:idx]
	for _, r := range key {
		if r == ' ' || r == '\t' {
			return line // request/status line, not a header
		}
	}
	return headerKey.Render(key) + line[idx:]
}

func formatDuration(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	// Trim by runes; good enough for URLs (mostly ASCII).
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimRight(s[:i], "\r")
	}
	return strings.TrimRight(s, "\r")
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
