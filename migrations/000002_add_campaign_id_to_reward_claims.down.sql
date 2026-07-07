DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM reward_claims
        GROUP BY player_id, reward_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot roll back campaign-scoped reward claims: duplicate player_id/reward_id rows exist across campaigns';
    END IF;
END $$;

ALTER TABLE reward_claims
    DROP CONSTRAINT reward_claims_status_chk;

UPDATE reward_claims
SET status = 'accepted'
WHERE status = 'claimed';

ALTER TABLE reward_claims
    ADD CONSTRAINT reward_claims_status_chk CHECK (status IN ('accepted'));

ALTER TABLE reward_claims
    DROP CONSTRAINT reward_claims_player_campaign_reward_uniq;

ALTER TABLE reward_claims
    ADD CONSTRAINT reward_claims_player_reward_uniq UNIQUE (player_id, reward_id);

ALTER TABLE reward_claims
    DROP CONSTRAINT reward_claims_campaign_id_length_chk;

ALTER TABLE reward_claims
    DROP COLUMN campaign_id;
