package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseModeRegular(t *testing.T) {
	t.Parallel()

	mode, err := ParseMode("regular")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://relay.local/", nil)
	req.RequestURI = "/https://example.com/a?x=1"
	target, err := mode.TargetURL(req)
	if err != nil {
		t.Fatalf("target error: %v", err)
	}
	if target.String() != "https://example.com/a?x=1" {
		t.Fatalf("target=%q", target.String())
	}
}

func TestRegularModeResolveNamespace(t *testing.T) {
	t.Parallel()

	mode, err := ParseMode("regular")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://relay.local/team-a/https://example.com/a?x=1", nil)
	req.RequestURI = "/team-a/https://example.com/a?x=1"
	resolved, err := mode.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Namespace != "team-a" || resolved.URL.String() != "https://example.com/a?x=1" {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestRegularModeResolveRewriteProfile(t *testing.T) {
	t.Parallel()
	mode := DefaultTargetMode()
	tests := []struct {
		path      string
		namespace string
		profile   string
		wantURL   string
	}{
		{"/@openai/https://example.com/a?x=1", "", "openai", "https://example.com/a?x=1"},
		{"/team-a/@openai/https://example.com/a?x=1", "team-a", "openai", "https://example.com/a?x=1"},
		{"/https://example.com/a", "", "", "https://example.com/a"},
		{"/team-a/https://example.com/a", "team-a", "", "https://example.com/a"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://relay.local"+tc.path, nil)
			req.RequestURI = tc.path
			resolved, err := mode.Resolve(req)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Namespace != tc.namespace || resolved.RewriteProfile != tc.profile || resolved.URL.String() != tc.wantURL {
				t.Fatalf("resolved=%+v", resolved)
			}
		})
	}
}

func TestRegularModeRejectsInvalidRewriteProfilePaths(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/@/https://example.com", "/@-bad/https://example.com", "/@bad%2Fname/https://example.com",
		"/%40openai/https://example.com", "/team-a/@openai/", "/team-a/@openai/@other/https://example.com",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://relay.local/", nil)
			req.RequestURI = path
			if _, err := DefaultTargetMode().Resolve(req); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func FuzzRegularModeResolveRewriteProfile(f *testing.F) {
	for _, path := range []string{"/@openai/https://example.com", "/team-a/@mock/http://example.com/a", "/@/https://example.com", "/%40x/https://example.com"} {
		f.Add(path)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if strings.ContainsAny(path, "\r\n") {
			return
		}
		req := httptest.NewRequest(http.MethodGet, "http://relay.local/", nil)
		req.RequestURI = path
		_, _ = DefaultTargetMode().Resolve(req)
	})
}

func TestReverseModeTargetURL(t *testing.T) {
	t.Parallel()

	mode, err := ParseMode("reverse:https://api.example.com/base?fixed=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://relay.local/v1/users?q=go", nil)
	target, err := mode.TargetURL(req)
	if err != nil {
		t.Fatalf("target error: %v", err)
	}

	want := "https://api.example.com/base/v1/users?fixed=1&q=go"
	if target.String() != want {
		t.Fatalf("target=%q want=%q", target.String(), want)
	}
}

func TestReverseModeDoesNotInterpretNamespace(t *testing.T) {
	t.Parallel()

	mode, err := ParseMode("reverse:https://api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://relay.local/team-a/@openai/users", nil)
	resolved, err := mode.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Namespace != "" || resolved.RewriteProfile != "" || resolved.URL.String() != "https://api.example.com/team-a/@openai/users" {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestParseModeRejectsInvalidReverseURL(t *testing.T) {
	t.Parallel()

	if _, err := ParseMode("reverse:ftp://api.example.com"); err == nil {
		t.Fatalf("expected invalid reverse URL error")
	}
}
