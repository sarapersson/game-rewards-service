# Runbook

This runbook covers the operational procedures that are specific to this repository. It is not a substitute for deployment-specific backup, access-control, networking, or incident-response procedures.

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

When troubleshooting worker configuration, verify that the lease can outlive the publisher call and its final database operation:

```text
OUTBOX_LOCK_TTL > OUTBOX_PUBLISH_TIMEOUT + DB_QUERY_TIMEOUT
```

Inspect outbox state directly when you need database-global information:

```sql
SELECT status, count(*)
FROM outbox_events
GROUP BY status
ORDER BY status;

SELECT id, event_type, status, attempts, available_at, locked_by, locked_until,
       last_error, created_at, updated_at
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
       attempts,
       last_error,
       dead_lettered_at
FROM outbox_events
WHERE status = 'dead_letter'
ORDER BY dead_lettered_at DESC
LIMIT 100;
```

The Prometheus registries intentionally do not expose database-global queue depth or oldest-event gauges.

When investigating repeated publish failures:

1. check `game_rewards_outbox_publish_attempts_total` and retry/dead-letter counters;
2. inspect controlled `last_error` classifications and worker logs;
3. distinguish publisher failures from durable finalization failures; a publish may succeed even if the later ownership-fenced database update fails;
4. do not manually mark `processing` rows published or delete them to "unstick" the queue.

Expired processing leases are recoverable by normal worker claiming.

## Duplicate claim or idempotency incident

First distinguish the cases:

* `reward_already_claimed`: the business uniqueness constraint rejected the same player/campaign/reward;
* `idempotency_key_reused`: the same key was used for a different accepted request;
* `idempotency_key_in_progress`: a committed `processing` record is visible and should be investigated if it remains stale;
* `Idempotent-Replayed: true`: the response was loaded from a completed idempotency record.

Inspect the business identity:

```sql
SELECT id, player_id, campaign_id, reward_id, status, created_at
FROM reward_claims
WHERE player_id = 'player-123'
  AND campaign_id = 'winter-2026'
  AND reward_id = 'daily-login';
```

Raw idempotency keys are not stored. The lookup key is `(operation, key_hash)`, where `key_hash` is SHA-256 of the idempotency key after leading and trailing whitespace has been removed.

If an operator is explicitly given the original key, enter the normalized value without leading or trailing whitespace and compute the hash locally without placing the raw value in shell history:

```bash
IFS= read -r -s NORMALIZED_IDEMPOTENCY_KEY
printf '\n'
printf '%s' "$NORMALIZED_IDEMPOTENCY_KEY" | shasum -a 256
unset NORMALIZED_IDEMPOTENCY_KEY
```

Use the resulting hex digest to inspect the specific record:

```sql
SELECT operation,
       state,
       response_status,
       reward_claim_id,
       created_at,
       updated_at,
       completed_at,
       expires_at
FROM idempotency_keys
WHERE operation = 'POST /v1/reward-claims'
  AND key_hash = decode('<sha256-hex>', 'hex');
```

For a broader recent-state view without exposing raw keys:

```sql
SELECT operation, encode(key_hash, 'hex') AS key_hash,
       state, response_status, reward_claim_id,
       created_at, updated_at, completed_at, expires_at
FROM idempotency_keys
ORDER BY updated_at DESC
LIMIT 100;
```

Do not paste raw idempotency keys into logs, tickets, dashboards, or shared incident notes.

A stale `processing` idempotency row is not routine retention data. Investigate how it was created before changing or deleting it.

## Retention and cleanup

The service does not run automatic cleanup jobs.

### Completed idempotency records

Only completed records whose `expires_at` has passed are candidates for routine cleanup. Deleting one removes its stored deterministic replay history.

Inspect first:

```sql
SELECT count(*)
FROM idempotency_keys
WHERE state = 'completed'
  AND expires_at < now();
```

Delete in small batches:

```sql
WITH batch AS (
    SELECT operation, key_hash
    FROM idempotency_keys
    WHERE state = 'completed'
      AND expires_at < now()
    ORDER BY expires_at
    LIMIT 500
)
DELETE FROM idempotency_keys AS i
USING batch
WHERE i.operation = batch.operation
  AND i.key_hash = batch.key_hash;
```

Never include `processing` rows in routine cleanup.

### Published outbox events

Choose a retention cutoff appropriate to the deployment; v1 does not define a fixed retention period.

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

Latest-migration rollback check:

```bash
make db-check
```

Full migration-chain rollback check:

```bash
ALLOW_DESTRUCTIVE_DB_CHECK=1 make db-check-full
```

Both commands execute down migrations and must only target disposable local or CI databases. `make db-check` rolls back and reapplies the latest migration. The full check rolls the complete schema down to version zero and additionally requires explicit opt-in. The opt-in flag does not make a shared database safe to modify or destroy.

Historical migrations are immutable. If a rollback precondition is not satisfied, do not edit or bypass the old migration; resolve the data/operational condition first.

A schema rollback must be coordinated with application rollback. Stop or quiesce affected processes and ensure the API/worker version you intend to run is compatible with the target schema before applying a down migration.

### Rollback of migration `000002`

`000002_add_campaign_id_to_reward_claims.down.sql` restores the older uniqueness model `(player_id, reward_id)` and removes `campaign_id`.

Before rollback, stop/quiesce writers and check for conflicts:

```sql
SELECT player_id, reward_id, count(*)
FROM reward_claims
GROUP BY player_id, reward_id
HAVING count(*) > 1;
```

Any returned row blocks a safe rollback because multiple campaign-scoped claims would violate the older uniqueness constraint. Define an explicit data-resolution plan; do not bypass the migration guard.

The down migration also converts `status = 'claimed'` back to `accepted` before restoring the older status constraint.

### Rollback of migration `000003`

`000003_store_idempotency_response_body_as_bytes.down.sql` converts completed `response_body` values from `bytea` back to `jsonb`.

Before rollback:

1. stop/quiesce writers;
2. take the deployment-appropriate backup;
3. verify completed bodies are valid UTF-8 JSON objects;
4. perform the rollback in a controlled maintenance window.

A useful validation query is:

```sql
SELECT operation, encode(key_hash, 'hex') AS key_hash
FROM idempotency_keys
WHERE state = 'completed'
  AND jsonb_typeof(convert_from(response_body, 'UTF8')::jsonb) <> 'object';
```

The query must complete successfully and return no rows. Invalid UTF-8 or invalid JSON causes the query to fail; valid JSON values that are not objects are returned as rows. Either result blocks rollback until the data is resolved.

### Rollback of migration `000005`

Stop worker processes before rolling back `000005_add_outbox_worker_leases`.

Inspect state before and after:

```sql
SELECT status, count(*)
FROM outbox_events
GROUP BY status
ORDER BY status;
```

The down migration intentionally converts `processing` rows back to recoverable `pending` rows and removes lease/dead-letter timestamp columns. Confirm the application/worker versions are compatible with the target schema before restarting processes.
