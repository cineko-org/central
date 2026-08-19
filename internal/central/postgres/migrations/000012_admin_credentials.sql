CREATE TABLE admin_credentials (
    user_id text PRIMARY KEY,
    display_name text NOT NULL,
    password_hash text NOT NULL CHECK (char_length(password_hash) BETWEEN 64 AND 512),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
