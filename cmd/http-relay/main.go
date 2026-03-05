package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"http-relay/internal/relay"
)

func main() {
	logger := log.Default()

	host := envOrDefault("HOST", "127.0.0.1")
	port := envOrDefault("PORT", "8080")
	addr := host + ":" + port

	transport, proxySummary, err := relay.NewTransportFromEnv()
	if err != nil {
		logger.Fatalf("failed to build transport: %v", err)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   120 * time.Second,
	}

	handler := relay.NewHandler(client, logger)

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Printf("http-relay starting on %s proxy_mode=%s", addr, proxySummary)
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
