package postgres

import (
	"context"
	"fmt"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"

	"github.com/jackc/pgx/v5"
)

func loadClientPreset(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	id string,
) (*clientpb.Resource, error) {
	var name, theaterID, auditoriumID string
	var seatCount int32
	var hasPreference, together, avoidEdges bool
	var createdAt, updatedAt *time.Time
	if err := queryer.QueryRow(ctx, `
		SELECT name, theater_id, auditorium_id, seat_count, has_seat_preference,
			together, avoid_edges, preset_created_at, preset_updated_at
		FROM client_presets WHERE user_id = $1 AND id = $2
	`, userID, id).Scan(
		&name, &theaterID, &auditoriumID, &seatCount, &hasPreference,
		&together, &avoidEdges, &createdAt, &updatedAt,
	); err != nil {
		return nil, fmt.Errorf("read normalized Client preset: %w", err)
	}
	preset := &clientpb.Preset{}
	preset.SetId(id)
	preset.SetUserId(userID)
	preset.SetName(name)
	preset.SetTheaterId(theaterID)
	preset.SetAuditoriumId(auditoriumID)
	preset.SetSeatCount(seatCount)
	preset.SetCreatedAt(nullableProtoTimestamp(createdAt))
	preset.SetUpdatedAt(nullableProtoTimestamp(updatedAt))
	if hasPreference {
		preference := &clientpb.SeatPreference{}
		preference.SetTogether(together)
		preference.SetAvoidEdges(avoidEdges)
		var err error
		preference.SetExplicitSeats(nil)
		if values, loadErr := loadOrderedStrings(ctx, queryer, `
			SELECT seat_label FROM client_preset_explicit_seats
			WHERE user_id = $1 AND preset_id = $2 ORDER BY position
		`, userID, id); loadErr != nil {
			err = loadErr
		} else {
			preference.SetExplicitSeats(values)
		}
		if err != nil {
			return nil, fmt.Errorf("read Client preset explicit seats: %w", err)
		}
		rows, loadErr := loadOrderedStrings(ctx, queryer, `
			SELECT row_label FROM client_preset_preferred_rows
			WHERE user_id = $1 AND preset_id = $2 ORDER BY position
		`, userID, id)
		if loadErr != nil {
			return nil, fmt.Errorf("read Client preset preferred rows: %w", loadErr)
		}
		preference.SetPreferredRows(rows)
		types, loadErr := loadOrderedStrings(ctx, queryer, `
			SELECT seat_type FROM client_preset_preferred_types
			WHERE user_id = $1 AND preset_id = $2 ORDER BY position
		`, userID, id)
		if loadErr != nil {
			return nil, fmt.Errorf("read Client preset preferred types: %w", loadErr)
		}
		preference.SetPreferredTypes(types)
		zones, loadErr := loadClientPresetZones(ctx, queryer, userID, id)
		if loadErr != nil {
			return nil, loadErr
		}
		preference.SetPreferredZones(zones)
		preset.SetSeatPreference(preference)
	}
	resource := &clientpb.Resource{}
	resource.SetPreset(preset)
	return resource, nil
}

func loadClientPresetZones(
	ctx context.Context,
	queryer clientResourceQueryer,
	userID string,
	presetID string,
) ([]*clientpb.SeatZone, error) {
	rows, err := queryer.Query(ctx, `
		SELECT name, min_x, max_x, min_y, max_y, weight
		FROM client_preset_preferred_zones
		WHERE user_id = $1 AND preset_id = $2 ORDER BY position
	`, userID, presetID)
	if err != nil {
		return nil, fmt.Errorf("list Client preset preferred zones: %w", err)
	}
	defer rows.Close()
	zones := make([]*clientpb.SeatZone, 0)
	for rows.Next() {
		var name string
		var minX, maxX, minY, maxY float64
		var weight int32
		if err := rows.Scan(&name, &minX, &maxX, &minY, &maxY, &weight); err != nil {
			return nil, fmt.Errorf("scan Client preset preferred zone: %w", err)
		}
		zone := &clientpb.SeatZone{}
		zone.SetName(name)
		zone.SetMinX(minX)
		zone.SetMaxX(maxX)
		zone.SetMinY(minY)
		zone.SetMaxY(maxY)
		zone.SetWeight(weight)
		zones = append(zones, zone)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Client preset preferred zones: %w", err)
	}
	return zones, nil
}

func writeClientPreset(ctx context.Context, tx pgx.Tx, resource storedClientResource) error {
	preset := resource.body.GetPreset()
	if preset == nil {
		return fmt.Errorf("client preset is required")
	}
	preference := preset.GetSeatPreference()
	hasPreference := preference != nil
	together, avoidEdges := false, false
	if preference != nil {
		together = preference.GetTogether()
		avoidEdges = preference.GetAvoidEdges()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO client_presets (
			user_id, resource_kind, id, name, theater_id, auditorium_id, seat_count,
			has_seat_preference, together, avoid_edges, preset_created_at, preset_updated_at
		) VALUES ($1, 'presets', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (user_id, id) DO UPDATE SET
			name = EXCLUDED.name,
			theater_id = EXCLUDED.theater_id,
			auditorium_id = EXCLUDED.auditorium_id,
			seat_count = EXCLUDED.seat_count,
			has_seat_preference = EXCLUDED.has_seat_preference,
			together = EXCLUDED.together,
			avoid_edges = EXCLUDED.avoid_edges,
			preset_created_at = EXCLUDED.preset_created_at,
			preset_updated_at = EXCLUDED.preset_updated_at
	`, resource.userID, resource.id, preset.GetName(), preset.GetTheaterId(), preset.GetAuditoriumId(),
		preset.GetSeatCount(), hasPreference, together, avoidEdges,
		protoTimestamp(preset.GetCreatedAt()), protoTimestamp(preset.GetUpdatedAt())); err != nil {
		return fmt.Errorf("write normalized Client preset: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_preset_explicit_seats WHERE user_id = $1 AND preset_id = $2
	`, resource.userID, resource.id); err != nil {
		return fmt.Errorf("clear normalized Client preset explicit seats: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_preset_preferred_rows WHERE user_id = $1 AND preset_id = $2
	`, resource.userID, resource.id); err != nil {
		return fmt.Errorf("clear normalized Client preset preferred rows: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_preset_preferred_zones WHERE user_id = $1 AND preset_id = $2
	`, resource.userID, resource.id); err != nil {
		return fmt.Errorf("clear normalized Client preset preferred zones: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM client_preset_preferred_types WHERE user_id = $1 AND preset_id = $2
	`, resource.userID, resource.id); err != nil {
		return fmt.Errorf("clear normalized Client preset preference: %w", err)
	}
	if preference == nil {
		return nil
	}
	if err := writeClientPresetStrings(
		ctx, tx, `
			INSERT INTO client_preset_explicit_seats
				(user_id, preset_id, position, seat_label)
			VALUES ($1, $2, $3, $4)
		`,
		resource.userID, resource.id, preference.GetExplicitSeats(),
	); err != nil {
		return err
	}
	if err := writeClientPresetStrings(
		ctx, tx, `
			INSERT INTO client_preset_preferred_rows
				(user_id, preset_id, position, row_label)
			VALUES ($1, $2, $3, $4)
		`,
		resource.userID, resource.id, preference.GetPreferredRows(),
	); err != nil {
		return err
	}
	if err := writeClientPresetStrings(
		ctx, tx, `
			INSERT INTO client_preset_preferred_types
				(user_id, preset_id, position, seat_type)
			VALUES ($1, $2, $3, $4)
		`,
		resource.userID, resource.id, preference.GetPreferredTypes(),
	); err != nil {
		return err
	}
	for position, zone := range preference.GetPreferredZones() {
		if zone == nil {
			return fmt.Errorf("client preset preferred zone %d is required", position)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_preset_preferred_zones (
				user_id, preset_id, position, name, min_x, max_x, min_y, max_y, weight
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, resource.userID, resource.id, position, zone.GetName(), zone.GetMinX(), zone.GetMaxX(),
			zone.GetMinY(), zone.GetMaxY(), zone.GetWeight()); err != nil {
			return fmt.Errorf("write Client preset preferred zone: %w", err)
		}
	}
	return nil
}

func writeClientPresetStrings(
	ctx context.Context,
	tx pgx.Tx,
	query string,
	userID string,
	presetID string,
	values []string,
) error {
	for position, value := range values {
		if _, err := tx.Exec(ctx, query, userID, presetID, position, value); err != nil {
			return fmt.Errorf("write normalized Client preset value: %w", err)
		}
	}
	return nil
}
