CREATE TABLE IF NOT EXISTS cineko_schema_migrations (
    version bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS probe_runtimes (
    id text PRIMARY KEY,
    installation_id text NOT NULL UNIQUE,
    kind text NOT NULL CHECK (kind IN ('container', 'client')),
    network_id text NOT NULL,
    network_hint text NOT NULL DEFAULT '',
    capabilities text[] NOT NULL,
    max_concurrency integer NOT NULL CHECK (max_concurrency BETWEEN 1 AND 32),
    runtime_version text NOT NULL,
    protocol integer NOT NULL CHECK (protocol > 0),
    browser_revision text NOT NULL,
    platform text NOT NULL,
    architecture text NOT NULL,
    token_hash bytea NOT NULL,
    token_expires_at timestamptz NOT NULL,
    status text NOT NULL CHECK (status IN ('online', 'offline')),
    draining boolean NOT NULL DEFAULT false,
    available_slots integer NOT NULL DEFAULT 0 CHECK (available_slots >= 0),
    health text NOT NULL DEFAULT 'healthy' CHECK (health IN ('healthy', 'degraded')),
    reason_code text NOT NULL DEFAULT '',
    last_heartbeat_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS probe_runtimes_health_idx
    ON probe_runtimes (status, last_heartbeat_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS probe_runtimes_token_idx
    ON probe_runtimes (token_hash);
CREATE INDEX IF NOT EXISTS probe_runtimes_network_idx
    ON probe_runtimes (network_id, status, available_slots);

CREATE TABLE IF NOT EXISTS observation_assignments (
    id text PRIMARY KEY,
    task_kind text NOT NULL,
    branch_id text NOT NULL,
    branch_region text NOT NULL,
    branch_name text NOT NULL,
    target_dates date[] NOT NULL,
    locale text NOT NULL,
    time_zone text NOT NULL,
    egress_policy_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'leased', 'completed', 'partial', 'failed', 'missed')),
    not_before timestamptz NOT NULL,
    deadline timestamptz NOT NULL,
    probe_id text REFERENCES probe_runtimes(id) ON DELETE SET NULL,
    lease_token_hash bytea,
    lease_expires_at timestamptz,
    run_id text,
    result_hash text,
    result_payload jsonb,
    started_at timestamptz,
    finished_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (deadline > not_before),
    CHECK ((status = 'leased') = (probe_id IS NOT NULL AND lease_token_hash IS NOT NULL AND lease_expires_at IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS observation_assignments_active_target_idx
    ON observation_assignments (task_kind, branch_id)
    WHERE status = 'leased';
CREATE INDEX IF NOT EXISTS observation_assignments_claim_idx
    ON observation_assignments (status, not_before, deadline, created_at);
CREATE INDEX IF NOT EXISTS observation_assignments_probe_idx
    ON observation_assignments (probe_id, status, lease_expires_at);

CREATE TABLE IF NOT EXISTS assignment_attempts (
    assignment_id text NOT NULL REFERENCES observation_assignments(id) ON DELETE RESTRICT,
    probe_id text NOT NULL REFERENCES probe_runtimes(id) ON DELETE RESTRICT,
    attempt integer NOT NULL CHECK (attempt > 0),
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    status text NOT NULL CHECK (status IN ('leased', 'completed', 'partial', 'failed', 'expired')),
    error_code text NOT NULL DEFAULT '',
    PRIMARY KEY (assignment_id, attempt)
);

CREATE INDEX IF NOT EXISTS assignment_attempts_probe_idx
    ON assignment_attempts (probe_id, started_at DESC);

CREATE TABLE IF NOT EXISTS observation_payloads (
    content_hash text PRIMARY KEY CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS schedule_captures (
    assignment_id text NOT NULL REFERENCES observation_assignments(id) ON DELETE RESTRICT,
    run_id text NOT NULL,
    target_date date NOT NULL,
    observed_at timestamptz NOT NULL,
    complete boolean NOT NULL,
    error_code text NOT NULL DEFAULT '',
    content_hash text NOT NULL REFERENCES observation_payloads(content_hash) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (assignment_id, run_id, target_date)
);

CREATE INDEX IF NOT EXISTS schedule_captures_observed_idx
    ON schedule_captures (target_date, observed_at DESC);

CREATE TABLE IF NOT EXISTS showtime_observations (
    assignment_id text NOT NULL,
    run_id text NOT NULL,
    target_date date NOT NULL,
    source_id text NOT NULL,
    branch_id text NOT NULL,
    auditorium_name text NOT NULL,
    screen_types text[] NOT NULL,
    movie_title text NOT NULL,
    poster_url text NOT NULL DEFAULT '',
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    available_seats integer NOT NULL CHECK (available_seats >= 0),
    capacity integer NOT NULL CHECK (capacity >= 0),
    sold_out boolean NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (assignment_id, run_id, target_date, source_id),
    FOREIGN KEY (assignment_id, run_id, target_date)
        REFERENCES schedule_captures(assignment_id, run_id, target_date) ON DELETE RESTRICT,
    CHECK (ends_at > starts_at),
    CHECK (available_seats <= capacity)
);

CREATE INDEX IF NOT EXISTS showtime_observations_analysis_idx
    ON showtime_observations (branch_id, auditorium_name, movie_title, target_date, observed_at DESC);
CREATE INDEX IF NOT EXISTS showtime_observations_start_idx
    ON showtime_observations (branch_id, starts_at, observed_at DESC);
