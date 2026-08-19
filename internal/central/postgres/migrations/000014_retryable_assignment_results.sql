ALTER TABLE observation_assignments
    DROP CONSTRAINT observation_assignments_status_check;
ALTER TABLE observation_assignments
    ADD CONSTRAINT observation_assignments_status_check
    CHECK (status IN ('queued', 'leased', 'retry_pending', 'completed', 'partial', 'failed', 'missed'));

ALTER TABLE assignment_attempts
    ADD COLUMN lease_token_hash bytea,
    ADD COLUMN network_id text,
    ADD COLUMN run_id text,
    ADD COLUMN result_hash text,
    ADD COLUMN result_payload jsonb,
    ADD CONSTRAINT assignment_attempts_result_identity_check
    CHECK ((run_id IS NULL) = (result_hash IS NULL));

DROP INDEX observation_assignments_active_policy_idx;
CREATE UNIQUE INDEX observation_assignments_active_policy_idx
    ON observation_assignments (policy_id)
    WHERE policy_id IS NOT NULL AND status IN ('queued', 'leased', 'retry_pending');
