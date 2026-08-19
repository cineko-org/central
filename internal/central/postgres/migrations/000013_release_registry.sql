CREATE TABLE release_components (
    kind text NOT NULL CHECK (kind IN ('client', 'browser', 'playwright', 'launcher', 'probe')),
    channel text NOT NULL,
    platform text NOT NULL,
    architecture text NOT NULL,
    version text NOT NULL,
    schema_version smallint NOT NULL CHECK (schema_version IN (1, 2)),
    payload jsonb NOT NULL,
    published_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, channel, platform, architecture, version)
);

CREATE INDEX release_components_lookup_idx
    ON release_components (kind, channel, platform, architecture, published_at DESC);

CREATE TABLE desktop_release_registry_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    generation bigint NOT NULL CHECK (generation >= 0),
	active_manifest_sha256 text NOT NULL CHECK (active_manifest_sha256 ~ '^[0-9a-f]{64}$'),
	resolver_version smallint NOT NULL CHECK (resolver_version = 1),
    updated_at timestamptz NOT NULL
);

INSERT INTO desktop_release_registry_state (
	singleton, generation, active_manifest_sha256, resolver_version, updated_at
) VALUES (
	true, 0, '44fe5a7c9bf3482e0601bf1c149accc2fd51272807849be39897090770fbe99f', 1, now()
);

ALTER TABLE client_launch_tickets
    ADD COLUMN release_generation bigint NOT NULL DEFAULT 1 CHECK (release_generation > 0);

ALTER TABLE client_launch_tickets
    ALTER COLUMN release_generation DROP DEFAULT;
