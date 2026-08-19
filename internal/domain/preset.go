package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type SeatZone struct {
	Name   string  `json:"name"`
	MinX   float64 `json:"minX"`
	MaxX   float64 `json:"maxX"`
	MinY   float64 `json:"minY"`
	MaxY   float64 `json:"maxY"`
	Weight int     `json:"weight"`
}

func (zone SeatZone) Contains(x, y float64) bool {
	return x >= zone.MinX && x <= zone.MaxX && y >= zone.MinY && y <= zone.MaxY
}

func (zone SeatZone) Validate() error {
	if strings.TrimSpace(zone.Name) == "" {
		return errors.New("seat preference zone name is required")
	}
	if zone.MinX < 0 || zone.MaxX > 1 || zone.MinY < 0 || zone.MaxY > 1 ||
		zone.MinX > zone.MaxX || zone.MinY > zone.MaxY {
		return fmt.Errorf("seat preference zone %s bounds must be ordered within 0..1", zone.Name)
	}
	return nil
}

type SeatPreference struct {
	ExplicitSeats  []string   `json:"explicitSeats"`
	PreferredRows  []string   `json:"preferredRows"`
	PreferredZones []SeatZone `json:"preferredZones"`
	PreferredTypes []SeatType `json:"preferredTypes"`
	Together       bool       `json:"together"`
	AvoidEdges     bool       `json:"avoidEdges"`
}

type Preset struct {
	ID             string         `json:"id"`
	UserID         string         `json:"userId"`
	Name           string         `json:"name"`
	TheaterID      string         `json:"theaterId"`
	AuditoriumID   string         `json:"auditoriumId"`
	SeatCount      int            `json:"seatCount"`
	SeatPreference SeatPreference `json:"seatPreference"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

func (preset Preset) Validate(seatMap *SeatMap) error {
	if err := preset.validateIdentity(); err != nil {
		return err
	}
	if err := preset.SeatPreference.Validate(); err != nil {
		return err
	}
	if seatMap == nil {
		return nil
	}
	return preset.validateSeatMap(*seatMap)
}

func (preset Preset) validateIdentity() error {
	if preset.ID == "" || preset.UserID == "" {
		return errors.New("preset id and user id are required")
	}
	if strings.TrimSpace(preset.Name) == "" {
		return errors.New("preset name is required")
	}
	if preset.TheaterID == "" || preset.AuditoriumID == "" {
		return errors.New("preset theater and auditorium are required")
	}
	if preset.SeatCount < 1 || preset.SeatCount > 8 {
		return errors.New("preset seat count must be between 1 and 8")
	}
	return nil
}

func (preference SeatPreference) Validate() error {
	for _, zone := range preference.PreferredZones {
		if err := zone.Validate(); err != nil {
			return err
		}
	}
	for _, seatType := range preference.PreferredTypes {
		if !seatType.Valid() {
			return fmt.Errorf("unknown preferred seat type %q", seatType)
		}
	}
	return nil
}

func (preset Preset) validateSeatMap(seatMap SeatMap) error {
	if seatMap.AuditoriumID != preset.AuditoriumID {
		return errors.New("preset auditorium does not match the seat map")
	}
	labels := make(map[string]struct{}, len(seatMap.Seats))
	for _, seat := range seatMap.Seats {
		labels[seat.Label] = struct{}{}
	}
	for _, label := range preset.SeatPreference.ExplicitSeats {
		if _, exists := labels[label]; !exists {
			return fmt.Errorf("preferred seat %s does not exist in auditorium", label)
		}
	}
	return nil
}

func (preset Preset) Owns(userID string) bool {
	return preset.UserID == userID
}

func (preference SeatPreference) TypeRank(seatType SeatType) int {
	index := slices.Index(preference.PreferredTypes, seatType)
	if index < 0 {
		return len(preference.PreferredTypes) + 1
	}
	return index
}
