// Package config loads the shared http-relay TOML configuration.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	EnvConfigPath = "HTTP_RELAY_CONFIG"
	EnvJWTSecret  = "WEB_AUTH_JWT_SECRET"
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

type Config struct {
	Web WebConfig `toml:"web"`
}

type WebConfig struct {
	Auth AuthConfig `toml:"auth"`
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
	return Config{Web: WebConfig{Auth: AuthConfig{
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

	warnings := configWarnings(f, strings.TrimSpace(cfg.Web.Auth.Secret) != "")
	if err := cfg.Validate(os.Getenv(EnvJWTSecret)); err != nil {
		return Config{}, warnings, err
	}
	return cfg, warnings, nil
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
