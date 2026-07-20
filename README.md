# game-rewards-service

A small Go backend service for an online gaming reward-claim platform.

The codebase is developed through small, reviewable changes with a focus on correctness, operability, and keeping the design easy to reason about.

## Status

Current implementation: Go HTTP API, PostgreSQL persistence, reward claim creation, deterministic idempotency replay, transactional outbox writes, async outbox worker, process-local Prometheus metrics, concurrency and failure-mode hardening, CI, and baseline security checks.

Implemented so far:

* HTTP server using the Go standard library
* API `/livez`, `/readyz`, and `/metrics`
* Worker admin `/livez`, `/readyz`, and `/metrics`
* `POST /v1/reward-claims`
* Required single non-empty `Idempotency-Key` support for `POST /v1/reward-claims`
* Rejection of ambiguous requests with multiple `Idempotency-Key` header values
* Deterministic idempotency replay for completed reward claim requests
* Request hash mismatch handling for reused idempotency keys
* PostgreSQL-backed reward claim creation
* PostgreSQL-backed idempotency state
* PostgreSQL-backed readiness check
* PostgreSQL availability classification for known transient connection, network, timeout, and server-availability failures
* Separation of dependency-unavailable failures from schema, invariant, authentication, and unexpected internal errors
* PostgreSQL 18.4 local development with Docker Compose
* SQL migrations
* Core schema for reward claims, idempotency keys, and outbox events
* Transactional outbox write for `RewardClaimed` events
* Async outbox worker for publishing due outbox events
* PostgreSQL outbox polling with `FOR UPDATE SKIP LOCKED`
* One-event-at-a-time outbox claiming
* Processing leases, retry backoff, and dead-letter handling for outbox events
* Lease ownership checks that prevent stale workers from overwriting reclaimed events
* Schema-level constraints for duplicate reward prevention and critical invariants
* Schema-level uniqueness for one outbox event of each type per aggregate
* Concurrent reward-claim integration tests that verify final claim, idempotency, and outbox state
* Full migration-chain verification against disposable databases
* Prometheus-style low-cardinality HTTP, reward, idempotency, and outbox worker metrics
* Separate explicit metric registries for the API and worker processes
* Structured logging with `log/slog`
* Request ID middleware
* Panic recovery
* Basic secure headers
* Environment-based configuration
* Explicit database and publisher timeouts
* Graceful shutdown
* Unit tests
* PostgreSQL integration tests
* HTTP + PostgreSQL integration tests
* Makefile
* Dockerfile
* GitHub Actions CI
* Baseline CI checks for formatting, module tidiness, vet, tests, race tests, full migration-chain verification, integration tests, and Docker builds
* Separate security workflow for CodeQL code scanning and Go vulnerability checks
* GitHub Actions concurrency cancellation for superseded workflow runs
* Dependabot baseline for Go modules, GitHub Actions, Dockerfiles, and Docker Compose

Planned work:

* PostgreSQL-backed outbox backlog and oldest-event observability after query cost and freshness semantics are designed
* Container scanning and SBOM generation
* OpenAPI contract
* Expanded architecture, reliability, observability, threat model, runbook, and tradeoff documentation

## Requirements

* Go 1.26.5
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

Run the outbox worker in a separate terminal:

```bash
make run-worker
```

Run PostgreSQL, the API, and the worker together with Docker Compose:

```bash
make stack-up
```

Follow the service logs:

```bash
make stack-logs
```

Stop the local stack:

```bash
make stack-down
```

The worker claims one due outbox event at a time, simulates publishing through the local publisher boundary, and marks the event as `published`, schedules retry with backoff, or moves it to `dead_letter` after the configured maximum attempts.

Additional worker processes can be run for more throughput. PostgreSQL coordinates concurrent workers through processing leases and `FOR UPDATE SKIP LOCKED`.

The API fails fast on startup if PostgreSQL cannot be reached. After startup, `/readyz` reports dependency readiness and returns `503` if PostgreSQL becomes unavailable.

Health and metrics endpoints:

```bash
curl -i http://localhost:8080/livez
curl -i http://localhost:8080/readyz
curl -i http://localhost:8080/metrics

curl -i http://localhost:8081/livez
curl -i http://localhost:8081/readyz
curl -i http://localhost:8081/metrics
```

The API and worker are separate operating-system processes and therefore expose separate process-local registries. API metrics are not forwarded through the worker, and worker metrics are not forwarded through the API.

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

Retrying the same request with the same `Idempotency-Key` returns the stored response body and includes `Idempotent-Replayed: true`.

## Database

Start local PostgreSQL:

```bash
make db-up
```

Stop the local Compose stack, including PostgreSQL and any running API or worker containers:

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

Verify the latest migration can apply, roll back, and re-apply:

```bash
make db-check
```

`db-check` applies pending migrations, rolls back the latest migration, and applies migrations again against the configured database. It verifies rollback of the latest migration only; it does not roll the complete historical migration chain down to version zero.

The command is intended for clean local and CI databases. If you have created manual local reward claim data, clear it or reset the local database before running `db-check`. Do not run it against shared, staging, or production-like databases.

Destructively verify the complete migration chain against a disposable database:

```bash
ALLOW_DESTRUCTIVE_DB_CHECK=1 make db-check-full
```

`db-check-full` applies all migrations, rolls the schema down to version zero, and reapplies all migrations. It refuses to run unless `ALLOW_DESTRUCTIVE_DB_CHECK=1` is explicitly set.

The opt-in flag is a guard against accidental use, not proof that the configured database is safe to destroy. Run this target only against a disposable local or CI database and never against shared, staging, or production-like databases.

### Retention

`idempotency_keys` includes `expires_at` metadata, but expiry and cleanup are intentionally kept out of the reward claim request path. The service does not currently run an automatic idempotency cleanup job.

Published and dead-lettered outbox events are also retained without automatic cleanup. Routine retention must never remove `pending` or `processing` events.

Retention periods, safe batched cleanup procedures, and operational safeguards are intentionally deferred to the runbook rather than implemented as an in-process scheduler.

The local Docker Compose setup uses PostgreSQL 18.4 and development-only credentials. PostgreSQL is bound to localhost for local development. Do not reuse the local credentials outside local development.

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

Verify the complete migration chain against a disposable database:

```bash
ALLOW_DESTRUCTIVE_DB_CHECK=1 make db-check-full
```

Run PostgreSQL integration tests against an already running and migrated local database:

```bash
make test-integration
```

Start PostgreSQL, apply migrations, and run integration tests:

```bash
make test-integration-local
```

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

The CI workflow uses least-privilege read-only repository permissions. It starts a PostgreSQL 18.4 service, verifies the complete migration chain against the workflow's disposable database, runs Go checks including race tests and PostgreSQL integration tests, and builds the local Docker image:

* `make check`
* `make test-race`
* `make db-check-full` against the workflow's disposable PostgreSQL service
* `make test-integration`
* `make docker-build`

Security checks run in a separate workflow:

* CodeQL code scanning
* `make vuln`

Both workflows cancel superseded runs for the same branch so pull requests show the latest result without waiting for older runs to finish.

The repository should be configured so changes to `main` go through pull requests with required passing CI checks.

## Build

Build the API and worker binaries:

```bash
make build
```

## Docker

Build the local Docker image:

```bash
make docker-build
```

Run the API from the image with a reachable PostgreSQL database:

```bash
docker run --rm -p 127.0.0.1:8080:8080 \
  -e DATABASE_URL='postgres://game_rewards:game_rewards_dev_password@host.docker.internal:5432/game_rewards?sslmode=disable' \
  game-rewards-service:local
```

Run the worker from the same image:

```bash
docker run --rm -p 127.0.0.1:8081:8081 \
  -e DATABASE_URL='postgres://game_rewards:game_rewards_dev_password@host.docker.internal:5432/game_rewards?sslmode=disable' \
  game-rewards-service:local /usr/local/bin/worker
```

The image uses versioned base images, avoids `latest` tags, and runs as a non-root user.

When running the Docker image directly, provide a `DATABASE_URL` that the container can reach. The API and worker both ping PostgreSQL during startup and exit if the dependency is unavailable.

`host.docker.internal` works out of the box on Docker Desktop. On Linux, use a reachable host address or Docker network configuration for PostgreSQL.

## Configuration

The service is configured with environment variables. The `HTTP_*_TIMEOUT` settings apply to both the API server and the worker admin server; only their listen addresses are configured separately.

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
| `WORKER_ADMIN_ADDR`        | `:8081`                                                                                         |
| `WORKER_POLL_INTERVAL`     | `1s`                                                                                            |
| `OUTBOX_LOCK_TTL`          | `30s`                                                                                           |
| `OUTBOX_PUBLISH_TIMEOUT`   | `5s`                                                                                            |
| `OUTBOX_MAX_ATTEMPTS`      | `5`                                                                                             |
| `OUTBOX_BASE_BACKOFF`      | `1s`                                                                                            |
| `OUTBOX_MAX_BACKOFF`       | `1m`                                                                                            |
| `SHUTDOWN_TIMEOUT`         | `10s`                                                                                           |
| `LOG_LEVEL`                | `info`                                                                                          |

Example:

```bash
LOG_LEVEL=debug HTTP_ADDR=:9090 make run
```

Run the worker with custom retry settings:

```bash
OUTBOX_MAX_ATTEMPTS=3 OUTBOX_BASE_BACKOFF=2s make run-worker
```

`OUTBOX_LOCK_TTL` must be greater than `OUTBOX_PUBLISH_TIMEOUT` plus `DB_QUERY_TIMEOUT`. This leaves enough lease time for the worker to persist the publish result after the publisher call completes.

`OUTBOX_MAX_BACKOFF` must be greater than or equal to `OUTBOX_BASE_BACKOFF`.

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

### `GET /metrics`

Exposes the API process registry in Prometheus text format. The API registry contains Go runtime, process, HTTP request, reward claim, and idempotency metrics.

The worker exposes its own registry on `http://localhost:8081/metrics`. Its admin server also exposes `/livez` and `/readyz`; worker readiness requires both PostgreSQL and the worker loop to be available.

Only `GET` is accepted. Metrics remain available when PostgreSQL is unavailable so failures can still be observed. In a real deployment, metric endpoints should be restricted by private networking, ingress policy, firewall rules, or an observability proxy rather than exposed publicly.

Key application metric families:

| Metric | Type | Labels |
| --- | --- | --- |
| `game_rewards_http_requests_total` | counter | `route`, `method`, `status_code` |
| `game_rewards_http_request_duration_seconds` | histogram | `route`, `method` |
| `game_rewards_reward_claim_operations_total` | counter | `outcome` |
| `game_rewards_idempotency_operations_total` | counter | `outcome` |
| `game_rewards_outbox_claim_attempts_total` | counter | `outcome` |
| `game_rewards_outbox_publish_attempts_total` | counter | `event_type`, `outcome` |
| `game_rewards_outbox_publish_duration_seconds` | histogram | `event_type`, `outcome` |
| `game_rewards_outbox_events_published_total` | counter | `event_type` |
| `game_rewards_outbox_retries_scheduled_total` | counter | `event_type`, `failure_reason` |
| `game_rewards_outbox_events_dead_lettered_total` | counter | `event_type`, `failure_reason` |
| `game_rewards_outbox_lease_losses_total` | counter | `event_type`, `operation` |
| `game_rewards_outbox_operation_errors_total` | counter | `operation` |

Metrics never use player, campaign, reward, aggregate, event, worker, request, or idempotency identifiers as labels. Raw URL paths and raw errors are also excluded. Unknown event types and routes are normalized to bounded `unknown` values.

`game_rewards_outbox_publish_attempts_total{outcome="success"}` means the publisher returned successfully. `game_rewards_outbox_events_published_total` increases only after PostgreSQL also accepts the lease-fenced `published` transition. A gap between the two, together with lease-loss or operation-error metrics, distinguishes downstream delivery from durable outbox finalization.

Useful PromQL examples:

```promql
sum by (route, method) (rate(game_rewards_http_requests_total[5m]))
```

```promql
histogram_quantile(
  0.95,
  sum by (le, route) (rate(game_rewards_http_request_duration_seconds_bucket[5m]))
)
```

```promql
sum by (event_type, outcome) (rate(game_rewards_outbox_publish_attempts_total[5m]))
```

```promql
increase(game_rewards_outbox_lease_losses_total[15m])
```

Prometheus creates the scrape-level `up` metric; the application does not expose its own `up` gauge. Current queue depth, oldest pending event age, and current dead-letter count are intentionally not approximated with process-local gauges. Those values require a bounded PostgreSQL collector or sampler with explicit query-cost and freshness semantics.

### `POST /v1/reward-claims`

Creates a reward claim for a player in a campaign.

Successful claim creation also stores a `RewardClaimed` event in the PostgreSQL-backed transactional outbox in the same database transaction as the claim and idempotency response. The event is stored with `pending` status for the async outbox worker; the request path does not call external publishers.

This endpoint requires exactly one non-empty `Idempotency-Key` header value. Requests with multiple header values are rejected as ambiguous. The service uses the key to provide deterministic retry behavior for reward claim creation.

Raw idempotency keys are not stored; the service stores a SHA-256 key hash, request hash, idempotency state, response status, and response body.

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
  "claimed_at": "2026-07-07T12:34:56.123456Z"
}
```

#### Idempotency behavior

A successful reward claim creation stores the response status and response body for the provided `Idempotency-Key`.

Retrying the same accepted request with the same `Idempotency-Key` returns the stored response with the same status and body. Replay responses include:

```text
Idempotent-Replayed: true
```

Reusing the same `Idempotency-Key` with a different request payload returns `409 Conflict`:

```json
{
  "error": {
    "code": "idempotency_key_reused",
    "message": "Idempotency-Key was reused with a different request payload"
  }
}
```

Concurrent requests with the same `Idempotency-Key` are serialized by PostgreSQL. In the normal case, a retry that arrives while the first transaction is completing waits for that transaction and then replays the completed stored response.

If the service observes an already committed processing idempotency record, it returns `409 Conflict` with `Retry-After: 1`:

```json
{
  "error": {
    "code": "idempotency_key_in_progress",
    "message": "A request with this Idempotency-Key is still processing"
  }
}
```

Validation errors, malformed JSON, unsupported content types, oversized request bodies, missing or invalid idempotency keys, dependency failures, and unexpected internal errors are not stored as idempotent responses.

Duplicate reward claims for the same `player_id`, `campaign_id`, and `reward_id` return `409 Conflict`:

```json
{
  "error": {
    "code": "reward_already_claimed",
    "message": "Reward has already been claimed"
  }
}
```

Duplicate reward responses are also stored for the idempotency key that produced them, so retrying the duplicate request with the same key replays the same `409 Conflict` response.

Known transient PostgreSQL availability failures are returned as `503 Service Unavailable`. Schema, invariant, authentication, and unexpected database failures remain internal errors and are not exposed as transient dependency failures. Raw PostgreSQL, network, connection-string, and credential details are never returned to clients.

Common responses:

| Scenario                                              | Status                       |
| ----------------------------------------------------- | ---------------------------- |
| Claim created                                         | `201 Created`                |
| Idempotent replay of stored created response          | `201 Created`                |
| Invalid JSON or validation error                      | `400 Bad Request`            |
| Missing or invalid `Idempotency-Key`                  | `400 Bad Request`            |
| Unsupported content type                              | `415 Unsupported Media Type` |
| Request body too large                                | `413 Payload Too Large`      |
| Reward already claimed                                | `409 Conflict`               |
| Idempotent replay of stored duplicate reward response | `409 Conflict`               |
| `Idempotency-Key` reused with different payload       | `409 Conflict`               |
| Visible processing `Idempotency-Key` record           | `409 Conflict`               |
| Dependency unavailable                                | `503 Service Unavailable`    |
| Unexpected internal failure                           | `500 Internal Server Error`  |

Current validation rules:

* Exactly one non-empty `Idempotency-Key` header value is required; multiple values are rejected as invalid.
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

Invalid JSON, invalid requests, unsupported media types, oversized request bodies, already-claimed rewards, unavailable dependencies, idempotency conflicts, and internal failures return the stable JSON error response format.

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
* `idempotency_key_reused`
* `idempotency_key_in_progress`
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

## Outbox worker

The outbox worker processes events created by the reward claim request path.

The request transaction creates a pending `RewardClaimed` outbox event together with the reward claim and idempotency response. The worker later claims due events from PostgreSQL, publishes them through a small publisher interface, and updates the outbox row based on the result.

Current local publisher behavior is intentionally simple: it logs a simulated publish and returns success. This keeps the worker focused on reliable asynchronous processing without introducing Kafka, Redis, webhooks, or another external broker.

Worker state transitions:

```text
pending -> processing
processing -> published
processing -> pending       retry scheduled with backoff
processing -> dead_letter
```

The worker uses processing leases:

* Each worker claims one event at a time.
* `locked_by` identifies the worker process that claimed the event.
* Worker identifiers include a cryptographically random process-instance component.
* `locked_until` allows another worker to reclaim the event if the original worker crashes.
* `FOR UPDATE SKIP LOCKED` allows safe horizontal polling without two workers claiming the same due row at the same time.
* Publish completion, retry, and dead-letter updates require the acting worker to still own the lease.
* A stale worker cannot overwrite event state after another worker has reclaimed the event.

Failure behavior:

* Successful publish marks the event as `published`.
* Publish failure increments `attempts` and schedules retry by moving `available_at` into the future.
* Retry timestamps are calculated using PostgreSQL time.
* Raw publisher errors are not logged or persisted by the worker.
* Publisher failures are stored as controlled values such as `publish_failed`, `publish_timeout`, and `publish_canceled`.
* Publish timeout is treated as a publish failure and goes through retry/backoff.
* Worker shutdown cancels in-flight work without incrementing attempts; the event remains `processing` until the lease expires.
* Worker shutdown is bounded by `SHUTDOWN_TIMEOUT`.
* After `OUTBOX_MAX_ATTEMPTS`, the worker marks the event as `dead_letter`.

Database operations use `DB_QUERY_TIMEOUT`, and publisher calls use `OUTBOX_PUBLISH_TIMEOUT`.

Delivery semantics are at-least-once. If publish succeeds but the worker crashes before marking the row as `published`, the event may be published again after lease expiry. Downstream consumers are expected to deduplicate by event ID.

### Worker admin lifecycle

The worker binds its admin listener before starting the processing loop. A listener failure therefore prevents the worker from starting without observability. If either the worker loop or admin server stops unexpectedly, the other component is canceled and the process exits non-zero. During graceful shutdown, readiness is disabled before the worker is canceled and the admin server is drained within `SHUTDOWN_TIMEOUT`.

## Project structure

```text
cmd/api
  Application entrypoint.

cmd/worker
  Outbox worker entrypoint.

internal/adminhttp
  Worker-only admin server for liveness, readiness, and process-local metrics.

internal/config
  Loads and validates runtime configuration.

internal/health
  Shared liveness and readiness handler semantics.

internal/httpapi
  API server, routes, middleware, reward claim endpoint, and JSON responses.

internal/idempotency
  Deterministic hashing helpers for idempotency keys and accepted request payloads.

internal/observability
  Explicit process-local Prometheus registries and bounded metric collectors.

internal/outbox
  Async outbox worker, observer boundary, publisher boundary, retry/backoff logic, and PostgreSQL-backed outbox processing.

internal/postgres
  PostgreSQL pool setup and health checks.

internal/rewards
  Reward claim domain logic, service layer, response marshaling, and PostgreSQL-backed persistence.

migrations
  Versioned SQL migrations for the core schema.

compose.yaml
  Local PostgreSQL, API, and worker development stack.
```

## Before opening a pull request

Run the main local checks before opening a PR:

```bash
make db-check
make test-integration-local
make ci
make docker-build
```

Use the destructive full migration-chain check separately when validating migration history against a disposable database:

```bash
ALLOW_DESTRUCTIVE_DB_CHECK=1 make db-check-full
```

## License

MIT
