ALTER TABLE showtime_observations
    ADD COLUMN auditorium_id text NOT NULL DEFAULT '';

CREATE TABLE client_execution_commands (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES client_users(id) ON DELETE CASCADE,
    monitor_id text NOT NULL,
    source_id text NOT NULL,
    starts_at timestamptz NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'leased', 'completed', 'failed')),
    leased_installation_id text REFERENCES client_devices(installation_id) ON DELETE SET NULL,
    last_installation_id text,
    lease_token_hash bytea,
    lease_expires_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    reason_code text NOT NULL DEFAULT '',
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (user_id, monitor_id, source_id, starts_at),
    CHECK ((status = 'leased') = (
        leased_installation_id IS NOT NULL AND lease_token_hash IS NOT NULL AND lease_expires_at IS NOT NULL
    ))
);

CREATE INDEX client_execution_commands_claim_idx
    ON client_execution_commands (user_id, status, created_at)
    WHERE status IN ('queued', 'leased');
