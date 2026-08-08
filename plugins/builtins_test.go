package plugins

import (
	"strings"
	"testing"
)

func TestReadBuiltIn(t *testing.T) {
	t.Parallel()

	source, err := ReadBuiltIn("rewrite.openai.js")
	if err != nil {
		t.Fatalf("ReadBuiltIn: %v", err)
	}
	if !strings.Contains(string(source), "function onRequest") {
		t.Fatalf("embedded script is missing onRequest: %s", source)
	}
}

func TestReadBuiltInRejectsInvalidNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "../rewrite.openai.js", "/rewrite.openai.js"} {
		if _, err := ReadBuiltIn(name); err == nil {
			t.Errorf("ReadBuiltIn(%q) succeeded, want error", name)
		}
	}
}
