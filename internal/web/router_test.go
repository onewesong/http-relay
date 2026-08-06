package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebNamespaceRouteBoundaries(t *testing.T) {
	handler, _ := New(Meta{})
	tests := []struct {
		path     string
		wantCode int
		location string
	}{
		{path: "/namespace/team-a/", wantCode: http.StatusOK},
		{path: "/namespace/admin/", wantCode: http.StatusOK},
		{path: "/namespace/login/", wantCode: http.StatusOK},
		{path: "/namespace/events/", wantCode: http.StatusOK},
		{path: "/namespace/app.js/", wantCode: http.StatusOK},
		{path: "/namespace/default/", wantCode: http.StatusOK},
		{path: "/namespace/team-a?x=1", wantCode: http.StatusTemporaryRedirect, location: "/namespace/team-a/?x=1"},
		{path: "/namespace/", wantCode: http.StatusNotFound},
		{path: "/namespace//", wantCode: http.StatusNotFound},
		{path: "/namespace/-bad/", wantCode: http.StatusNotFound},
		{path: "/namespace/bad%2Fname/", wantCode: http.StatusNotFound},
		{path: "/namespace/bad%252Fname/", wantCode: http.StatusNotFound},
		{path: "/namespace/./", wantCode: http.StatusNotFound},
		{path: "/namespace/../", wantCode: http.StatusNotFound},
		{path: "/namespace/team-a/unknown/child", wantCode: http.StatusNotFound},
		{path: "/team-a/", wantCode: http.StatusNotFound},
		{path: "/team-a/events", wantCode: http.StatusNotFound},
		{path: "/team-a/api/transactions", wantCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != tt.wantCode || (tt.location != "" && rec.Header().Get("Location") != tt.location) {
				t.Fatalf("status=%d location=%q body=%q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
			}
			if strings.HasPrefix(tt.path, "/team-a") && strings.Contains(rec.Body.String(), "<title>http-relay</title>") {
				t.Fatal("legacy route returned the default page")
			}
		})
	}
}

func TestForwardedOriginRequiresExplicitTrust(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://internal/login", nil)
	req.Header.Set("Origin", "https://relay.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "relay.example")
	if sameOrigin(req, false) {
		t.Fatal("untrusted forwarded headers must be ignored")
	}
	if !sameOrigin(req, true) {
		t.Fatal("trusted forwarded origin should match")
	}
}

func TestNullOriginRequiresSameOriginFetchMetadata(t *testing.T) {
	for _, tc := range []struct {
		fetchSite string
		want      bool
	}{
		{fetchSite: "same-origin", want: true},
		{fetchSite: "SAME-ORIGIN", want: true},
		{fetchSite: "same-site", want: false},
		{fetchSite: "cross-site", want: false},
		{fetchSite: "none", want: false},
		{fetchSite: "", want: false},
	} {
		t.Run(tc.fetchSite, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7090/login", nil)
			req.Header.Set("Origin", "null")
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			if got := sameOrigin(req, false); got != tc.want {
				t.Fatalf("sameOrigin()=%t want=%t", got, tc.want)
			}
		})
	}
}
