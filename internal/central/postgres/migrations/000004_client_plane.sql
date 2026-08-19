CREATE TABLE client_users (
    id text PRIMARY KEY,
    display_name text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE client_credentials (
    user_id text PRIMARY KEY REFERENCES client_users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE client_sessions (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES client_users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL
);

CREATE INDEX client_sessions_active_idx
    ON client_sessions (user_id, expires_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE client_devices (
    installation_id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES client_users(id) ON DELETE RESTRICT,
    device_id text NOT NULL,
    platform text NOT NULL,
    architecture text NOT NULL,
    app_version text NOT NULL,
    last_seen_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (user_id, device_id)
);

CREATE INDEX client_devices_owner_idx
    ON client_devices (user_id, last_seen_at DESC);

CREATE TABLE client_resources (
    user_id text NOT NULL REFERENCES client_users(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN (
        'settings', 'branches', 'auditoria', 'seat-maps', 'booking-catalogs',
        'presets', 'monitors', 'collections', 'collection-runs', 'schedule-snapshots',
        'opening-observations', 'reservations', 'external-operations', 'app-events'
    )),
    id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    PRIMARY KEY (user_id, kind, id)
);

CREATE INDEX client_resources_list_idx
    ON client_resources (user_id, kind, updated_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE TABLE client_commands (
    user_id text NOT NULL REFERENCES client_users(id) ON DELETE RESTRICT,
    command_id text NOT NULL,
    operation text NOT NULL CHECK (operation IN ('put', 'delete')),
    resource_kind text NOT NULL,
    resource_id text NOT NULL,
    result_revision bigint NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, command_id)
);

CREATE TABLE client_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id text NOT NULL UNIQUE,
    user_id text NOT NULL REFERENCES client_users(id) ON DELETE RESTRICT,
    event_type text NOT NULL,
    resource_kind text NOT NULL,
    resource_id text NOT NULL,
    resource_revision bigint NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL
);

CREATE INDEX client_events_stream_idx
    ON client_events (user_id, sequence);

CREATE INDEX client_events_retention_idx
    ON client_events (occurred_at);
