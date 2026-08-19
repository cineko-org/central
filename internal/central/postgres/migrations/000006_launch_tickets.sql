CREATE TABLE client_launch_tickets (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES client_users(id) ON DELETE CASCADE,
    installation_id text NOT NULL REFERENCES client_devices(installation_id) ON DELETE CASCADE,
    device_id text NOT NULL,
    client_version text NOT NULL,
    artifact_sha256 text NOT NULL,
    protocol integer NOT NULL,
    browser_revision text NOT NULL,
    launcher_nonce text NOT NULL,
    client_nonce text,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL,
    UNIQUE (user_id, launcher_nonce)
);

CREATE INDEX client_launch_tickets_retention_idx
    ON client_launch_tickets (expires_at);
