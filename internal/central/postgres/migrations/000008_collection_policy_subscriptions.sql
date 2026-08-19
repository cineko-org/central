CREATE TABLE observation_policy_subscriptions (
    user_id text NOT NULL REFERENCES client_users(id) ON DELETE RESTRICT,
    collection_id text NOT NULL,
    policy_id text NOT NULL REFERENCES observation_policies(id) ON DELETE RESTRICT,
    enabled boolean NOT NULL,
    horizon_days integer NOT NULL CHECK (horizon_days BETWEEN 1 AND 90),
    min_interval_seconds integer NOT NULL CHECK (min_interval_seconds > 0),
    max_interval_seconds integer NOT NULL CHECK (max_interval_seconds > min_interval_seconds),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, collection_id)
);

CREATE INDEX observation_policy_subscriptions_policy_idx
    ON observation_policy_subscriptions (policy_id, enabled);
