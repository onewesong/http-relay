APP := http-relay
CMD := ./cmd/http-relay
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
VERSION ?= dev

.PHONY: help fmt vet test build run clean docker-build

help:
	@echo "Available targets:"
	@echo "  make fmt           Format Go files"
	@echo "  make vet           Run go vet"
	@echo "  make test          Run tests"
	@echo "  make build         Build $(APP) into $(BIN)"
	@echo "  make run           Run $(APP) locally"
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

run:
	go run $(CMD)

clean:
	rm -rf $(BIN_DIR)

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(APP):$(VERSION) .
