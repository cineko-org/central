package domain

import (
	"testing"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
)

func validPresetProto() *clientpb.Preset {
	preset := &clientpb.Preset{}
	preset.SetId("preset-1")
	preset.SetUserId("user-1")
	preset.SetName("center")
	preset.SetTheaterId("theater-1")
	preset.SetAuditoriumId("auditorium-1")
	preset.SetSeatCount(1)
	preset.SetSeatPreference(&clientpb.SeatPreference{})
	return preset
}

func TestValidatePresetRejectsInvalidPreferenceData(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*clientpb.SeatPreference){
		"empty explicit seat": func(preference *clientpb.SeatPreference) {
			preference.SetExplicitSeats([]string{" "})
		},
		"empty preferred row": func(preference *clientpb.SeatPreference) {
			preference.SetExplicitSeats([]string{"H10"})
			preference.SetPreferredRows([]string{" "})
		},
		"nil preferred zone": func(preference *clientpb.SeatPreference) {
			preference.SetPreferredZones([]*clientpb.SeatZone{nil})
		},
		"unnamed preferred zone": func(preference *clientpb.SeatPreference) {
			preference.SetPreferredZones([]*clientpb.SeatZone{{}})
		},
		"reversed zone bounds": func(preference *clientpb.SeatPreference) {
			zone := &clientpb.SeatZone{}
			zone.SetName("bad")
			zone.SetMinX(.8)
			zone.SetMaxX(.2)
			zone.SetMaxY(1)
			preference.SetPreferredZones([]*clientpb.SeatZone{zone})
		},
		"unknown seat type": func(preference *clientpb.SeatPreference) {
			preference.SetPreferredTypes([]string{"made-up"})
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			preset := validPresetProto()
			mutate(preset.GetSeatPreference())
			if err := ValidatePreset(preset); err == nil {
				t.Fatalf("ValidatePreset() accepted %s", name)
			}
		})
	}
}
