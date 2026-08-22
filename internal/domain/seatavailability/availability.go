// Package seatavailability owns normalization and matching for exact-showtime
// availability snapshots. It operates directly on the canonical Proto types.
package seatavailability

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
)

const maximumFutureClockSkew = 5 * time.Minute

// Match reports whether the preset has an acceptable group and whether the
// result was derived from the exact layout identified by the snapshot.
type Match struct {
	Available bool
	Exact     bool
}

// Normalize validates identity and time fields, trims seat identities, and
// canonicalizes the available-seat set for deterministic persistence.
func Normalize(snapshot *seatmappb.AvailabilitySnapshot, now time.Time) error {
	if snapshot == nil {
		return errors.New("availability snapshot is required")
	}
	snapshot.SetShowtimeId(strings.TrimSpace(snapshot.GetShowtimeId()))
	snapshot.SetAuditoriumId(strings.TrimSpace(snapshot.GetAuditoriumId()))
	snapshot.SetLayoutHash(strings.ToLower(strings.TrimSpace(snapshot.GetLayoutHash())))
	if snapshot.GetShowtimeId() == "" || snapshot.GetAuditoriumId() == "" {
		return errors.New("availability showtime and auditorium identities are required")
	}
	if len(snapshot.GetLayoutHash()) != sha256.Size*2 {
		return errors.New("availability layout hash must be a SHA-256 digest")
	}
	if _, err := hex.DecodeString(snapshot.GetLayoutHash()); err != nil {
		return errors.New("availability layout hash must be lowercase hexadecimal")
	}
	if snapshot.GetObservedAt() == nil || snapshot.GetObservedAt().CheckValid() != nil {
		return errors.New("availability observedAt is required")
	}
	if snapshot.GetObservedAt().AsTime().After(now.Add(maximumFutureClockSkew)) {
		return errors.New("availability observedAt is too far in the future")
	}

	seen := make(map[string]struct{}, len(snapshot.GetAvailableSeats()))
	seats := make([]*seatmappb.AvailableSeat, 0, len(snapshot.GetAvailableSeats()))
	for _, value := range snapshot.GetAvailableSeats() {
		if value == nil {
			return errors.New("availability seat is required")
		}
		seatID := strings.TrimSpace(value.GetSeatId())
		if seatID == "" {
			return errors.New("availability seat id is required")
		}
		if _, duplicate := seen[seatID]; duplicate {
			return fmt.Errorf("duplicate availability seat %q", seatID)
		}
		seen[seatID] = struct{}{}
		seat := &seatmappb.AvailableSeat{}
		seat.SetSeatId(seatID)
		seats = append(seats, seat)
	}
	sort.Slice(seats, func(left, right int) bool {
		return seats[left].GetSeatId() < seats[right].GetSeatId()
	})
	snapshot.SetAvailableSeats(seats)
	return nil
}

// ContentHash identifies one normalized live state independently of its
// observation timestamp.
func ContentHash(snapshot *seatmappb.AvailabilitySnapshot) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(snapshot.GetShowtimeId()))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(snapshot.GetAuditoriumId()))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(snapshot.GetLayoutHash()))
	for _, seat := range snapshot.GetAvailableSeats() {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(seat.GetSeatId()))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Evaluate determines whether the live set contains one adjacent group for the
// preset. Ranking-only preferences do not exclude an otherwise valid group.
func Evaluate(
	layout *seatmappb.Layout,
	preset *clientpb.Preset,
	snapshot *seatmappb.AvailabilitySnapshot,
) Match {
	if !requiresAdjacentGroup(preset, snapshot) {
		return Match{Exact: layout != nil}
	}
	available := availableSeatIDs(snapshot)
	if layout == nil || len(layout.GetSeats()) == 0 {
		return Match{Available: len(available) >= int(preset.GetSeatCount())}
	}

	rows, exact := availableSeatRows(layout, available, explicitSeatLabels(preset))
	if !exact {
		return Match{Available: len(available) >= int(preset.GetSeatCount())}
	}
	if rowsHaveAdjacentGroup(rows, int(preset.GetSeatCount())) {
		return Match{Available: true, Exact: true}
	}
	return Match{Exact: true}
}

// requiresAdjacentGroup reports whether the preset needs layout-aware matching.
func requiresAdjacentGroup(preset *clientpb.Preset, snapshot *seatmappb.AvailabilitySnapshot) bool {
	return preset != nil && snapshot != nil && preset.GetSeatCount() > 0 &&
		preset.GetSeatPreference() != nil && preset.GetSeatPreference().GetTogether()
}

// availableSeatIDs indexes the normalized live seats by provider identity.
func availableSeatIDs(snapshot *seatmappb.AvailabilitySnapshot) map[string]struct{} {
	available := make(map[string]struct{}, len(snapshot.GetAvailableSeats()))
	for _, seat := range snapshot.GetAvailableSeats() {
		available[seat.GetSeatId()] = struct{}{}
	}
	return available
}

// explicitSeatLabels indexes the user's optional exact-seat filter.
func explicitSeatLabels(preset *clientpb.Preset) map[string]struct{} {
	explicit := make(map[string]struct{}, len(preset.GetSeatPreference().GetExplicitSeats()))
	for _, label := range preset.GetSeatPreference().GetExplicitSeats() {
		explicit[strings.TrimSpace(label)] = struct{}{}
	}
	return explicit
}

// availableSeatRows groups known live seats by row and reports identity coverage.
func availableSeatRows(
	layout *seatmappb.Layout,
	available map[string]struct{},
	explicit map[string]struct{},
) (map[string][]*seatmappb.Seat, bool) {
	rows := make(map[string][]*seatmappb.Seat)
	known := 0
	for _, seat := range layout.GetSeats() {
		if seat == nil {
			continue
		}
		if _, ok := available[seat.GetId()]; !ok {
			continue
		}
		known++
		if len(explicit) > 0 {
			if _, ok := explicit[seat.GetLabel()]; !ok {
				continue
			}
		}
		rows[seat.GetRow()] = append(rows[seat.GetRow()], seat)
	}
	return rows, known == len(available)
}

// rowsHaveAdjacentGroup checks each row after sorting seats by physical number.
func rowsHaveAdjacentGroup(rows map[string][]*seatmappb.Seat, count int) bool {
	for _, row := range rows {
		slices.SortFunc(row, func(left, right *seatmappb.Seat) int {
			return int(left.GetNumber() - right.GetNumber())
		})
		if hasAdjacentGroup(row, count) {
			return true
		}
	}
	return false
}

func hasAdjacentGroup(row []*seatmappb.Seat, count int) bool {
	for start := 0; start+count <= len(row); start++ {
		valid := true
		for index := start + 1; index < start+count; index++ {
			previous, current := row[index-1], row[index]
			if current.GetNumber() != previous.GetNumber()+1 || previous.GetRightAisle() || current.GetLeftAisle() {
				valid = false
				break
			}
		}
		if valid {
			return true
		}
	}
	return false
}
