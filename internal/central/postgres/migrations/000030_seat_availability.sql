ALTER TABLE observation_assignments
    ADD COLUMN showtime_id text REFERENCES showtimes(id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX observation_assignments_active_showtime_availability_idx
    ON observation_assignments (showtime_id)
    WHERE task_kind = 'cgv.seat-availability.capture'
        AND showtime_id IS NOT NULL
        AND status IN ('queued', 'leased', 'retry_pending');

CREATE TABLE seat_availability_snapshots (
    id text PRIMARY KEY,
    showtime_id text NOT NULL REFERENCES showtimes(id) ON DELETE RESTRICT,
    auditorium_id text NOT NULL REFERENCES auditoriums(id) ON DELETE RESTRICT,
    layout_hash text NOT NULL CHECK (layout_hash ~ '^[0-9a-f]{64}$'),
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    observed_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (showtime_id, observed_at)
);

CREATE INDEX seat_availability_snapshots_history_idx
    ON seat_availability_snapshots (showtime_id, observed_at DESC);

CREATE TABLE seat_availability_snapshot_seats (
    snapshot_id text NOT NULL REFERENCES seat_availability_snapshots(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 1),
    seat_id text NOT NULL CHECK (seat_id <> ''),
    PRIMARY KEY (snapshot_id, position),
    UNIQUE (snapshot_id, seat_id)
);

CREATE INDEX seat_availability_snapshot_seats_lookup_idx
    ON seat_availability_snapshot_seats (seat_id, snapshot_id);

CREATE TABLE monitor_showtime_availability (
    user_id text NOT NULL,
    monitor_id text NOT NULL,
    showtime_id text NOT NULL REFERENCES showtimes(id) ON DELETE RESTRICT,
    snapshot_id text REFERENCES seat_availability_snapshots(id) ON DELETE RESTRICT,
    matched boolean NOT NULL,
    observed_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, monitor_id, showtime_id),
    FOREIGN KEY (user_id, monitor_id)
        REFERENCES client_monitors(user_id, id) ON DELETE CASCADE
);

CREATE INDEX monitor_showtime_availability_showtime_idx
    ON monitor_showtime_availability (showtime_id, matched, observed_at DESC);

COMMENT ON TABLE seat_availability_snapshots IS
    'Distinct adjacent live-seat states for an exact showtime; repeated unchanged captures are not stored.';
COMMENT ON TABLE monitor_showtime_availability IS
    'Last exact or coarse preset match used to fence false-to-true execution wakes.';
