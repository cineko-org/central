ALTER TABLE probe_runtimes
    ADD COLUMN IF NOT EXISTS owner_user_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS device_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS probe_runtimes_owner_idx
    ON probe_runtimes (owner_user_id, status, last_heartbeat_at DESC)
    WHERE owner_user_id <> '';

CREATE TABLE IF NOT EXISTS consumed_probe_bootstrap_tickets (
    ticket_id text PRIMARY KEY,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS consumed_probe_bootstrap_expiry_idx
    ON consumed_probe_bootstrap_tickets (expires_at);
