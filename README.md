# game-rewards-service

A small Go backend service for retry-safe game reward claims.

The service uses PostgreSQL to enforce reward uniqueness, persist deterministic idempotency responses, and atomically create transactional outbox events. A separate worker publishes outbox events with leases, retries, and at-least-once delivery semantics.

## What it includes

* `POST /v1/reward-claims` with required `Idempotency-Key`
* PostgreSQL-backed business invariants and deterministic replay
* transactional `RewardClaimed` outbox events
* separate async worker with `FOR UPDATE SKIP LOCKED`, leases, retries, and dead-letter handling
* structured `log/slog` logging, `/livez`, `/readyz`, and Prometheus metrics
* SQL migrations, Docker Compose, CI, CodeQL, `govulncheck`, and database-backed concurrency tests

## Architecture at a glance

```text
Caller
  |
  v
API (:8080)
  |
  | PostgreSQL transaction
  v
reward_claims + reward_claim_idempotency_keys + outbox_events
                               |
                               v
                         Worker (:8081 admin)
                               |
                               v
                     Publisher boundary
                     (simulated in this repository)
```

Three mechanisms solve three different problems:

| Problem                  | Mechanism                                                       |
| ------------------------ | --------------------------------------------------------------- |
| Client retries           | Deterministic `Idempotency-Key` replay                          |
| Duplicate reward grants  | PostgreSQL `UNIQUE (player_id, campaign_id, reward_id)`         |
| Duplicate async delivery | At-least-once delivery + downstream deduplication by `event_id` |

See [`docs/architecture.md`](docs/architecture.md) for the detailed consistency boundaries, worker flow, delivery semantics, and key design choices.

## Quick start

Requirements:

* Minimum Go version declared by the `go` directive in [`go.mod`](go.mod)
* Docker with Compose
* Make

Start PostgreSQL, apply migrations, and run the API and worker:

```bash
make stack-up
```

Check health:

```bash
curl -i http://localhost:8080/livez
curl -i http://localhost:8080/readyz
curl -i http://localhost:8081/livez
curl -i http://localhost:8081/readyz
```

Create a reward claim:

```bash
curl -i -X POST http://localhost:8080/v1/reward-claims \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: claim-player-123-winter-2026-daily-login' \
  -d '{"player_id":"player-123","campaign_id":"winter-2026","reward_id":"daily-login"}'
```

A successful request returns `201 Created`:

```json
{
  "claim_id": "8f2e7b38-7e2d-4d4c-91b5-1c4d5f5c7c0a",
  "player_id": "player-123",
  "campaign_id": "winter-2026",
  "reward_id": "daily-login",
  "claimed_at": "2026-07-07T12:34:56.123456Z"
}
```

Retrying the same accepted request with the same `Idempotency-Key` replays the stored response while that idempotency record is retained. Replays include:

```text
Idempotent-Replayed: true
```

Follow logs or stop the stack:

```bash
make stack-logs
make stack-down
```

## API behavior

`POST /v1/reward-claims` requires exactly one non-empty `Idempotency-Key` header value and a JSON body containing `player_id`, `campaign_id`, and `reward_id`.

The three identifiers are trimmed, required, valid UTF-8, limited to 128 Unicode characters, and must not contain NUL. The idempotency key is trimmed, limited to 255 bytes, and rejects ASCII control characters and DEL. Request bodies must be valid UTF-8 and are limited to 64 KiB; unknown JSON fields and multiple JSON values are rejected.

| Scenario                                       | Result                                               |
| ---------------------------------------------- | ---------------------------------------------------- |
| New valid claim                                | `201 Created`                                        |
| Same key + same accepted request               | stored response replay while the record is retained  |
| Same key + different accepted request          | `409 idempotency_key_reused` while retained          |
| Different key + same player/campaign/reward    | `409 reward_already_claimed`                         |
| Invalid input                                  | `400`, `413`, or `415`                               |
| Known PostgreSQL availability failure          | `503 service_unavailable`                            |
| Unexpected internal failure                    | `500 internal_error`                                 |

Idempotency records with stored responses become eligible for routine cleanup 24 hours after creation. The service does not run automatic cleanup; once a record is deleted, its key-level replay and reuse history is no longer available.

Error responses from `POST /v1/reward-claims` use the stable envelope:

```json
{
  "error": {
    "code": "...",
    "message": "..."
  }
}
```

Every API response includes `X-Request-ID`. Clients may provide a bounded safe value or let the service generate one.

The full client-facing contract is documented in [`openapi.yaml`](openapi.yaml).

## Architecture and reliability

A new claim, its stored idempotency response, and its `RewardClaimed` outbox event commit in one PostgreSQL transaction. PostgreSQL is the request transaction's durable boundary; the transaction does not call the publisher or another downstream service.

The worker atomically claims due outbox rows and commits the lease update before publishing. Finalization uses lease ownership checks when marking an event published, scheduling a retry, or dead-lettering it. No database row lock or transaction is held while the publisher runs.

Delivery is at-least-once. If publishing succeeds but durable finalization does not, the lease can expire and the event may be delivered again. A real downstream consumer must therefore deduplicate by `event_id`.

Worker timing configuration is validated at startup. In particular, the processing lease must be greater than the publisher timeout plus two database query-timeout budgets: one for claiming and one for finalization.

See [`docs/architecture.md`](docs/architecture.md) for the system flow, reliability guarantees, and key design choices.

## Observability

Direct local runs bind the API to `127.0.0.1:8080` and the worker admin server to `127.0.0.1:8081` by default. The container image uses wildcard listeners so published container ports remain reachable; local Compose publishes those ports only on host loopback.

| Process | `/livez`         | `/readyz`                       | `/metrics`                                  |
| ------- | ---------------- | ------------------------------- | ------------------------------------------- |
| API     | process liveness | PostgreSQL readiness            | HTTP, reward-claim, and idempotency metrics |
| Worker  | process liveness | PostgreSQL + active worker loop | HTTP and outbox metrics                     |

Metrics use separate process-local Prometheus registries and bounded labels. High-cardinality identifiers such as player IDs, idempotency keys, request IDs, event IDs, raw paths, and raw errors are not used as metric labels.

`/metrics` does not query PostgreSQL during scrapes. Database-global queue depth, oldest pending age, and current dead-letter count are intentionally not approximated with process-local gauges.

Inspect metrics locally with:

```bash
curl -s http://localhost:8080/metrics
curl -s http://localhost:8081/metrics
```

## Development

Common commands:

| Command                                           | Purpose                                                                  |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `make check`                                      | formatting, module tidiness, vet, unit tests                             |
| `make ci`                                         | fast checks plus race tests and `govulncheck`                            |
| `make test-integration-local`                     | start PostgreSQL, migrate, run integration tests                         |
| `make test-runtime-resilience`                    | verify a prebuilt image across PostgreSQL outage/recovery                  |
| `make test-runtime-resilience-local`              | build the local image, then run the runtime resilience test                |
| `make docker-build`                               | build the local container image for the current Docker platform           |
| `make docker-build-arm64`                         | build the `linux/arm64` deployment container image                        |
| `ALLOW_DESTRUCTIVE_DB_RESET=1 make db-reset`     | reset the local Compose database and apply the current schema             |
| `ALLOW_DESTRUCTIVE_DB_CHECK=1 make db-check`     | verify the schema up/down-to-zero/up round-trip on disposable data        |

Container build and runtime verification are deliberately separate. `make test-runtime-resilience` consumes `DOCKER_IMAGE` without rebuilding it; `make test-runtime-resilience-local` is the convenience path that builds first. The deployment container target is `linux/arm64`: `make docker-build` keeps local development native to the current Docker platform, while `make docker-build-arm64` builds the deployment artifact explicitly. CI builds and runtime-verifies that ARM64 deployment image. Override `DOCKER_IMAGE` to build and verify a specifically named artifact, for example:

```bash
DOCKER_IMAGE=game-rewards-service:test make docker-build
DOCKER_IMAGE=game-rewards-service:test make test-runtime-resilience
```

The runtime-resilience smoke test uses an isolated Compose project and disposable database volume, verifies that both API and worker run the requested prebuilt image, and reserves the repository's standard loopback ports. Stop any existing repository stack before running it.

Both destructive database commands require explicit opt-in and are intended only for disposable local data. See [`docs/runbook.md`](docs/runbook.md) for reset and migration procedures.

Run the processes directly instead of Compose:

```bash
make db-up
make migrate-up
make run
```

In another terminal:

```bash
make run-worker
```

Runtime configuration is environment-based. Each process loads and validates only the settings it owns, while shared database, HTTP-timeout, logging, and shutdown settings apply to both processes. Unset variables use defaults, while explicitly blank overrides are rejected for the process that owns them. Direct process defaults keep HTTP listeners on loopback; set API-owned `HTTP_ADDR` or worker-owned `WORKER_ADMIN_ADDR` explicitly when another bind address is required. Defaults, ownership, and supported variables are documented in [`.env.example`](.env.example). Local Compose credentials are development-only.

## Documentation

* [`openapi.yaml`](openapi.yaml) — reward-claim HTTP contract
* [`docs/architecture.md`](docs/architecture.md) — architecture, consistency, reliability, and key design choices
* [`docs/runbook.md`](docs/runbook.md) — troubleshooting, retention, and migration procedures
* [`SECURITY.md`](SECURITY.md) — security assumptions, boundaries, and repository security posture

## Scope

This repository is a compact reference service rather than a complete production platform. It focuses on retry-safe reward claims, PostgreSQL-backed consistency, and transactional outbox delivery. Authentication, authorization, deployment networking and secrets, and an external publisher integration are intentionally outside the current scope.

The current publisher is simulated. Delivery is at-least-once, not exactly-once; a real downstream consumer must deduplicate by `event_id`.

## License

MIT
