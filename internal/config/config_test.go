package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSecret() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, 32))
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "http-relay.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadJWTConfig(t *testing.T) {
	t.Setenv(EnvJWTSecret, "")
	path := writeConfig(t, `[web.auth]
mode = "jwt"
secret = "`+testSecret()+`"
token_ttl = "24h"
max_token_ttl = "48h"
allow_permanent_tokens = true
admin_enabled = true
default_protected = true
fallback_protected = false
[web.auth.namespaces]
team-a = true
public-demo = false
`)
	cfg, warnings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || cfg.Web.Auth.Mode != "jwt" || len(cfg.Web.Auth.SecretBytes) != 32 {
		t.Fatalf("cfg=%+v warnings=%v", cfg, warnings)
	}
	if cfg.Web.Auth.TokenTTL.Duration != 24*time.Hour || !cfg.Web.Auth.Protected("team-a") || cfg.Web.Auth.Protected("public-demo") {
		t.Fatalf("unexpected auth config: %+v", cfg.Web.Auth)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	t.Setenv(EnvJWTSecret, "")
	path := writeConfig(t, "[web.auth]\nunknown = true\n")
	if _, _, err := Load(path); err == nil || !strings.Contains(err.Error(), "strict mode") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadRejectsConfiguredEmptyMode(t *testing.T) {
	t.Setenv(EnvJWTSecret, "")
	path := writeConfig(t, "[web.auth]\nmode = \"  \"\n")
	if _, _, err := Load(path); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected empty mode error, got %v", err)
	}
}

func TestEmbeddedSecretPermissionWarning(t *testing.T) {
	t.Setenv(EnvJWTSecret, "")
	path := writeConfig(t, `[web.auth]
mode = "jwt"
secret = "`+testSecret()+`"
`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := Load(path)
	if err != nil || len(warnings) != 1 {
		t.Fatalf("warnings=%v err=%v", warnings, err)
	}
}

func TestSecretEnvironmentOverride(t *testing.T) {
	envSecret := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz123456"))
	t.Setenv(EnvJWTSecret, envSecret)
	path := writeConfig(t, `[web.auth]
mode = "jwt"
secret = "invalid"
`)
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Web.Auth.Secret != envSecret {
		t.Fatalf("secret was not overridden")
	}
}

func TestJWTSecretDoesNotEnableMode(t *testing.T) {
	t.Setenv(EnvJWTSecret, testSecret())
	path := writeConfig(t, "[web]\n")
	cfg, _, err := Load(path)
	if err != nil || cfg.Web.Auth.Mode != "" || len(cfg.Web.Auth.SecretBytes) != 0 {
		t.Fatalf("cfg=%+v error=%v", cfg, err)
	}
}

func TestDecodeSecretStrict(t *testing.T) {
	for _, value := range []string{"", "abcd", testSecret() + "="} {
		if _, err := DecodeSecret(value); err == nil {
			t.Fatalf("expected %q to fail", value)
		}
	}
}

func TestTTLValidation(t *testing.T) {
	t.Setenv(EnvJWTSecret, "")
	path := writeConfig(t, `[web.auth]
mode = "jwt"
secret = "`+testSecret()+`"
token_ttl = "2h"
max_token_ttl = "1h"
`)
	if _, _, err := Load(path); err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEmptyConfigKeepsDefaultsWithoutAuth(t *testing.T) {
	t.Setenv(EnvJWTSecret, "")
	cfg, warnings, err := Load("")
	if err != nil || len(warnings) != 0 || cfg.Web.Auth.Mode != "" || cfg.Web.Auth.TokenTTL.Duration <= 0 {
		t.Fatalf("cfg=%+v warnings=%v err=%v", cfg, warnings, err)
	}
}

func TestResolvePathPriority(t *testing.T) {
	t.Setenv(EnvConfigPath, "/from-env.toml")
	if got := ResolvePath(" /from-flag.toml "); got != "/from-flag.toml" {
		t.Fatalf("flag priority got %q", got)
	}
	if got := ResolvePath(""); got != "/from-env.toml" {
		t.Fatalf("env fallback got %q", got)
	}
}
