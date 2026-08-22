ALTER TABLE observation_policies
    DROP CONSTRAINT observation_policies_demand_interval_check;

ALTER TABLE observation_policies
    ADD CONSTRAINT observation_policies_demand_interval_check CHECK (
        demand_min_interval_seconds >= 1
        AND demand_max_interval_seconds > demand_min_interval_seconds
    );
