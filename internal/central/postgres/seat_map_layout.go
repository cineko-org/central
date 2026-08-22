package postgres

import (
	"context"
	"fmt"

	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"github.com/jackc/pgx/v5"
)

// storeSeatMapLayout persists the queryable parts of a seat map under its immutable version.
func storeSeatMapLayout(ctx context.Context, tx pgx.Tx, snapshot *seatmappb.Snapshot) error {
	versionID := snapshot.GetId()
	seatRows := make([][]any, 0, len(snapshot.GetLayout().GetSeats()))
	for position, seat := range snapshot.GetLayout().GetSeats() {
		seatRows = append(seatRows, []any{
			versionID, position + 1, seat.GetId(), seat.GetLabel(), seat.GetRow(), seat.GetNumber(),
			seat.GetX(), seat.GetY(), seat.GetType(), seat.GetZoneName(), seat.GetZoneKind(),
			seat.GetSaleFormCode(), seat.GetSaleFormName(), seat.GetLeftAisle(), seat.GetRightAisle(),
			seat.GetSourceLabel(), seat.GetSourceSeatKindCode(), seat.GetSourceSeatKindName(),
		})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"seat_map_seats"}, []string{
		"version_id", "position", "seat_id", "label", "row_label", "seat_number", "x", "y", "seat_type",
		"zone_name", "zone_kind", "sale_form_code", "sale_form_name", "left_aisle", "right_aisle",
		"source_label", "source_seat_kind_code", "source_seat_kind_name",
	}, pgx.CopyFromRows(seatRows)); err != nil {
		return fmt.Errorf("store seat-map seats: %w", err)
	}
	featureRows := make([][]any, 0)
	sourceClassRows := make([][]any, 0)
	for _, seat := range snapshot.GetLayout().GetSeats() {
		for position, feature := range seat.GetFeatures() {
			featureRows = append(featureRows, []any{versionID, seat.GetId(), position + 1, feature})
		}
		for position, sourceClass := range seat.GetSourceClasses() {
			sourceClassRows = append(sourceClassRows, []any{versionID, seat.GetId(), position + 1, sourceClass})
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"seat_map_seat_features"}, []string{
		"version_id", "seat_id", "position", "feature",
	}, pgx.CopyFromRows(featureRows)); err != nil {
		return fmt.Errorf("store seat-map seat features: %w", err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"seat_map_seat_source_classes"}, []string{
		"version_id", "seat_id", "position", "source_class",
	}, pgx.CopyFromRows(sourceClassRows)); err != nil {
		return fmt.Errorf("store seat-map seat source classes: %w", err)
	}
	zoneRows := make([][]any, 0, len(snapshot.GetLayout().GetZones()))
	for position, zone := range snapshot.GetLayout().GetZones() {
		zoneRows = append(zoneRows, []any{
			versionID, position + 1, zone.GetCode(), zone.GetName(), zone.GetKindCode(), zone.GetKindName(),
			zone.GetMinX(), zone.GetMaxX(), zone.GetMinY(), zone.GetMaxY(), zone.GetCapacity(),
		})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"seat_map_zones"}, []string{
		"version_id", "position", "code", "name", "kind_code", "kind_name",
		"min_x", "max_x", "min_y", "max_y", "capacity",
	}, pgx.CopyFromRows(zoneRows)); err != nil {
		return fmt.Errorf("store seat-map zones: %w", err)
	}
	blockRows := make([][]any, 0, len(snapshot.GetLayout().GetBlocks()))
	for position, block := range snapshot.GetLayout().GetBlocks() {
		blockRows = append(blockRows, []any{
			versionID, position + 1, block.GetCode(), block.GetName(), block.GetKindCode(), block.GetKindName(),
			block.GetMinX(), block.GetMaxX(), block.GetMinY(), block.GetMaxY(),
		})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"seat_map_blocks"}, []string{
		"version_id", "position", "code", "name", "kind_code", "kind_name",
		"min_x", "max_x", "min_y", "max_y",
	}, pgx.CopyFromRows(blockRows)); err != nil {
		return fmt.Errorf("store seat-map blocks: %w", err)
	}
	return nil
}

type seatMapLayoutQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// readSeatMapLayout reconstructs the latest Proto layout from normalized version rows.
func readSeatMapLayout(
	ctx context.Context,
	querier seatMapLayoutQuerier,
	versionID string,
	auditoriumID string,
) (*seatmappb.Layout, error) {
	layout := &seatmappb.Layout{}
	seatRows, err := querier.Query(ctx, `
		SELECT seat_id, label, row_label, seat_number, x, y, seat_type,
			zone_name, zone_kind, sale_form_code, sale_form_name, left_aisle, right_aisle,
			ARRAY(
				SELECT feature.feature FROM seat_map_seat_features AS feature
				WHERE feature.version_id = seat.version_id AND feature.seat_id = seat.seat_id
				ORDER BY feature.position
			) AS features,
			source_label, source_seat_kind_code, source_seat_kind_name,
			ARRAY(
				SELECT source_class.source_class FROM seat_map_seat_source_classes AS source_class
				WHERE source_class.version_id = seat.version_id AND source_class.seat_id = seat.seat_id
				ORDER BY source_class.position
			) AS source_classes
		FROM seat_map_seats AS seat WHERE version_id = $1 ORDER BY position
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("read seat-map seats: %w", err)
	}
	defer seatRows.Close()
	for seatRows.Next() {
		seat := &seatmappb.Seat{}
		var features, sourceClasses []string
		var seatID, label, rowLabel, seatType, zoneName, zoneKind, saleFormCode, saleFormName string
		var sourceLabel, sourceSeatKindCode, sourceSeatKindName string
		var seatNumber int32
		var x, y float64
		var leftAisle, rightAisle bool
		if err := seatRows.Scan(
			&seatID, &label, &rowLabel, &seatNumber, &x, &y, &seatType,
			&zoneName, &zoneKind, &saleFormCode, &saleFormName, &leftAisle, &rightAisle,
			&features, &sourceLabel, &sourceSeatKindCode, &sourceSeatKindName, &sourceClasses,
		); err != nil {
			return nil, fmt.Errorf("scan seat-map seat: %w", err)
		}
		seat.SetId(seatID)
		seat.SetAuditoriumId(auditoriumID)
		seat.SetLabel(label)
		seat.SetRow(rowLabel)
		seat.SetNumber(seatNumber)
		seat.SetX(x)
		seat.SetY(y)
		seat.SetType(seatType)
		seat.SetZoneName(zoneName)
		seat.SetZoneKind(zoneKind)
		seat.SetSaleFormCode(saleFormCode)
		seat.SetSaleFormName(saleFormName)
		seat.SetLeftAisle(leftAisle)
		seat.SetRightAisle(rightAisle)
		seat.SetFeatures(features)
		seat.SetSourceLabel(sourceLabel)
		seat.SetSourceSeatKindCode(sourceSeatKindCode)
		seat.SetSourceSeatKindName(sourceSeatKindName)
		seat.SetSourceClasses(sourceClasses)
		layout.SetSeats(append(layout.GetSeats(), seat))
	}
	if err := seatRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate seat-map seats: %w", err)
	}
	zoneRows, err := querier.Query(ctx, `
		SELECT code, name, kind_code, kind_name, min_x, max_x, min_y, max_y, capacity
		FROM seat_map_zones WHERE version_id = $1 ORDER BY position
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("read seat-map zones: %w", err)
	}
	defer zoneRows.Close()
	for zoneRows.Next() {
		zone := &seatmappb.LayoutZone{}
		var code, name, kindCode, kindName string
		var minX, maxX, minY, maxY float64
		var capacity int32
		if err := zoneRows.Scan(&code, &name, &kindCode, &kindName, &minX, &maxX, &minY, &maxY, &capacity); err != nil {
			return nil, fmt.Errorf("scan seat-map zone: %w", err)
		}
		zone.SetCode(code)
		zone.SetName(name)
		zone.SetKindCode(kindCode)
		zone.SetKindName(kindName)
		zone.SetMinX(minX)
		zone.SetMaxX(maxX)
		zone.SetMinY(minY)
		zone.SetMaxY(maxY)
		zone.SetCapacity(capacity)
		layout.SetZones(append(layout.GetZones(), zone))
	}
	if err := zoneRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate seat-map zones: %w", err)
	}
	blockRows, err := querier.Query(ctx, `
		SELECT code, name, kind_code, kind_name, min_x, max_x, min_y, max_y
		FROM seat_map_blocks WHERE version_id = $1 ORDER BY position
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("read seat-map blocks: %w", err)
	}
	defer blockRows.Close()
	for blockRows.Next() {
		block := &seatmappb.LayoutBlock{}
		var code, name, kindCode, kindName string
		var minX, maxX, minY, maxY float64
		if err := blockRows.Scan(&code, &name, &kindCode, &kindName, &minX, &maxX, &minY, &maxY); err != nil {
			return nil, fmt.Errorf("scan seat-map block: %w", err)
		}
		block.SetCode(code)
		block.SetName(name)
		block.SetKindCode(kindCode)
		block.SetKindName(kindName)
		block.SetMinX(minX)
		block.SetMaxX(maxX)
		block.SetMinY(minY)
		block.SetMaxY(maxY)
		layout.SetBlocks(append(layout.GetBlocks(), block))
	}
	if err := blockRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate seat-map blocks: %w", err)
	}
	return layout, nil
}
