ALTER TABLE idempotency_keys
    DROP CONSTRAINT IF EXISTS idempotency_keys_response_state_chk;

ALTER TABLE idempotency_keys
    ALTER COLUMN response_body TYPE bytea
    USING CASE
        WHEN response_body IS NULL THEN NULL
        ELSE convert_to(response_body::text, 'UTF8')
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
            AND octet_length(response_body) > 0
            AND completed_at IS NOT NULL
        )
    );
