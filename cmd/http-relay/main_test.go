package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	appconfig "github.com/onewesong/http-relay/internal/config"
	relayscript "github.com/onewesong/http-relay/internal/script"
)

func TestResolveWebOptionsCompatibility(t *testing.T) {
	base := appconfig.Config{}
	base.Web.MaxTransactionsPerNamespace = 321
	opts, err := resolveWebOptions(base, "legacy", false)
	if err != nil || opts.AuthKey != "legacy" || opts.JWTAuth != nil || opts.MaxTransactionsPerNamespace != 321 {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	cfg := appconfig.Config{}
	cfg.Web.Auth.Mode = "jwt"
	if _, err := resolveWebOptions(cfg, "legacy", false); err == nil {
		t.Fatal("expected JWT and WEB_AUTH_KEY conflict")
	}
	opts, err = resolveWebOptions(cfg, "", true)
	if err != nil || opts.JWTAuth == nil || !opts.TrustForwardedHeaders {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
}

func TestBuildScriptRegistryAppliesProfileOverrides(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "profile.js")
	if err := os.WriteFile(path, []byte(`function onRequest(req) {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := appconfig.Config{Rewrite: appconfig.RewriteConfig{Profiles: map[string]appconfig.RewriteProfile{
		"openai": {
			Script:  path,
			Timeout: appconfig.OptionalDuration{Duration: 750 * time.Millisecond, Set: true},
			Reload:  "off",
		},
	}}}

	registry, err := buildScriptRegistry(cfg, nil, 200*time.Millisecond, relayscript.ReloadWatch, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	profiles := registry.Profiles()
	if len(profiles) != 1 || profiles[0].Name != "openai" || profiles[0].Timeout != 750*time.Millisecond || profiles[0].Reload != relayscript.ReloadOff {
		t.Fatalf("profiles=%+v", profiles)
	}
}

func TestBuildScriptRegistryLoadsBuiltInProfile(t *testing.T) {
	t.Parallel()

	cfg := appconfig.Config{Rewrite: appconfig.RewriteConfig{Profiles: map[string]appconfig.RewriteProfile{
		"openai": {Script: "builtin:rewrite.openai.js", Reload: "watch"},
	}}}

	registry, err := buildScriptRegistry(cfg, nil, 200*time.Millisecond, relayscript.ReloadWatch, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	profiles := registry.Profiles()
	if len(profiles) != 1 || profiles[0].Path != "builtin:rewrite.openai.js" || profiles[0].Reload != relayscript.ReloadOff {
		t.Fatalf("profiles=%+v", profiles)
	}
}
