ALTER TABLE observation_policies
    ADD COLUMN display_name text NOT NULL DEFAULT '',
    ADD COLUMN demand_min_interval_seconds integer NOT NULL DEFAULT 120,
    ADD COLUMN demand_max_interval_seconds integer NOT NULL DEFAULT 300,
    ADD COLUMN burst_min_interval_seconds integer NOT NULL DEFAULT 30,
    ADD COLUMN burst_max_interval_seconds integer NOT NULL DEFAULT 90,
    ADD COLUMN burst_duration_seconds integer NOT NULL DEFAULT 3600,
    ADD COLUMN burst_until timestamptz;

-- Earlier policies allowed a fixed interval. Convert them to the smallest
-- valid randomized range before enforcing the additive-only contract.
UPDATE observation_policies
SET min_interval_seconds = GREATEST(min_interval_seconds, 30),
    max_interval_seconds = GREATEST(
        max_interval_seconds,
        GREATEST(min_interval_seconds, 30) + 1
    );

UPDATE observation_policies
SET demand_min_interval_seconds = LEAST(120, min_interval_seconds),
    demand_max_interval_seconds = LEAST(300, max_interval_seconds);

UPDATE observation_policies
SET burst_min_interval_seconds = LEAST(30, demand_min_interval_seconds),
    burst_max_interval_seconds = LEAST(90, demand_max_interval_seconds);

ALTER TABLE observation_policies
    ADD CONSTRAINT observation_policies_baseline_interval_check CHECK (
        min_interval_seconds >= 30
        AND max_interval_seconds > min_interval_seconds
    ),
    ADD CONSTRAINT observation_policies_demand_interval_check CHECK (
        demand_min_interval_seconds >= 30
        AND demand_max_interval_seconds > demand_min_interval_seconds
    ),
    ADD CONSTRAINT observation_policies_burst_interval_check CHECK (
        burst_min_interval_seconds >= 15
        AND burst_max_interval_seconds > burst_min_interval_seconds
        AND burst_duration_seconds BETWEEN 300 AND 21600
    );

UPDATE observation_policies
SET display_name = branch_name
WHERE display_name = '';

DELETE FROM client_events
WHERE resource_kind IN ('collections', 'collection-runs', 'schedule-snapshots', 'opening-observations');

DELETE FROM client_commands
WHERE resource_kind IN ('collections', 'collection-runs', 'schedule-snapshots', 'opening-observations');

DELETE FROM client_resources
WHERE kind IN ('collections', 'collection-runs', 'schedule-snapshots', 'opening-observations');

ALTER TABLE client_resources DROP CONSTRAINT client_resources_kind_check;
ALTER TABLE client_resources ADD CONSTRAINT client_resources_kind_check CHECK (kind IN (
    'settings', 'branches', 'auditoria', 'seat-maps', 'booking-catalogs',
    'presets', 'monitors', 'reservations', 'external-operations', 'app-events'
));

DROP TABLE IF EXISTS observation_policy_subscriptions;
