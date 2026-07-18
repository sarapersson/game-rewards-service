# Security Notes

This project is a personal backend portfolio service under active development. It is not operated as a production service and does not process real user data.

## Current baseline

* Local `.env` files are ignored by Git
* HTTP server timeouts are configured explicitly
* Database operations and worker shutdown use explicit timeouts
* Request IDs are bounded in length and restricted to a safe character set
* Application logging is designed to avoid request bodies, secrets, full idempotency keys, authorization headers, and sensitive runtime configuration
* Worker logging is designed to avoid outbox payloads, player identifiers, raw metadata, idempotency keys, request bodies, and raw publisher errors
* PostgreSQL is configured through `DATABASE_URL`
* The local Docker Compose PostgreSQL credentials are development-only
* The local Docker Compose PostgreSQL port is bound to `127.0.0.1`
* `/readyz` checks PostgreSQL readiness without exposing raw database errors
* API and worker expose separate process-local Prometheus registries
* Metric labels are restricted to bounded routes, methods, status codes, outcomes, event types, failure reasons, and operations
* Metrics do not include player, campaign, reward, aggregate, event, worker, request, or idempotency identifiers
* Raw URL paths and raw errors are not used as metric labels
* Local Compose binds both API and worker admin ports to `127.0.0.1`
* `POST /v1/reward-claims` requires and validates `Idempotency-Key`
* Raw idempotency keys are hashed before persistence
* Idempotency request hashes are stored to detect key reuse with different request payloads
* Completed idempotent responses are stored for deterministic retry replay
* Reward claim request bodies are size-limited
* Duplicate reward claims for the same player, campaign, and reward are prevented by a PostgreSQL unique constraint
* SQL migrations define schema-level constraints for critical invariants
* Successful reward claim creation writes a `RewardClaimed` event to the transactional outbox in the same database transaction as the claim and idempotency response
* Outbox events are protected by a PostgreSQL uniqueness constraint for one event of each type per aggregate
* The async outbox worker claims due events with PostgreSQL `FOR UPDATE SKIP LOCKED`
* Each worker claims one event at a time
* Outbox processing uses processing leases with `locked_by` and `locked_until` so events can be reclaimed after worker crashes
* Worker identifiers include a cryptographically random process-instance component
* Outbox state updates require the acting worker to still own the lease, preventing stale workers from overwriting reclaimed events
* Outbox publish failures are retried with bounded exponential backoff
* Retry timestamps are calculated using PostgreSQL time
* Raw publisher errors are mapped to controlled failure classifications before logging or persistence
* Outbox events are moved to `dead_letter` after the configured maximum number of failed attempts
* Outbox delivery is at-least-once; downstream consumers are expected to deduplicate by event ID
* The current local publisher simulates publishing and does not call external brokers or services
* The Docker container runs as a non-root user
* Docker images use versioned base images and avoid `latest` tags
* GitHub Actions workflows use least-privilege permissions
* The CI workflow uses read-only repository permissions
* The security workflow grants CodeQL only the permissions required to publish code scanning results
* CI runs formatting, module tidiness, vet, tests, race tests, Docker builds, latest-migration rollback verification, and PostgreSQL integration tests
* CodeQL and Go vulnerability checks run in a separate GitHub Actions security workflow
* Dependabot is configured for Go modules, GitHub Actions, Dockerfiles, and Docker Compose

## Current scope

The current implementation includes the Go HTTP API, health endpoints, baseline CI, repository hygiene, local PostgreSQL development, SQL migrations, the core database schema, PostgreSQL-backed readiness checks, PostgreSQL-backed reward claim creation, PostgreSQL-backed idempotency state, deterministic idempotency replay, transactional outbox writes, async outbox worker processing, CodeQL, and Go vulnerability checks.

The core schema includes tables for reward claims, idempotency keys, and outbox events. Successful reward claim creation stores a `RewardClaimed` event in the transactional outbox with `pending` status. The async worker later claims due events, marks them as `processing`, publishes them through a small publisher interface, and then marks them as `published`, schedules retry, or moves them to `dead_letter`.

The reward-claim API currently requires `Idempotency-Key`, validates the key, stores only the hashed key, records a request hash, persists completed response status and response body, replays completed responses for matching retries, and rejects reused keys with different request payloads.

Validation errors, malformed JSON, unsupported content types, oversized request bodies, missing or invalid idempotency keys, dependency failures, and unexpected internal errors are not stored as idempotent responses.

The outbox worker currently uses a local simulated publisher. It demonstrates worker reliability mechanics, retry behavior, dead-letter handling, lease recovery, and safe PostgreSQL polling without introducing Kafka, Redis, webhooks, or another external broker.

Prometheus and Grafana deployments, authentication, authorization, rate limiting, external integrations, administrative dead-letter replay tooling, and production incident response processes are not implemented yet.

The API must not be exposed to untrusted or public networks without authentication, authorization, transport security, rate limiting, abuse controls, appropriate network restrictions, and a complete production security review.

More complete security documentation will be added as the service grows, including a threat model for reward claims, idempotency, persistence, and async event delivery.

## Metrics exposure

The API exposes `/metrics` on its API listener and the worker exposes `/metrics` on its separate admin listener. Metrics are process-local; neither process proxies or aggregates the other process's registry.

The local Docker Compose mappings bind both listeners to localhost. A production adaptation should place metric endpoints on private networking or restrict them through ingress, firewall, service mesh, or observability proxy policy. This repository intentionally does not implement a separate metric authentication secret or custom authentication scheme.

Metric labels are designed to remain low-cardinality. Identifiers such as player ID, campaign ID, reward ID, aggregate ID, event ID, worker ID, request ID, and idempotency key are prohibited. Raw request paths, raw errors, user agents, remote addresses, hostnames, process IDs, and attempt numbers are also excluded from labels.

Metrics remain available when PostgreSQL is unavailable. Liveness checks process health only, while readiness checks required dependencies and worker-loop availability.

## Database and secrets

The default `DATABASE_URL` is intended for local development only.

Production, staging, shared-environment, or otherwise sensitive credentials must not be committed to the repository, included in examples, or logged. Credentials shown in this repository are development-only defaults for the isolated local Docker Compose environment.

Production-like environments should provide database credentials through their runtime secret-management mechanism.

Application logs should not include full connection strings, authorization headers, request bodies, raw idempotency keys, outbox payloads, raw reward metadata, raw publisher errors, or other sensitive metadata.

The local PostgreSQL service is bound to `127.0.0.1:5432` for developer convenience. Anyone adapting this project for production should review network exposure, transport encryption, database roles, backup strategy, credential rotation, and least-privilege database access.

## Outbox worker security and reliability notes

The outbox worker is designed for at-least-once delivery. If publishing succeeds but the worker crashes before marking the event as `published`, the event may be published again after the processing lease expires. Downstream consumers should deduplicate by the outbox event ID.

The worker claims one event at a time. It does not hold database row locks while publishing.

Processing leases are owned through `locked_by`. Completion, retry, and dead-letter updates fail if the event has been reclaimed by another worker.

The worker does not log outbox payloads or raw publisher errors. Logs should remain limited to safe operational fields such as worker ID, event ID, event type, aggregate type, aggregate ID, attempt number, failed-attempt count, status, retry delay, and retry time.

`last_error` is stored for operational debugging and is bounded by a database constraint. The worker stores controlled failure classifications rather than raw publisher error messages.

Publisher implementations must honor context cancellation and deadlines.

A shutdown cancellation during publishing is recorded in metrics with outcome `canceled`, but it does not increment `outbox_events.attempts` or persist a retry or dead-letter transition. The event remains `processing` until the lease expires and can then be recovered by another worker.

Dead-lettered events are retained in PostgreSQL for inspection. This project does not yet include administrative tooling for replaying or deleting dead-lettered events. Operational handling for dead-lettered events will be documented later in the runbook.

## Repository protection

The repository should be configured so changes to `main` go through pull requests with required passing CI checks.

Direct pushes, force pushes, and branch deletion should be disabled for `main`.

## Supported versions

This repository does not publish or maintain supported release versions.

Security-related changes may be made on the `main` branch as the project evolves, but no compatibility, maintenance, update, remediation, or response-time guarantees are provided.

## Security scope and responsibility

This repository is a personal portfolio project. It is not operated as a production service, distributed as a supported product, or offered for use with real user data.

No security reporting channel, vulnerability disclosure program, bug bounty program, production incident response service, service-level agreement, response-time commitment, remediation-time commitment, or support obligation is provided.

The repository owner does not accept responsibility for deployments, modifications, integrations, forks, copies, or derivative systems created or operated by other parties.

Anyone who copies, forks, adapts, deploys, integrates, or otherwise uses this code is solely responsible for evaluating its suitability and for securing, testing, operating, monitoring, updating, and maintaining their own version.
