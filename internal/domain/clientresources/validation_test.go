package clientresources

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/domain"
)

func TestValidatePayloads(t *testing.T) {
	t.Parallel()
	preset := domain.Preset{
		ID: "preset", UserID: "user", Name: "Preset", TheaterID: "theater",
		AuditoriumID: "auditorium", SeatCount: 1,
	}
	if err := ValidatePayload("user", "presets", "preset", marshalResource(t, preset)); err != nil {
		t.Fatalf("preset validation = %v", err)
	}
	monitor := domain.MonitorJob{
		ID: "monitor", UserID: "user", PresetID: "preset", MovieID: "movie_1", Movie: "Movie",
		TargetDates: []string{"2026-08-12"}, PollInterval: 2 * time.Second,
		PollIntervalMax: 3 * time.Second, Status: domain.MonitorPending,
	}
	if err := ValidatePayload("user", "monitors", "monitor", marshalResource(t, monitor)); err != nil {
		t.Fatalf("monitor validation = %v", err)
	}
	monitor.Movie = ""
	if err := ValidatePayload("user", "monitors", "monitor", marshalResource(t, monitor)); err != nil {
		t.Fatalf("monitor validation rejected a missing display title snapshot = %v", err)
	}
	monitor.MovieID = ""
	if err := ValidatePayload("user", "monitors", "monitor", marshalResource(t, monitor)); err == nil {
		t.Fatal("monitor validation accepted a missing canonical movie id")
	}
	if err := ValidatePayload("user", "settings", "settings", json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("untyped validation = %v", err)
	}
}

func TestValidatePayloadsRejectsCorruption(t *testing.T) {
	t.Parallel()
	validPreset := json.RawMessage(`{"id":"preset","userId":"user","name":"Preset","theaterId":"theater","auditoriumId":"auditorium","seatCount":1,"seatPreference":{}}`)
	tests := []struct {
		kind    string
		id      string
		payload json.RawMessage
	}{
		{kind: "presets", id: "other", payload: validPreset},
		{kind: "presets", id: "preset", payload: json.RawMessage(`{"id":"preset","userId":"other"}`)},
		{kind: "monitors", id: "monitor", payload: json.RawMessage(`{"id":"monitor","userId":"other"}`)},
		{kind: "monitors", id: "monitor", payload: json.RawMessage(`{"unknown":true}`)},
		{kind: "monitors", id: "monitor", payload: json.RawMessage(`{"id":"monitor","userId":"user"}`)},
		{kind: "settings", id: "settings", payload: json.RawMessage(`{`)},
		{kind: "settings", id: "settings", payload: json.RawMessage(`z`)},
		{kind: "settings", id: "other", payload: json.RawMessage(`{}`)},
		{kind: "settings", id: "settings", payload: json.RawMessage(`null`)},
		{kind: "settings", id: "settings", payload: json.RawMessage(`[]`)},
	}
	for _, test := range tests {
		if err := ValidatePayload("user", test.kind, test.id, test.payload); err == nil {
			t.Fatalf("corrupt %s payload was accepted: %s", test.kind, test.payload)
		}
	}
	if err := ValidatePayload(
		"user", "presets", "preset", append(validPreset, []byte(` {}`)...),
	); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
}

func marshalResource(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
