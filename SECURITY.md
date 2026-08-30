# Security

`game-rewards-service` is a reference service rather than an operated production service. This document defines the repository's security boundaries, implemented safeguards, and deployment assumptions.

## Security model

The main trust boundaries are:

```text
caller -> API -> PostgreSQL
                 ^
worker ----------|
worker -> publisher boundary
operator/monitoring -> health and metrics endpoints
```

Important assets include reward-claim integrity, idempotency response integrity, outbox event integrity, PostgreSQL credentials and persisted data, operational telemetry, and build/dependency integrity.

The service does **not** implement authentication or authorization, rate limiting, TLS termination, production network policy, or a secret-management platform. It must not be exposed directly to untrusted networks without appropriate controls in the deployment environment.

## Implemented safeguards

* Requests are bounded and strictly decoded: valid UTF-8, a 64 KiB body limit, unknown-field rejection, single-JSON-value enforcement, bounded reward identifiers, and exactly one validated `Idempotency-Key`.
* Raw idempotency keys are SHA-256 hashed before persistence.
* PostgreSQL constraints enforce critical invariants, and claim creation, storage of the deterministic idempotency response, and outbox creation commit atomically.
* Known dependency-availability failures are distinguished from unexpected internal, schema, and invariant failures; low-level database and network details are not returned to clients or exposed by worker store failure logs.
* HTTP, database, publisher, and shutdown operations use explicit timeouts.
* API shutdown force-closes remaining HTTP connections after the graceful timeout, and reward-claim transaction rollback uses an independent bounded cleanup context.
* Worker publishing uses leases and ownership-fenced finalization; no database transaction or row lock is held while the publisher executes.
* Docker uses a `scratch` runtime containing only the statically built API and worker binaries plus the CA certificate bundle required for outbound TLS. The runtime executes as a non-root numeric user. The Dockerfile frontend and builder image are pinned to immutable digests, and the deployment image is built explicitly for `linux/arm64`. Direct local listener defaults bind to `127.0.0.1`, and local Compose publishes ports only on `127.0.0.1`.
* GitHub Actions use least-privilege permissions and `persist-credentials: false`; third-party actions are pinned to immutable commit SHAs. Repository workflows run formatting, vet, tests, race tests, migration/integration checks, Docker builds, CodeQL, `govulncheck`, and dependency review. Runtime resilience verifies the same prebuilt application image that CI built rather than rebuilding it inside the smoke test.
* Renovate owns dependency-update pull requests for direct Go modules, Go language/toolchain releases, versioned Go CLI tools in the Makefile, GitHub Actions, Dockerfiles, and Docker Compose; indirect Go modules are enabled only for vulnerability remediation. GitHub Dependabot Alerts remain the vulnerability signal Renovate consumes for remediation pull requests. Renovate proposes stable releases only and does not automerge; Docker images and GitHub Actions remain pinned to immutable digests or commit SHAs.

## Repository enforcement

`main` is protected by an active GitHub ruleset. Changes require a pull request, resolved review conversations, and an up-to-date branch. The `Checks`, `govulncheck`, and `Dependency Review` checks must pass before merge.

CodeQL results are also enforced: security alerts at `High or higher` and standard alerts at `Errors` block merge. Force pushes and deletion of `main` are blocked. GitHub secret scanning and push protection are enabled.

These controls are repository-level GitHub settings rather than workflow YAML and should be re-verified if the repository's hosting or ownership changes.

## Logging and sensitive data

Do not log or commit:

* credentials, tokens, private keys, or production connection strings;
* authorization headers;
* raw idempotency keys;
* full request bodies or outbox payloads;
* raw publisher errors or other low-level details that may expose sensitive configuration.

The application uses structured `log/slog` logging. Publisher failures are reduced to controlled classifications before persistence, API responses sanitize low-level database and network failures, and worker store failures are reduced to bounded availability/internal classifications before process-level logging. Unexpected reward-claim failures are logged with bounded error classifications and request correlation context rather than raw errors or claim identifiers.

Operational identifiers such as request IDs, event IDs, aggregate IDs, or worker IDs may appear in logs when useful for debugging and correlation. They are not used as Prometheus metric labels.

The local PostgreSQL credentials in `compose.yaml` and `.env.example` are development-only and must not be reused in shared or production environments.

## Health and metrics exposure

The API and worker expose `/livez`, `/readyz`, and `/metrics` on their respective listeners. Direct local runs bind them to localhost by default, and local Compose likewise publishes them only on host loopback.

A production adaptation should restrict health and metrics access through private networking, firewall or ingress rules, or an observability proxy as appropriate. The service does not implement a separate metrics-authentication mechanism.

Metrics use bounded labels and exclude high-cardinality identifiers such as player IDs, reward IDs, event IDs, worker IDs, request IDs, and idempotency keys. Raw paths and raw errors are not metric labels.

## Database, migrations, and retention

Production-like environments should use least-privilege database roles, transport encryption, managed secrets, backups, restore testing, and credential rotation appropriate to the deployment.

`ALLOW_DESTRUCTIVE_DB_RESET=1 make db-reset` removes the local Compose database volume and reapplies the current schema baseline. `ALLOW_DESTRUCTIVE_DB_CHECK=1 make db-check` rolls the schema down to version zero and back up. Both commands must only target disposable data. The opt-in flags prevent accidental invocation; they do not prove that a database is safe to destroy.

The service does not perform automatic idempotency or outbox cleanup. Routine retention must never remove `pending` or `processing` outbox events.

Migration rollback preconditions and safe retention procedures are documented in [`docs/runbook.md`](docs/runbook.md).

## Future integration security

A future external publisher integration must define its own authentication, transport security, credential management, timeout and retry policy, and payload/data-classification requirements.

Dependency automation is not a deployment identity. Renovate-authored pull-request workflows must not receive cloud publish or deploy credentials; future cloud OIDC trust must remain restricted to explicitly trusted post-merge contexts.
