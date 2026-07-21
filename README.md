# game-rewards-service

A small Go backend service for retry-safe game reward claims.

The service uses PostgreSQL to enforce reward uniqueness, persist deterministic idempotency state, and atomically create transactional outbox events. A separate worker publishes outbox events with leases, retries, and at-least-once delivery semantics.

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
reward_claims + idempotency_keys + outbox_events
                               |
                               v
                         Worker (:8081 admin)
                               |
                               v
                     Publisher boundary
                     (simulated today)
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

* Go 1.26.5
* Docker
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
  "status": "claimed",
  "claimed_at": "2026-07-07T12:34:56.123456Z"
}
```

Retry the same accepted request with the same `Idempotency-Key` to receive the stored response. Replays include:

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

The three identifiers are trimmed, required, and limited to 128 Unicode characters. The idempotency key is trimmed, limited to 255 bytes, and rejects control characters. Request bodies are limited to 64 KiB; unknown JSON fields and multiple JSON values are rejected.

| Scenario                                       | Result                                               |
| ---------------------------------------------- | ---------------------------------------------------- |
| New valid claim                                | `201 Created`                                        |
| Same key + same accepted request               | stored response replay                               |
| Same key + different accepted request          | `409 idempotency_key_reused`                         |
| Different key + same player/campaign/reward    | `409 reward_already_claimed`                         |
| Visible committed processing idempotency state | `409 idempotency_key_in_progress` + `Retry-After: 1` |
| Invalid input                                  | `400`, `413`, or `415`                               |
| Known PostgreSQL availability failure          | `503 service_unavailable`                            |
| Unexpected internal failure                    | `500 internal_error`                                 |

Errors use the stable envelope:

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

A new claim, its completed idempotency response, and its `RewardClaimed` outbox event commit in one PostgreSQL transaction. The request path does not call external systems.

The worker claims due rows atomically, completes the claim database operation before publishing, then uses lease ownership checks when marking an event published, scheduling a retry, or dead-lettering it. It does not hold a database row lock or transaction while publishing.

Delivery is at-least-once. If publishing succeeds but durable finalization does not, the lease can expire and the event may be delivered again. A real downstream consumer must therefore deduplicate by `event_id`.

Worker timing configuration is validated at startup. In particular, the processing lease must outlive the publisher timeout plus the final database operation so ownership remains valid through normal finalization.

See [`docs/architecture.md`](docs/architecture.md) for the system flow, reliability guarantees, and key design choices.

## Observability

The API listens on `:8080` and the worker admin server on `:8081` by default.

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
| `make docker-build`                               | build the local container image                                          |
| `make db-check`                                   | latest migration down-one/up check; disposable databases only            |
| `ALLOW_DESTRUCTIVE_DB_CHECK=1 make db-check-full` | full migration chain down to zero and back up; disposable databases only |

Both migration checks execute down migrations and must only target disposable local or CI databases. `make db-check` rolls back and reapplies the latest migration. `db-check-full` rolls the complete schema down to version zero and additionally requires explicit opt-in. The opt-in flag does not make a shared database safe to modify or destroy.

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

Runtime configuration is environment-based. Defaults and supported variables are documented in [`.env.example`](.env.example). Local Compose credentials are development-only.

## Documentation

* [`openapi.yaml`](openapi.yaml) — reward-claim HTTP contract
* [`docs/architecture.md`](docs/architecture.md) — architecture, consistency, reliability, and key design choices
* [`docs/runbook.md`](docs/runbook.md) — troubleshooting, retention, and migration rollback guidance
* [`SECURITY.md`](SECURITY.md) — security assumptions, boundaries, and repository security posture

## Scope and limitations

This repository is a compact reference service rather than a complete production platform.

It intentionally does not include authentication or authorization, rate limiting, TLS termination, a real external event broker, automatic retention jobs, dead-letter replay tooling, Prometheus/Grafana deployment, Kubernetes, an ORM, Redis, or a distributed tracing stack.

The current publisher is simulated. Delivery is at-least-once, not exactly-once; a real downstream consumer must deduplicate by `event_id`.

## License

MIT
