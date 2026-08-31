# Runbook

This runbook covers operational procedures specific to this repository. Backup, access-control, networking, and incident-response procedures remain deployment-specific.

For local inspection, open PostgreSQL with:

```bash
docker compose exec postgres psql -U game_rewards -d game_rewards
```

Never run destructive migration or cleanup commands against a shared or production-like database without an explicit maintenance plan and verified backup/recovery path.

## API or PostgreSQL not ready

Check the API first:

```bash
curl -i http://localhost:8080/livez
curl -i http://localhost:8080/readyz
```

Interpretation:

* `/livez` failing: the process/listener is unhealthy or unavailable;
* `/livez` healthy and `/readyz` returning `503`: PostgreSQL readiness failed;
* reward-claim `503 service_unavailable`: a known dependency-availability failure reached the request path;
* reward-claim `500 internal_error`: do not assume a temporary database outage; inspect logs for an internal/schema/invariant failure.

Useful local checks:

```bash
docker compose ps
docker compose exec postgres pg_isready -U game_rewards -d game_rewards
make db-logs
make stack-logs
```

After recovery, verify `/readyz` returns `200` and make a known-safe test request if appropriate.

## Worker not ready or outbox processing stalled

Check the worker admin endpoints:

```bash
curl -i http://localhost:8081/livez
curl -i http://localhost:8081/readyz
curl -s http://localhost:8081/metrics
```

Worker readiness requires both PostgreSQL and an active worker loop.

Worker failure handling is intentionally split by semantics:

* recognized PostgreSQL availability failures are retried after the poll interval;
* publisher failures are persisted as controlled retry/dead-letter outcomes;
* lease loss is an expected fencing outcome, recorded in `game_rewards_outbox_lease_losses_total` rather than `operation_errors_total`, and the event is left for normal lease recovery/reclaim;
* schema, permission, invariant, programming, and other unexpected store failures terminate the worker component and make the process exit non-zero instead of polling forever.

If the worker process exits while PostgreSQL itself is reachable, inspect the bounded `operation`, `error_class`, and `action` fields in `outbox_worker_iteration_failed` together with deployment/schema state rather than assuming a transient database outage. Raw PostgreSQL or network errors are intentionally omitted from that iteration-failure log; startup failures may still log their underlying error for diagnosis.

When troubleshooting worker configuration, verify that the lease can outlive the claim database operation, publisher call, and final database operation:

```text
OUTBOX_LOCK_TTL > OUTBOX_PUBLISH_TIMEOUT + 2 * DB_QUERY_TIMEOUT
```

This is the minimum normal-path I/O budget, not a guarantee against scheduler stalls or other pauses; lease loss remains an expected fencing outcome.

Inspect outbox state directly when you need database-global information:

```sql
SELECT status, count(*)
FROM outbox_events
GROUP BY status
ORDER BY status;

SELECT id, event_type, status, failed_attempts, available_at, locked_by, locked_until,
       last_failure_reason, created_at, updated_at
FROM outbox_events
WHERE status IN ('pending', 'processing', 'dead_letter')
ORDER BY created_at
LIMIT 100;
```

For more targeted investigation:

```sql
SELECT min(available_at) AS oldest_pending_available_at
FROM outbox_events
WHERE status = 'pending'
  AND available_at <= now();

SELECT count(*) AS expired_processing_leases
FROM outbox_events
WHERE status = 'processing'
  AND locked_until <= now();

SELECT id,
       aggregate_type,
       aggregate_id,
       event_type,
       failed_attempts,
       last_failure_reason,
       dead_lettered_at
FROM outbox_events
WHERE status = 'dead_letter'
ORDER BY dead_lettered_at DESC
LIMIT 100;
```

The Prometheus registries intentionally do not expose database-global queue depth or oldest-event gauges.

When investigating repeated publish failures:

1. check `game_rewards_outbox_publish_attempts_total` and retry/dead-letter counters;
2. inspect controlled `last_failure_reason` classifications (`failed`, `timeout`, or `canceled`) and worker logs;
3. distinguish publisher failures from durable finalization failures; a publish may succeed even if the later ownership-fenced database update fails;
4. do not manually mark `processing` rows published or delete them to "unstick" the queue.

Expired processing leases are recoverable by normal worker claiming.

## Duplicate claim or idempotency incident

First distinguish the cases:

* `reward_already_claimed`: the business uniqueness constraint rejected the same player/campaign/reward;
* `idempotency_key_reused`: the same key was used for a different accepted request;
* `Idempotent-Replayed: true`: the response was loaded from a retained idempotency record with a stored response;
* encountering a committed idempotency row without a stored response yields `500 internal_error` and indicates an invariant failure.

Inspect the business identity:

```sql
SELECT id, player_id, campaign_id, reward_id, created_at
FROM reward_claims
WHERE player_id = 'player-123'
  AND campaign_id = 'winter-2026'
  AND reward_id = 'daily-login';
```

Raw idempotency keys are not stored. The lookup key is the SHA-256 `key_hash` of the idempotency key after leading and trailing whitespace has been removed.

If an operator is explicitly given the original key, enter the normalized value without leading or trailing whitespace and compute the hash locally without placing the raw value in shell history:

```bash
IFS= read -r -s NORMALIZED_IDEMPOTENCY_KEY
printf '\n'
printf '%s' "$NORMALIZED_IDEMPOTENCY_KEY" | shasum -a 256
unset NORMALIZED_IDEMPOTENCY_KEY
```

Use the resulting hex digest to inspect the specific record:

```sql
SELECT player_id, campaign_id, reward_id,
       response_status,
       reward_claim_id,
       created_at
FROM reward_claim_idempotency_keys
WHERE key_hash = decode('<sha256-hex>', 'hex');
```

For a broader recent-state view without exposing raw keys:

```sql
SELECT encode(key_hash, 'hex') AS key_hash,
       player_id, campaign_id, reward_id,
       response_status, reward_claim_id,
       created_at
FROM reward_claim_idempotency_keys
ORDER BY created_at DESC
LIMIT 100;
```

Do not paste raw idempotency keys into logs, tickets, dashboards, or shared incident notes.

A committed idempotency row with a null response is an invariant violation: successful reward-claim transactions establish the stored response before commit. Investigate how the row was created before changing or deleting it, and do not treat it as a normal retry or routine retention case.

## Retention and cleanup

The service does not run automatic cleanup jobs.

### Idempotency records with stored responses

Only records with a stored response older than 24 hours are candidates for routine cleanup. The cutoff is derived from `created_at`; there is no separate persisted expiry timestamp.

Request handling does not apply an expiry check, so a retained record continues to replay after the 24-hour cleanup threshold until it is deleted. Deleting one removes its stored deterministic replay history. Cleanup may overlap request handling: if a request has already observed the retained key but the completed row is deleted before replay reads it, the request retries the reservation and is evaluated against current business state rather than returning an internal error. A later request is likewise evaluated against current business state once the retained history is gone.

Inspect first:

```sql
SELECT count(*)
FROM reward_claim_idempotency_keys
WHERE response_status IS NOT NULL
  AND created_at < now() - interval '24 hours';
```

Delete in small batches:

```sql
WITH batch AS (
    SELECT key_hash
    FROM reward_claim_idempotency_keys
    WHERE response_status IS NOT NULL
      AND created_at < now() - interval '24 hours'
    ORDER BY created_at
    LIMIT 500
)
DELETE FROM reward_claim_idempotency_keys AS i
USING batch
WHERE i.key_hash = batch.key_hash;
```

Never include incomplete idempotency rows in routine cleanup.

### Published outbox events

Choose a retention cutoff appropriate to the deployment; the repository does not define a fixed retention period.

After replacing the example cutoff, inspect before deleting:

```sql
SELECT count(*)
FROM outbox_events
WHERE status = 'published'
  AND published_at < TIMESTAMPTZ 'YYYY-MM-DDTHH:MM:SSZ';
```

Delete in bounded batches:

```sql
WITH batch AS (
    SELECT id
    FROM outbox_events
    WHERE status = 'published'
      AND published_at < TIMESTAMPTZ 'YYYY-MM-DDTHH:MM:SSZ'
    ORDER BY published_at, id
    LIMIT 500
)
DELETE FROM outbox_events AS o
USING batch
WHERE o.id = batch.id;
```

`pending` and `processing` events must never be removed by routine retention.

Dead-lettered events should normally be retained until they have been investigated and any required recovery/reconciliation is complete. There is no automatic dead-letter replay tooling in this repository.

Before automating high-volume cleanup, review query cost and indexes for the chosen retention predicates rather than turning these manual procedures directly into a scheduler.

## Migration verification

The repository currently uses a single development schema baseline. If a disposable local Compose database has migration history that no longer matches the current baseline, rebuild it rather than forcing or hand-editing the migration version:

```bash
ALLOW_DESTRUCTIVE_DB_RESET=1 make db-reset
```

This stops the Compose stack, removes its database volume, starts a fresh PostgreSQL instance, and applies the current schema. It destroys all data in that local Compose database.

Verify the baseline's complete round trip only against a disposable database:

```bash
ALLOW_DESTRUCTIVE_DB_CHECK=1 make db-check
```

The check migrates up, down to version zero, and back up. The explicit opt-in does not make a shared or production-like database safe to modify or destroy.

Once a migration has been distributed or applied outside disposable development environments, treat it as immutable. Future schema changes should add migrations rather than rewriting deployed history.

After the baseline is deployed or shared persistent data exists, schema rollback must be handled as an explicit migration/application rollback decision rather than by rewriting the baseline. Stop or quiesce affected processes and ensure the application version is compatible with the target schema.
