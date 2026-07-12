.PHONY: help fmt fmt-check mod-tidy-check vet test test-race test-integration test-integration-local vuln check ci run run-worker build clean docker-build stack-up stack-down stack-logs db-up db-down db-logs migrate-up migrate-down migrate-status db-check

BIN_DIR := bin
API_BIN := $(BIN_DIR)/api
WORKER_BIN := $(BIN_DIR)/worker
DOCKER_IMAGE := game-rewards-service:local
GOVULNCHECK_VERSION := v1.6.0
GO_FILES := $(shell find . -name '*.go' -not -path './.git/*')
MIGRATE_VERSION := v4.19.1
MIGRATIONS_DIR := migrations
DATABASE_URL ?= postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable

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

test-integration: ## Run PostgreSQL integration tests against a running migrated database
	go test -count=1 -p=1 -tags=integration ./...

test-integration-local: ## Start PostgreSQL, apply migrations, and run integration tests
	$(MAKE) db-up
	$(MAKE) migrate-up
	$(MAKE) test-integration

vuln: ## Run govulncheck
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

check: fmt-check mod-tidy-check vet test ## Run fast local checks

ci: fmt-check mod-tidy-check vet test test-race vuln ## Run the full local check set

run: ## Run the API locally
	go run ./cmd/api

run-worker: ## Run the outbox worker locally
	go run ./cmd/worker

build: ## Build API and worker binaries
	mkdir -p $(BIN_DIR)
	go build -mod=readonly -trimpath -o $(API_BIN) ./cmd/api
	go build -mod=readonly -trimpath -o $(WORKER_BIN) ./cmd/worker

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

docker-build: ## Build the Docker image
	docker build --pull -t $(DOCKER_IMAGE) .

stack-up: ## Build and start PostgreSQL, API, and worker
	$(MAKE) db-up
	$(MAKE) migrate-up
	docker compose up -d --build --wait api worker

stack-down: ## Stop and remove the local Compose stack
	docker compose down --remove-orphans

stack-logs: ## Follow PostgreSQL, API, and worker logs
	docker compose logs -f postgres api worker

db-up: ## Start local PostgreSQL and wait until it is healthy
	docker compose up -d --wait postgres

db-down: stack-down ## Stop and remove the local Compose stack

db-logs: ## Show PostgreSQL logs
	docker compose logs -f postgres

migrate-up: ## Apply database migrations
	@go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION) \
		-path $(MIGRATIONS_DIR) \
		-database "$(DATABASE_URL)" \
		up

migrate-down: ## Roll back one database migration
	@go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION) \
		-path $(MIGRATIONS_DIR) \
		-database "$(DATABASE_URL)" \
		down 1

migrate-status: ## Show database migration version
	@go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION) \
		-path $(MIGRATIONS_DIR) \
		-database "$(DATABASE_URL)" \
		version

db-check: ## Verify the latest migration can apply, roll back, and re-apply
	$(MAKE) migrate-up
	$(MAKE) migrate-down
	$(MAKE) migrate-up
