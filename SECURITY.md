# Security Notes

This project is an early-stage portfolio service.

## Current baseline

* Local `.env` files are ignored by Git
* HTTP server timeouts are configured explicitly
* Request IDs are bounded in length and restricted to a safe character set
* Logs avoid request bodies, secrets, and sensitive headers
* The Docker container runs as a non-root user
* Docker images use versioned base images and avoid `latest` tags
* GitHub Actions CI uses least-privilege read-only repository permissions
* CI runs formatting, vet, test, race, and Go vulnerability checks
* Dependabot is configured for Go modules, GitHub Actions, and Docker

## Current scope

Slice 2 contains the HTTP API scaffold, health endpoints, baseline CI, and repository hygiene.

There is no authentication, database, reward-claim API, idempotency store, or external integration yet.

More complete security documentation will be added as the service grows, including a threat model for reward claims, idempotency, persistence, and async event delivery.

## Repository protection

The repository should be configured so changes to `main` go through pull requests with required passing CI checks.

Direct pushes, force pushes, and branch deletion should be disabled for `main`.

## Security scope

This repository is a portfolio project and reference implementation.

It is not operated as a production service, does not process real user data, and does not provide a formal vulnerability reporting or response process.

Anyone adapting this code for their own use is responsible for reviewing, hardening, deploying, monitoring, and maintaining their own version.
