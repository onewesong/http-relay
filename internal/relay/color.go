package relay

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ColorMode controls when ANSI colors are emitted.
type ColorMode uint8

const (
	ColorAuto ColorMode = iota // color only on a TTY and when NO_COLOR is unset
	ColorAlways
	ColorNever
)

// ParseColorMode parses the --color flag value. An empty value means auto.
func ParseColorMode(raw string) (ColorMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return ColorAuto, true
	case "always", "force", "on":
		return ColorAlways, true
	case "never", "off", "none":
		return ColorNever, true
	default:
		return ColorAuto, false
	}
}

// Palette renders semantic, colorized fragments. The zero value is disabled,
// so output stays plain unless a palette is explicitly enabled.
type Palette struct {
	enabled bool
}

// NewPalette decides whether color is on for the given output file.
func NewPalette(mode ColorMode, out *os.File) Palette {
	switch mode {
	case ColorAlways:
		return Palette{enabled: true}
	case ColorNever:
		return Palette{enabled: false}
	default:
		return Palette{enabled: os.Getenv("NO_COLOR") == "" && isTerminal(out)}
	}
}

func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Enabled reports whether the palette emits color.
func (p Palette) Enabled() bool { return p.enabled }

func (p Palette) wrap(code, s string) string {
	if !p.enabled || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p Palette) Dim(s string) string  { return p.wrap("2", s) }
func (p Palette) Bold(s string) string { return p.wrap("1", s) }

// Timestamp renders a dim timestamp prefix (plain when color is off).
func (p Palette) Timestamp(t time.Time) string {
	return p.Dim(t.Format("2006/01/02 15:04:05"))
}

// Method colors an HTTP method by intent: read / write / delete / other.
func (p Palette) Method(method string) string {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET":
		return p.wrap("32", method)
	case "POST", "PUT", "PATCH":
		return p.wrap("33", method)
	case "DELETE":
		return p.wrap("31", method)
	default:
		return p.wrap("36", method)
	}
}

// Status colors an HTTP status code by class.
func (p Palette) Status(code int) string {
	s := strconv.Itoa(code)
	switch {
	case code >= 500:
		return p.wrap("91;1", s)
	case code >= 400:
		return p.wrap("33", s)
	case code >= 300:
		return p.wrap("36", s)
	case code >= 200:
		return p.wrap("32", s)
	default:
		return p.wrap("37", s)
	}
}

// Duration colors elapsed time, warning as it grows.
func (p Palette) Duration(d time.Duration) string {
	s := formatDuration(d)
	switch {
	case d >= time.Second:
		return p.wrap("31", s)
	case d >= 500*time.Millisecond:
		return p.wrap("33", s)
	default:
		return p.wrap("2", s)
	}
}

// URL renders a target URL with link-like emphasis.
func (p Palette) URL(s string) string { return p.wrap("36", s) }

// ReqArrow / RespArrow mark dump direction: inbound vs upstream.
func (p Palette) ReqArrow() string  { return p.wrap("34", "▶") }
func (p Palette) RespArrow() string { return p.wrap("35", "◀") }

// Error renders an error fragment.
func (p Palette) Error(s string) string { return p.wrap("91", s) }

// Header dims the key of a "Key: value" header line; non-headers pass through.
func (p Palette) Header(line string) string {
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
	return p.Dim(key) + line[idx:]
}

func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	default:
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
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
