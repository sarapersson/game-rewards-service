DROP INDEX IF EXISTS outbox_events_processing_expired_idx;

ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_dead_letter_status_chk;

ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_locked_by_length_chk;

ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_processing_lock_chk;

ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_published_status_chk;

UPDATE outbox_events
SET status = 'pending',
    available_at = now(),
    locked_by = NULL,
    locked_until = NULL,
    updated_at = now()
WHERE status = 'processing';

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_published_status_chk CHECK (
        (status = 'published' AND published_at IS NOT NULL)
        OR
        (status IN ('pending', 'dead_letter') AND published_at IS NULL)
    );

ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_status_chk;

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_status_chk CHECK (status IN ('pending', 'published', 'dead_letter'));

ALTER TABLE outbox_events
    DROP COLUMN dead_lettered_at,
    DROP COLUMN locked_until,
    DROP COLUMN locked_by;
