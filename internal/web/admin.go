package web

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/onewesong/http-relay/internal/authjwt"
	appconfig "github.com/onewesong/http-relay/internal/config"
)

const (
	maxAdminBodyBytes = 16 << 10
	issueLimit        = 10
	issueWindow       = time.Minute
)

type adminServer struct {
	store   *store
	auth    *authenticator
	static  fs.FS
	logger  *log.Logger
	limiter issueLimiter
}

type issueLimiter struct {
	mu      sync.Mutex
	clients map[string]issueBucket
	now     func() time.Time
}

type issueBucket struct {
	start time.Time
	count int
}

type namespaceStatus struct {
	Namespace   string     `json:"namespace"`
	Protected   bool       `json:"protected"`
	Policy      string     `json:"policy"`
	Records     int        `json:"records"`
	LastAt      *time.Time `json:"lastAt,omitempty"`
	Subscribers int        `json:"subscribers"`
}

type adminState struct {
	Type                 string            `json:"type,omitempty"`
	Namespaces           []namespaceStatus `json:"namespaces"`
	AllowPermanentTokens bool              `json:"allowPermanentTokens"`
	DefaultTokenTTL      string            `json:"defaultTokenTTL"`
	MaxTokenTTL          string            `json:"maxTokenTTL"`
}

func newAdminServer(store *store, auth *authenticator, static fs.FS, logger *log.Logger) *adminServer {
	if logger == nil {
		logger = log.Default()
	}
	return &adminServer{
		store: store, auth: auth, static: static, logger: logger,
		limiter: issueLimiter{clients: make(map[string]issueBucket), now: time.Now},
	}
}

func (a *adminServer) routes(mux *http.ServeMux) {
	mux.Handle("GET /admin/", adminHeaders(a.auth.protectAdmin(http.HandlerFunc(a.handlePage))))
	mux.Handle("GET /admin/app.mjs", adminHeaders(a.auth.protectAdmin(http.HandlerFunc(a.handleScript))))
	mux.Handle("GET /admin/admin.css", adminHeaders(a.auth.protectAdmin(http.HandlerFunc(a.handleStyle))))
	mux.Handle("GET /admin/events", adminHeaders(a.auth.protectAdmin(http.HandlerFunc(a.handleEvents))))
	mux.Handle("GET /admin/api/namespaces", adminHeaders(a.auth.protectAdmin(http.HandlerFunc(a.handleNamespaces))))
	mux.Handle("POST /admin/api/tokens", adminHeaders(a.auth.protectAdmin(http.HandlerFunc(a.handleIssue))))
	mux.Handle("POST /admin/logout", adminHeaders(http.HandlerFunc(a.auth.handleLogout)))
}

func adminHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (a *adminServer) serveFile(w http.ResponseWriter, name, contentType string) {
	raw, err := fs.ReadFile(a.static, name)
	if err != nil {
		http.Error(w, "admin asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(raw)
}

func (a *adminServer) handlePage(w http.ResponseWriter, _ *http.Request) {
	a.serveFile(w, "admin.html", "text/html; charset=utf-8")
}

func (a *adminServer) handleScript(w http.ResponseWriter, _ *http.Request) {
	a.serveFile(w, "admin.mjs", "text/javascript; charset=utf-8")
}

func (a *adminServer) handleStyle(w http.ResponseWriter, _ *http.Request) {
	a.serveFile(w, "admin.css", "text/css; charset=utf-8")
}

func (a *adminServer) configuredNamespaces() []string {
	out := make([]string, 0, len(a.auth.jwt.Namespaces))
	for namespace := range a.auth.jwt.Namespaces {
		out = append(out, namespace)
	}
	return out
}

func (a *adminServer) snapshot() []namespaceStatus {
	metrics := a.store.namespaceMetrics(a.configuredNamespaces())
	out := make([]namespaceStatus, 0, len(metrics))
	for _, metric := range metrics {
		policy := "fallback"
		protected := a.auth.jwt.FallbackProtected
		if metric.Namespace == "" {
			policy = "default"
			protected = a.auth.jwt.DefaultProtected
		} else if value, ok := a.auth.jwt.Namespaces[metric.Namespace]; ok {
			policy = "explicit"
			protected = value
		}
		var lastAt *time.Time
		if !metric.LastAt.IsZero() {
			value := metric.LastAt
			lastAt = &value
		}
		out = append(out, namespaceStatus{
			Namespace: metric.Namespace, Protected: protected, Policy: policy,
			Records: metric.Records, LastAt: lastAt, Subscribers: metric.Subscribers,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace == "" {
			return true
		}
		if out[j].Namespace == "" {
			return false
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out
}

func (a *adminServer) handleNamespaces(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.state(""))
}

func (a *adminServer) state(eventType string) adminState {
	return adminState{
		Type: eventType, Namespaces: a.snapshot(),
		AllowPermanentTokens: a.auth.jwt.AllowPermanentTokens,
		DefaultTokenTTL:      a.auth.jwt.TokenTTL.Duration.String(),
		MaxTokenTTL:          a.auth.jwt.MaxTokenTTL.Duration.String(),
	}
}

func (a *adminServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	updates, cancel := a.store.subscribeAdmin()
	defer cancel()

	keepalive := time.NewTicker(keepAlive)
	defer keepalive.Stop()
	var expiry <-chan time.Time
	var expiryTimer *time.Timer
	if expires := authFromRequest(r).expires; expires != nil {
		delay := time.Until(expires.Add(authjwt.ClockSkew))
		if delay <= 0 {
			return
		}
		expiryTimer = time.NewTimer(delay)
		expiry = expiryTimer.C
		defer expiryTimer.Stop()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-expiry:
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-updates:
			data, _ := json.Marshal(a.state("namespaces"))
			writeSSE(w, data)
			flusher.Flush()
		}
	}
}

type issueRequest struct {
	Namespace string `json:"namespace"`
	TTL       string `json:"ttl"`
	Permanent bool   `json:"permanent"`
}

type issueResponse struct {
	Token     string     `json:"token"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Permanent bool       `json:"permanent"`
	Protected bool       `json:"protected"`
}

func (a *adminServer) handleIssue(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r, a.auth.trustForwardedHeaders) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	if !a.limiter.allow(clientIP(r.RemoteAddr)) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request issueRequest
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "invalid trailing request data", http.StatusBadRequest)
		return
	}
	if !appconfig.ValidNamespace(request.Namespace) {
		http.Error(w, "namespace is required and must be valid", http.StatusBadRequest)
		return
	}
	if request.Permanent && request.TTL != "" {
		http.Error(w, "permanent and ttl are mutually exclusive", http.StatusBadRequest)
		return
	}
	if request.Permanent && !a.auth.jwt.AllowPermanentTokens {
		http.Error(w, "permanent tokens are disabled", http.StatusBadRequest)
		return
	}
	ttl := a.auth.jwt.TokenTTL.Duration
	if request.TTL != "" {
		parsed, err := time.ParseDuration(request.TTL)
		if err != nil {
			http.Error(w, "invalid ttl", http.StatusBadRequest)
			return
		}
		ttl = parsed
	}
	if !request.Permanent && (ttl <= 0 || ttl > a.auth.jwt.MaxTokenTTL.Duration) {
		http.Error(w, "ttl is outside the allowed range", http.StatusBadRequest)
		return
	}
	token, claims, err := authjwt.Issue(authjwt.Options{
		Secret: a.auth.jwt.SecretBytes, Issuer: a.auth.jwt.Issuer, Audience: a.auth.jwt.Audience,
		Namespace: request.Namespace, TTL: ttl, Permanent: request.Permanent,
		AllowPermanent: a.auth.jwt.AllowPermanentTokens, Now: a.auth.now(),
	})
	if err != nil {
		http.Error(w, "failed to issue token", http.StatusInternalServerError)
		return
	}
	response := issueResponse{Token: token, Permanent: claims.Permanent(), Protected: a.auth.jwt.Protected(request.Namespace)}
	if claims.ExpiresAt != nil {
		expires := time.Unix(*claims.ExpiresAt, 0)
		response.ExpiresAt = &expires
	}
	adminClaims, _ := a.auth.validJWTSession(r, "", true)
	a.logger.Printf("web auth token issued admin_jti=%q namespace=%q token_jti=%q expires=%q", adminClaims.JWTID, request.Namespace, claims.JWTID, expiryLabel(claims))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func expiryLabel(claims authjwt.Claims) string {
	if claims.ExpiresAt == nil {
		return "never"
	}
	return time.Unix(*claims.ExpiresAt, 0).Format(time.RFC3339)
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

func (l *issueLimiter) allow(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.clients) >= 1024 {
		for key, existing := range l.clients {
			if now.Sub(existing.start) >= issueWindow {
				delete(l.clients, key)
			}
		}
	}
	bucket := l.clients[client]
	if bucket.start.IsZero() || now.Sub(bucket.start) >= issueWindow {
		if bucket.start.IsZero() && len(l.clients) >= 4096 {
			return false
		}
		l.clients[client] = issueBucket{start: now, count: 1}
		return true
	}
	if bucket.count >= issueLimit {
		return false
	}
	bucket.count++
	l.clients[client] = bucket
	return true
}
