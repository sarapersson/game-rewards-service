DROP INDEX IF EXISTS outbox_events_pending_idx;
DROP INDEX IF EXISTS outbox_events_aggregate_idx;
DROP TABLE IF EXISTS outbox_events;
DROP INDEX IF EXISTS idempotency_keys_expires_at_idx;
DROP INDEX IF EXISTS idempotency_keys_reward_claim_id_idx;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS reward_claims;
