CREATE TABLE client_pins (
    user_id text PRIMARY KEY REFERENCES client_users(id) ON DELETE CASCADE,
    pin_digest bytea NOT NULL UNIQUE,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE client_pin_attempts (
    scope_hash bytea PRIMARY KEY,
    failure_count integer NOT NULL CHECK (failure_count >= 0),
    blocked_until timestamptz,
    updated_at timestamptz NOT NULL
);

CREATE INDEX client_pin_attempts_retention_idx ON client_pin_attempts (updated_at);
