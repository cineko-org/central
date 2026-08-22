package domain

// SeatType identifies the seat categories accepted by a preset preference.
// Catalog and seat-map records themselves use their generated Proto messages.
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

// Valid reports whether the value is part of the preset seat preference vocabulary.
func (seatType SeatType) Valid() bool {
	switch seatType {
	case SeatTypeStandard, SeatTypeWheelchair, SeatTypeCompanion, SeatTypeCouple,
		SeatTypeRecliner, SeatTypeMotion, SeatTypeBed, SeatTypeUnknown:
		return true
	default:
		return false
	}
}
