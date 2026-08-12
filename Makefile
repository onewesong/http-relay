APP := http-relay
CMD := ./cmd/http-relay
AUTH_APP := http-relay-auth
AUTH_CMD := ./cmd/http-relay-auth
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
AUTH_BIN := $(BIN_DIR)/$(AUTH_APP)
VERSION ?= dev
EXAMPLE_SCRIPT := examples/relay.example.js

.PHONY: help fmt vet test build run run-example clean docker-build

help:
	@echo "Available targets:"
	@echo "  make fmt           Format Go files"
	@echo "  make vet           Run go vet"
	@echo "  make test          Run tests"
	@echo "  make build         Build $(APP) and $(AUTH_APP) into $(BIN_DIR)"
	@echo "  make run           Run $(APP) locally"
	@echo "  make run-example   Run $(APP) with the example rewrite script (hot-reload)"
	@echo "  make clean         Remove build output"
	@echo "  make docker-build  Build Docker image"

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

build:
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(BIN) $(CMD)
	go build -trimpath -ldflags="-s -w" -o $(AUTH_BIN) $(AUTH_CMD)

run:
	go run $(CMD) --web

clean:
	rm -rf $(BIN_DIR)

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(APP):$(VERSION) .
