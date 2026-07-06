# game-rewards-service

A small Go backend service for an online gaming reward-claim platform.

The project is built in slices and is intended to demonstrate production-minded backend engineering with a simple, readable design.

## Status

Slice 3.5: CodeQL + CI split.

Implemented:

* HTTP server using Go standard library
* `/livez` and `/readyz`
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
* Makefile
* Dockerfile
* GitHub Actions CI
* Fast CI checks for formatting, module tidiness, vet, tests, migrations, and Docker builds
* Separate security workflow for CodeQL code scanning and Go vulnerability checks
* GitHub Actions concurrency cancellation for superseded workflow runs
* Dependabot baseline for Go modules, GitHub Actions, and Docker

Planned later:

* `POST /v1/reward-claims`
* Idempotency behavior with `Idempotency-Key`
* Transactional reward claim creation
* Transactional outbox writes
* Async worker
* `/metrics`
* Additional supply-chain hardening

## Requirements

* Go 1.26.4
* Docker, for local PostgreSQL and building the local container image

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

`db-check` applies migrations, rolls them back, and applies them again against the configured database. It is intended for local and CI databases only because it runs a down migration and drops the Slice 3 tables. Do not run it against shared, staging, or production-like databases.

The local Docker Compose setup uses PostgreSQL 18.4 and development-only credentials. Do not reuse the local credentials outside local development.

## Run tests

Run fast local checks:

```bash
make check
```

Run the full local check set:

```bash
make ci
```

Verify database migrations locally:

```bash
make db-check
```

Run individual checks:

```bash
make mod-tidy-check
make fmt-check
make vet
make test
make test-race
make vuln
```

## CI

GitHub Actions runs fast baseline checks on pull requests to `main`, pushes to `main`, and manual workflow dispatches.

The CI workflow uses least-privilege read-only repository permissions. It starts a PostgreSQL 18.4 service, verifies database migrations, runs the fast Go checks, and builds the local Docker image:

* `make check`
* `make db-check`
* `make docker-build`

Security checks run in a separate workflow:

* CodeQL code scanning
* `make vuln`

Both workflows cancel superseded runs for the same branch so pull requests show the latest result without waiting for older runs to finish.

The repository should be configured so changes to `main` go through pull requests with required passing CI checks.

## Build

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

When running the Docker image directly, provide a reachable `DATABASE_URL` if the API should pass readiness checks.

## Configuration

The service is configured with environment variables.

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

### Request IDs

The API returns an `X-Request-ID` response header for request correlation.

Clients may provide `X-Request-ID`. Empty, oversized, or unsafe request IDs are replaced with a generated ID.

### Errors

Unknown routes return `404 Not Found`.

Unsupported methods return `405 Method Not Allowed` with an `Allow` header listing supported methods.

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
  HTTP server, routes, middleware, health checks, and JSON responses.

internal/postgres
  PostgreSQL pool setup and health checks.

migrations
  Versioned SQL migrations for the core schema.

compose.yaml
  Local PostgreSQL development environment.
```

## Pre-commit checklist

Before committing a slice, run:

```bash
git diff --check
make db-check
make ci
make docker-build
```

## License

MIT
