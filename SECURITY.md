# Security Notes

`game-rewards-service` is a personal reference project, not an operated production service. It does not process real user data and does not provide a security SLA, vulnerability disclosure program, or support commitment.

Anyone deploying or adapting the code is responsible for authentication, authorization, network security, secret management, monitoring, backups, patching, and incident response appropriate to their environment.

## Security model

The main trust boundaries are:

```text
caller -> API -> PostgreSQL
                 ^
worker ----------|
worker -> publisher boundary
operator/monitoring -> health and metrics endpoints
```

Important assets include reward-claim integrity, idempotency state, outbox event integrity, PostgreSQL credentials and persisted data, operational telemetry, and build/dependency integrity.

The service does **not** implement authentication or authorization, rate limiting, TLS termination, production network policy, or a secret-management platform. It must not be exposed directly to untrusted networks without appropriate controls in the deployment environment.

## Implemented safeguards

* Requests are bounded and strictly decoded: 64 KiB body limit, unknown-field rejection, single-JSON-value enforcement, bounded reward identifiers, and exactly one validated `Idempotency-Key`.
* Raw idempotency keys are SHA-256 hashed before persistence.
* PostgreSQL constraints enforce critical invariants, and claim creation, idempotency completion, and outbox creation commit atomically.
* Known dependency-availability failures are distinguished from unexpected internal, schema, and invariant failures; low-level database and network details are not returned to clients.
* HTTP, database, publisher, and shutdown operations use explicit timeouts.
* Worker publishing uses leases and ownership-fenced finalization; no database transaction or row lock is held while the publisher executes.
* Docker runs as a non-root user with versioned base images, and local Compose ports bind to `127.0.0.1`.
* GitHub Actions use least-privilege permissions and `persist-credentials: false`, and run formatting, vet, tests, race tests, migration/integration checks, Docker builds, CodeQL, and `govulncheck`.
* Dependabot covers Go modules, GitHub Actions, Dockerfiles, and Docker Compose.

## Logging and sensitive data

Do not log or commit:

* credentials, tokens, private keys, or production connection strings;
* authorization headers;
* raw idempotency keys;
* full request bodies or outbox payloads;
* raw publisher errors or other low-level details that may expose sensitive configuration.

The application uses structured `log/slog` logging. Publisher failures are reduced to controlled classifications before persistence, and API responses sanitize low-level database and network failures. Unexpected reward-claim failures are logged with bounded error classifications and request correlation context rather than raw errors or claim identifiers.

Operational identifiers such as request IDs, event IDs, aggregate IDs, or worker IDs may appear in logs when useful for debugging and correlation. They are not used as Prometheus metric labels.

The local PostgreSQL credentials in `compose.yaml` and `.env.example` are development-only and must not be reused in shared or production environments.

## Health and metrics exposure

The API and worker expose `/livez`, `/readyz`, and `/metrics` on their respective listeners. Local Compose binds these listeners to localhost.

A production adaptation should restrict health and metrics access through private networking, firewall or ingress rules, or an observability proxy as appropriate. The service does not implement a separate metrics-authentication mechanism.

Metrics use bounded labels and exclude high-cardinality identifiers such as player IDs, reward IDs, event IDs, worker IDs, request IDs, and idempotency keys. Raw paths and raw errors are not metric labels.

## Database, migrations, and retention

Production-like environments should use least-privilege database roles, transport encryption, managed secrets, backups, restore testing, and credential rotation appropriate to the deployment.

`ALLOW_DESTRUCTIVE_DB_CHECK=1 make db-check-full` rolls the complete schema down to version zero and must only target disposable local or CI databases. The opt-in flag prevents accidental invocation; it does not prove that the configured database is safe to destroy.

The service does not perform automatic idempotency or outbox cleanup. Routine retention must never remove `pending` or `processing` outbox events.

Migration rollback preconditions and safe retention procedures are documented in [`docs/runbook.md`](docs/runbook.md).

## Future integration security

A future external publisher integration must define its own authentication, transport security, credential management, timeout and retry policy, and payload/data-classification requirements.

## Deliberate exclusions

The repository does not currently provide:

* authentication or authorization;
* rate limiting or abuse prevention;
* TLS termination or production network policy;
* a real external broker or publisher;
* automatic retention cleanup;
* dead-letter replay tooling;
* a Prometheus or Grafana deployment;
* a supported production release or operational response commitment.

These capabilities should be introduced only when concrete deployment or product requirements justify them.
