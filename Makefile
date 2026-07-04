.PHONY: help fmt vet test test-race check run build clean docker-build

APP_NAME := game-rewards-service
BIN_DIR := bin
API_BIN := $(BIN_DIR)/api
DOCKER_IMAGE := game-rewards-service:local

help: ## Show available commands
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

fmt: ## Format Go code
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

vet: ## Run go vet
	go vet ./...

test: ## Run tests
	go test ./...

test-race: ## Run tests with the race detector
	go test -race ./...

check: fmt vet test ## Run local pre-commit checks

run: ## Run the API locally
	go run ./cmd/api

build: ## Build the API binary
	mkdir -p $(BIN_DIR)
	go build -trimpath -o $(API_BIN) ./cmd/api

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

docker-build: ## Build the Docker image
	docker build -t $(DOCKER_IMAGE) .
