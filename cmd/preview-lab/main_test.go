package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFixturesAreValid(t *testing.T) {
	fixtures, err := validateFixtures()
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < 10 {
		t.Fatalf("fixture count = %d, want at least 10", len(fixtures))
	}
	seen := make(map[string]bool)
	for _, fixture := range fixtures {
		if seen[fixture.Name] {
			t.Fatalf("duplicate fixture name %q", fixture.Name)
		}
		seen[fixture.Name] = true
	}
}

func TestLabRoutesOnlyDevelopmentAssets(t *testing.T) {
	root, err := resolveRepoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newHandler(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/lab.mjs", "/fixtures.json", "/preview/core.mjs", "/fixture-assets/logo.svg", "/shared-style.css"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d", path, recorder.Code)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("production app.js exposed with status %d", recorder.Code)
	}
}

func TestFixturesEndpointReturnsJSON(t *testing.T) {
	root, _ := resolveRepoRoot("")
	handler, err := newHandler(root)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/fixtures.json", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content type = %q", recorder.Header().Get("Content-Type"))
	}
	var fixtures []fixture
	if err := json.Unmarshal(recorder.Body.Bytes(), &fixtures); err != nil || len(fixtures) == 0 {
		t.Fatalf("invalid fixture response: count=%d err=%v", len(fixtures), err)
	}
}
