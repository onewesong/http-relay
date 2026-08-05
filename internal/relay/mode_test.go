package relay

import (
	"net/http"
	"net/http/httptest"
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
	req := httptest.NewRequest(http.MethodGet, "http://relay.local/team-a/users", nil)
	resolved, err := mode.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Namespace != "" || resolved.URL.String() != "https://api.example.com/team-a/users" {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestParseModeRejectsInvalidReverseURL(t *testing.T) {
	t.Parallel()

	if _, err := ParseMode("reverse:ftp://api.example.com"); err == nil {
		t.Fatalf("expected invalid reverse URL error")
	}
}
