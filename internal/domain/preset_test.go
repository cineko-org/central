package domain

import "testing"

func TestPresetValidateRejectsInvalidPreferenceData(t *testing.T) {
	t.Parallel()

	preset := Preset{
		ID: "preset-1", UserID: "user-1", Name: "중앙", TheaterID: "theater-1",
		AuditoriumID: "auditorium-1", SeatCount: 1,
		SeatPreference: SeatPreference{PreferredZones: []SeatZone{{
			Name: "bad", MinX: .8, MaxX: .2, MinY: 0, MaxY: 1,
		}}},
	}
	if err := preset.Validate(nil); err == nil {
		t.Fatal("Validate() accepted reversed zone bounds")
	}

	preset.SeatPreference.PreferredZones = nil
	preset.SeatPreference.PreferredTypes = []SeatType{"made-up"}
	if err := preset.Validate(nil); err == nil {
		t.Fatal("Validate() accepted an unknown seat type")
	}
}
