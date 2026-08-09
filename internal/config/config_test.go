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
	t.Setenv(EnvMaxTransactionsPerNamespace, "")
	path := filepath.Join(t.TempDir(), "http-relay.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMaxTransactionsPerNamespace(t *testing.T) {
	path := writeConfig(t, "[web]\nmax_transactions_per_namespace = 250\n")
	cfg, _, err := Load(path)
	if err != nil || cfg.Web.MaxTransactionsPerNamespace != 250 {
		t.Fatalf("max=%d err=%v", cfg.Web.MaxTransactionsPerNamespace, err)
	}

	t.Setenv(EnvMaxTransactionsPerNamespace, "75")
	cfg, _, err = Load(path)
	if err != nil || cfg.Web.MaxTransactionsPerNamespace != 75 {
		t.Fatalf("environment override max=%d err=%v", cfg.Web.MaxTransactionsPerNamespace, err)
	}
}

func TestRewriteProfiles(t *testing.T) {
	path := writeConfig(t, `[rewrite.profiles.openai]
script = "./scripts/openai.js"
timeout = "350ms"
reload = "poll"
`)
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := cfg.Rewrite.Profiles["openai"]
	wantPath := filepath.Join(filepath.Dir(path), "scripts", "openai.js")
	if profile.Script != wantPath || !profile.Timeout.Set || profile.Timeout.Duration != 350*time.Millisecond || profile.Reload != "poll" {
		t.Fatalf("profile=%+v", profile)
	}
}

func TestRewriteProfileKeepsBuiltInReference(t *testing.T) {
	path := writeConfig(t, `[rewrite.profiles.openai]
script = "builtin:rewrite.openai.js"
`)
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Rewrite.Profiles["openai"].Script; got != "builtin:rewrite.openai.js" {
		t.Fatalf("script=%q", got)
	}
}

func TestRewriteHTTPDefaultsDisabled(t *testing.T) {
	cfg, _, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Rewrite.HTTP
	if h.Enabled || h.Timeout.Duration != DefaultRewriteHTTPTimeout || h.MaxTimeout.Duration != DefaultRewriteHTTPMaxTimeout || h.MaxCallsPerHook != DefaultRewriteHTTPMaxCalls {
		t.Fatalf("rewrite.http defaults=%+v", h)
	}
}

func TestRewriteSSELimits(t *testing.T) {
	path := writeConfig(t, `[rewrite]
max_sse_event_bytes = 2048
max_sse_events_per_response = 12
`)
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rewrite.MaxSSEEventBytes != 2048 || cfg.Rewrite.MaxSSEEventsPerResponse != 12 {
		t.Fatalf("rewrite limits = %+v", cfg.Rewrite)
	}
}

func TestRewriteHTTPConfig(t *testing.T) {
	path := writeConfig(t, `[rewrite.http]
enabled = true
allowed_origins = ["HTTPS://Bücher.Example:443", "https://xn--bcher-kva.example"]
timeout = "500ms"
max_timeout = "2s"
max_request_body_bytes = 1024
max_response_body_bytes = 2048
max_calls_per_hook = 2
follow_redirects = true
allow_private_networks = true
`)
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Rewrite.HTTP
	if len(h.AllowedOrigins) != 1 || h.AllowedOrigins[0] != "https://xn--bcher-kva.example:443" {
		t.Fatalf("origins=%v", h.AllowedOrigins)
	}
	if !h.Enabled || h.Timeout.Duration != 500*time.Millisecond || h.MaxTimeout.Duration != 2*time.Second || h.MaxRequestBodyBytes != 1024 || h.MaxResponseBodyBytes != 2048 || h.MaxCallsPerHook != 2 || !h.FollowRedirects || !h.AllowPrivateNetworks {
		t.Fatalf("rewrite.http=%+v", h)
	}
}

func TestRewriteHTTPValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"enabled without origins", "[rewrite.http]\nenabled=true\n", "allowed_origins"},
		{"origin path", "[rewrite.http]\nallowed_origins=[\"https://example.com/path\"]\n", "must not contain"},
		{"origin userinfo", "[rewrite.http]\nallowed_origins=[\"https://u:p@example.com\"]\n", "must not contain"},
		{"origin wildcard", "[rewrite.http]\nallowed_origins=[\"https://*.example.com\"]\n", "invalid hostname"},
		{"origin query", "[rewrite.http]\nallowed_origins=[\"https://example.com?q=1\"]\n", "must not contain"},
		{"bad scheme", "[rewrite.http]\nallowed_origins=[\"ftp://example.com\"]\n", "http or https"},
		{"max below default", "[rewrite.http]\ntimeout=\"2s\"\nmax_timeout=\"1s\"\n", "max_timeout"},
		{"body too large", "[rewrite.http]\nmax_response_body_bytes=16777217\n", "max_response_body_bytes"},
		{"calls too large", "[rewrite.http]\nmax_calls_per_hook=17\n", "max_calls_per_hook"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.body)
			if _, _, err := Load(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want=%q", err, tc.want)
			}
		})
	}
}

func TestRewriteProfileValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"invalid name", `[rewrite.profiles."bad/name"]
script = "x.js"
`, "invalid rewrite profile name"},
		{"missing script", `[rewrite.profiles.openai]
`, "script is required"},
		{"zero timeout", `[rewrite.profiles.openai]
script = "x.js"
timeout = "0s"
`, "timeout must be greater"},
		{"invalid reload", `[rewrite.profiles.openai]
script = "x.js"
reload = "sometimes"
`, "invalid reload"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.body)
			if _, _, err := Load(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want=%q", err, tc.want)
			}
		})
	}
}

func TestMaxTransactionsPerNamespaceEnvironmentWithoutConfig(t *testing.T) {
	t.Setenv(EnvMaxTransactionsPerNamespace, "42")
	cfg, _, err := Load("")
	if err != nil || cfg.Web.MaxTransactionsPerNamespace != 42 {
		t.Fatalf("max=%d err=%v", cfg.Web.MaxTransactionsPerNamespace, err)
	}
}

func TestRejectsInvalidMaxTransactionsPerNamespace(t *testing.T) {
	for _, value := range []string{"0", "-1", "many"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(EnvMaxTransactionsPerNamespace, value)
			if _, _, err := Load(""); err == nil || !strings.Contains(err.Error(), EnvMaxTransactionsPerNamespace) {
				t.Fatalf("expected invalid environment error, got %v", err)
			}
		})
	}

	path := writeConfig(t, "[web]\nmax_transactions_per_namespace = 0\n")
	if _, _, err := Load(path); err == nil || !strings.Contains(err.Error(), "must be greater than zero") {
		t.Fatalf("expected invalid TOML error, got %v", err)
	}
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
