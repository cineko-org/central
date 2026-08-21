ALTER TABLE observation_assignments
    ADD COLUMN lane text NOT NULL DEFAULT 'baseline',
    ADD COLUMN hot_target_fingerprint text NOT NULL DEFAULT '';

UPDATE observation_assignments
SET lane = COALESCE(NULLIF(task_data->>'_cinekoLane', ''), 'baseline'),
    hot_target_fingerprint = COALESCE(task_data->>'_cinekoHotFingerprint', '');

UPDATE probe_runtimes
SET capabilities = ARRAY(
        SELECT CASE capability
            WHEN 'cgv.schedule.capture.v2' THEN 'cgv.schedule.capture'
            WHEN 'cgv.catalog.capture.v1' THEN 'cgv.catalog.capture'
            WHEN 'cgv.seat-map.capture.v1' THEN 'cgv.seat-map.capture'
            ELSE capability
        END
        FROM unnest(capabilities) AS capability
    ),
    available_capabilities = ARRAY(
        SELECT CASE capability
            WHEN 'cgv.schedule.capture.v2' THEN 'cgv.schedule.capture'
            WHEN 'cgv.catalog.capture.v1' THEN 'cgv.catalog.capture'
            WHEN 'cgv.seat-map.capture.v1' THEN 'cgv.seat-map.capture'
            ELSE capability
        END
        FROM unnest(available_capabilities) AS capability
    );

UPDATE observation_policies
SET task_kind = CASE task_kind
    WHEN 'cgv.schedule.capture.v2' THEN 'cgv.schedule.capture'
    WHEN 'cgv.catalog.capture.v1' THEN 'cgv.catalog.capture'
    WHEN 'cgv.seat-map.capture.v1' THEN 'cgv.seat-map.capture'
    ELSE task_kind
END;

UPDATE observation_assignments AS assignment
SET task_kind = CASE assignment.task_kind
        WHEN 'cgv.schedule.capture.v2' THEN 'cgv.schedule.capture'
        WHEN 'cgv.catalog.capture.v1' THEN 'cgv.catalog.capture'
        WHEN 'cgv.seat-map.capture.v1' THEN 'cgv.seat-map.capture'
        ELSE assignment.task_kind
    END,
    task_data = jsonb_build_object(
        'egress', jsonb_build_object('managedScan', jsonb_build_object()),
        CASE assignment.task_kind
            WHEN 'cgv.catalog.capture.v1' THEN 'catalog'
            WHEN 'cgv.seat-map.capture.v1' THEN 'seatMap'
            ELSE 'schedule'
        END,
        CASE assignment.task_kind
            WHEN 'cgv.seat-map.capture.v1' THEN jsonb_strip_nulls(jsonb_build_object(
                'theater', jsonb_build_object(
                    'id', assignment.theater_id,
                    'providerId', assignment.theater_provider_id,
                    'sourceKey', assignment.theater_source_key,
                    'region', assignment.theater_region,
                    'name', assignment.theater_name
                ),
                'auditorium', CASE
                    WHEN assignment.task_data ? 'auditorium' THEN
                        ((assignment.task_data->'auditorium') - 'seatMapVersion'::text) ||
                        CASE WHEN assignment.task_data->'auditorium' ? 'seatMapVersion'
                            THEN jsonb_build_object('currentLayoutHash', assignment.task_data->'auditorium'->'seatMapVersion')
                            ELSE '{}'::jsonb
                        END
                    ELSE NULL
                END,
                'showtime', CASE
                    WHEN assignment.task_data ? 'showtime' THEN
                        ((assignment.task_data->'showtime') - 'auditorium'::text) ||
                        jsonb_build_object(
                            'auditorium',
                            (((assignment.task_data->'showtime')->'auditorium') - 'seatMapVersion'::text) ||
                            CASE WHEN assignment.task_data->'showtime'->'auditorium' ? 'seatMapVersion'
                                THEN jsonb_build_object(
                                    'currentLayoutHash',
                                    assignment.task_data->'showtime'->'auditorium'->'seatMapVersion'
                                )
                                ELSE '{}'::jsonb
                            END
                        )
                    ELSE NULL
                END,
                'locale', assignment.locale,
                'timeZone', assignment.time_zone
            ))
            ELSE jsonb_build_object(
                'theater', jsonb_build_object(
                    'id', assignment.theater_id,
                    'providerId', assignment.theater_provider_id,
                    'sourceKey', assignment.theater_source_key,
                    'region', assignment.theater_region,
                    'name', assignment.theater_name
                ),
                'targetDates', (
                    SELECT COALESCE(jsonb_agg(jsonb_build_object(
                        'year', EXTRACT(YEAR FROM target_date)::integer,
                        'month', EXTRACT(MONTH FROM target_date)::integer,
                        'day', EXTRACT(DAY FROM target_date)::integer
                    ) ORDER BY target_date), '[]'::jsonb)
                    FROM unnest(assignment.target_dates) AS target_date
                ),
                'locale', assignment.locale,
                'timeZone', assignment.time_zone
            )
        END
    );

-- Assignment results are fully normalized into capture, showtime, catalog, and
-- seat-map tables. Drop the unread legacy DTO copy instead of retaining a
-- second historical contract beside the generated result message.
UPDATE observation_assignments
SET result_payload = NULL
WHERE result_payload IS NOT NULL;

UPDATE assignment_attempts
SET result_payload = NULL
WHERE result_payload IS NOT NULL;
