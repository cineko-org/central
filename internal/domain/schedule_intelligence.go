package domain

import "time"

// ScheduleIntelligence is derived from Central-owned, complete Probe captures.
// Opening times are interval-censored estimates; demand values describe seat
// availability changes rather than confirmed sales.
type ScheduleIntelligence struct {
	SnapshotCount        int              `json:"snapshotCount"`
	ShowtimeObservations int              `json:"showtimeObservations"`
	LastObservedAt       time.Time        `json:"lastObservedAt,omitempty"`
	OpeningPatterns      []OpeningPattern `json:"openingPatterns"`
	DemandPatterns       []DemandPattern  `json:"demandPatterns"`
}

type OpeningPattern struct {
	TheaterID            string    `json:"theaterId"`
	TheaterName          string    `json:"theaterName"`
	AuditoriumID         string    `json:"auditoriumId"`
	AuditoriumName       string    `json:"auditoriumName"`
	Movie                string    `json:"movie"`
	ScreenTypes          []string  `json:"screenTypes"`
	SampleSize           int       `json:"sampleSize"`
	TypicalOpenTime      string    `json:"typicalOpenTime"`
	TypicalLeadHours     int       `json:"typicalLeadHours"`
	TypicalPrecisionMins int       `json:"typicalPrecisionMinutes"`
	LastObservedAt       time.Time `json:"lastObservedAt"`
}

type DemandPattern struct {
	TheaterID                   string    `json:"theaterId"`
	TheaterName                 string    `json:"theaterName"`
	AuditoriumID                string    `json:"auditoriumId"`
	AuditoriumName              string    `json:"auditoriumName"`
	Movie                       string    `json:"movie"`
	OccurrenceCount             int       `json:"occurrenceCount"`
	FirstHourSampleSize         int       `json:"firstHourSampleSize"`
	TypicalFirstHourSellThrough int       `json:"typicalFirstHourSellThrough"`
	HalfSoldSampleSize          int       `json:"halfSoldSampleSize"`
	TypicalHalfSoldMinutes      int       `json:"typicalHalfSoldMinutes"`
	SoldOutSampleSize           int       `json:"soldOutSampleSize"`
	TypicalSoldOutMinutes       int       `json:"typicalSoldOutMinutes"`
	LastObservedAt              time.Time `json:"lastObservedAt"`
}
