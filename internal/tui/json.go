package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// jsonIndent is the per-level indent used when pretty-printing JSON bodies.
const jsonIndent = "  "

// isFoldableJSON reports whether body is a JSON object or array, the only kinds
// worth pretty-printing and folding. Scalars are rendered raw.
func isFoldableJSON(body []byte) bool {
	return looksLikeJSONContainer(body) && json.Valid(bytes.TrimSpace(body))
}

// highlightJSON returns an indented, syntax-highlighted form of a JSON object or
// array. It walks the token stream rather than re-formatting text so that string
// values containing ':' or '"' can't be mistaken for structure, and key order is
// preserved. ok is false when body isn't a valid JSON container.
func highlightJSON(body []byte) (out string, ok bool) {
	if !looksLikeJSONContainer(body) {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(body)))
	dec.UseNumber() // keep the original number text instead of float64 rounding
	tok, err := dec.Token()
	if err != nil {
		return "", false
	}
	var b strings.Builder
	if err := writeJSONValue(&b, dec, tok, 0); err != nil {
		return "", false
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", false // trailing garbage after the top-level value
	}
	return b.String(), true
}

// writeJSONValue renders one already-read token and, for containers, the tokens
// that follow until the matching close delimiter.
func writeJSONValue(b *strings.Builder, dec *json.Decoder, tok json.Token, depth int) error {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return writeJSONObject(b, dec, depth)
		case '[':
			return writeJSONArray(b, dec, depth)
		default:
			return fmt.Errorf("tui: unexpected delim %q", t)
		}
	case string:
		b.WriteString(jsonStringStyle.Render(encodeJSONString(t)))
	case json.Number:
		b.WriteString(jsonNumberStyle.Render(t.String()))
	case bool:
		b.WriteString(jsonBoolStyle.Render(strconv.FormatBool(t)))
	case nil:
		b.WriteString(jsonNullStyle.Render("null"))
	default:
		return fmt.Errorf("tui: unexpected token %T", tok)
	}
	return nil
}

func writeJSONObject(b *strings.Builder, dec *json.Decoder, depth int) error {
	b.WriteString(jsonPunctStyle.Render("{"))
	first := true
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("tui: non-string object key %T", keyTok)
		}
		if !first {
			b.WriteString(jsonPunctStyle.Render(","))
		}
		first = false
		b.WriteByte('\n')
		b.WriteString(jsonIndentFor(depth + 1))
		b.WriteString(jsonKeyStyle.Render(encodeJSONString(key)))
		b.WriteString(jsonPunctStyle.Render(":") + " ")

		valTok, err := dec.Token()
		if err != nil {
			return err
		}
		if err := writeJSONValue(b, dec, valTok, depth+1); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // consume closing '}'
		return err
	}
	if !first {
		b.WriteByte('\n')
		b.WriteString(jsonIndentFor(depth))
	}
	b.WriteString(jsonPunctStyle.Render("}"))
	return nil
}

func writeJSONArray(b *strings.Builder, dec *json.Decoder, depth int) error {
	b.WriteString(jsonPunctStyle.Render("["))
	first := true
	for dec.More() {
		if !first {
			b.WriteString(jsonPunctStyle.Render(","))
		}
		first = false
		b.WriteByte('\n')
		b.WriteString(jsonIndentFor(depth + 1))

		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if err := writeJSONValue(b, dec, tok, depth+1); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // consume closing ']'
		return err
	}
	if !first {
		b.WriteByte('\n')
		b.WriteString(jsonIndentFor(depth))
	}
	b.WriteString(jsonPunctStyle.Render("]"))
	return nil
}

// jsonSummary returns a compact one-line description of a JSON object or array,
// e.g. "{…} 5 keys" or "[…] 3 items". ok is false when body isn't such JSON.
func jsonSummary(body []byte) (summary string, ok bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "", false
	}
	switch trimmed[0] {
	case '{':
		var m map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &m); err != nil {
			return "", false
		}
		return fmt.Sprintf("{…} %s", plural(len(m), "key", "keys")), true
	case '[':
		var a []json.RawMessage
		if err := json.Unmarshal(trimmed, &a); err != nil {
			return "", false
		}
		return fmt.Sprintf("[…] %s", plural(len(a), "item", "items")), true
	}
	return "", false
}

// looksLikeJSONContainer reports whether body, ignoring surrounding whitespace,
// starts with an object or array delimiter. It's a cheap pre-filter before the
// full parse in highlightJSON/jsonSummary.
func looksLikeJSONContainer(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	return trimmed[0] == '{' || trimmed[0] == '['
}

// encodeJSONString renders s as a JSON string literal, leaving HTML-significant
// runes ('<', '>', '&') unescaped so displayed values match the wire form.
func encodeJSONString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return strconv.Quote(s)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func jsonIndentFor(depth int) string {
	return strings.Repeat(jsonIndent, depth)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
