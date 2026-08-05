package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWebAuthDisabledKeepsUIPublic(t *testing.T) {
	handler, _ := New(Meta{})
	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWebAuthProtectsUIAndAllowsLoggedInSession(t *testing.T) {
	handler, _ := New(Meta{}, Options{AuthKey: "correct horse battery staple"})

	for _, path := range []string{"/", "/style.css"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login?next=") {
			t.Fatalf("GET %s: status=%d location=%q", path, rec.Code, rec.Header().Get("Location"))
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/team-a/?view=requests", nil))
	location := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || location != "/login?next=%2Fteam-a%2F%3Fview%3Drequests" {
		t.Fatalf("namespaced login redirect: status=%d location=%q", rec.Code, location)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/transactions", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("API status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events", nil))
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("SSE status=%d cache-control=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}

	wrong := loginRequest("wrong", "/")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, wrong)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "密钥不正确") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("wrong login: status=%d body=%q cookies=%v", rec.Code, rec.Body.String(), rec.Result().Cookies())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, loginRequest("correct horse battery staple", "/api/transactions"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/api/transactions" {
		t.Fatalf("login: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].MaxAge <= 0 {
		t.Fatalf("unexpected session cookie: %+v", cookies)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated API status = %d, want %d", rec.Code, http.StatusOK)
	}

	tampered := *cookies[0]
	tampered.Value += "x"
	req = httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	req.AddCookie(&tampered)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered session status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("logout: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	if got := rec.Result().Cookies(); len(got) != 1 || got[0].MaxAge >= 0 {
		t.Fatalf("logout cookie = %+v", got)
	}
}

func TestSessionExpiryAndSecureForwardedCookie(t *testing.T) {
	a := newAuthenticator(Options{AuthKey: "key", SessionTTL: time.Hour})
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	a.now = func() time.Time { return now }
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	a.setSession(rec, req)
	cookie := rec.Result().Cookies()[0]
	if !cookie.Secure {
		t.Fatal("forwarded HTTPS session cookie must be Secure")
	}

	check := httptest.NewRequest(http.MethodGet, "/", nil)
	check.AddCookie(cookie)
	if !a.validSession(check) {
		t.Fatal("fresh session should be valid")
	}
	if newAuthenticator(Options{AuthKey: "key", SessionTTL: time.Hour}).validSession(check) {
		t.Fatal("session must be invalid after a server restart")
	}
	now = now.Add(time.Hour)
	if a.validSession(check) {
		t.Fatal("expired session should be invalid")
	}
}

func loginRequest(key, next string) *http.Request {
	form := url.Values{"key": {key}, "next": {next}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}
