ALTER TABLE showtimes
    ADD COLUMN schedule_date date;

UPDATE showtimes AS showtime
SET schedule_date = COALESCE(
    (
        SELECT observation.target_date
        FROM showtime_observations AS observation
        WHERE observation.theater_id = showtime.theater_id
            AND observation.source_key = showtime.source_key
            AND observation.starts_at = showtime.starts_at
        ORDER BY observation.observed_at DESC
        LIMIT 1
    ),
    CASE
        WHEN showtime.source_key ~ '/[0-9]{4}-[0-9]{2}-[0-9]{2}/'
            THEN substring(showtime.source_key FROM '/([0-9]{4}-[0-9]{2}-[0-9]{2})/')::date
        ELSE NULL
    END
);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM showtimes WHERE schedule_date IS NULL) THEN
        RAISE EXCEPTION 'showtime schedule_date backfill requires a capture or canonical provider source key';
    END IF;
END;
$$;

ALTER TABLE showtimes
    ALTER COLUMN schedule_date SET NOT NULL;

CREATE INDEX showtimes_schedule_date_idx
    ON showtimes (theater_id, schedule_date, starts_at, id);

ALTER TABLE client_reservation_showtimes
    ADD COLUMN IF NOT EXISTS schedule_date date;

UPDATE client_reservation_showtimes AS reservation_showtime
SET schedule_date = showtime.schedule_date
FROM showtimes AS showtime
WHERE showtime.id = reservation_showtime.showtime_id
    AND reservation_showtime.schedule_date IS NULL;

UPDATE client_reservation_showtimes
SET schedule_date = substring(source_key FROM '/([0-9]{4}-[0-9]{2}-[0-9]{2})/')::date
WHERE schedule_date IS NULL
    AND source_key ~ '/[0-9]{4}-[0-9]{2}-[0-9]{2}/';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM client_reservation_showtimes WHERE schedule_date IS NULL) THEN
        RAISE EXCEPTION 'reservation showtime schedule_date backfill requires a canonical showtime identity';
    END IF;
END;
$$;

ALTER TABLE client_reservation_showtimes
    ALTER COLUMN schedule_date SET NOT NULL;

WITH reservation_events AS (
    SELECT event.sequence,
        COALESCE(
            showtime.schedule_date,
            CASE
                WHEN event.payload->'showtime'->>'sourceKey' ~ '/[0-9]{4}-[0-9]{2}-[0-9]{2}/'
                    THEN substring(event.payload->'showtime'->>'sourceKey' FROM '/([0-9]{4}-[0-9]{2}-[0-9]{2})/')::date
                ELSE NULL
            END
        ) AS schedule_date
    FROM client_events AS event
    LEFT JOIN showtimes AS showtime
        ON showtime.id = event.payload->'showtime'->>'id'
    WHERE event.resource_kind = 'reservations'
        AND event.event_type = 'reservations.updated'
        AND NOT (event.payload->'showtime' ? 'scheduleDate')
)
UPDATE client_events AS event
SET payload = jsonb_set(
    event.payload,
    '{showtime,scheduleDate}',
    jsonb_build_object(
        'year', EXTRACT(YEAR FROM candidate.schedule_date)::integer,
        'month', EXTRACT(MONTH FROM candidate.schedule_date)::integer,
        'day', EXTRACT(DAY FROM candidate.schedule_date)::integer
    ),
    true
)
FROM reservation_events AS candidate
WHERE event.sequence = candidate.sequence
    AND candidate.schedule_date IS NOT NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM client_events
        WHERE resource_kind = 'reservations'
            AND event_type = 'reservations.updated'
            AND NOT (payload->'showtime' ? 'scheduleDate')
    ) THEN
        RAISE EXCEPTION 'reservation event schedule_date backfill requires a canonical showtime identity';
    END IF;
END;
$$;
