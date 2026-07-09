# Security Notes

This project is an early-stage backend service.

## Current baseline

* Local `.env` files are ignored by Git
* HTTP server timeouts are configured explicitly
* Request IDs are bounded in length and restricted to a safe character set
* Application logging is designed to avoid request bodies, secrets, full idempotency keys, authorization headers, and sensitive runtime configuration
* PostgreSQL is configured through `DATABASE_URL`
* The local Docker Compose PostgreSQL credentials are development-only
* The local Docker Compose PostgreSQL port is bound to `127.0.0.1`
* `/readyz` checks PostgreSQL readiness without exposing raw database errors
* `POST /v1/reward-claims` requires and validates `Idempotency-Key`
* Raw idempotency keys are hashed before persistence
* Idempotency request hashes are stored to detect key reuse with different request payloads
* Completed idempotent responses are stored for deterministic retry replay
* Reward claim request bodies are size-limited
* Duplicate reward claims for the same player, campaign, and reward are prevented by a PostgreSQL unique constraint
* SQL migrations define schema-level constraints for critical invariants
* Successful reward claim creation writes a `RewardClaimed` event to the transactional outbox in the same database transaction as the claim and idempotency response
* Outbox events are protected by a PostgreSQL uniqueness constraint for one event of each type per aggregate
* The Docker container runs as a non-root user
* Docker images use versioned base images and avoid `latest` tags
* GitHub Actions workflows use least-privilege permissions
* The CI workflow uses read-only repository permissions
* The security workflow grants CodeQL only the permissions required to publish code scanning results
* CI runs formatting, module tidiness, vet, tests, race tests, Docker build, database migration verification, and PostgreSQL integration tests
* CodeQL and Go vulnerability checks run in a separate GitHub Actions security workflow
* Dependabot is configured for Go modules, GitHub Actions, and Docker

## Current scope

The current implementation includes the HTTP API scaffold, health endpoints, baseline CI, repository hygiene, local PostgreSQL development, SQL migrations, the core database schema, PostgreSQL-backed readiness checks, PostgreSQL-backed reward claim creation, PostgreSQL-backed idempotency state, deterministic idempotency replay, transactional outbox writes, CodeQL, and Go vulnerability checks.

The core schema includes tables for reward claims, idempotency keys, and outbox events. Successful reward claim creation stores a `RewardClaimed` event in the transactional outbox with `pending` status for future async processing.

The reward-claim API currently requires `Idempotency-Key`, validates the key, stores only the hashed key, records a request hash, persists completed response status and response body, replays completed responses for matching retries, and rejects reused keys with different request payloads.

Validation errors, malformed JSON, unsupported content types, oversized request bodies, missing or invalid idempotency keys, dependency failures, and unexpected internal errors are not stored as idempotent responses.

Async worker processing, metrics, authentication, authorization, rate limiting, and external integrations are not implemented yet.

More complete security documentation will be added as the service grows, including a threat model for reward claims, idempotency, persistence, and async event delivery.

## Database and secrets

The default `DATABASE_URL` is intended for local development only.

Real credentials must not be committed to the repository, included in examples, or logged. Production-like environments should provide database credentials through their runtime secret-management mechanism.

Application logs should not include full connection strings, authorization headers, request bodies, raw idempotency keys, or other sensitive metadata.

The local PostgreSQL service is bound to `127.0.0.1:5432` for developer convenience. Anyone adapting this project for production should review network exposure, TLS requirements, database roles, backup strategy, credential rotation, and least-privilege database access.

## Repository protection

The repository should be configured so changes to `main` go through pull requests with required passing CI checks.

Direct pushes, force pushes, and branch deletion should be disabled for `main`.

## Supported versions

This project does not have supported release versions yet.

Security-related changes are made on the main branch as the project evolves.

## Security scope

This repository is not operated as a production service and does not process real user data.

There is no bug bounty program, vulnerability disclosure program, production incident response process, or service-level agreement for this project.

Anyone adapting this code for their own use is responsible for reviewing, hardening, deploying, monitoring, and maintaining their own version.
