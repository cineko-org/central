package domain

import (
	"errors"
	"fmt"
	"strings"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
)

// ValidatePreset enforces Central's domain invariants on the canonical Proto preset.
func ValidatePreset(preset *clientpb.Preset, seatMap *SeatMap) error {
	if preset == nil {
		return errors.New("preset is required")
	}
	if preset.GetId() == "" || preset.GetUserId() == "" {
		return errors.New("preset id and user id are required")
	}
	if strings.TrimSpace(preset.GetName()) == "" {
		return errors.New("preset name is required")
	}
	if preset.GetTheaterId() == "" || preset.GetAuditoriumId() == "" {
		return errors.New("preset theater and auditorium are required")
	}
	if preset.GetSeatCount() < 1 || preset.GetSeatCount() > 8 {
		return errors.New("preset seat count must be between 1 and 8")
	}
	if err := validateSeatPreference(preset.GetSeatPreference()); err != nil {
		return err
	}
	if seatMap == nil {
		return nil
	}
	if seatMap.AuditoriumID != preset.GetAuditoriumId() {
		return errors.New("preset auditorium does not match the seat map")
	}
	labels := make(map[string]struct{}, len(seatMap.Seats))
	for _, seat := range seatMap.Seats {
		labels[seat.Label] = struct{}{}
	}
	for _, label := range preset.GetSeatPreference().GetExplicitSeats() {
		if _, exists := labels[label]; !exists {
			return fmt.Errorf("preferred seat %s does not exist in auditorium", label)
		}
	}
	return nil
}

func validateSeatPreference(preference *clientpb.SeatPreference) error {
	if preference == nil {
		return nil
	}
	for _, zone := range preference.GetPreferredZones() {
		if strings.TrimSpace(zone.GetName()) == "" {
			return errors.New("seat preference zone name is required")
		}
		if zone.GetMinX() < 0 || zone.GetMaxX() > 1 || zone.GetMinY() < 0 || zone.GetMaxY() > 1 ||
			zone.GetMinX() > zone.GetMaxX() || zone.GetMinY() > zone.GetMaxY() {
			return fmt.Errorf("seat preference zone %s bounds must be ordered within 0..1", zone.GetName())
		}
	}
	for _, preferredType := range preference.GetPreferredTypes() {
		if !SeatType(preferredType).Valid() {
			return fmt.Errorf("unknown preferred seat type %q", preferredType)
		}
	}
	return nil
}

func seatZoneContains(zone *clientpb.SeatZone, x, y float64) bool {
	return zone != nil && x >= zone.GetMinX() && x <= zone.GetMaxX() && y >= zone.GetMinY() && y <= zone.GetMaxY()
}
