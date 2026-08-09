// Package config loads the shared http-relay TOML configuration.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/net/idna"
)

const (
	EnvConfigPath                      = "HTTP_RELAY_CONFIG"
	EnvJWTSecret                       = "WEB_AUTH_JWT_SECRET"
	EnvMaxTransactionsPerNamespace     = "WEB_MAX_TRANSACTIONS_PER_NAMESPACE"
	DefaultMaxTransactionsPerNamespace = 100
	DefaultRewriteHTTPTimeout          = time.Second
	DefaultRewriteHTTPMaxTimeout       = 3 * time.Second
	DefaultRewriteHTTPMaxBodyBytes     = int64(1 << 20)
	DefaultRewriteHTTPMaxCalls         = 3
	DefaultRewriteMaxSSEEventBytes     = 1 << 20
	DefaultRewriteMaxSSEEvents         = 100000
	MaxRewriteHTTPBodyBytes            = int64(16 << 20)
	MaxRewriteHTTPCalls                = 16
	MaxRewriteSSEEventBytes            = 16 << 20
)

var namespacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

type OptionalDuration struct {
	time.Duration
	Set bool
}

func (d *OptionalDuration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = v
	d.Set = true
	return nil
}

type Config struct {
	Web     WebConfig     `toml:"web"`
	Rewrite RewriteConfig `toml:"rewrite"`
}

type RewriteConfig struct {
	Profiles                map[string]RewriteProfile `toml:"profiles"`
	HTTP                    RewriteHTTPConfig         `toml:"http"`
	MaxSSEEventBytes        int                       `toml:"max_sse_event_bytes"`
	MaxSSEEventsPerResponse int                       `toml:"max_sse_events_per_response"`
}

type RewriteHTTPConfig struct {
	Enabled              bool     `toml:"enabled"`
	AllowedOrigins       []string `toml:"allowed_origins"`
	Timeout              Duration `toml:"timeout"`
	MaxTimeout           Duration `toml:"max_timeout"`
	MaxRequestBodyBytes  int64    `toml:"max_request_body_bytes"`
	MaxResponseBodyBytes int64    `toml:"max_response_body_bytes"`
	MaxCallsPerHook      int      `toml:"max_calls_per_hook"`
	FollowRedirects      bool     `toml:"follow_redirects"`
	AllowPrivateNetworks bool     `toml:"allow_private_networks"`
}

type RewriteProfile struct {
	Script  string           `toml:"script"`
	Timeout OptionalDuration `toml:"timeout"`
	Reload  string           `toml:"reload"`
}

type WebConfig struct {
	MaxTransactionsPerNamespace int        `toml:"max_transactions_per_namespace"`
	Auth                        AuthConfig `toml:"auth"`
}

type AuthConfig struct {
	Mode                  string          `toml:"mode"`
	Secret                string          `toml:"secret"`
	Issuer                string          `toml:"issuer"`
	Audience              string          `toml:"audience"`
	TokenTTL              Duration        `toml:"token_ttl"`
	MaxTokenTTL           Duration        `toml:"max_token_ttl"`
	AllowPermanentTokens  bool            `toml:"allow_permanent_tokens"`
	AdminEnabled          bool            `toml:"admin_enabled"`
	DefaultProtected      bool            `toml:"default_protected"`
	FallbackProtected     bool            `toml:"fallback_protected"`
	TrustForwardedHeaders bool            `toml:"trust_forwarded_headers"`
	Namespaces            map[string]bool `toml:"namespaces"`

	SecretBytes []byte `toml:"-"`
}

func defaults() Config {
	return Config{Rewrite: RewriteConfig{
		Profiles:                make(map[string]RewriteProfile),
		MaxSSEEventBytes:        DefaultRewriteMaxSSEEventBytes,
		MaxSSEEventsPerResponse: DefaultRewriteMaxSSEEvents,
		HTTP: RewriteHTTPConfig{
			Timeout:              Duration{DefaultRewriteHTTPTimeout},
			MaxTimeout:           Duration{DefaultRewriteHTTPMaxTimeout},
			MaxRequestBodyBytes:  DefaultRewriteHTTPMaxBodyBytes,
			MaxResponseBodyBytes: DefaultRewriteHTTPMaxBodyBytes,
			MaxCallsPerHook:      DefaultRewriteHTTPMaxCalls,
		},
	}, Web: WebConfig{MaxTransactionsPerNamespace: DefaultMaxTransactionsPerNamespace, Auth: AuthConfig{
		Issuer:      "http-relay",
		Audience:    "http-relay-web",
		TokenTTL:    Duration{30 * 24 * time.Hour},
		MaxTokenTTL: Duration{90 * 24 * time.Hour},
		Namespaces:  make(map[string]bool),
	}}}
}

// ResolvePath applies the shared CLI-over-environment config path priority.
func ResolvePath(flagValue string) string {
	if path := strings.TrimSpace(flagValue); path != "" {
		return path
	}
	return strings.TrimSpace(os.Getenv(EnvConfigPath))
}

// Load reads path in strict mode. An empty path returns default configuration.
func Load(path string) (Config, []string, error) {
	cfg := defaults()
	path = strings.TrimSpace(path)
	if path == "" {
		if err := applyWebEnvironment(&cfg); err != nil {
			return Config{}, nil, err
		}
		if err := cfg.Validate(os.Getenv(EnvJWTSecret)); err != nil {
			return Config{}, nil, err
		}
		return cfg, nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return Config{}, nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	decoder := toml.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, nil, fmt.Errorf("decode config: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return Config{}, nil, fmt.Errorf("rewind config: %w", err)
	}
	var raw map[string]any
	if err := toml.NewDecoder(f).Decode(&raw); err != nil {
		return Config{}, nil, fmt.Errorf("inspect config: %w", err)
	}
	if auth, ok := nestedTable(raw, "web", "auth"); ok {
		if _, configured := auth["mode"]; configured && strings.TrimSpace(cfg.Web.Auth.Mode) == "" {
			return Config{}, nil, errors.New("web.auth.mode must not be empty when configured")
		}
	}
	resolveRewritePaths(&cfg, filepath.Dir(path))

	if err := applyWebEnvironment(&cfg); err != nil {
		return Config{}, nil, err
	}
	warnings := configWarnings(f, strings.TrimSpace(cfg.Web.Auth.Secret) != "")
	if err := cfg.Validate(os.Getenv(EnvJWTSecret)); err != nil {
		return Config{}, warnings, err
	}
	return cfg, warnings, nil
}

func resolveRewritePaths(cfg *Config, configDir string) {
	for name, profile := range cfg.Rewrite.Profiles {
		profile.Script = strings.TrimSpace(profile.Script)
		if profile.Script != "" && !filepath.IsAbs(profile.Script) && !strings.HasPrefix(profile.Script, "builtin:") {
			profile.Script = filepath.Clean(filepath.Join(configDir, profile.Script))
		}
		cfg.Rewrite.Profiles[name] = profile
	}
}

func applyWebEnvironment(cfg *Config) error {
	raw := strings.TrimSpace(os.Getenv(EnvMaxTransactionsPerNamespace))
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fmt.Errorf("%s must be a positive integer", EnvMaxTransactionsPerNamespace)
	}
	cfg.Web.MaxTransactionsPerNamespace = value
	return nil
}

func nestedTable(root map[string]any, names ...string) (map[string]any, bool) {
	current := root
	for _, name := range names {
		next, ok := current[name].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func configWarnings(f *os.File, containsSecret bool) []string {
	if !containsSecret {
		return nil
	}
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return []string{"config contains or may contain secrets but permissions are wider than 0600"}
	}
	return nil
}

func (c *Config) Validate(envSecret string) error {
	if c.Web.MaxTransactionsPerNamespace <= 0 {
		return errors.New("web.max_transactions_per_namespace must be greater than zero")
	}
	for name, profile := range c.Rewrite.Profiles {
		if !ValidNamespace(name) {
			return fmt.Errorf("invalid rewrite profile name %q", name)
		}
		if strings.TrimSpace(profile.Script) == "" {
			return fmt.Errorf("rewrite profile %q script is required", name)
		}
		if profile.Timeout.Set && profile.Timeout.Duration <= 0 {
			return fmt.Errorf("rewrite profile %q timeout must be greater than zero", name)
		}
		profile.Reload = strings.ToLower(strings.TrimSpace(profile.Reload))
		switch profile.Reload {
		case "", "watch", "poll", "off":
		default:
			return fmt.Errorf("rewrite profile %q has invalid reload %q", name, profile.Reload)
		}
		c.Rewrite.Profiles[name] = profile
	}
	if err := c.Rewrite.HTTP.validate(); err != nil {
		return fmt.Errorf("rewrite.http: %w", err)
	}
	if c.Rewrite.MaxSSEEventBytes <= 0 || c.Rewrite.MaxSSEEventBytes > MaxRewriteSSEEventBytes {
		return fmt.Errorf("rewrite.max_sse_event_bytes must be between 1 and %d", MaxRewriteSSEEventBytes)
	}
	if c.Rewrite.MaxSSEEventsPerResponse <= 0 {
		return errors.New("rewrite.max_sse_events_per_response must be greater than zero")
	}
	a := &c.Web.Auth
	a.Mode = strings.TrimSpace(strings.ToLower(a.Mode))
	if a.Mode == "" {
		return nil
	}
	if a.Mode != "jwt" {
		return fmt.Errorf("unsupported web.auth.mode %q", a.Mode)
	}
	if strings.TrimSpace(a.Issuer) == "" || strings.TrimSpace(a.Audience) == "" {
		return errors.New("web.auth issuer and audience must not be empty")
	}
	if a.TokenTTL.Duration <= 0 || a.MaxTokenTTL.Duration <= 0 {
		return errors.New("web.auth token_ttl and max_token_ttl must be greater than zero")
	}
	if a.TokenTTL.Duration > a.MaxTokenTTL.Duration {
		return errors.New("web.auth token_ttl must not exceed max_token_ttl")
	}
	for namespace := range a.Namespaces {
		if !ValidNamespace(namespace) {
			return fmt.Errorf("invalid namespace %q in web.auth.namespaces", namespace)
		}
	}

	secret := strings.TrimSpace(envSecret)
	if secret == "" {
		secret = strings.TrimSpace(a.Secret)
	}
	decoded, err := DecodeSecret(secret)
	if err != nil {
		return fmt.Errorf("web.auth secret: %w", err)
	}
	a.Secret = secret
	a.SecretBytes = decoded
	return nil
}

func (c *RewriteHTTPConfig) validate() error {
	if c.Timeout.Duration <= 0 {
		return errors.New("timeout must be greater than zero")
	}
	if c.MaxTimeout.Duration < c.Timeout.Duration {
		return errors.New("max_timeout must be greater than or equal to timeout")
	}
	if c.MaxRequestBodyBytes <= 0 || c.MaxRequestBodyBytes > MaxRewriteHTTPBodyBytes {
		return fmt.Errorf("max_request_body_bytes must be between 1 and %d", MaxRewriteHTTPBodyBytes)
	}
	if c.MaxResponseBodyBytes <= 0 || c.MaxResponseBodyBytes > MaxRewriteHTTPBodyBytes {
		return fmt.Errorf("max_response_body_bytes must be between 1 and %d", MaxRewriteHTTPBodyBytes)
	}
	if c.MaxCallsPerHook <= 0 || c.MaxCallsPerHook > MaxRewriteHTTPCalls {
		return fmt.Errorf("max_calls_per_hook must be between 1 and %d", MaxRewriteHTTPCalls)
	}
	if c.Enabled && len(c.AllowedOrigins) == 0 {
		return errors.New("allowed_origins must not be empty when enabled")
	}
	seen := make(map[string]struct{}, len(c.AllowedOrigins))
	normalized := make([]string, 0, len(c.AllowedOrigins))
	for _, raw := range c.AllowedOrigins {
		origin, err := normalizeHTTPOrigin(raw)
		if err != nil {
			return err
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		normalized = append(normalized, origin)
	}
	c.AllowedOrigins = normalized
	return nil
}

func normalizeHTTPOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid allowed origin %q", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("allowed origin %q must use http or https", raw)
	}
	if u.Opaque != "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("allowed origin %q must not contain userinfo, path, query, or fragment", raw)
	}
	hostname := u.Hostname()
	if hostname == "" || strings.HasSuffix(hostname, ".") || strings.Contains(hostname, "*") {
		return "", fmt.Errorf("allowed origin %q has an invalid hostname", raw)
	}
	if ip := net.ParseIP(hostname); ip == nil {
		hostname, err = idna.Lookup.ToASCII(hostname)
		if err != nil || hostname == "" {
			return "", fmt.Errorf("allowed origin %q has an invalid hostname", raw)
		}
		hostname = strings.ToLower(hostname)
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil || port == "0" {
		return "", fmt.Errorf("allowed origin %q has an invalid port", raw)
	}
	return scheme + "://" + net.JoinHostPort(hostname, port), nil
}

func DecodeSecret(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("is required in JWT mode")
	}
	if strings.Contains(value, "=") {
		return nil, errors.New("must use unpadded Base64URL encoding")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("must be canonical unpadded Base64URL")
	}
	if len(decoded) < 32 {
		return nil, errors.New("must decode to at least 32 bytes")
	}
	return decoded, nil
}

func ValidNamespace(namespace string) bool {
	return namespacePattern.MatchString(namespace)
}

func (a AuthConfig) Protected(namespace string) bool {
	if namespace == "" {
		return a.DefaultProtected
	}
	if protected, ok := a.Namespaces[namespace]; ok {
		return protected
	}
	return a.FallbackProtected
}
