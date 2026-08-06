package authjwt

import (
	"strings"
	"testing"
	"time"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func issueAt(t *testing.T, namespace string, permanent bool) (string, Claims) {
	t.Helper()
	token, claims, err := Issue(Options{
		Secret: testKey, Issuer: "issuer", Audience: "audience", Namespace: namespace,
		TTL: time.Hour, Permanent: permanent, AllowPermanent: true,
		Now: time.Unix(1_800_000_000, 0), Random: strings.NewReader("0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return token, claims
}

func TestIssueAndVerify(t *testing.T) {
	token, want := issueAt(t, "team-a", false)
	got, err := Verify(token, VerifyOptions{
		Secret: testKey, Issuer: "issuer", Audience: "audience",
		Now: time.Unix(1_800_000_100, 0), AllowPermanent: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Namespace != "team-a" || got.JWTID != want.JWTID || got.ExpiresAt == nil {
		t.Fatalf("claims=%+v", got)
	}
}

func TestPermanentTokenPolicy(t *testing.T) {
	token, claims := issueAt(t, "", true)
	if !claims.Permanent() {
		t.Fatal("expected permanent claims")
	}
	base := VerifyOptions{Secret: testKey, Issuer: "issuer", Audience: "audience", Now: time.Unix(2_000_000_000, 0)}
	if _, err := Verify(token, base); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
	base.AllowPermanent = true
	if _, err := Verify(token, base); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsInvalidTokens(t *testing.T) {
	token, _ := issueAt(t, "team-a", false)
	future, _, err := Issue(Options{
		Secret: testKey, Issuer: "issuer", Audience: "audience", Namespace: "team-a",
		TTL: time.Hour, Now: time.Unix(1_800_001_000, 0), Random: strings.NewReader("0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		token string
		opts  VerifyOptions
	}{
		{name: "signature", token: token + "x", opts: VerifyOptions{Secret: testKey, Issuer: "issuer", Audience: "audience", Now: time.Unix(1_800_000_100, 0)}},
		{name: "audience", token: token, opts: VerifyOptions{Secret: testKey, Issuer: "issuer", Audience: "wrong", Now: time.Unix(1_800_000_100, 0)}},
		{name: "expired", token: token, opts: VerifyOptions{Secret: testKey, Issuer: "issuer", Audience: "audience", Now: time.Unix(1_800_004_000, 0)}},
		{name: "not-yet-valid", token: future, opts: VerifyOptions{Secret: testKey, Issuer: "issuer", Audience: "audience", Now: time.Unix(1_800_000_000, 0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Verify(tt.token, tt.opts); err == nil {
				t.Fatal("expected verification failure")
			}
		})
	}
}

func TestIssueValidation(t *testing.T) {
	if _, _, err := Issue(Options{Secret: testKey, Issuer: "i", Audience: "a", Namespace: "bad/name", TTL: time.Hour}); err == nil {
		t.Fatal("expected invalid namespace")
	}
	if _, _, err := Issue(Options{Secret: testKey, Issuer: "i", Audience: "a", Permanent: true}); err == nil {
		t.Fatal("expected permanent policy error")
	}
	if _, _, err := Issue(Options{Secret: testKey, Issuer: "i", Audience: "a", Namespace: "*", TTL: time.Hour}); err == nil {
		t.Fatal("expected wildcard namespace error")
	}
}
