ALTER TABLE observation_policies
    RENAME COLUMN branch_id TO theater_id;
ALTER TABLE observation_policies
    RENAME COLUMN branch_region TO theater_region;
ALTER TABLE observation_policies
    RENAME COLUMN branch_name TO theater_name;
ALTER TABLE observation_policies
    ADD COLUMN theater_provider_id text NOT NULL DEFAULT 'cgv',
    ADD COLUMN theater_source_key text NOT NULL DEFAULT '';

ALTER TABLE observation_assignments
    RENAME COLUMN branch_id TO theater_id;
ALTER TABLE observation_assignments
    RENAME COLUMN branch_region TO theater_region;
ALTER TABLE observation_assignments
    RENAME COLUMN branch_name TO theater_name;
ALTER TABLE observation_assignments
    ADD COLUMN theater_provider_id text NOT NULL DEFAULT 'cgv',
    ADD COLUMN theater_source_key text NOT NULL DEFAULT '';

ALTER TABLE showtime_observations
    RENAME COLUMN branch_id TO theater_id;
ALTER TABLE showtime_observations
    RENAME COLUMN source_id TO source_key;

ALTER TABLE client_execution_commands
    RENAME COLUMN source_id TO showtime_id;

ALTER INDEX observation_policies_active_target_idx
    RENAME TO observation_policies_active_theater_idx;
ALTER INDEX observation_assignments_active_target_idx
    RENAME TO observation_assignments_active_theater_idx;
ALTER INDEX showtime_observations_analysis_idx
    RENAME TO showtime_observations_theater_analysis_idx;
ALTER INDEX showtime_observations_start_idx
    RENAME TO showtime_observations_theater_start_idx;

DELETE FROM client_events
WHERE resource_kind IN ('branches', 'auditoria', 'seat-maps', 'booking-catalogs');
DELETE FROM client_commands
WHERE resource_kind IN ('branches', 'auditoria', 'seat-maps', 'booking-catalogs');
DELETE FROM client_resources
WHERE kind IN ('branches', 'auditoria', 'seat-maps', 'booking-catalogs');

DROP TABLE client_blob_references;
DROP TABLE client_blobs;

UPDATE client_resources
SET payload = (payload - 'branchId') || jsonb_build_object('theaterId', payload->'branchId')
WHERE kind = 'presets' AND payload ? 'branchId';

ALTER TABLE client_resources DROP CONSTRAINT client_resources_kind_check;
ALTER TABLE client_resources ADD CONSTRAINT client_resources_kind_check CHECK (kind IN (
    'settings', 'presets', 'monitors', 'reservations',
    'external-operations', 'app-events'
));

CREATE TABLE catalog_state (
    id smallint PRIMARY KEY CHECK (id = 1),
    generation bigint NOT NULL CHECK (generation > 0),
    updated_at timestamptz NOT NULL
);

INSERT INTO catalog_state (id, generation, updated_at)
VALUES (1, 1, now());

CREATE TABLE providers (
    id text PRIMARY KEY,
    name text NOT NULL,
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE theaters (
    id text PRIMARY KEY,
    provider_id text NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
    source_key text NOT NULL,
    region text NOT NULL,
    name text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (provider_id, source_key)
);

CREATE INDEX theaters_browse_idx
    ON theaters (provider_id, active DESC, region, name, id);

CREATE TABLE movies (
    id text PRIMARY KEY,
    provider_id text NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
    source_key text NOT NULL,
    title text NOT NULL,
    poster_url text NOT NULL DEFAULT '',
    active boolean NOT NULL DEFAULT true,
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (provider_id, source_key)
);

CREATE INDEX movies_browse_idx
    ON movies (provider_id, active DESC, title, id);

CREATE TABLE auditoriums (
    id text PRIMARY KEY,
    theater_id text NOT NULL REFERENCES theaters(id) ON DELETE RESTRICT,
    source_key text NOT NULL,
    name text NOT NULL,
    screen_types text[] NOT NULL DEFAULT '{}',
    capacity integer NOT NULL DEFAULT 0 CHECK (capacity >= 0),
    active boolean NOT NULL DEFAULT true,
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (theater_id, source_key)
);

CREATE INDEX auditoriums_browse_idx
    ON auditoriums (theater_id, active DESC, name, id);

CREATE TABLE showtimes (
    id text PRIMARY KEY,
    provider_id text NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
    source_key text NOT NULL,
    theater_id text NOT NULL REFERENCES theaters(id) ON DELETE RESTRICT,
    movie_id text NOT NULL REFERENCES movies(id) ON DELETE RESTRICT,
    auditorium_id text NOT NULL REFERENCES auditoriums(id) ON DELETE RESTRICT,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    active boolean NOT NULL DEFAULT true,
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (provider_id, source_key, starts_at),
    CHECK (ends_at > starts_at)
);

CREATE INDEX showtimes_browse_idx
    ON showtimes (theater_id, starts_at, movie_id, auditorium_id);

CREATE TABLE seat_map_versions (
    id text PRIMARY KEY,
    auditorium_id text NOT NULL REFERENCES auditoriums(id) ON DELETE RESTRICT,
    layout_hash text NOT NULL CHECK (layout_hash ~ '^[0-9a-f]{64}$'),
    capacity integer NOT NULL CHECK (capacity > 0),
    layout jsonb NOT NULL,
    observed_at timestamptz NOT NULL,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    UNIQUE (auditorium_id, layout_hash)
);

ALTER TABLE auditoriums
    ADD COLUMN current_seat_map_version_id text
        REFERENCES seat_map_versions(id) ON DELETE RESTRICT;
