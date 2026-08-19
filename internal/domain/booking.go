package domain

import "time"

type Showtime struct {
	ID             string    `json:"id"`
	Movie          string    `json:"movie"`
	PosterURL      string    `json:"posterUrl,omitempty"`
	TheaterID      string    `json:"theaterId"`
	TheaterName    string    `json:"theaterName"`
	AuditoriumID   string    `json:"auditoriumId"`
	AuditoriumName string    `json:"auditoriumName"`
	ScreenTypes    []string  `json:"screenTypes"`
	Date           string    `json:"date"`
	StartsAt       string    `json:"startsAt"`
	EndsAt         string    `json:"endsAt"`
	AvailableSeats int       `json:"availableSeats"`
	Capacity       int       `json:"capacity"`
	SoldOut        bool      `json:"soldOut"`
	ObservedAt     time.Time `json:"observedAt"`
	SourceLabel    string    `json:"sourceLabel"`
}

type LiveSeat struct {
	Label        string    `json:"label"`
	Available    bool      `json:"available"`
	StatusCode   string    `json:"statusCode"`
	StatusName   string    `json:"statusName"`
	SaleFormCode string    `json:"saleFormCode"`
	ObservedAt   time.Time `json:"observedAt"`
	Source       string    `json:"source"`
}

type BookingDraft struct {
	Showtime   Showtime `json:"showtime"`
	SeatLabels []string `json:"seatLabels"`
	TotalPrice string   `json:"totalPrice"`
}

type Reservation struct {
	ID            string       `json:"id"`
	UserID        string       `json:"userId"`
	MonitorID     string       `json:"monitorId"`
	BookingNumber string       `json:"bookingNumber"`
	Draft         BookingDraft `json:"draft"`
	Status        string       `json:"status"`
	BookedAt      time.Time    `json:"bookedAt"`
	CancelledAt   *time.Time   `json:"cancelledAt"`
	RefundAmount  string       `json:"refundAmount"`
}

type CancellationDraft struct {
	ReservationID string `json:"reservationId"`
	BookingNumber string `json:"bookingNumber"`
	RefundAmount  string `json:"refundAmount"`
}
