# game-rewards-service

A small Go backend service for an online gaming reward-claim platform.

The project is built in slices and is intended to demonstrate production-minded backend engineering with a simple, readable design.

## Status

Slice 1: API scaffold.

Implemented:

* HTTP server using Go standard library
* `/livez` and `/readyz`
* Structured logging with `log/slog`
* Request ID middleware
* Panic recovery
* Basic secure headers
* Environment-based configuration
* Graceful shutdown
* Unit tests
* Makefile
* Dockerfile

Planned later:

* `POST /v1/reward-claims`
* PostgreSQL persistence
* SQL migrations
* Idempotency with `Idempotency-Key`
* Duplicate reward prevention
* Transactional outbox
* Async worker
* `/metrics`
* Docker Compose
* CI and security checks

## Requirements

* Go 1.26.4
* Docker, for building and running the local container image

## Run locally

```bash
make run
```

Health checks:

```bash
curl -i http://localhost:8080/livez
curl -i http://localhost:8080/readyz
```

## Run tests

Run the local pre-commit checks:

```bash
make check
```

Run the race-enabled test suite:

```bash
make test-race
```

## Build

```bash
make build
```

## Docker

Build and run the local Docker image:

```bash
make docker-build
docker run --rm -p 8080:8080 game-rewards-service:local
```

The image uses pinned base images and runs the API as a non-root user.

## Configuration

The service is configured with environment variables.

| Variable                   | Default                |
| -------------------------- | ---------------------- |
| `APP_ENV`                  | `local`                |
| `SERVICE_NAME`             | `game-rewards-service` |
| `HTTP_ADDR`                | `:8080`                |
| `HTTP_READ_TIMEOUT`        | `5s`                   |
| `HTTP_READ_HEADER_TIMEOUT` | `2s`                   |
| `HTTP_WRITE_TIMEOUT`       | `10s`                  |
| `HTTP_IDLE_TIMEOUT`        | `60s`                  |
| `SHUTDOWN_TIMEOUT`         | `10s`                  |
| `LOG_LEVEL`                | `info`                 |

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

Status: `200 OK`

```json
{
  "status": "ready",
  "checks": {}
}
```

In Slice 1, readiness has no external dependency checks yet. PostgreSQL readiness will be added when persistence is introduced.

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
```

## Pre-commit checklist

Before committing a slice, run:

```bash
make check
make test-race
make docker-build
```

## License

MIT
