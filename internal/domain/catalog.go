package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type SeatType string

const (
	SeatTypeStandard   SeatType = "standard"
	SeatTypeWheelchair SeatType = "wheelchair"
	SeatTypeCompanion  SeatType = "companion"
	SeatTypeCouple     SeatType = "couple"
	SeatTypeRecliner   SeatType = "recliner"
	SeatTypeMotion     SeatType = "motion"
	SeatTypeBed        SeatType = "bed"
	SeatTypeUnknown    SeatType = "unknown"
)

func (seatType SeatType) Valid() bool {
	switch seatType {
	case SeatTypeStandard, SeatTypeWheelchair, SeatTypeCompanion, SeatTypeCouple,
		SeatTypeRecliner, SeatTypeMotion, SeatTypeBed, SeatTypeUnknown:
		return true
	default:
		return false
	}
}

type Theater struct {
	ID         string    `json:"id"`
	Region     string    `json:"region"`
	Name       string    `json:"name"`
	SourceKey  string    `json:"sourceKey"`
	ObservedAt time.Time `json:"observedAt"`
}

func (theater Theater) Validate() error {
	if strings.TrimSpace(theater.ID) == "" {
		return errors.New("theater id is required")
	}
	if strings.TrimSpace(theater.Region) == "" {
		return errors.New("theater region is required")
	}
	if strings.TrimSpace(theater.Name) == "" {
		return errors.New("theater name is required")
	}
	return nil
}

type Auditorium struct {
	ID             string    `json:"id"`
	TheaterID      string    `json:"theaterId"`
	Name           string    `json:"name"`
	ScreenTypes    []string  `json:"screenTypes"`
	Capacity       int       `json:"capacity"`
	SeatMapVersion string    `json:"seatMapVersion"`
	ObservedAt     time.Time `json:"observedAt"`
}

func (auditorium Auditorium) Validate() error {
	if auditorium.ID == "" || auditorium.TheaterID == "" || strings.TrimSpace(auditorium.Name) == "" {
		return errors.New("auditorium id, theater id, and name are required")
	}
	if auditorium.Capacity < 0 {
		return errors.New("auditorium capacity cannot be negative")
	}
	return nil
}

type Seat struct {
	ID                 string   `json:"id"`
	AuditoriumID       string   `json:"auditoriumId"`
	Label              string   `json:"label"`
	Row                string   `json:"row"`
	Number             int      `json:"number"`
	X                  float64  `json:"x"`
	Y                  float64  `json:"y"`
	Type               SeatType `json:"type"`
	ZoneName           string   `json:"zoneName"`
	ZoneKind           string   `json:"zoneKind"`
	SaleFormCode       string   `json:"saleFormCode"`
	SaleFormName       string   `json:"saleFormName"`
	LeftAisle          bool     `json:"leftAisle"`
	RightAisle         bool     `json:"rightAisle"`
	Features           []string `json:"features"`
	SourceLabel        string   `json:"sourceLabel"`
	SourceSeatKindCode string   `json:"sourceSeatKindCode"`
	SourceSeatKindName string   `json:"sourceSeatKindName"`
	SourceClasses      []string `json:"sourceClasses,omitempty"`
}

func (seat Seat) Validate() error {
	if seat.ID == "" || seat.AuditoriumID == "" || seat.Label == "" || seat.Row == "" {
		return errors.New("seat id, auditorium id, label, and row are required")
	}
	if seat.Number < 1 {
		return errors.New("seat number must be positive")
	}
	if seat.X < 0 || seat.X > 1 || seat.Y < 0 || seat.Y > 1 {
		return fmt.Errorf("seat %s position must be normalized to 0..1", seat.Label)
	}
	return nil
}

type SeatMap struct {
	AuditoriumID string         `json:"auditoriumId"`
	Version      string         `json:"version"`
	Seats        []Seat         `json:"seats"`
	Zones        []LayoutZone   `json:"zones"`
	Blocks       []LayoutBlock  `json:"blocks"`
	Evidence     LayoutEvidence `json:"evidence"`
	ObservedAt   time.Time      `json:"observedAt"`
}

type LayoutZone struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	KindCode string  `json:"kindCode"`
	KindName string  `json:"kindName"`
	MinX     float64 `json:"minX"`
	MaxX     float64 `json:"maxX"`
	MinY     float64 `json:"minY"`
	MaxY     float64 `json:"maxY"`
	Capacity int     `json:"capacity"`
}

type LayoutBlock struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	KindCode string  `json:"kindCode"`
	KindName string  `json:"kindName"`
	MinX     float64 `json:"minX"`
	MaxX     float64 `json:"maxX"`
	MinY     float64 `json:"minY"`
	MaxY     float64 `json:"maxY"`
}

func (zone LayoutZone) Validate() error {
	if strings.TrimSpace(zone.Code) == "" || strings.TrimSpace(zone.Name) == "" {
		return errors.New("layout zone code and name are required")
	}
	if zone.Capacity < 0 || !normalizedBounds(zone.MinX, zone.MaxX, zone.MinY, zone.MaxY) {
		return fmt.Errorf("layout zone %s has invalid bounds or capacity", zone.Name)
	}
	return nil
}

func (block LayoutBlock) Validate() error {
	if strings.TrimSpace(block.Code) == "" || strings.TrimSpace(block.Name) == "" {
		return errors.New("layout block code and name are required")
	}
	if !normalizedBounds(block.MinX, block.MaxX, block.MinY, block.MaxY) {
		return fmt.Errorf("layout block %s has invalid bounds", block.Name)
	}
	return nil
}

func normalizedBounds(minX, maxX, minY, maxY float64) bool {
	return minX >= 0 && maxX <= 1 && minY >= 0 && maxY <= 1 && minX <= maxX && minY <= maxY
}

type LayoutEvidence struct {
	ScreenshotPath    string    `json:"screenshotPath"`
	ScreenshotSHA256  string    `json:"screenshotSha256"`
	SnapshotSHA256    string    `json:"snapshotSha256"`
	SourceShowtimeID  string    `json:"sourceShowtimeId"`
	DOMSeatCount      int       `json:"domSeatCount"`
	SnapshotSeatCount int       `json:"snapshotSeatCount"`
	CaptureTrigger    string    `json:"captureTrigger"`
	CapturedAt        time.Time `json:"capturedAt"`
}

func (seatMap SeatMap) Validate() error {
	if seatMap.AuditoriumID == "" || seatMap.Version == "" {
		return errors.New("seat map auditorium id and version are required")
	}
	if len(seatMap.Seats) == 0 {
		return errors.New("seat map must contain seats")
	}
	if err := seatMap.Evidence.Validate(len(seatMap.Seats)); err != nil {
		return err
	}
	if err := seatMap.validateSeats(); err != nil {
		return err
	}
	return seatMap.validateLayout()
}

func (evidence LayoutEvidence) Validate(seatCount int) error {
	required := []string{
		evidence.ScreenshotPath,
		evidence.ScreenshotSHA256,
		evidence.SnapshotSHA256,
		evidence.SourceShowtimeID,
		evidence.CaptureTrigger,
	}
	for _, value := range required {
		if strings.TrimSpace(value) == "" {
			return errors.New("seat map requires screenshot and seat snapshot evidence")
		}
	}
	if evidence.CapturedAt.IsZero() {
		return errors.New("seat map requires screenshot and seat snapshot evidence")
	}
	if evidence.DOMSeatCount != seatCount || evidence.SnapshotSeatCount != seatCount {
		return errors.New("seat map evidence counts must match persisted seats")
	}
	return nil
}

func (seatMap SeatMap) validateSeats() error {
	seen := make(map[string]struct{}, len(seatMap.Seats))
	for _, seat := range seatMap.Seats {
		if err := seat.Validate(); err != nil {
			return err
		}
		if seat.AuditoriumID != seatMap.AuditoriumID {
			return fmt.Errorf("seat %s belongs to another auditorium", seat.Label)
		}
		if _, exists := seen[seat.Label]; exists {
			return fmt.Errorf("duplicate seat label: %s", seat.Label)
		}
		seen[seat.Label] = struct{}{}
	}
	return nil
}

func (seatMap SeatMap) validateLayout() error {
	for _, zone := range seatMap.Zones {
		if err := zone.Validate(); err != nil {
			return err
		}
	}
	for _, block := range seatMap.Blocks {
		if err := block.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (seatMap SeatMap) SortedSeats() []Seat {
	seats := append([]Seat(nil), seatMap.Seats...)
	sort.Slice(seats, func(i, j int) bool {
		if seats[i].Row == seats[j].Row {
			return seats[i].Number < seats[j].Number
		}
		return seats[i].Row < seats[j].Row
	})
	return seats
}

type AuditoriumAnalysis struct {
	AuditoriumID string           `json:"auditoriumId"`
	Capacity     int              `json:"capacity"`
	Rows         int              `json:"rows"`
	SeatTypes    map[SeatType]int `json:"seatTypes"`
	Zones        map[string]int   `json:"zones"`
	SaleForms    map[string]int   `json:"saleForms"`
	MinX         float64          `json:"minX"`
	MaxX         float64          `json:"maxX"`
	MinY         float64          `json:"minY"`
	MaxY         float64          `json:"maxY"`
}

func AnalyzeSeatMap(seatMap SeatMap) AuditoriumAnalysis {
	analysis := AuditoriumAnalysis{
		AuditoriumID: seatMap.AuditoriumID,
		Capacity:     len(seatMap.Seats),
		SeatTypes:    make(map[SeatType]int),
		Zones:        make(map[string]int),
		SaleForms:    make(map[string]int),
		MinX:         1,
		MinY:         1,
	}
	rows := make(map[string]struct{})
	for _, seat := range seatMap.Seats {
		rows[seat.Row] = struct{}{}
		analysis.SeatTypes[seat.Type]++
		analysis.Zones[seat.ZoneName]++
		analysis.SaleForms[seat.SaleFormName]++
		analysis.MinX = min(analysis.MinX, seat.X)
		analysis.MaxX = max(analysis.MaxX, seat.X)
		analysis.MinY = min(analysis.MinY, seat.Y)
		analysis.MaxY = max(analysis.MaxY, seat.Y)
	}
	analysis.Rows = len(rows)
	if len(seatMap.Seats) == 0 {
		analysis.MinX, analysis.MinY = 0, 0
	}
	return analysis
}
