CREATE TABLE reward_claims (
    id uuid PRIMARY KEY,
    player_id text NOT NULL,
    reward_id text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reward_claims_player_id_length_chk CHECK (length(player_id) BETWEEN 1 AND 128),
    CONSTRAINT reward_claims_reward_id_length_chk CHECK (length(reward_id) BETWEEN 1 AND 128),
    CONSTRAINT reward_claims_status_chk CHECK (status IN ('accepted')),
    CONSTRAINT reward_claims_updated_at_chk CHECK (updated_at >= created_at),
    CONSTRAINT reward_claims_player_reward_uniq UNIQUE (player_id, reward_id)
);

CREATE TABLE idempotency_keys (
    operation text NOT NULL,
    key_hash bytea NOT NULL,
    request_hash bytea NOT NULL,
    state text NOT NULL,
    response_status integer,
    response_body jsonb,
    reward_claim_id uuid REFERENCES reward_claims (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    PRIMARY KEY (operation, key_hash),
    CONSTRAINT idempotency_keys_operation_length_chk CHECK (length(operation) BETWEEN 1 AND 64),
    CONSTRAINT idempotency_keys_key_hash_length_chk CHECK (octet_length(key_hash) = 32),
    CONSTRAINT idempotency_keys_request_hash_length_chk CHECK (octet_length(request_hash) = 32),
    CONSTRAINT idempotency_keys_state_chk CHECK (state IN ('processing', 'completed')),
    CONSTRAINT idempotency_keys_updated_at_chk CHECK (updated_at >= created_at),
    CONSTRAINT idempotency_keys_expires_at_chk CHECK (expires_at > created_at),
    CONSTRAINT idempotency_keys_response_state_chk CHECK (
        (
            state = 'processing'
            AND response_status IS NULL
            AND response_body IS NULL
            AND completed_at IS NULL
        )
        OR
        (
            state = 'completed'
            AND response_status IS NOT NULL
            AND response_status BETWEEN 100 AND 599
            AND response_body IS NOT NULL
            AND jsonb_typeof(response_body) = 'object'
            AND completed_at IS NOT NULL
        )
    )
);

CREATE INDEX idempotency_keys_reward_claim_id_idx
    ON idempotency_keys (reward_claim_id)
    WHERE reward_claim_id IS NOT NULL;

CREATE INDEX idempotency_keys_expires_at_idx
    ON idempotency_keys (expires_at);

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
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    CONSTRAINT outbox_events_aggregate_type_length_chk CHECK (length(aggregate_type) BETWEEN 1 AND 64),
    CONSTRAINT outbox_events_event_type_length_chk CHECK (length(event_type) BETWEEN 1 AND 128),
    CONSTRAINT outbox_events_payload_object_chk CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT outbox_events_status_chk CHECK (status IN ('pending', 'published', 'dead_letter')),
    CONSTRAINT outbox_events_attempts_chk CHECK (attempts >= 0),
    CONSTRAINT outbox_events_last_error_length_chk CHECK (last_error IS NULL OR length(last_error) <= 2000),
    CONSTRAINT outbox_events_updated_at_chk CHECK (updated_at >= created_at),
    CONSTRAINT outbox_events_published_status_chk CHECK (
        (status = 'published' AND published_at IS NOT NULL)
        OR
        (status IN ('pending', 'dead_letter') AND published_at IS NULL)
    )
);

CREATE INDEX outbox_events_aggregate_idx
    ON outbox_events (aggregate_type, aggregate_id);

CREATE INDEX outbox_events_pending_idx
    ON outbox_events (available_at, id)
    WHERE status = 'pending';
