ALTER TABLE probe_runtimes
    ADD COLUMN available_capabilities text[] NOT NULL DEFAULT '{}';

ALTER TABLE observation_assignments
    ADD COLUMN task_data jsonb;

ALTER TABLE auditoriums
    ADD COLUMN seat_map_requested_at timestamptz;

CREATE INDEX auditoriums_missing_seat_map_idx
    ON auditoriums (seat_map_requested_at DESC NULLS LAST, updated_at)
    WHERE active AND (current_seat_map_version_id IS NULL OR seat_map_requested_at IS NOT NULL);
