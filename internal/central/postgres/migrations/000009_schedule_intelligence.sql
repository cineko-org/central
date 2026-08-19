CREATE INDEX showtime_observations_occurrence_idx
    ON showtime_observations (branch_id, source_id, starts_at, observed_at);

CREATE INDEX schedule_captures_analysis_idx
    ON schedule_captures (assignment_id, target_date, complete, observed_at);

CREATE INDEX observation_policy_subscriptions_user_policy_idx
    ON observation_policy_subscriptions (user_id, policy_id);
