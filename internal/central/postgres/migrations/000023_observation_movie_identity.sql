ALTER TABLE showtime_observations
    ADD COLUMN movie_id text REFERENCES movies(id) ON DELETE RESTRICT;

UPDATE showtime_observations AS observation
SET movie_id = showtime.movie_id
FROM showtimes AS showtime
WHERE observation.movie_id IS NULL
  AND showtime.theater_id = observation.theater_id
  AND showtime.source_key = observation.source_key
  AND showtime.starts_at = observation.starts_at;

CREATE INDEX showtime_observations_movie_history_idx
    ON showtime_observations (movie_id, observed_at DESC)
    WHERE movie_id IS NOT NULL;
