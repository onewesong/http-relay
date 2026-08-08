// Package plugins provides scripts bundled with the http-relay binary.
package plugins

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// BuiltInPrefix identifies a script that is embedded in the binary.
const BuiltInPrefix = "builtin:"

//go:embed built-in
var builtInFS embed.FS

// ReadBuiltIn returns an embedded script by its file name, such as
// "rewrite.openai.js". Nested paths are allowed; paths outside built-in are
// rejected.
func ReadBuiltIn(name string) ([]byte, error) {
	name = strings.TrimSpace(name)
	if name == "" || !fs.ValidPath(name) {
		return nil, fmt.Errorf("invalid built-in plugin name %q", name)
	}
	data, err := builtInFS.ReadFile("built-in/" + name)
	if err != nil {
		return nil, fmt.Errorf("read built-in plugin %q: %w", name, err)
	}
	return data, nil
}

// IsBuiltIn reports whether reference uses the embedded-plugin syntax and,
// when it does, returns the embedded file name.
func IsBuiltIn(reference string) (name string, ok bool) {
	if !strings.HasPrefix(reference, BuiltInPrefix) {
		return "", false
	}
	return strings.TrimPrefix(reference, BuiltInPrefix), true
}
