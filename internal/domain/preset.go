package domain

import (
	"errors"
	"fmt"
	"strings"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
)

// ValidatePreset enforces Central's domain invariants on the canonical Proto preset.
func ValidatePreset(preset *clientpb.Preset) error {
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
	return nil
}

func validateSeatPreference(preference *clientpb.SeatPreference) error {
	if preference == nil {
		return nil
	}
	if err := validateSeatLabels(preference.GetExplicitSeats(), "explicit seat labels"); err != nil {
		return err
	}
	if err := validateSeatLabels(preference.GetPreferredRows(), "preferred row labels"); err != nil {
		return err
	}
	if err := validatePreferredZones(preference.GetPreferredZones()); err != nil {
		return err
	}
	return validatePreferredSeatTypes(preference.GetPreferredTypes())
}

func validateSeatLabels(labels []string, description string) error {
	for _, label := range labels {
		if strings.TrimSpace(label) == "" {
			return fmt.Errorf("%s must not be empty", description)
		}
	}
	return nil
}

func validatePreferredZones(zones []*clientpb.SeatZone) error {
	for _, zone := range zones {
		if zone == nil {
			return errors.New("seat preference zones must not be nil")
		}
		if strings.TrimSpace(zone.GetName()) == "" {
			return errors.New("seat preference zone name is required")
		}
		if zone.GetMinX() < 0 || zone.GetMaxX() > 1 || zone.GetMinY() < 0 || zone.GetMaxY() > 1 ||
			zone.GetMinX() > zone.GetMaxX() || zone.GetMinY() > zone.GetMaxY() {
			return fmt.Errorf("seat preference zone %s bounds must be ordered within 0..1", zone.GetName())
		}
	}
	return nil
}

func validatePreferredSeatTypes(preferredTypes []string) error {
	for _, preferredType := range preferredTypes {
		if !SeatType(preferredType).Valid() {
			return fmt.Errorf("unknown preferred seat type %q", preferredType)
		}
	}
	return nil
}
