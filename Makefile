.PHONY: help fmt fmt-check mod-tidy-check vet test test-race vuln check ci run build clean docker-build

APP_NAME := game-rewards-service
BIN_DIR := bin
API_BIN := $(BIN_DIR)/api
DOCKER_IMAGE := game-rewards-service:local
GOVULNCHECK_VERSION := v1.5.0
GO_FILES := $(shell find . -name '*.go' -not -path './.git/*')

help: ## Show available commands
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

fmt: ## Format Go code
	gofmt -w $(GO_FILES)

fmt-check: ## Check Go formatting
	@test -z "$$(gofmt -l $(GO_FILES))" || \
		(echo "Go files are not gofmt-formatted:"; gofmt -l $(GO_FILES); exit 1)

mod-tidy-check: ## Check go.mod and go.sum are tidy
	go mod tidy -diff

vet: ## Run go vet
	go vet ./...

test: ## Run tests
	go test ./...

test-race: ## Run tests with the race detector
	go test -race ./...

vuln: ## Run govulncheck
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

check: fmt-check mod-tidy-check vet test ## Run fast local checks

ci: fmt-check mod-tidy-check vet test test-race vuln ## Run the full Go CI check set

run: ## Run the API locally
	go run ./cmd/api

build: ## Build the API binary
	mkdir -p $(BIN_DIR)
	go build -trimpath -o $(API_BIN) ./cmd/api

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

docker-build: ## Build the Docker image
	docker build -t $(DOCKER_IMAGE) .
