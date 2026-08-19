package domain

import "time"

type MovieOption struct {
	Name      string `json:"name"`
	PosterURL string `json:"posterUrl,omitempty"`
}

type TheaterOption struct {
	Region string `json:"region"`
	Name   string `json:"name"`
}

type BookingCatalog struct {
	Movies     []MovieOption   `json:"movies"`
	Theaters   []TheaterOption `json:"theaters"`
	ObservedAt time.Time       `json:"observedAt"`
}
