package domain

import (
	"testing"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
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
	preset := validPresetProto()
	zone := &clientpb.SeatZone{}
	zone.SetName("bad")
	zone.SetMinX(.8)
	zone.SetMaxX(.2)
	zone.SetMaxY(1)
	preset.GetSeatPreference().SetPreferredZones([]*clientpb.SeatZone{zone})
	if err := ValidatePreset(preset, nil); err == nil {
		t.Fatal("ValidatePreset() accepted reversed zone bounds")
	}
	preset.GetSeatPreference().SetPreferredZones(nil)
	preset.GetSeatPreference().SetPreferredTypes([]string{"made-up"})
	if err := ValidatePreset(preset, nil); err == nil {
		t.Fatal("ValidatePreset() accepted an unknown seat type")
	}
}
