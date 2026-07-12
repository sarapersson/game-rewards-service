ALTER TABLE outbox_events
    ADD COLUMN locked_by text,
    ADD COLUMN locked_until timestamptz,
    ADD COLUMN dead_lettered_at timestamptz;

UPDATE outbox_events
SET dead_lettered_at = updated_at
WHERE status = 'dead_letter'
  AND dead_lettered_at IS NULL;

ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_status_chk;

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_status_chk CHECK (status IN ('pending', 'processing', 'published', 'dead_letter'));

ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_published_status_chk;

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_published_status_chk CHECK (
        (status = 'published' AND published_at IS NOT NULL)
        OR
        (status <> 'published' AND published_at IS NULL)
    );

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_processing_lock_chk CHECK (
        (
            status = 'processing'
            AND locked_by IS NOT NULL
            AND locked_until IS NOT NULL
        )
        OR
        (
            status <> 'processing'
            AND locked_by IS NULL
            AND locked_until IS NULL
        )
    );

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_locked_by_length_chk CHECK (
        locked_by IS NULL OR length(locked_by) BETWEEN 1 AND 128
    );

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_dead_letter_status_chk CHECK (
        (
            status = 'dead_letter'
            AND dead_lettered_at IS NOT NULL
        )
        OR
        (
            status <> 'dead_letter'
            AND dead_lettered_at IS NULL
        )
    );

CREATE INDEX outbox_events_processing_expired_idx
    ON outbox_events (locked_until, id)
    WHERE status = 'processing';
