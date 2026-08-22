-- Catalog capture targets an entire provider, not a fabricated theater. Work
-- serialized with the removed theater-shaped contract cannot be decoded by the
-- current Proto and must not keep the provider refresh lane permanently busy.
DELETE FROM assignment_attempts
WHERE assignment_id IN (
    SELECT id FROM observation_assignments WHERE task_kind = 'cgv.catalog.capture'
);

DELETE FROM assignment_eligible_probes
WHERE assignment_id IN (
    SELECT id FROM observation_assignments WHERE task_kind = 'cgv.catalog.capture'
);

DELETE FROM observation_assignments
WHERE task_kind = 'cgv.catalog.capture';

DROP INDEX observation_assignments_active_theater_idx;
CREATE UNIQUE INDEX observation_assignments_active_target_idx
    ON observation_assignments (task_kind, theater_provider_id, theater_id)
    WHERE status = 'leased';

ALTER TABLE observation_assignments
    ADD CONSTRAINT observation_assignments_target_shape_check CHECK (
        (task_kind = 'cgv.catalog.capture'
            AND theater_provider_id <> ''
            AND theater_id = ''
            AND theater_source_key = ''
            AND theater_region = ''
            AND theater_name = '')
        OR
        (task_kind <> 'cgv.catalog.capture'
            AND theater_provider_id <> ''
            AND theater_id <> ''
            AND theater_source_key <> '')
    );
