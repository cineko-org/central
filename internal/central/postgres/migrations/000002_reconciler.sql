CREATE TABLE observation_policies (
    id text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT true,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    task_kind text NOT NULL,
    branch_id text NOT NULL,
    branch_region text NOT NULL,
    branch_name text NOT NULL,
    target_date_mode text NOT NULL CHECK (target_date_mode IN ('explicit', 'rolling')),
    target_dates date[] NOT NULL DEFAULT '{}',
    horizon_days integer,
    locale text NOT NULL,
    time_zone text NOT NULL,
    egress_policy_id text NOT NULL,
    priority smallint NOT NULL DEFAULT 50 CHECK (priority BETWEEN 0 AND 100),
    min_interval_seconds integer NOT NULL CHECK (min_interval_seconds > 0),
    max_interval_seconds integer NOT NULL CHECK (max_interval_seconds >= min_interval_seconds),
    execution_window_seconds integer NOT NULL CHECK (execution_window_seconds > 0),
    next_run_at timestamptz,
    last_finished_at timestamptz,
    last_outcome text CHECK (last_outcome IN ('completed', 'partial', 'failed', 'missed')),
    last_error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    CHECK (
        (target_date_mode = 'explicit' AND cardinality(target_dates) > 0 AND horizon_days IS NULL)
        OR
        (target_date_mode = 'rolling' AND cardinality(target_dates) = 0 AND horizon_days BETWEEN 1 AND 90)
    )
);

CREATE UNIQUE INDEX observation_policies_active_target_idx
    ON observation_policies (task_kind, branch_id)
    WHERE deleted_at IS NULL;
CREATE INDEX observation_policies_due_idx
    ON observation_policies (next_run_at, priority DESC)
    WHERE enabled AND deleted_at IS NULL;

ALTER TABLE observation_assignments
    ADD COLUMN policy_id text REFERENCES observation_policies(id) ON DELETE RESTRICT,
    ADD COLUMN priority smallint NOT NULL DEFAULT 50 CHECK (priority BETWEEN 0 AND 100),
    ADD COLUMN terminal_reason text NOT NULL DEFAULT '',
    ADD COLUMN completed_by_probe_id text;

CREATE UNIQUE INDEX observation_assignments_active_policy_idx
    ON observation_assignments (policy_id)
    WHERE policy_id IS NOT NULL AND status IN ('queued', 'leased');

DROP INDEX observation_assignments_claim_idx;
CREATE INDEX observation_assignments_claim_idx
    ON observation_assignments (status, priority DESC, not_before, deadline, created_at);

ALTER TABLE assignment_attempts
    DROP CONSTRAINT assignment_attempts_probe_id_fkey;
CREATE UNIQUE INDEX assignment_attempts_probe_once_idx
    ON assignment_attempts (assignment_id, probe_id);

CREATE TABLE assignment_eligible_probes (
    assignment_id text NOT NULL REFERENCES observation_assignments(id) ON DELETE RESTRICT,
    probe_id text NOT NULL,
    network_id text NOT NULL,
    eligible_at timestamptz NOT NULL,
    PRIMARY KEY (assignment_id, probe_id)
);

CREATE INDEX assignment_eligible_probes_probe_idx
    ON assignment_eligible_probes (probe_id, assignment_id);
