ALTER TABLE reward_claims
    ADD COLUMN campaign_id text NOT NULL DEFAULT 'default';

ALTER TABLE reward_claims
    ALTER COLUMN campaign_id DROP DEFAULT;

ALTER TABLE reward_claims
    ADD CONSTRAINT reward_claims_campaign_id_length_chk CHECK (length(campaign_id) BETWEEN 1 AND 128);

ALTER TABLE reward_claims
    DROP CONSTRAINT reward_claims_player_reward_uniq;

ALTER TABLE reward_claims
    ADD CONSTRAINT reward_claims_player_campaign_reward_uniq UNIQUE (player_id, campaign_id, reward_id);

ALTER TABLE reward_claims
    DROP CONSTRAINT reward_claims_status_chk;

UPDATE reward_claims
SET status = 'claimed'
WHERE status = 'accepted';

ALTER TABLE reward_claims
    ADD CONSTRAINT reward_claims_status_chk CHECK (status IN ('claimed'));
