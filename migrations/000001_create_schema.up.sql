CREATE TABLE reward_claims (
    id uuid PRIMARY KEY,
    player_id text NOT NULL,
    campaign_id text NOT NULL,
    reward_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reward_claims_player_id_length_chk CHECK (length(player_id) BETWEEN 1 AND 128),
    CONSTRAINT reward_claims_campaign_id_length_chk CHECK (length(campaign_id) BETWEEN 1 AND 128),
    CONSTRAINT reward_claims_reward_id_length_chk CHECK (length(reward_id) BETWEEN 1 AND 128),
    CONSTRAINT reward_claims_player_campaign_reward_uniq UNIQUE (player_id, campaign_id, reward_id)
);

CREATE TABLE reward_claim_idempotency_keys (
    key_hash bytea PRIMARY KEY,
    player_id text NOT NULL,
    campaign_id text NOT NULL,
    reward_id text NOT NULL,
    response_status integer,
    response_body bytea,
    reward_claim_id uuid REFERENCES reward_claims (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reward_claim_idempotency_keys_key_hash_length_chk CHECK (octet_length(key_hash) = 32),
    CONSTRAINT reward_claim_idempotency_keys_player_id_length_chk CHECK (length(player_id) BETWEEN 1 AND 128),
    CONSTRAINT reward_claim_idempotency_keys_campaign_id_length_chk CHECK (length(campaign_id) BETWEEN 1 AND 128),
    CONSTRAINT reward_claim_idempotency_keys_reward_id_length_chk CHECK (length(reward_id) BETWEEN 1 AND 128),
    CONSTRAINT reward_claim_idempotency_keys_response_shape_chk CHECK (
        CASE
            WHEN response_status IS NULL THEN
                response_body IS NULL
                AND reward_claim_id IS NULL
            WHEN response_status = 201 THEN
                response_body IS NOT NULL
                AND octet_length(response_body) > 0
                AND reward_claim_id IS NOT NULL
            WHEN response_status = 409 THEN
                response_body IS NOT NULL
                AND octet_length(response_body) > 0
                AND reward_claim_id IS NULL
            ELSE
                FALSE
        END
    )
);

CREATE INDEX reward_claim_idempotency_keys_stored_response_created_at_idx
    ON reward_claim_idempotency_keys (created_at)
    WHERE response_status IS NOT NULL;

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    locked_by text,
    locked_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    dead_lettered_at timestamptz,
    CONSTRAINT outbox_events_aggregate_type_length_chk CHECK (length(aggregate_type) BETWEEN 1 AND 64),
    CONSTRAINT outbox_events_event_type_length_chk CHECK (length(event_type) BETWEEN 1 AND 128),
    CONSTRAINT outbox_events_payload_object_chk CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT outbox_events_status_chk CHECK (status IN ('pending', 'processing', 'published', 'dead_letter')),
    CONSTRAINT outbox_events_attempts_chk CHECK (attempts >= 0),
    CONSTRAINT outbox_events_last_error_length_chk CHECK (last_error IS NULL OR length(last_error) <= 2000),
    CONSTRAINT outbox_events_locked_by_length_chk CHECK (locked_by IS NULL OR length(locked_by) BETWEEN 1 AND 128),
    CONSTRAINT outbox_events_updated_at_chk CHECK (updated_at >= created_at),
    CONSTRAINT outbox_events_published_status_chk CHECK (
        (status = 'published' AND published_at IS NOT NULL)
        OR
        (status <> 'published' AND published_at IS NULL)
    ),
    CONSTRAINT outbox_events_processing_lock_chk CHECK (
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
    ),
    CONSTRAINT outbox_events_dead_letter_status_chk CHECK (
        (
            status = 'dead_letter'
            AND dead_lettered_at IS NOT NULL
        )
        OR
        (
            status <> 'dead_letter'
            AND dead_lettered_at IS NULL
        )
    ),
    CONSTRAINT outbox_events_aggregate_event_type_uniq UNIQUE (aggregate_type, aggregate_id, event_type)
);

CREATE INDEX outbox_events_pending_idx
    ON outbox_events (available_at, id)
    WHERE status = 'pending';

CREATE INDEX outbox_events_processing_expired_idx
    ON outbox_events (locked_until, id)
    WHERE status = 'processing';
