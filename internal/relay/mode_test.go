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

func TestParseModeRejectsInvalidReverseURL(t *testing.T) {
	t.Parallel()

	if _, err := ParseMode("reverse:ftp://api.example.com"); err == nil {
		t.Fatalf("expected invalid reverse URL error")
	}
}
