ALTER TABLE client_sessions
    ADD COLUMN refresh_token_hash bytea,
    ADD COLUMN refresh_expires_at timestamptz;

DELETE FROM client_sessions;

ALTER TABLE client_sessions
    ALTER COLUMN refresh_token_hash SET NOT NULL,
    ALTER COLUMN refresh_expires_at SET NOT NULL,
    ADD CONSTRAINT client_sessions_refresh_token_hash_key UNIQUE (refresh_token_hash);
