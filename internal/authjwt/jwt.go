// Package authjwt signs and verifies the JWT access tokens used by the Web UI.
package authjwt

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onewesong/http-relay/internal/config"
)

const ClockSkew = 30 * time.Second

type header struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

type Claims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Namespace string `json:"namespace"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	ExpiresAt *int64 `json:"exp,omitempty"`
	JWTID     string `json:"jti"`
}

func (c Claims) Permanent() bool { return c.ExpiresAt == nil }

type Options struct {
	Secret         []byte
	Issuer         string
	Audience       string
	Namespace      string
	TTL            time.Duration
	Permanent      bool
	AllowPermanent bool
	Now            time.Time
	Random         io.Reader
}

type VerifyOptions struct {
	Secret         []byte
	Issuer         string
	Audience       string
	AllowPermanent bool
	Now            time.Time
	ClockSkew      time.Duration
}

func Issue(opts Options) (string, Claims, error) {
	if err := validateCommon(opts.Secret, opts.Issuer, opts.Audience, opts.Namespace); err != nil {
		return "", Claims{}, err
	}
	if opts.Permanent && !opts.AllowPermanent {
		return "", Claims{}, errors.New("permanent tokens are disabled")
	}
	if !opts.Permanent && opts.TTL <= 0 {
		return "", Claims{}, errors.New("token TTL must be greater than zero")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	random := opts.Random
	if random == nil {
		random = rand.Reader
	}
	jtiRaw := make([]byte, 16)
	if _, err := io.ReadFull(random, jtiRaw); err != nil {
		return "", Claims{}, fmt.Errorf("generate jti: %w", err)
	}

	claims := Claims{
		Issuer:    opts.Issuer,
		Audience:  opts.Audience,
		Namespace: opts.Namespace,
		IssuedAt:  now.Unix(),
		NotBefore: now.Unix(),
		JWTID:     base64.RawURLEncoding.EncodeToString(jtiRaw),
	}
	if !opts.Permanent {
		expires := now.Add(opts.TTL)
		if expires.Before(now) {
			return "", Claims{}, errors.New("token expiry overflow")
		}
		unix := expires.Unix()
		claims.ExpiresAt = &unix
	}

	headerJSON, _ := json.Marshal(header{Algorithm: "HS256", Type: "JWT"})
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", Claims{}, err
	}
	unsigned := encode(headerJSON) + "." + encode(claimsJSON)
	signature := sign(opts.Secret, unsigned)
	return unsigned + "." + encode(signature), claims, nil
}

func Verify(token string, opts VerifyOptions) (Claims, error) {
	if len(opts.Secret) < 32 || strings.TrimSpace(opts.Issuer) == "" || strings.TrimSpace(opts.Audience) == "" {
		return Claims{}, errors.New("invalid verifier configuration")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Claims{}, errors.New("malformed JWT")
	}
	unsigned := parts[0] + "." + parts[1]
	gotSignature, err := decode(parts[2])
	if err != nil || !hmac.Equal(gotSignature, sign(opts.Secret, unsigned)) {
		return Claims{}, errors.New("invalid JWT signature")
	}

	var h header
	if err := decodeStrict(parts[0], &h); err != nil {
		return Claims{}, fmt.Errorf("invalid JWT header: %w", err)
	}
	if h.Algorithm != "HS256" || h.Type != "JWT" {
		return Claims{}, errors.New("JWT must use alg=HS256 and typ=JWT")
	}
	var claims Claims
	if err := decodeStrict(parts[1], &claims); err != nil {
		return Claims{}, fmt.Errorf("invalid JWT claims: %w", err)
	}
	if claims.Issuer != opts.Issuer || claims.Audience != opts.Audience {
		return Claims{}, errors.New("JWT issuer or audience mismatch")
	}
	if claims.Namespace != "" && !config.ValidNamespace(claims.Namespace) {
		return Claims{}, errors.New("invalid JWT namespace")
	}
	if claims.JWTID == "" || claims.IssuedAt <= 0 || claims.NotBefore <= 0 {
		return Claims{}, errors.New("JWT is missing required claims")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	skew := opts.ClockSkew
	if skew == 0 {
		skew = ClockSkew
	}
	if time.Unix(claims.IssuedAt, 0).After(now.Add(skew)) {
		return Claims{}, errors.New("JWT issued-at is in the future")
	}
	if time.Unix(claims.NotBefore, 0).After(now.Add(skew)) {
		return Claims{}, errors.New("JWT is not active yet")
	}
	if claims.ExpiresAt == nil {
		if !opts.AllowPermanent {
			return Claims{}, errors.New("permanent JWTs are disabled")
		}
		return claims, nil
	}
	if *claims.ExpiresAt <= claims.IssuedAt {
		return Claims{}, errors.New("JWT expiry must be after issued-at")
	}
	if now.After(time.Unix(*claims.ExpiresAt, 0).Add(skew)) {
		return Claims{}, errors.New("JWT has expired")
	}
	return claims, nil
}

func validateCommon(secret []byte, issuer, audience, namespace string) error {
	if len(secret) < 32 {
		return errors.New("JWT secret must contain at least 32 bytes")
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(audience) == "" {
		return errors.New("JWT issuer and audience are required")
	}
	if namespace != "" && !config.ValidNamespace(namespace) {
		return errors.New("invalid namespace")
	}
	return nil
}

func sign(secret []byte, unsigned string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(unsigned))
	return mac.Sum(nil)
}

func encode(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func decode(part string) ([]byte, error) {
	if strings.Contains(part, "=") {
		return nil, errors.New("JWT segments must be unpadded Base64URL")
	}
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil || encode(raw) != part {
		return nil, errors.New("invalid Base64URL")
	}
	return raw, nil
}

func decodeStrict(part string, dst any) error {
	raw, err := decode(part)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}
