package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	authCookieName    = "http_relay_web_session"
	defaultSessionTTL = 24 * time.Hour
)

// Options configures the optional Web UI authentication.
// An empty AuthKey leaves the UI publicly accessible, preserving the default
// behavior for local use.
type Options struct {
	AuthKey    string
	SessionTTL time.Duration
}

type authenticator struct {
	key        []byte
	sessionKey []byte
	ttl        time.Duration
	now        func() time.Time
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
	return &authenticator{key: []byte(opts.AuthKey), sessionKey: sessionKey, ttl: ttl, now: time.Now}
}

func (a *authenticator) enabled() bool { return len(a.key) != 0 }

func (a *authenticator) protect(next http.Handler) http.Handler {
	if !a.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if a.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/events" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(safeNext(r.URL.RequestURI())), http.StatusSeeOther)
	})
}

func (a *authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.enabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	next := safeNext(r.URL.Query().Get("next"))
	switch r.Method {
	case http.MethodGet:
		if a.validSession(r) {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		a.renderLogin(w, next, false)
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		if err := r.ParseForm(); err != nil {
			a.renderLogin(w, next, true)
			return
		}
		next = safeNext(r.Form.Get("next"))
		if !a.matches(r.Form.Get("key")) {
			a.renderLogin(w, next, true)
			return
		}
		a.setSession(w, r)
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
	http.SetCookie(w, &http.Cookie{Name: authCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r)})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *authenticator) matches(got string) bool {
	wantSum := sha256.Sum256(a.key)
	gotSum := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(wantSum[:], gotSum[:]) == 1
}

func (a *authenticator) setSession(w http.ResponseWriter, r *http.Request) {
	expires := a.now().Add(a.ttl)
	value := a.sessionValue(expires.Unix())
	http.SetCookie(w, &http.Cookie{Name: authCookieName, Value: value, Path: "/", Expires: expires, MaxAge: int(a.ttl.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r)})
}

func (a *authenticator) validSession(r *http.Request) bool {
	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || !a.now().Before(time.Unix(expires, 0)) {
		return false
	}
	want := a.sessionValue(expires)
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(want)) == 1
}

func (a *authenticator) sessionValue(expires int64) string {
	prefix := strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, a.sessionKey)
	_, _ = mac.Write([]byte(prefix))
	return prefix + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func (a *authenticator) renderLogin(w http.ResponseWriter, next string, failed bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	errorText := ""
	if failed {
		errorText = `<p class="error">密钥不正确，请重试。</p>`
	}
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>http-relay 登录</title><style>body{font-family:system-ui,sans-serif;background:#111827;color:#e5e7eb;display:grid;place-items:center;min-height:100vh;margin:0}.box{width:min(360px,calc(100%% - 32px));padding:28px;border:1px solid #374151;border-radius:10px;background:#1f2937}label,input,button{display:block;width:100%%;box-sizing:border-box}input{margin:8px 0 16px;padding:10px;border-radius:6px;border:1px solid #4b5563;background:#111827;color:inherit}button{padding:10px;border:0;border-radius:6px;background:#38bdf8;color:#082f49;font-weight:700}.error{color:#fca5a5}</style></head><body><form class="box" method="post" action="/login"><h1>http-relay</h1><p>请输入访问密钥。</p>%s<input type="hidden" name="next" value="%s"><label for="key">密钥</label><input id="key" name="key" type="password" required autofocus autocomplete="current-password"><button type="submit">登录</button></form></body></html>`, errorText, htmlEscape(next))
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;", ">", "&gt;").Replace(s)
}
