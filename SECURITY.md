# Security Notes

This project is an early-stage portfolio service.

## Current baseline

* Local `.env` files are ignored by Git
* HTTP server timeouts are configured explicitly
* Request IDs are bounded in length and restricted to a safe character set
* Logs avoid request bodies, secrets, full idempotency keys, authorization headers, and sensitive runtime configuration
* PostgreSQL is configured through `DATABASE_URL`
* The local Docker Compose PostgreSQL credentials are development-only
* `/readyz` checks PostgreSQL readiness without exposing raw database errors
* Idempotency storage is designed to use hashed idempotency keys rather than raw keys
* SQL migrations define schema-level constraints for critical invariants
* The Docker container runs as a non-root user
* Docker images use versioned base images and avoid `latest` tags
* GitHub Actions workflows use least-privilege permissions
* The CI workflow uses read-only repository permissions
* The security workflow grants CodeQL only the permissions required to publish code scanning results
* CI runs formatting, module tidiness, vet, tests, Docker build, and database migration verification
* CodeQL and Go vulnerability checks run in a separate GitHub Actions security workflow
* Dependabot is configured for Go modules, GitHub Actions, and Docker

## Current scope

The current implementation includes the HTTP API scaffold, health endpoints, baseline CI, repository hygiene, PostgreSQL local development, SQL migrations, core database schema, PostgreSQL-backed readiness checks, CodeQL, and Go vulnerability checks.

The core schema includes tables for reward claims, idempotency keys, and outbox events. The reward-claim API, idempotency behavior, transactional claim creation, transactional outbox writes, async worker, metrics, authentication, and external integrations are not implemented yet.

More complete security documentation will be added as the service grows, including a threat model for reward claims, idempotency, persistence, and async event delivery.

## Database and secrets

The default `DATABASE_URL` is intended for local development only.

Real credentials must not be committed to the repository, included in examples, or logged. Production-like environments should provide database credentials through their runtime secret-management mechanism.

Application logs should not include full connection strings, authorization headers, request bodies, raw idempotency keys, or other sensitive metadata.

The local PostgreSQL service is exposed on `localhost:5432` for developer convenience. Anyone adapting this project for production should review network exposure, TLS requirements, database roles, backup strategy, and credential rotation.

## Repository protection

The repository should be configured so changes to `main` go through pull requests with required passing CI checks.

Direct pushes, force pushes, and branch deletion should be disabled for `main`.

## Security scope

This repository is a portfolio project and reference implementation.

It is not operated as a production service, does not process real user data, and does not provide a formal vulnerability reporting or response process.

Anyone adapting this code for their own use is responsible for reviewing, hardening, deploying, monitoring, and maintaining their own version.
