package main

import (
	"testing"

	appconfig "github.com/onewesong/http-relay/internal/config"
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
