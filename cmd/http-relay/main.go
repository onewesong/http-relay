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

	wire := flag.Bool("w", false, "dump inbound request headers and body")
	maskAuth := flag.Bool("mask-auth", false, "mask authentication headers in request dump")
	flag.Usage = func() {
		name := filepath.Base(os.Args[0])
		out := flag.CommandLine.Output()

		fmt.Fprintf(out, "Usage:\n")
		fmt.Fprintf(out, "  %s [flags]\n", name)
		fmt.Fprintf(out, "  %s version\n\n", name)

		fmt.Fprintf(out, "Flags:\n")
		flag.PrintDefaults()

		fmt.Fprintf(out, "\nEnvironment Variables:\n")
		fmt.Fprintf(out, "  HOST                  listen host (default: 127.0.0.1)\n")
		fmt.Fprintf(out, "  PORT                  listen port (default: 8080)\n")
		fmt.Fprintf(out, "  WIRE_SCOPE            dump scope when -w is enabled: req, resp, req,resp (default)\n")
		fmt.Fprintf(out, "  ALL_PROXY             proxy for both HTTP and HTTPS, highest priority\n")
		fmt.Fprintf(out, "  HTTP_PROXY            upstream proxy for HTTP targets\n")
		fmt.Fprintf(out, "  HTTPS_PROXY           upstream proxy for HTTPS targets\n")
		fmt.Fprintf(out, "  NO_PROXY              bypass proxy for matching hosts\n")
	}
	flag.Parse()

	logger := log.Default()

	host := envOrDefault("HOST", "127.0.0.1")
	port := envOrDefault("PORT", "8080")
	addr := host + ":" + port
	wireScope, scopeOK := relay.ParseDumpScope(os.Getenv("WIRE_SCOPE"))
	if !scopeOK {
		logger.Printf("invalid WIRE_SCOPE=%q, fallback to %q", os.Getenv("WIRE_SCOPE"), (relay.DumpScopeReq | relay.DumpScopeResp).String())
	}

	transport, proxySummary, err := relay.NewTransportFromEnv()
	if err != nil {
		logger.Fatalf("failed to build transport: %v", err)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   120 * time.Second,
	}

	handler := relay.NewHandler(client, logger, *wire, wireScope, *maskAuth)

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Printf("http-relay starting on %s proxy_mode=%s wire_dump=%t wire_scope=%s mask_auth=%t", addr, proxySummary, *wire, wireScope, *maskAuth)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server stopped: %v", err)
	}
}

func envOrDefault(key, defaultValue string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return defaultValue
}
