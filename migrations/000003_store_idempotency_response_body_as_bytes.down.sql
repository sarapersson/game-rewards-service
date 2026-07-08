ALTER TABLE idempotency_keys
    DROP CONSTRAINT IF EXISTS idempotency_keys_response_state_chk;

ALTER TABLE idempotency_keys
    ALTER COLUMN response_body TYPE jsonb
    USING CASE
        WHEN response_body IS NULL THEN NULL
        ELSE convert_from(response_body, 'UTF8')::jsonb
    END;

ALTER TABLE idempotency_keys
    ADD CONSTRAINT idempotency_keys_response_state_chk CHECK (
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
    );
