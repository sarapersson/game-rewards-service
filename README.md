# game-rewards-service

A small Go backend service for an online gaming reward-claim platform.

The codebase is developed in small, reviewable slices with a focus on correctness, operability, and keeping the design easy to reason about.

## Status

Current implementation: HTTP API scaffold, PostgreSQL persistence, reward claim creation, CI, and baseline security checks.

Implemented so far:

* HTTP server using the Go standard library
* `/livez` and `/readyz`
* `POST /v1/reward-claims`
* PostgreSQL-backed reward claim creation
* PostgreSQL-backed readiness check
* PostgreSQL 18.4 local development with Docker Compose
* SQL migrations
* Core schema for reward claims, idempotency keys, and outbox events
* Schema-level constraints for duplicate reward prevention and critical invariants
* Structured logging with `log/slog`
* Request ID middleware
* Panic recovery
* Basic secure headers
* Environment-based configuration
* Graceful shutdown
* Unit tests
* PostgreSQL integration tests
* Makefile
* Dockerfile
* GitHub Actions CI
* Baseline CI checks for formatting, module tidiness, vet, tests, race tests, migrations, integration tests, and Docker builds
* Separate security workflow for CodeQL code scanning and Go vulnerability checks
* GitHub Actions concurrency cancellation for superseded workflow runs
* Dependabot baseline for Go modules, GitHub Actions, and Docker

Planned work:

* Deterministic idempotency replay with `Idempotency-Key`
* Transactional idempotency state for reward claim creation
* Transactional outbox writes
* Async worker
* `/metrics`
* Container scanning and SBOM generation

## Requirements

* Go 1.26.4
* Docker, for local PostgreSQL and building the local container image
* Make, for the documented local development commands

## Run locally

Start PostgreSQL and apply migrations:

```bash
make db-up
make migrate-up
```

Run the API:

```bash
make run
```

The API fails fast on startup if PostgreSQL cannot be reached. After startup, `/readyz` reports dependency readiness and returns `503` if PostgreSQL becomes unavailable.

Health checks:

```bash
curl -i http://localhost:8080/livez
curl -i http://localhost:8080/readyz
```

When PostgreSQL is reachable, `/readyz` includes the dependency check:

```json
{
  "status": "ready",
  "checks": {
    "postgres": "ok"
  }
}
```

Create a reward claim:

```bash
curl -i -X POST http://localhost:8080/v1/reward-claims \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: claim-player-123-winter-2026-daily-login' \
  -d '{"player_id":"player-123","campaign_id":"winter-2026","reward_id":"daily-login"}'
```

## Database

Start local PostgreSQL:

```bash
make db-up
```

Stop local PostgreSQL:

```bash
make db-down
```

View PostgreSQL logs:

```bash
make db-logs
```

Apply migrations:

```bash
make migrate-up
```

Show migration version:

```bash
make migrate-status
```

Verify migrations can apply, roll back, and re-apply:

```bash
make db-check
```

`db-check` applies pending migrations, rolls back the latest migration, and applies migrations again against the configured database. It is intended for clean local and CI databases. If you have created manual local reward claim data, clear it or reset the local database before running `db-check`. Do not run it against shared, staging, or production-like databases.

The local Docker Compose setup uses PostgreSQL 18.4 and development-only credentials. Do not reuse the local credentials outside local development.

## Run tests

Run fast local checks:

```bash
make check
```

Run the full local Go check set:

```bash
make ci
```

Verify database migrations locally:

```bash
make db-check
```

Run PostgreSQL integration tests:

```bash
make test-integration
```

Integration tests require PostgreSQL to be running and migrations to be applied. Locally, run `make db-up` and `make migrate-up` first.

Run individual checks:

```bash
make mod-tidy-check
make fmt-check
make vet
make test
make test-race
make test-integration
make vuln
```

## CI

GitHub Actions runs baseline checks on pull requests to `main`, pushes to `main`, and manual workflow dispatches.

The CI workflow uses least-privilege read-only repository permissions. It starts a PostgreSQL 18.4 service, verifies database migrations, runs Go checks including race tests and PostgreSQL integration tests, and builds the local Docker image:

* `make check`
* `make test-race`
* `make db-check`
* `make test-integration`
* `make docker-build`

Security checks run in a separate workflow:

* CodeQL code scanning
* `make vuln`

Both workflows cancel superseded runs for the same branch so pull requests show the latest result without waiting for older runs to finish.

The repository should be configured so changes to `main` go through pull requests with required passing CI checks.

## Build

Build the API binary:

```bash
make build
```

## Docker

Build the local Docker image:

```bash
make docker-build
```

Run the image with a reachable PostgreSQL database:

```bash
docker run --rm -p 8080:8080 \
  -e DATABASE_URL='postgres://game_rewards:game_rewards_dev_password@host.docker.internal:5432/game_rewards?sslmode=disable' \
  game-rewards-service:local
```

The image uses versioned base images, avoids `latest` tags, and runs the API as a non-root user.

When running the Docker image directly, provide a `DATABASE_URL` that the container can reach. The API pings PostgreSQL during startup and exits if the dependency is unavailable.

`host.docker.internal` works out of the box on Docker Desktop. On Linux, use a reachable host address or Docker network configuration for PostgreSQL.

## Configuration

The service is configured with environment variables.

For local development, `.env.example` documents the default environment variables used by the service. The application reads environment variables from the process; it does not load `.env` files automatically.

| Variable                   | Default                                                                                         |
| -------------------------- | ----------------------------------------------------------------------------------------------- |
| `APP_ENV`                  | `local`                                                                                         |
| `SERVICE_NAME`             | `game-rewards-service`                                                                          |
| `HTTP_ADDR`                | `:8080`                                                                                         |
| `HTTP_READ_TIMEOUT`        | `5s`                                                                                            |
| `HTTP_READ_HEADER_TIMEOUT` | `2s`                                                                                            |
| `HTTP_WRITE_TIMEOUT`       | `10s`                                                                                           |
| `HTTP_IDLE_TIMEOUT`        | `60s`                                                                                           |
| `DATABASE_URL`             | `postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable` |
| `DB_PING_TIMEOUT`          | `2s`                                                                                            |
| `DB_QUERY_TIMEOUT`         | `2s`                                                                                            |
| `SHUTDOWN_TIMEOUT`         | `10s`                                                                                           |
| `LOG_LEVEL`                | `info`                                                                                          |

Example:

```bash
LOG_LEVEL=debug HTTP_ADDR=:9090 make run
```

## API

### `GET /livez`

Returns process liveness.

Status: `200 OK`

```json
{
  "status": "ok"
}
```

### `GET /readyz`

Returns readiness.

Status: `200 OK` when PostgreSQL is reachable.

```json
{
  "status": "ready",
  "checks": {
    "postgres": "ok"
  }
}
```

If PostgreSQL is unavailable, the endpoint returns `503 Service Unavailable` and reports the dependency as `"error"` without exposing raw database errors.

Example unavailable response:

```json
{
  "status": "not_ready",
  "checks": {
    "postgres": "error"
  }
}
```

### `POST /v1/reward-claims`

Creates a reward claim for a player in a campaign.

This endpoint requires an `Idempotency-Key` header. In the current slice, the header is required and validated, but completed response replay and request hash mismatch handling are planned for the next idempotency slice.

Request:

```bash
curl -i -X POST http://localhost:8080/v1/reward-claims \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: claim-player-123-winter-2026-daily-login' \
  -d '{"player_id":"player-123","campaign_id":"winter-2026","reward_id":"daily-login"}'
```

Request body:

```json
{
  "player_id": "player-123",
  "campaign_id": "winter-2026",
  "reward_id": "daily-login"
}
```

Successful response:

Status: `201 Created`

```json
{
  "claim_id": "8f2e7b38-7e2d-4d4c-91b5-1c4d5f5c7c0a",
  "player_id": "player-123",
  "campaign_id": "winter-2026",
  "reward_id": "daily-login",
  "status": "claimed",
  "claimed_at": "2026-07-07T12:34:56Z"
}
```

Duplicate reward claims for the same `player_id`, `campaign_id`, and `reward_id` return `409 Conflict`:

```json
{
  "error": {
    "code": "reward_already_claimed",
    "message": "Reward has already been claimed"
  }
}
```

Common responses:

| Scenario                             | Status                       |
| ------------------------------------ | ---------------------------- |
| Claim created                        | `201 Created`                |
| Invalid JSON or validation error     | `400 Bad Request`            |
| Missing or invalid `Idempotency-Key` | `400 Bad Request`            |
| Unsupported content type             | `415 Unsupported Media Type` |
| Request body too large               | `413 Payload Too Large`      |
| Reward already claimed               | `409 Conflict`               |
| Dependency unavailable               | `503 Service Unavailable`    |
| Unexpected internal failure          | `500 Internal Server Error`  |

Current validation rules:

* `Idempotency-Key` is required.
* `Idempotency-Key` must be at most 255 bytes and must not contain control characters.
* `Content-Type` must be `application/json`; parameters such as `charset=utf-8` are accepted.
* Request body size is limited to 64 KiB.
* Unknown JSON fields are rejected.
* Multiple JSON values in one request body are rejected.
* `player_id`, `campaign_id`, and `reward_id` are required.
* `player_id`, `campaign_id`, and `reward_id` must each be at most 128 characters.

### Request IDs

The API returns an `X-Request-ID` response header for request correlation.

Clients may provide `X-Request-ID`. Empty, oversized, or unsafe request IDs are replaced with a generated ID.

### Errors

Unknown routes return `404 Not Found`.

Unsupported methods return `405 Method Not Allowed` with an `Allow` header listing supported methods.

Invalid JSON, invalid requests, unsupported media types, oversized request bodies, already-claimed rewards, unavailable dependencies, and internal failures return the stable JSON error response format.

Internal panics return `500 Internal Server Error`.

Error response format:

```json
{
  "error": {
    "code": "not_found",
    "message": "Not found"
  }
}
```

Current error codes:

* `not_found`
* `method_not_allowed`
* `invalid_json`
* `invalid_request`
* `idempotency_key_required`
* `invalid_idempotency_key`
* `unsupported_media_type`
* `request_body_too_large`
* `reward_already_claimed`
* `service_unavailable`
* `internal_error`

Example unsupported method response:

```bash
curl -i -X POST http://localhost:8080/livez
```

```text
HTTP/1.1 405 Method Not Allowed
Allow: GET
Content-Type: application/json
```

## Project structure

```text
cmd/api
  Application entrypoint.

internal/config
  Loads and validates runtime configuration.

internal/httpapi
  HTTP server, routes, middleware, health checks, reward claim endpoint, and JSON responses.

internal/postgres
  PostgreSQL pool setup and health checks.

internal/rewards
  Reward claim domain logic, service layer, and PostgreSQL-backed persistence.

migrations
  Versioned SQL migrations for the core schema.

compose.yaml
  Local PostgreSQL development environment.
```

## Before opening a pull request

Run the main local checks before opening a PR:

```bash
make db-check
make test-integration
make ci
make docker-build
```

## License

MIT
