CREATE TABLE admin_sessions (
    token_hash bytea PRIMARY KEY,
    user_id text NOT NULL,
    display_name text NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL
);

CREATE INDEX admin_sessions_expiry_idx ON admin_sessions (expires_at);
