# Security Notes

This project is an early-stage portfolio service.

## Current baseline

* Local `.env` files are ignored by Git
* HTTP server timeouts are configured explicitly
* Request IDs are bounded in length and restricted to a safe character set
* Logs avoid request bodies, secrets, and sensitive headers
* The Docker container runs as a non-root user
* Docker images avoid unpinned `latest` tags

## Current scope

Slice 1 contains only the HTTP API scaffold and health endpoints. There is no authentication, database, reward-claim API, idempotency store, or external integration yet.

More complete security documentation will be added as the service grows, including a threat model for reward claims, idempotency, persistence, and async event delivery.
