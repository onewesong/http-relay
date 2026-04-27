package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onewesong/http-relay/internal/relay"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}

	var dump bool
	var addHeaders repeatedStringFlag
	var modifyHeaders repeatedStringFlag

	showVersion := flag.Bool("version", false, "print version and exit")
	modeRaw := flag.String("mode", "regular", "target mode: regular or reverse:<url>")
	listen := flag.String("listen", "", "listen address, overrides --host and --port")
	host := flag.String("host", envOrDefault("HOST", "127.0.0.1"), "listen host")
	port := flag.String("port", envOrDefault("PORT", "8080"), "listen port")
	timeout := flag.Duration("timeout", 120*time.Second, "upstream request timeout")
	dumpScopeRaw := flag.String("dump-scope", os.Getenv("WIRE_SCOPE"), "dump scope when dump is enabled: req, resp, req,resp")
	maskAuth := flag.Bool("mask-auth", false, "mask authentication headers in request dump")

	flag.BoolVar(&dump, "w", false, "dump inbound request headers and body")
	flag.BoolVar(&dump, "dump", false, "dump inbound request/response traffic")
	flag.Var(&addHeaders, "add-header", "add an upstream request header, repeatable: Name: value")
	flag.Var(&modifyHeaders, "modify-header", "set an upstream request header, repeatable: Name: value")

	flag.Usage = func() {
		name := filepath.Base(os.Args[0])
		out := flag.CommandLine.Output()

		fmt.Fprintf(out, "Usage:\n")
		fmt.Fprintf(out, "  %s [flags]\n", name)
		fmt.Fprintf(out, "  %s version\n\n", name)

		fmt.Fprintf(out, "Modes:\n")
		fmt.Fprintf(out, "  regular                       target URL comes from /{absolute-url} (default)\n")
		fmt.Fprintf(out, "  reverse:<url>                 reverse proxy to an upstream URL\n\n")

		fmt.Fprintf(out, "Examples:\n")
		fmt.Fprintf(out, "  %s --mode reverse:https://api.example.com --modify-header 'User-Agent: http-relay'\n", name)
		fmt.Fprintf(out, "  %s --add-header 'X-Debug: 1' -w\n\n", name)

		fmt.Fprintf(out, "Flags:\n")
		flag.PrintDefaults()

		fmt.Fprintf(out, "\nEnvironment Variables:\n")
		fmt.Fprintf(out, "  HOST                  listen host fallback (default: 127.0.0.1)\n")
		fmt.Fprintf(out, "  PORT                  listen port fallback (default: 8080)\n")
		fmt.Fprintf(out, "  WIRE_SCOPE            dump scope fallback when dump is enabled: req, resp, req,resp (default)\n")
		fmt.Fprintf(out, "  ALL_PROXY             proxy for both HTTP and HTTPS, highest priority\n")
		fmt.Fprintf(out, "  HTTP_PROXY            upstream proxy for HTTP targets\n")
		fmt.Fprintf(out, "  HTTPS_PROXY           upstream proxy for HTTPS targets\n")
		fmt.Fprintf(out, "  NO_PROXY              bypass proxy for matching hosts\n")
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	logger := log.Default()

	if *timeout <= 0 {
		logger.Fatalf("timeout must be greater than zero")
	}

	mode, err := relay.ParseMode(*modeRaw)
	if err != nil {
		logger.Fatalf("invalid mode: %v", err)
	}

	headerRules, err := relay.ParseHeaderRules(addHeaders.values, modifyHeaders.values)
	if err != nil {
		logger.Fatalf("invalid header rule: %v", err)
	}

	addr := strings.TrimSpace(*listen)
	if addr == "" {
		addr = strings.TrimSpace(*host) + ":" + strings.TrimSpace(*port)
	}

	wireScope, scopeOK := relay.ParseDumpScope(*dumpScopeRaw)
	if !scopeOK {
		logger.Printf("invalid dump scope=%q, fallback to %q", *dumpScopeRaw, (relay.DumpScopeReq | relay.DumpScopeResp).String())
	}

	transport, proxySummary, err := relay.NewTransportFromEnv()
	if err != nil {
		logger.Fatalf("failed to build transport: %v", err)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   *timeout,
	}

	handler := relay.NewHandlerWithOptions(client, logger, relay.HandlerOptions{
		TargetMode:  mode,
		HeaderRules: headerRules,
		DumpRequest: dump,
		DumpScope:   wireScope,
		MaskAuth:    *maskAuth,
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logStartup(logger, addr, mode, proxySummary, dump, wireScope, *maskAuth, *timeout, headerRules)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server stopped: %v", err)
	}
}

type repeatedStringFlag struct {
	values []string
}

func (f *repeatedStringFlag) Set(value string) error {
	f.values = append(f.values, value)
	return nil
}

func (f *repeatedStringFlag) String() string {
	return strings.Join(f.values, ", ")
}

func logStartup(logger *log.Logger, addr string, mode relay.TargetMode, proxySummary string, dump bool, dumpScope relay.DumpScope, maskAuth bool, timeout time.Duration, headerRules []relay.HeaderRule) {
	logger.Printf("http-relay %s", version)
	logger.Printf("listen: %s", addr)
	logger.Printf("mode: %s", mode.String())
	logger.Printf("upstream proxy: %s", proxySummary)
	logger.Printf("timeout: %s", timeout)
	logger.Printf("dump: enabled=%t scope=%s mask_auth=%t", dump, dumpScope.String(), maskAuth)
	if len(headerRules) == 0 {
		logger.Printf("request header rules: none")
		return
	}

	logger.Printf("request header rules:")
	for _, rule := range headerRules {
		logger.Printf("  %s", rule.Summary())
	}
}

func envOrDefault(key, defaultValue string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return defaultValue
}
