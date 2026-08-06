package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onewesong/http-relay/internal/authjwt"
	appconfig "github.com/onewesong/http-relay/internal/config"
)

const (
	exitOK      = 0
	exitUsage   = 2
	exitConfig  = 3
	exitInvalid = 4
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "secret":
		return runSecret(args[1:], stdout, stderr)
	case "issue":
		return runIssue(args[1:], stdout, stderr)
	case "inspect":
		return runInspect(args[1:], stdin, stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return exitUsage
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  http-relay-auth secret")
	fmt.Fprintln(w, "  http-relay-auth issue --config <file> (--namespace <name> | --admin) [--ttl <duration> | --permanent]")
	fmt.Fprintln(w, "  http-relay-auth inspect --config <file> <token|->")
}

func runSecret(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("secret", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		fmt.Fprintf(stderr, "generate secret: %v\n", err)
		return exitInvalid
	}
	fmt.Fprintln(stdout, base64.RawURLEncoding.EncodeToString(raw))
	return exitOK
}

func runIssue(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "TOML configuration path")
	namespace := fs.String("namespace", "", "namespace for a restricted token")
	admin := fs.Bool("admin", false, "issue an empty-namespace management token")
	ttlRaw := fs.String("ttl", "", "override token lifetime")
	permanent := fs.Bool("permanent", false, "issue a token without expiry")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	if (*namespace == "") == !*admin {
		fmt.Fprintln(stderr, "exactly one of --namespace or --admin is required")
		return exitUsage
	}
	if *permanent && *ttlRaw != "" {
		fmt.Fprintln(stderr, "--permanent and --ttl are mutually exclusive")
		return exitUsage
	}

	cfg, code := loadJWTConfig(*configPath, stderr)
	if code != exitOK {
		return code
	}
	a := cfg.Web.Auth
	ttl := a.TokenTTL.Duration
	if *ttlRaw != "" {
		parsed, err := time.ParseDuration(*ttlRaw)
		if err != nil {
			fmt.Fprintf(stderr, "invalid --ttl: %v\n", err)
			return exitUsage
		}
		ttl = parsed
	}
	if !*permanent && (ttl <= 0 || ttl > a.MaxTokenTTL.Duration) {
		fmt.Fprintf(stderr, "TTL must be greater than zero and at most %s\n", a.MaxTokenTTL.Duration)
		return exitUsage
	}
	if *permanent && !a.AllowPermanentTokens {
		fmt.Fprintln(stderr, "permanent tokens are disabled by configuration")
		return exitConfig
	}
	token, _, err := authjwt.Issue(authjwt.Options{
		Secret: a.SecretBytes, Issuer: a.Issuer, Audience: a.Audience,
		Namespace: *namespace, TTL: ttl, Permanent: *permanent, AllowPermanent: a.AllowPermanentTokens,
	})
	if err != nil {
		fmt.Fprintf(stderr, "issue token: %v\n", err)
		return exitInvalid
	}
	fmt.Fprintln(stdout, token)
	return exitOK
}

func runInspect(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "TOML configuration path")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(stderr, "inspect requires exactly one token or - for stdin")
		return exitUsage
	}
	token := fs.Arg(0)
	if token == "-" {
		raw, err := io.ReadAll(io.LimitReader(stdin, 64*1024))
		if err != nil {
			fmt.Fprintf(stderr, "read token: %v\n", err)
			return exitInvalid
		}
		token = strings.TrimSpace(string(raw))
	}
	cfg, code := loadJWTConfig(*configPath, stderr)
	if code != exitOK {
		return code
	}
	a := cfg.Web.Auth
	claims, err := authjwt.Verify(strings.TrimSpace(token), authjwt.VerifyOptions{
		Secret: a.SecretBytes, Issuer: a.Issuer, Audience: a.Audience, AllowPermanent: a.AllowPermanentTokens,
	})
	if err != nil {
		fmt.Fprintf(stderr, "invalid token: %v\n", err)
		return exitInvalid
	}
	fmt.Fprintln(stdout, "valid: true")
	if claims.Namespace == "" {
		fmt.Fprintln(stdout, "namespace: (admin)")
	} else {
		fmt.Fprintf(stdout, "namespace: %s\n", claims.Namespace)
	}
	fmt.Fprintf(stdout, "issued-at: %s\n", time.Unix(claims.IssuedAt, 0).Format(time.RFC3339))
	if claims.ExpiresAt == nil {
		fmt.Fprintln(stdout, "expires: never")
	} else {
		fmt.Fprintf(stdout, "expires: %s\n", time.Unix(*claims.ExpiresAt, 0).Format(time.RFC3339))
	}
	fmt.Fprintf(stdout, "jti: %s\n", claims.JWTID)
	return exitOK
}

func loadJWTConfig(flagPath string, stderr io.Writer) (appconfig.Config, int) {
	path := appconfig.ResolvePath(flagPath)
	if path == "" {
		fmt.Fprintln(stderr, "a config path is required via --config or HTTP_RELAY_CONFIG")
		return appconfig.Config{}, exitConfig
	}
	cfg, warnings, err := appconfig.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return appconfig.Config{}, exitConfig
	}
	for _, warning := range warnings {
		fmt.Fprintf(stderr, "warning: %s\n", warning)
	}
	if cfg.Web.Auth.Mode != "jwt" {
		fmt.Fprintln(stderr, "web.auth.mode must be jwt")
		return appconfig.Config{}, exitConfig
	}
	return cfg, exitOK
}
