package tui

import (
	"regexp"
	"testing"
)

// ansiRE strips SGR color escapes so highlighted output can be compared by
// structure regardless of the active lipgloss color profile.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func TestHighlightJSON(t *testing.T) {
	// Key order preserved, nested array/object, and string values containing a
	// colon must not be mistaken for structure.
	hl, ok := highlightJSON([]byte(`{"b":1,"a":"x:y","c":[true,null]}`))
	if !ok {
		t.Fatal("object should be recognized as JSON")
	}
	want := "{\n" +
		"  \"b\": 1,\n" +
		"  \"a\": \"x:y\",\n" +
		"  \"c\": [\n" +
		"    true,\n" +
		"    null\n" +
		"  ]\n" +
		"}"
	if got := stripANSI(hl); got != want {
		t.Fatalf("highlight mismatch:\ngot  %q\nwant %q", got, want)
	}

	// Empty containers stay on one line.
	if hl, _ := highlightJSON([]byte(`{}`)); stripANSI(hl) != "{}" {
		t.Fatalf("empty object: got %q", stripANSI(hl))
	}
	if hl, _ := highlightJSON([]byte(`[]`)); stripANSI(hl) != "[]" {
		t.Fatalf("empty array: got %q", stripANSI(hl))
	}

	if _, ok := highlightJSON([]byte(`"just a string"`)); ok {
		t.Fatal("scalars should not be treated as foldable JSON")
	}
	if _, ok := highlightJSON([]byte(`{not json`)); ok {
		t.Fatal("invalid JSON should be rejected")
	}
	if _, ok := highlightJSON([]byte(`{} trailing`)); ok {
		t.Fatal("trailing garbage should be rejected")
	}
	if _, ok := highlightJSON([]byte("   ")); ok {
		t.Fatal("blank body should be rejected")
	}
}

func TestJSONSummary(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"a":1,"b":2,"c":3}`, "{…} 3 keys"},
		{`{"only":1}`, "{…} 1 key"},
		{`{}`, "{…} 0 keys"},
		{`[1,2,3]`, "[…] 3 items"},
		{`["one"]`, "[…] 1 item"},
		{`  [ ]  `, "[…] 0 items"},
	}
	for _, c := range cases {
		got, ok := jsonSummary([]byte(c.body))
		if !ok {
			t.Fatalf("jsonSummary(%s) not ok", c.body)
		}
		if got != c.want {
			t.Fatalf("jsonSummary(%s) = %q, want %q", c.body, got, c.want)
		}
	}

	if _, ok := jsonSummary([]byte(`42`)); ok {
		t.Fatal("scalar should not produce a summary")
	}
}
