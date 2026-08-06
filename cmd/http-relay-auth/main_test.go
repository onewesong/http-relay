package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/onewesong/http-relay/internal/config"
)

func cliConfig(t *testing.T, permanent bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	secret := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	body := `[web.auth]
mode = "jwt"
secret = "` + secret + `"
token_ttl = "1h"
max_token_ttl = "24h"
allow_permanent_tokens = `
	if permanent {
		body += "true\n"
	} else {
		body += "false\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCLI(t *testing.T, args []string, stdin string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestSecretOutput(t *testing.T) {
	code, out, errOut := runCLI(t, []string{"secret"}, "")
	if code != exitOK || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil || len(raw) != 32 {
		t.Fatalf("bad secret %q: %v", out, err)
	}
}

func TestIssueAndInspectFromStdin(t *testing.T) {
	path := cliConfig(t, true)
	code, token, errOut := runCLI(t, []string{"issue", "--config", path, "--namespace", "team-a"}, "")
	if code != exitOK || errOut != "" || strings.Count(strings.TrimSpace(token), ".") != 2 {
		t.Fatalf("code=%d token=%q stderr=%q", code, token, errOut)
	}
	code, out, errOut := runCLI(t, []string{"inspect", "--config", path, "-"}, token)
	if code != exitOK || errOut != "" || !strings.Contains(out, "namespace: team-a") || strings.Contains(out, strings.TrimSpace(token)) {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, errOut)
	}
}

func TestConfigPathEnvironmentFallback(t *testing.T) {
	path := cliConfig(t, false)
	t.Setenv(appconfig.EnvConfigPath, path)
	code, token, errOut := runCLI(t, []string{"issue", "--namespace", "team-a"}, "")
	if code != exitOK || strings.TrimSpace(token) == "" || errOut != "" {
		t.Fatalf("code=%d token=%q stderr=%q", code, token, errOut)
	}
}

func TestIssuePermanentAdmin(t *testing.T) {
	path := cliConfig(t, true)
	code, token, errOut := runCLI(t, []string{"issue", "--config", path, "--admin", "--permanent"}, "")
	if code != exitOK || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	code, out, _ := runCLI(t, []string{"inspect", "--config", path, "-"}, token)
	if code != exitOK || !strings.Contains(out, "namespace: (admin)") || !strings.Contains(out, "expires: never") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestIssueArgumentValidation(t *testing.T) {
	path := cliConfig(t, false)
	tests := [][]string{
		{"issue", "--config", path},
		{"issue", "--config", path, "--admin", "--namespace", "team-a"},
		{"issue", "--config", path, "--admin", "--permanent"},
		{"issue", "--config", path, "--namespace", "team-a", "--ttl", "25h"},
	}
	for _, args := range tests {
		if code, _, _ := runCLI(t, args, ""); code == exitOK {
			t.Fatalf("expected failure for %v", args)
		}
	}
}
