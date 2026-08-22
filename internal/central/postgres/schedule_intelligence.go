package postgres

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cineko-org/central/internal/support/numeric"
	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AdminObservationIntelligence derives opening and seat-availability patterns
// from complete schedule captures without treating availability loss as a sale.
func (store *Store) AdminObservationIntelligence(
	ctx context.Context,
	location *time.Location,
) (*adminpb.ObservationIntelligence, error) {
	if location == nil {
		location = time.UTC
	}
	result, err := store.scheduleIntelligenceSummary(ctx)
	if err != nil {
		return nil, err
	}
	openingPatterns, err := store.openingPatterns(ctx, location.String())
	if err != nil {
		return nil, err
	}
	result.SetOpeningPatterns(openingPatterns)
	demandPatterns, err := store.demandPatterns(ctx)
	if err != nil {
		return nil, err
	}
	result.SetDemandPatterns(demandPatterns)
	return result, nil
}

func (store *Store) scheduleIntelligenceSummary(
	ctx context.Context,
) (*adminpb.ObservationIntelligence, error) {
	result := &adminpb.ObservationIntelligence{}
	var snapshotCount, showtimeObservations int
	var lastObservedAt *time.Time
	err := store.pool.QueryRow(ctx, `
		WITH captures AS (
			SELECT capture.assignment_id, capture.run_id, capture.target_date,
				capture.observed_at, capture.complete
			FROM schedule_captures AS capture
		)
		SELECT COUNT(*), MAX(observed_at), (
			SELECT COUNT(*) FROM showtime_observations AS observation
			JOIN captures AS complete_capture
				ON complete_capture.assignment_id = observation.assignment_id
				AND complete_capture.run_id = observation.run_id
				AND complete_capture.target_date = observation.target_date
			WHERE complete_capture.complete
		) FROM captures
	`).Scan(&snapshotCount, &lastObservedAt, &showtimeObservations)
	if err != nil {
		return nil, fmt.Errorf("summarize schedule intelligence: %w", err)
	}
	result.SetSnapshotCount(numeric.ClampInt32(snapshotCount))
	result.SetShowtimeObservations(numeric.ClampInt32(showtimeObservations))
	if lastObservedAt != nil {
		result.SetLastObservedAt(timestamppb.New(lastObservedAt.UTC()))
	}
	return result, nil
}

func (store *Store) openingPatterns(
	ctx context.Context,
	timeZone string,
) ([]*adminpb.OpeningPattern, error) {
	rows, err := store.pool.Query(ctx, `
		WITH complete_captures AS (
			SELECT capture.assignment_id, capture.run_id, capture.target_date,
				capture.observed_at, assignment.theater_id, assignment.theater_name
			FROM schedule_captures AS capture
			JOIN observation_assignments AS assignment ON assignment.id = capture.assignment_id
			WHERE capture.complete
		), observations AS (
			SELECT observation.*, capture.theater_name
			FROM showtime_observations AS observation
			JOIN complete_captures AS capture
				ON capture.assignment_id = observation.assignment_id
				AND capture.run_id = observation.run_id
				AND capture.target_date = observation.target_date
		), first_occurrences AS (
			SELECT DISTINCT ON (theater_id, source_key, starts_at)
				theater_id, theater_name, auditorium_id, auditorium_name, movie_title, target_date,
				observed_at AS first_seen_at
			FROM observations
			ORDER BY theater_id, source_key, starts_at, observed_at
		), opening_events AS (
			SELECT occurrence.*, previous.observed_at AS previous_seen_at
			FROM first_occurrences AS occurrence
			JOIN LATERAL (
				SELECT MAX(capture.observed_at) AS observed_at
				FROM complete_captures AS capture
				WHERE capture.theater_id = occurrence.theater_id
					AND capture.target_date = occurrence.target_date
					AND capture.observed_at < occurrence.first_seen_at
			) AS previous ON previous.observed_at IS NOT NULL
		), batches AS (
			SELECT theater_id, MAX(theater_name) AS theater_name, auditorium_id,
				MAX(auditorium_name) AS auditorium_name, movie_title, target_date,
				first_seen_at, previous_seen_at,
				previous_seen_at + ((first_seen_at - previous_seen_at) / 2) AS estimated_open_at
			FROM opening_events
			GROUP BY theater_id, auditorium_id, movie_title, target_date, first_seen_at, previous_seen_at
		), screen_types AS (
			SELECT observation.theater_id, observation.auditorium_id, observation.movie_title,
				ARRAY_AGG(DISTINCT screen_type ORDER BY screen_type) AS values
			FROM observations AS observation
			CROSS JOIN LATERAL UNNEST(observation.screen_types) AS screen_type
			GROUP BY observation.theater_id, observation.auditorium_id, observation.movie_title
		)
		SELECT batch.theater_id, batch.theater_name, batch.auditorium_id,
			batch.auditorium_name, batch.movie_title,
			COALESCE(screen_types.values, '{}'), COUNT(*)::integer,
			PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY
				(EXTRACT(HOUR FROM batch.estimated_open_at AT TIME ZONE $1)::integer * 60) +
				 EXTRACT(MINUTE FROM batch.estimated_open_at AT TIME ZONE $1)::integer
			)::integer,
			PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY FLOOR(EXTRACT(EPOCH FROM (
				batch.target_date::timestamp - (batch.estimated_open_at AT TIME ZONE $1)
			)) / 3600)::integer)::integer,
			PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY FLOOR(EXTRACT(EPOCH FROM (
				batch.first_seen_at - batch.previous_seen_at
			)) / 60)::integer)::integer,
			MAX(batch.first_seen_at)
		FROM batches AS batch
		LEFT JOIN screen_types
			ON screen_types.theater_id = batch.theater_id
			AND screen_types.auditorium_id = batch.auditorium_id
			AND screen_types.movie_title = batch.movie_title
		GROUP BY batch.theater_id, batch.theater_name, batch.auditorium_id,
			batch.auditorium_name, batch.movie_title, screen_types.values
		ORDER BY COUNT(*) DESC, MAX(batch.first_seen_at) DESC
	`, timeZone)
	if err != nil {
		return nil, fmt.Errorf("query opening patterns: %w", err)
	}
	defer rows.Close()
	patterns := make([]*adminpb.OpeningPattern, 0)
	for rows.Next() {
		pattern := &adminpb.OpeningPattern{}
		var theaterID, theaterName, auditoriumID, auditoriumName, movie string
		var screenTypes []string
		var sampleSize, typicalLeadHours, typicalPrecisionMinutes int
		var typicalMinute int
		var lastObservedAt time.Time
		if err := rows.Scan(
			&theaterID, &theaterName, &auditoriumID,
			&auditoriumName, &movie, &screenTypes, &sampleSize,
			&typicalMinute, &typicalLeadHours, &typicalPrecisionMinutes,
			&lastObservedAt,
		); err != nil {
			return nil, fmt.Errorf("scan opening pattern: %w", err)
		}
		pattern.SetTheaterId(theaterID)
		pattern.SetTheaterName(theaterName)
		pattern.SetAuditoriumId(auditoriumID)
		pattern.SetAuditoriumName(auditoriumName)
		pattern.SetMovie(movie)
		pattern.SetScreenTypes(screenTypes)
		pattern.SetSampleSize(numeric.ClampInt32(sampleSize))
		pattern.SetTypicalOpenTime(fmt.Sprintf("%02d:%02d", typicalMinute/60, typicalMinute%60))
		pattern.SetTypicalLeadHours(numeric.ClampInt32(typicalLeadHours))
		pattern.SetTypicalPrecisionMinutes(numeric.ClampInt32(typicalPrecisionMinutes))
		pattern.SetLastObservedAt(timestamppb.New(lastObservedAt.UTC()))
		patterns = append(patterns, pattern)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opening patterns: %w", err)
	}
	return patterns, nil
}

func (store *Store) demandPatterns(
	ctx context.Context,
) ([]*adminpb.DemandPattern, error) {
	rows, err := store.pool.Query(ctx, `
		WITH observations AS (
			SELECT observation.*, assignment.theater_name
			FROM showtime_observations AS observation
			JOIN schedule_captures AS capture
				ON capture.assignment_id = observation.assignment_id
				AND capture.run_id = observation.run_id
				AND capture.target_date = observation.target_date
			JOIN observation_assignments AS assignment ON assignment.id = observation.assignment_id
			WHERE capture.complete
		), first_points AS (
			SELECT DISTINCT ON (theater_id, source_key, starts_at)
				theater_id, theater_name, source_key, starts_at, auditorium_id, auditorium_name,
				movie_title, observed_at AS first_seen_at, available_seats AS first_available,
				capacity, sold_out AS first_sold_out
			FROM observations
			WHERE capacity > 0
			ORDER BY theater_id, source_key, starts_at, observed_at
		), occurrences AS (
			SELECT first_point.*,
				hour_point.available_seats AS hour_available,
				half_point.observed_at AS half_sold_at,
				sold_out_point.observed_at AS sold_out_at,
				last_point.observed_at AS last_seen_at
			FROM first_points AS first_point
			LEFT JOIN LATERAL (
				SELECT observation.available_seats
				FROM observations AS observation
				WHERE observation.theater_id = first_point.theater_id
					AND observation.source_key = first_point.source_key
					AND observation.starts_at = first_point.starts_at
					AND observation.observed_at > first_point.first_seen_at
					AND observation.observed_at BETWEEN first_point.first_seen_at + INTERVAL '30 minutes'
						AND first_point.first_seen_at + INTERVAL '90 minutes'
				ORDER BY ABS(EXTRACT(EPOCH FROM (
					observation.observed_at - (first_point.first_seen_at + INTERVAL '1 hour')
				)))
				LIMIT 1
			) AS hour_point ON true
			LEFT JOIN LATERAL (
				SELECT MIN(observation.observed_at) AS observed_at
				FROM observations AS observation
				WHERE first_point.first_available > first_point.capacity / 2
					AND observation.theater_id = first_point.theater_id
					AND observation.source_key = first_point.source_key
					AND observation.starts_at = first_point.starts_at
					AND observation.observed_at > first_point.first_seen_at
					AND observation.available_seats <= first_point.capacity / 2
			) AS half_point ON true
			LEFT JOIN LATERAL (
				SELECT MIN(observation.observed_at) AS observed_at
				FROM observations AS observation
				WHERE NOT first_point.first_sold_out AND first_point.first_available > 0
					AND observation.theater_id = first_point.theater_id
					AND observation.source_key = first_point.source_key
					AND observation.starts_at = first_point.starts_at
					AND observation.observed_at > first_point.first_seen_at
					AND (observation.sold_out OR observation.available_seats = 0)
			) AS sold_out_point ON true
			JOIN LATERAL (
				SELECT MAX(observation.observed_at) AS observed_at
				FROM observations AS observation
				WHERE observation.theater_id = first_point.theater_id
					AND observation.source_key = first_point.source_key
					AND observation.starts_at = first_point.starts_at
			) AS last_point ON true
		)
		SELECT theater_id, MAX(theater_name), auditorium_id, MAX(auditorium_name),
			movie_title, COUNT(*)::integer,
			COUNT(hour_available)::integer,
			COALESCE(PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY
				GREATEST(0, LEAST(100, ((first_available - hour_available) * 100 / capacity)))
			) FILTER (WHERE hour_available IS NOT NULL), 0)::integer,
			COUNT(half_sold_at)::integer,
			COALESCE(PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY
				FLOOR(EXTRACT(EPOCH FROM (half_sold_at - first_seen_at)) / 60)::integer
			) FILTER (WHERE half_sold_at IS NOT NULL), 0)::integer,
			COUNT(sold_out_at)::integer,
			COALESCE(PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY
				FLOOR(EXTRACT(EPOCH FROM (sold_out_at - first_seen_at)) / 60)::integer
			) FILTER (WHERE sold_out_at IS NOT NULL), 0)::integer,
			MAX(last_seen_at)
		FROM occurrences
		GROUP BY theater_id, auditorium_id, movie_title
		HAVING COUNT(hour_available) + COUNT(half_sold_at) + COUNT(sold_out_at) > 0
		ORDER BY COUNT(*) DESC, MAX(last_seen_at) DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query demand patterns: %w", err)
	}
	defer rows.Close()
	patterns := make([]*adminpb.DemandPattern, 0)
	for rows.Next() {
		pattern := &adminpb.DemandPattern{}
		var theaterID, theaterName, auditoriumID, auditoriumName, movie string
		var occurrenceCount, firstHourSampleSize, typicalFirstHourSellThrough int
		var halfSoldSampleSize, typicalHalfSoldMinutes, soldOutSampleSize, typicalSoldOutMinutes int
		var lastObservedAt time.Time
		if err := rows.Scan(
			&theaterID, &theaterName, &auditoriumID,
			&auditoriumName, &movie, &occurrenceCount,
			&firstHourSampleSize, &typicalFirstHourSellThrough,
			&halfSoldSampleSize, &typicalHalfSoldMinutes,
			&soldOutSampleSize, &typicalSoldOutMinutes,
			&lastObservedAt,
		); err != nil {
			return nil, fmt.Errorf("scan demand pattern: %w", err)
		}
		pattern.SetTheaterId(theaterID)
		pattern.SetTheaterName(theaterName)
		pattern.SetAuditoriumId(auditoriumID)
		pattern.SetAuditoriumName(auditoriumName)
		pattern.SetMovie(movie)
		pattern.SetOccurrenceCount(numeric.ClampInt32(occurrenceCount))
		pattern.SetFirstHourSampleSize(numeric.ClampInt32(firstHourSampleSize))
		pattern.SetTypicalFirstHourSellThrough(numeric.ClampInt32(typicalFirstHourSellThrough))
		pattern.SetHalfSoldSampleSize(numeric.ClampInt32(halfSoldSampleSize))
		pattern.SetTypicalHalfSoldMinutes(numeric.ClampInt32(typicalHalfSoldMinutes))
		pattern.SetSoldOutSampleSize(numeric.ClampInt32(soldOutSampleSize))
		pattern.SetTypicalSoldOutMinutes(numeric.ClampInt32(typicalSoldOutMinutes))
		pattern.SetLastObservedAt(timestamppb.New(lastObservedAt.UTC()))
		patterns = append(patterns, pattern)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate demand patterns: %w", err)
	}
	sort.SliceStable(patterns, func(left, right int) bool {
		if patterns[left].GetOccurrenceCount() == patterns[right].GetOccurrenceCount() {
			return patterns[left].GetLastObservedAt().AsTime().After(patterns[right].GetLastObservedAt().AsTime())
		}
		return patterns[left].GetOccurrenceCount() > patterns[right].GetOccurrenceCount()
	})
	return patterns, nil
}
