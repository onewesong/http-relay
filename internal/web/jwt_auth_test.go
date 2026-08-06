package web

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onewesong/http-relay/internal/authjwt"
	appconfig "github.com/onewesong/http-relay/internal/config"
	"github.com/onewesong/http-relay/internal/relay"
)

func jwtTestConfig() *appconfig.AuthConfig {
	return &appconfig.AuthConfig{
		Mode: "jwt", SecretBytes: []byte("0123456789abcdef0123456789abcdef"),
		Issuer: "http-relay", Audience: "http-relay-web",
		TokenTTL: appconfig.Duration{Duration: time.Hour}, MaxTokenTTL: appconfig.Duration{Duration: 24 * time.Hour},
		AllowPermanentTokens: true, AdminEnabled: true, DefaultProtected: true, FallbackProtected: true,
		Namespaces: map[string]bool{"public-demo": false, "configured": true},
	}
}

func jwtToken(t *testing.T, cfg *appconfig.AuthConfig, namespace string, permanent bool) string {
	t.Helper()
	token, _, err := authjwt.Issue(authjwt.Options{
		Secret: cfg.SecretBytes, Issuer: cfg.Issuer, Audience: cfg.Audience,
		Namespace: namespace, TTL: time.Hour, Permanent: permanent,
		AllowPermanent: cfg.AllowPermanentTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func jwtLoginRequest(token, next string) *http.Request {
	form := url.Values{"key": {token}, "next": {next}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://example.com")
	return req
}

func loginJWT(t *testing.T, handler http.Handler, token, wantLocation string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, jwtLoginRequest(token, "/attacker-controlled"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != wantLocation {
		t.Fatalf("login status=%d location=%q body=%q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%+v", cookies)
	}
	return cookies[0]
}

func requestWithCookie(method, target string, cookie *http.Cookie) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return req
}

func TestJWTNamespaceIsolationAndAdminBypass(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg})

	teamCookie := loginJWT(t, handler, jwtToken(t, cfg, "team-a", false), "/namespace/team-a/")
	if teamCookie.Path != "/namespace/team-a/" || len(teamCookie.Name) < len("http_relay_web_session_")+32 {
		t.Fatalf("team cookie=%+v", teamCookie)
	}
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/namespace/team-a/api/transactions", http.StatusOK},
		{"/namespace/team-b/api/transactions", http.StatusUnauthorized},
		{"/api/transactions", http.StatusUnauthorized},
		{"/admin/api/namespaces", http.StatusUnauthorized},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, requestWithCookie(http.MethodGet, tc.path, teamCookie))
		if rec.Code != tc.want {
			t.Errorf("GET %s status=%d want=%d", tc.path, rec.Code, tc.want)
		}
	}

	adminCookie := loginJWT(t, handler, jwtToken(t, cfg, "", false), "/admin/")
	if adminCookie.Name != adminAuthCookieName || adminCookie.Path != "/" {
		t.Fatalf("admin cookie=%+v", adminCookie)
	}
	for _, path := range []string{"/", "/api/transactions", "/namespace/team-a/api/transactions", "/namespace/team-b/api/transactions", "/admin/api/namespaces"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, requestWithCookie(http.MethodGet, path, adminCookie))
		if rec.Code != http.StatusOK {
			t.Errorf("admin GET %s status=%d", path, rec.Code)
		}
	}
}

func TestJWTAuthPublicNamespaceAndPermanentCookie(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/namespace/public-demo/api/transactions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("public status=%d", rec.Code)
	}
	cookie := loginJWT(t, handler, jwtToken(t, cfg, "public-demo", true), "/namespace/public-demo/")
	if !cookie.Expires.IsZero() || cookie.MaxAge != 0 {
		t.Fatalf("permanent JWT should use a browser-session cookie: %+v", cookie)
	}
}

func TestUnifiedLoginRejectsOriginAndInvalidToken(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg})
	req := jwtLoginRequest(jwtToken(t, cfg, "team-a", false), "")
	req.Header.Del("Origin")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing origin status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, jwtLoginRequest("not-a-token", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "密钥无效") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("invalid login status=%d cookies=%v", rec.Code, rec.Result().Cookies())
	}
}

func TestJWTScopedLogoutKeepsCookiePath(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg})
	cookie := loginJWT(t, handler, jwtToken(t, cfg, "team-a", false), "/namespace/team-a/")
	req := requestWithCookie(http.MethodPost, "/namespace/team-a/logout", cookie)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	deleted := rec.Result().Cookies()
	if rec.Code != http.StatusSeeOther || len(deleted) != 1 || deleted[0].Name != cookie.Name || deleted[0].Path != cookie.Path || deleted[0].MaxAge >= 0 {
		t.Fatalf("logout status=%d cookies=%+v", rec.Code, deleted)
	}
}

func TestJWTNamespaceCookiesCoexistAndLogoutIsScoped(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg})
	teamA := loginJWT(t, handler, jwtToken(t, cfg, "team-a", false), "/namespace/team-a/")
	teamB := loginJWT(t, handler, jwtToken(t, cfg, "team-b", false), "/namespace/team-b/")
	if teamA.Name == teamB.Name || teamA.Path == teamB.Path {
		t.Fatalf("namespace cookies overlap: a=%+v b=%+v", teamA, teamB)
	}

	logout := requestWithCookie(http.MethodPost, "/namespace/team-a/logout", teamA)
	logout.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, logout)
	deleted := rec.Result().Cookies()
	if len(deleted) != 1 || deleted[0].Name != teamA.Name || deleted[0].Name == teamB.Name {
		t.Fatalf("logout deleted wrong cookie: %+v", deleted)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithCookie(http.MethodGet, "/namespace/team-b/api/transactions", teamB))
	if rec.Code != http.StatusOK {
		t.Fatalf("team-b session was affected: status=%d", rec.Code)
	}
}

func TestJWTNamespaceCannotReadStreamOrClearAnotherNamespace(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg})
	teamA := loginJWT(t, handler, jwtToken(t, cfg, "team-a", false), "/namespace/team-a/")
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/namespace/team-b/"},
		{http.MethodGet, "/namespace/team-b/events"},
		{http.MethodGet, "/namespace/team-b/api/transactions"},
		{http.MethodDelete, "/namespace/team-b/api/transactions"},
	} {
		req := requestWithCookie(tc.method, tc.path, teamA)
		if tc.method == http.MethodDelete {
			req.Header.Set("Origin", "http://example.com")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if tc.path == "/namespace/team-b/" {
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
				t.Fatalf("%s %s status=%d location=%q", tc.method, tc.path, rec.Code, rec.Header().Get("Location"))
			}
		} else if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status=%d", tc.method, tc.path, rec.Code)
		}
	}
}

func TestBrowserWritesRequireOriginAndLimitBodies(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg})
	team := loginJWT(t, handler, jwtToken(t, cfg, "team-a", false), "/namespace/team-a/")
	for _, path := range []string{"/namespace/team-a/logout", "/namespace/team-a/api/transactions"} {
		method := http.MethodPost
		if strings.Contains(path, "transactions") {
			method = http.MethodDelete
		}
		for _, origin := range []string{"", "https://attacker.example"} {
			req := requestWithCookie(method, path, team)
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s origin=%q status=%d", path, origin, rec.Code)
			}
		}
		req := httptest.NewRequest(method, path, strings.NewReader(strings.Repeat("x", maxWriteBodyBytes+1)))
		req.Header.Set("Origin", "http://example.com")
		req.AddCookie(team)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s oversized body status=%d", path, rec.Code)
		}
	}
}

func TestAdminDisabledReturnsNotFound(t *testing.T) {
	cfg := jwtTestConfig()
	cfg.AdminEnabled = false
	handler, _ := New(Meta{}, Options{JWTAuth: cfg})
	for _, path := range []string{"/admin/", "/admin/app.mjs", "/admin/events", "/admin/api/namespaces"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s status=%d", path, rec.Code)
		}
	}
}

func TestAdminSnapshotAndTokenIssue(t *testing.T) {
	cfg := jwtTestConfig()
	handler, reporter := New(Meta{}, Options{JWTAuth: cfg, Logger: logDiscard()})
	reporter.Access(relay.AccessRecord{Seq: 1, Namespace: "recorded", Method: "GET", Status: 200})
	adminCookie := loginJWT(t, handler, jwtToken(t, cfg, "", false), "/admin/")

	webHandler := handler
	// Create an active subscriber with no retained records.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/namespace/subscribed/events", nil).WithContext(ctx)
	req.AddCookie(adminCookie)
	stream := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { webHandler.ServeHTTP(stream, req); close(done) }()
	time.Sleep(10 * time.Millisecond)

	rec := httptest.NewRecorder()
	webHandler.ServeHTTP(rec, requestWithCookie(http.MethodGet, "/admin/api/namespaces", adminCookie))
	var state adminState
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &state) != nil {
		t.Fatalf("snapshot status=%d body=%q", rec.Code, rec.Body.String())
	}
	seen := map[string]bool{}
	for _, status := range state.Namespaces {
		seen[status.Namespace] = true
	}
	for _, namespace := range []string{"", "configured", "recorded", "subscribed"} {
		if !seen[namespace] {
			t.Errorf("missing namespace %q in %+v", namespace, state.Namespaces)
		}
	}
	cancel()
	<-done

	body := `{"namespace":"public-demo","ttl":"2h"}`
	issueReq := httptest.NewRequest(http.MethodPost, "/admin/api/tokens", strings.NewReader(body))
	issueReq.Header.Set("Content-Type", "application/json")
	issueReq.Header.Set("Origin", "http://example.com")
	issueReq.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	webHandler.ServeHTTP(rec, issueReq)
	var issued issueResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &issued) != nil || issued.Token == "" || issued.Protected {
		t.Fatalf("issue status=%d body=%q", rec.Code, rec.Body.String())
	}
	claims, err := authjwt.Verify(issued.Token, authjwt.VerifyOptions{Secret: cfg.SecretBytes, Issuer: cfg.Issuer, Audience: cfg.Audience, AllowPermanent: true})
	if err != nil || claims.Namespace != "public-demo" {
		t.Fatalf("issued claims=%+v err=%v", claims, err)
	}
	issuedCookie := loginJWT(t, handler, issued.Token, "/namespace/public-demo/")
	for path, want := range map[string]int{
		"/namespace/public-demo/api/transactions": http.StatusOK,
		"/namespace/team-a/api/transactions":      http.StatusUnauthorized,
	} {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, requestWithCookie(http.MethodGet, path, issuedCookie))
		if rec.Code != want {
			t.Fatalf("issued token GET %s status=%d want=%d", path, rec.Code, want)
		}
	}
}

func TestAdminIssueRateLimit(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg, Logger: logDiscard()})
	admin := loginJWT(t, handler, jwtToken(t, cfg, "", false), "/admin/")
	for i := 0; i <= issueLimit; i++ {
		req := httptest.NewRequest(http.MethodPost, "/admin/api/tokens", strings.NewReader(`{"namespace":"team-a","ttl":"1h"}`))
		req.RemoteAddr = "192.0.2.10:1234"
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(admin)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == issueLimit {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("attempt=%d status=%d want=%d", i+1, rec.Code, want)
		}
	}
}

func TestAdminSecurityAndIssueValidation(t *testing.T) {
	cfg := jwtTestConfig()
	handler, _ := New(Meta{}, Options{JWTAuth: cfg, Logger: logDiscard()})
	admin := loginJWT(t, handler, jwtToken(t, cfg, "", false), "/admin/")
	team := loginJWT(t, handler, jwtToken(t, cfg, "team-a", false), "/namespace/team-a/")

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, requestWithCookie(http.MethodGet, "/admin/", admin))
	if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("Content-Security-Policy") == "" || page.Header().Get("Referrer-Policy") != "no-referrer" || page.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("admin security headers missing: status=%d headers=%v", page.Code, page.Header())
	}
	for _, path := range []string{"/admin/", "/admin/api/namespaces"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, requestWithCookie(http.MethodGet, path, team))
		if path == "/admin/" && rec.Code != http.StatusSeeOther {
			t.Fatalf("restricted token admin page status=%d", rec.Code)
		}
		if path != "/admin/" && rec.Code != http.StatusUnauthorized {
			t.Fatalf("restricted token admin API status=%d", rec.Code)
		}
	}

	tests := []struct {
		name        string
		body        string
		origin      string
		contentType string
		want        int
	}{
		{"empty namespace", `{"namespace":"","ttl":"1h"}`, "http://example.com", "application/json", http.StatusBadRequest},
		{"claim injection", `{"namespace":"team-a","ttl":"1h","sub":"root"}`, "http://example.com", "application/json", http.StatusBadRequest},
		{"excess ttl", `{"namespace":"team-a","ttl":"25h"}`, "http://example.com", "application/json", http.StatusBadRequest},
		{"cross origin", `{"namespace":"team-a","ttl":"1h"}`, "https://attacker.example", "application/json", http.StatusForbidden},
		{"missing origin", `{"namespace":"team-a","ttl":"1h"}`, "", "application/json", http.StatusForbidden},
		{"wrong content type", `{"namespace":"team-a","ttl":"1h"}`, "http://example.com", "text/plain", http.StatusUnsupportedMediaType},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/api/tokens", strings.NewReader(tc.body))
			req.RemoteAddr = "192.0.2." + string(rune('1'+i)) + ":1234"
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			req.Header.Set("Content-Type", tc.contentType)
			req.AddCookie(admin)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%q", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestSSEStopsAtAuthenticationExpiry(t *testing.T) {
	s := newStore(Meta{})
	expires := time.Now().Add(-authjwt.ClockSkew + 30*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	req = withAuthContext(req, authContext{enabled: true, expires: &expires})
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.handleEvents(rec, req); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("SSE did not stop at token expiry")
	}
}

func TestAdminSSEStopsAtAuthenticationExpiry(t *testing.T) {
	cfg := jwtTestConfig()
	auth := newAuthenticator(Options{JWTAuth: cfg})
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		t.Fatal(err)
	}
	admin := newAdminServer(newStore(Meta{}), auth, static, logDiscard())
	expires := time.Now().Add(-authjwt.ClockSkew + 30*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/admin/events", nil).WithContext(ctx)
	req = withAuthContext(req, authContext{enabled: true, expires: &expires, admin: true})
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { admin.handleEvents(rec, req); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("admin SSE did not stop at token expiry")
	}
}

func logDiscard() *log.Logger { return log.New(io.Discard, "", 0) }
