-- Latest-contract hard cutover. Catalog identities and queued work created by
-- older contracts cannot be interpreted safely, so discard them atomically.
-- User identity, credentials, sessions, devices, settings, release state, and
-- Probe runtimes are intentionally preserved.
DELETE FROM monitor_showtime_availability;
DELETE FROM seat_availability_snapshots;

DELETE FROM client_execution_commands;
DELETE FROM client_events;
DELETE FROM client_commands;
DELETE FROM client_event_cursors;
DELETE FROM client_resources WHERE kind <> 'settings';

DELETE FROM showtime_observations;
DELETE FROM schedule_captures;
DELETE FROM assignment_attempts;
DELETE FROM assignment_eligible_probes;
DELETE FROM observation_assignments;
DELETE FROM observation_policies;
DELETE FROM observation_payloads;

DELETE FROM showtimes;
UPDATE auditoriums SET current_seat_map_version_id = NULL;
DELETE FROM seat_map_versions;
DELETE FROM auditoriums;
DELETE FROM movies;
DELETE FROM theaters;
DELETE FROM providers;

WITH cutover AS (
    SELECT clock_timestamp() AS happened_at
)
UPDATE catalog_state
SET generation = 1,
    refresh_requested_at = cutover.happened_at,
    updated_at = cutover.happened_at
FROM cutover
WHERE id = 1;

-- Seat-map collection is durable workflow state, independent from the immutable
-- current layout pointer. The old timestamp marker cannot represent retry,
-- waiting, blocked, or an in-flight assignment and is removed at the cutover.
CREATE TABLE seat_map_collection_states (
    auditorium_id text PRIMARY KEY REFERENCES auditoriums(id) ON DELETE CASCADE,
    state text NOT NULL CHECK (state IN (
        'queued', 'collecting', 'waiting_for_showtime', 'retry_scheduled', 'blocked'
    )),
    trigger_kind text NOT NULL CHECK (trigger_kind IN (
        'client_request', 'active_monitor', 'layout_missing', 'layout_changed',
        'catalog_refresh', 'operator_request'
    )),
    priority smallint NOT NULL DEFAULT 30 CHECK (priority BETWEEN 0 AND 100),
    assignment_id text REFERENCES observation_assignments(id) ON DELETE RESTRICT,
    showtime_id text REFERENCES showtimes(id) ON DELETE RESTRICT,
    reason_code text NOT NULL DEFAULT '',
    requested_at timestamptz NOT NULL,
    last_attempt_at timestamptz,
    next_attempt_at timestamptz,
    consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    updated_at timestamptz NOT NULL,
    CHECK ((state = 'collecting' AND assignment_id IS NOT NULL) OR state <> 'collecting'),
    CHECK ((state <> 'collecting' AND assignment_id IS NULL) OR state = 'collecting'),
    CHECK ((state = 'retry_scheduled' AND next_attempt_at IS NOT NULL) OR state <> 'retry_scheduled'),
    CHECK ((state <> 'retry_scheduled' AND next_attempt_at IS NULL) OR state = 'retry_scheduled'),
    CHECK (
        (state IN ('queued', 'collecting', 'retry_scheduled') AND showtime_id IS NOT NULL)
        OR state IN ('waiting_for_showtime', 'blocked')
    ),
    CHECK (
        (state = 'waiting_for_showtime' AND reason_code IN (
            'showtime_not_discovered', 'no_bookable_showtime', 'target_date_unavailable'
        ))
        OR (state = 'blocked' AND reason_code IN (
            'provider_blocked', 'provider_throttled', 'browser_start_failed',
            'provider_transport_failed', 'provider_server_error', 'timeout',
            'identity_mismatch', 'captcha_required', 'authentication_required',
            'ui_contract_changed', 'invalid_result'
        ))
        OR (state IN ('queued', 'collecting') AND reason_code = '')
        OR (state = 'retry_scheduled' AND reason_code IN (
            'provider_blocked', 'provider_throttled', 'browser_start_failed',
            'provider_transport_failed', 'provider_server_error', 'timeout'
        ))
    )
);

CREATE UNIQUE INDEX seat_map_collection_states_assignment_idx
    ON seat_map_collection_states (assignment_id)
    WHERE assignment_id IS NOT NULL;

CREATE INDEX seat_map_collection_states_due_idx
    ON seat_map_collection_states (state, next_attempt_at, priority DESC, requested_at)
    WHERE state IN ('queued', 'retry_scheduled');

-- Hard cutover: the legacy timestamp did not identify whether an operator or a
-- Client requested work, so it is not promoted into a durable trigger. Seed
-- only canonical baseline work for an active auditorium that lacks a layout and
-- already has a real future active showtime. A fresh Resolve request creates a
-- waiting row when no showtime is known.
WITH migration_now AS (
    SELECT clock_timestamp() AS now
)
INSERT INTO seat_map_collection_states (
    auditorium_id, state, trigger_kind, priority, showtime_id, reason_code,
    requested_at, next_attempt_at, updated_at
)
SELECT
    auditorium.id,
    'queued',
    'layout_missing',
    30,
    future_showtime.id,
    '',
    migration_now.now,
    NULL,
    migration_now.now
FROM auditoriums AS auditorium
CROSS JOIN migration_now
LEFT JOIN LATERAL (
    SELECT showtime.id
    FROM showtimes AS showtime
    WHERE showtime.auditorium_id = auditorium.id
        AND showtime.active
        AND showtime.starts_at > migration_now.now
    ORDER BY showtime.starts_at, showtime.id
    LIMIT 1
) AS future_showtime ON true
WHERE auditorium.active
    AND auditorium.current_seat_map_version_id IS NULL
    AND future_showtime.id IS NOT NULL;

DROP INDEX IF EXISTS auditoriums_missing_seat_map_idx;
ALTER TABLE auditoriums DROP COLUMN seat_map_requested_at;

COMMENT ON TABLE seat_map_collection_states IS
    'Central-owned seat-map collection lifecycle; no row means idle and does not change the immutable current layout pointer.';
COMMENT ON COLUMN seat_map_collection_states.showtime_id IS
    'Exact future showtime hint for the next collection; NULL means Central is waiting for catalog discovery.';
