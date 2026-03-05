package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"http-relay/internal/relay"
)

func main() {
	wire := flag.Bool("w", false, "dump inbound request headers and body")
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

	handler := relay.NewHandler(client, logger, *wire, wireScope)

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Printf("http-relay starting on %s proxy_mode=%s wire_dump=%t wire_scope=%s", addr, proxySummary, *wire, wireScope)
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
