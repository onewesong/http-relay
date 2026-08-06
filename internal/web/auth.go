package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onewesong/http-relay/internal/authjwt"
	appconfig "github.com/onewesong/http-relay/internal/config"
)

const (
	legacyAuthCookieName = "http_relay_web_session"
	adminAuthCookieName  = "http_relay_web_admin_session"
	defaultSessionTTL    = 24 * time.Hour
	maxLoginBodyBytes    = 64 << 10
	maxWriteBodyBytes    = 16 << 10
)

type Options struct {
	AuthKey                     string
	SessionTTL                  time.Duration
	MaxTransactionsPerNamespace int
	JWTAuth                     *appconfig.AuthConfig
	Logger                      *log.Logger
	TrustForwardedHeaders       bool
}

type authenticator struct {
	legacyKey             []byte
	jwt                   *appconfig.AuthConfig
	sessionKey            []byte
	ttl                   time.Duration
	now                   func() time.Time
	trustForwardedHeaders bool
}

type authContextKey struct{}

type authContext struct {
	enabled               bool
	expires               *time.Time
	admin                 bool
	trustForwardedHeaders bool
}

func newAuthenticator(opts Options) *authenticator {
	ttl := opts.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	sessionKey := make([]byte, 32)
	if _, err := rand.Read(sessionKey); err != nil {
		panic("web: generate session signing key: " + err.Error())
	}
	return &authenticator{
		legacyKey: []byte(opts.AuthKey), jwt: opts.JWTAuth,
		sessionKey: sessionKey, ttl: ttl, now: time.Now,
		trustForwardedHeaders: opts.TrustForwardedHeaders || (opts.JWTAuth != nil && opts.JWTAuth.TrustForwardedHeaders),
	}
}

func (a *authenticator) jwtEnabled() bool    { return a.jwt != nil && a.jwt.Mode == "jwt" }
func (a *authenticator) legacyEnabled() bool { return len(a.legacyKey) != 0 }

func (a *authenticator) protected(namespace string) bool {
	if a.jwtEnabled() {
		return a.jwt.Protected(namespace)
	}
	return a.legacyEnabled()
}

func (a *authenticator) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		namespace := namespaceFromRequest(r)
		required := a.protected(namespace)
		if a.jwtEnabled() {
			claims, ok := a.validJWTSession(r, namespace, false)
			if ok {
				next.ServeHTTP(w, withAuthContext(r, authContext{enabled: required, expires: claimExpiry(claims), admin: claims.Namespace == "", trustForwardedHeaders: a.trustForwardedHeaders}))
				return
			}
			if !required {
				next.ServeHTTP(w, withAuthContext(r, authContext{enabled: false, trustForwardedHeaders: a.trustForwardedHeaders}))
				return
			}
			a.unauthorized(w, r)
			return
		}

		if !a.legacyEnabled() {
			next.ServeHTTP(w, withAuthContext(r, authContext{trustForwardedHeaders: a.trustForwardedHeaders}))
			return
		}
		expires, ok := a.validLegacySession(r)
		if ok {
			next.ServeHTTP(w, withAuthContext(r, authContext{enabled: true, expires: &expires, trustForwardedHeaders: a.trustForwardedHeaders}))
			return
		}
		a.unauthorized(w, r)
	})
}

func (a *authenticator) protectAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.jwtEnabled() || !a.jwt.AdminEnabled {
			http.NotFound(w, r)
			return
		}
		claims, ok := a.validJWTSession(r, "", true)
		if !ok {
			a.unauthorized(w, r)
			return
		}
		next.ServeHTTP(w, withAuthContext(r, authContext{enabled: true, expires: claimExpiry(claims), admin: true, trustForwardedHeaders: a.trustForwardedHeaders}))
	})
}

func withAuthContext(r *http.Request, value authContext) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authContextKey{}, value))
}

func authFromRequest(r *http.Request) authContext {
	value, _ := r.Context().Value(authContextKey{}).(authContext)
	return value
}

func (a *authenticator) unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if isAPIPath(r.URL.Path) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	location := "/login"
	if a.legacyEnabled() && !a.jwtEnabled() {
		location += "?next=" + url.QueryEscape(safeNext(originalURIFromRequest(r)))
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func isAPIPath(path string) bool {
	return path == "/events" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/admin/api/") || path == "/admin/events"
}

func (a *authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	next := safeNext(r.URL.Query().Get("next"))
	switch r.Method {
	case http.MethodGet:
		a.renderLogin(w, next, false)
	case http.MethodPost:
		if !sameOrigin(r, a.trustForwardedHeaders) {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxLoginBodyBytes)
		if err := r.ParseForm(); err != nil {
			a.renderLogin(w, next, true)
			return
		}
		next = safeNext(r.Form.Get("next"))
		key := strings.TrimSpace(r.Form.Get("key"))
		if a.jwtEnabled() {
			claims, err := a.verifyJWT(key)
			if err != nil {
				a.renderLogin(w, "/", true)
				return
			}
			a.setJWTCookie(w, r, key, claims)
			target := "/admin/"
			if claims.Namespace != "" {
				target = "/namespace/" + url.PathEscape(claims.Namespace) + "/"
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		if !a.legacyEnabled() || !a.matchesLegacy(key) {
			a.renderLogin(w, next, true)
			return
		}
		a.setLegacySession(w, r)
		http.Redirect(w, r, next, http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *authenticator) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r, a.trustForwardedHeaders) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	if !discardLimitedBody(w, r, maxWriteBodyBytes) {
		return
	}
	if a.jwtEnabled() {
		namespace := namespaceFromRequest(r)
		name, path := jwtCookie(namespace)
		if strings.HasPrefix(originalPathFromRequest(r), "/admin/") || namespace == "" {
			name, path = adminAuthCookieName, "/"
		}
		deleteCookie(w, r, name, path, a.trustForwardedHeaders)
	} else {
		deleteCookie(w, r, legacyAuthCookieName, "/", a.trustForwardedHeaders)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func discardLimitedBody(w http.ResponseWriter, r *http.Request, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return false
	}
	return true
}

func (a *authenticator) verifyJWT(token string) (authjwt.Claims, error) {
	return authjwt.Verify(token, authjwt.VerifyOptions{
		Secret: a.jwt.SecretBytes, Issuer: a.jwt.Issuer, Audience: a.jwt.Audience,
		AllowPermanent: a.jwt.AllowPermanentTokens, Now: a.now(),
	})
}

func (a *authenticator) validJWTSession(r *http.Request, namespace string, adminOnly bool) (authjwt.Claims, bool) {
	if cookie, err := r.Cookie(adminAuthCookieName); err == nil {
		if claims, err := a.verifyJWT(cookie.Value); err == nil && claims.Namespace == "" {
			return claims, true
		}
	}
	if adminOnly || namespace == "" {
		return authjwt.Claims{}, false
	}
	name, _ := jwtCookie(namespace)
	cookie, err := r.Cookie(name)
	if err != nil {
		return authjwt.Claims{}, false
	}
	claims, err := a.verifyJWT(cookie.Value)
	return claims, err == nil && claims.Namespace == namespace
}

func (a *authenticator) setJWTCookie(w http.ResponseWriter, r *http.Request, token string, claims authjwt.Claims) {
	name, path := jwtCookie(claims.Namespace)
	cookie := &http.Cookie{
		Name: name, Value: token, Path: path, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: secureCookie(r, a.trustForwardedHeaders),
	}
	if claims.ExpiresAt != nil {
		cookie.Expires = time.Unix(*claims.ExpiresAt, 0)
		remaining := cookie.Expires.Sub(a.now())
		if remaining > 0 {
			cookie.MaxAge = int(remaining.Seconds())
		}
	}
	http.SetCookie(w, cookie)
}

func jwtCookie(namespace string) (name, path string) {
	if namespace == "" {
		return adminAuthCookieName, "/"
	}
	sum := sha256.Sum256([]byte(namespace))
	return "http_relay_web_session_" + hex.EncodeToString(sum[:16]), "/namespace/" + url.PathEscape(namespace) + "/"
}

func deleteCookie(w http.ResponseWriter, r *http.Request, name, path string, trustForwarded bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: secureCookie(r, trustForwarded),
	})
}

func claimExpiry(claims authjwt.Claims) *time.Time {
	if claims.ExpiresAt == nil {
		return nil
	}
	v := time.Unix(*claims.ExpiresAt, 0)
	return &v
}

func (a *authenticator) matchesLegacy(got string) bool {
	wantSum := sha256.Sum256(a.legacyKey)
	gotSum := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(wantSum[:], gotSum[:]) == 1
}

func (a *authenticator) setLegacySession(w http.ResponseWriter, r *http.Request) {
	expires := a.now().Add(a.ttl)
	value := a.legacySessionValue(expires.Unix())
	http.SetCookie(w, &http.Cookie{Name: legacyAuthCookieName, Value: value, Path: "/", Expires: expires, MaxAge: int(a.ttl.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r, a.trustForwardedHeaders)})
}

func (a *authenticator) validLegacySession(r *http.Request) (time.Time, bool) {
	cookie, err := r.Cookie(legacyAuthCookieName)
	if err != nil {
		return time.Time{}, false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return time.Time{}, false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || !a.now().Before(time.Unix(expires, 0)) {
		return time.Time{}, false
	}
	want := a.legacySessionValue(expires)
	return time.Unix(expires, 0), subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(want)) == 1
}

func (a *authenticator) legacySessionValue(expires int64) string {
	prefix := strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, a.sessionKey)
	_, _ = mac.Write([]byte(prefix))
	return prefix + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func sameOrigin(r *http.Request, trustForwarded bool) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	// Sandboxed documents have an opaque origin and browsers serialize it as
	// "null" even when the navigation or fetch targets the same origin. Fetch
	// Metadata request headers cannot be set by page JavaScript, so accept this
	// case only when the browser explicitly classifies it as same-origin.
	if origin == "null" {
		return strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if trustForwarded {
		if forwarded := firstForwarded(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
			scheme = forwarded
		}
		if forwarded := firstForwarded(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
			host = forwarded
		}
	}
	return strings.EqualFold(u.Scheme, scheme) && strings.EqualFold(u.Host, host)
}

func secureCookie(r *http.Request, trustForwarded bool) bool {
	return r.TLS != nil || (trustForwarded && strings.EqualFold(firstForwarded(r.Header.Get("X-Forwarded-Proto")), "https"))
}

func firstForwarded(value string) string {
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

func safeNext(raw string) string {
	if raw == "" {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") {
		return "/"
	}
	return raw
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func (a *authenticator) renderLogin(w http.ResponseWriter, next string, failed bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	errorText := ""
	if failed {
		errorText = `<p class="error">密钥无效，请重试。</p>`
	}
	label := "访问密钥"
	if a.jwtEnabled() {
		label = "JWT 访问密钥"
		next = "/"
	}
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>http-relay 登录</title><style>body{font-family:system-ui,sans-serif;background:#111827;color:#e5e7eb;display:grid;place-items:center;min-height:100vh;margin:0}.box{width:min(440px,calc(100%% - 32px));padding:28px;border:1px solid #374151;border-radius:10px;background:#1f2937}label,textarea,button{display:block;width:100%%;box-sizing:border-box}textarea{min-height:120px;margin:8px 0 16px;padding:10px;border-radius:6px;border:1px solid #4b5563;background:#111827;color:inherit;resize:vertical}button{padding:10px;border:0;border-radius:6px;background:#38bdf8;color:#082f49;font-weight:700}.error{color:#fca5a5}</style></head><body><form class="box" method="post" action="/login"><h1>http-relay</h1><p>请输入%s。</p>%s<input type="hidden" name="next" value="%s"><label for="key">密钥</label><textarea id="key" name="key" required autofocus autocomplete="off"></textarea><button type="submit">登录</button></form></body></html>`, label, errorText, htmlEscape(next))
}
