package clientresources

import (
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestValidatePayloadsUseCanonicalProto(t *testing.T) {
	t.Parallel()
	preset := validPresetPayload()
	if err := ValidatePayload("user", "presets", "preset", marshalProto(t, preset)); err != nil {
		t.Fatalf("preset validation = %v", err)
	}
	monitor := validMonitorPayload()
	if err := ValidatePayload("user", "monitors", "monitor", marshalProto(t, monitor)); err != nil {
		t.Fatalf("monitor validation = %v", err)
	}
	monitor.SetMovieId("")
	if err := ValidatePayload("user", "monitors", "monitor", marshalProto(t, monitor)); err == nil {
		t.Fatal("monitor validation accepted a missing movie identity")
	}
	if err := ValidatePayload("user", "settings", "settings", marshalProto(t, &clientpb.Settings{})); err != nil {
		t.Fatalf("settings validation = %v", err)
	}
}

func TestValidatePayloadsRejectCorruption(t *testing.T) {
	t.Parallel()
	preset := validPresetPayload()
	if err := ValidatePayload("user", "presets", "other", marshalProto(t, preset)); err == nil {
		t.Fatal("mismatched preset identity was accepted")
	}
	preset.SetUserId("other")
	if err := ValidatePayload("user", "presets", "preset", marshalProto(t, preset)); err == nil {
		t.Fatal("mismatched preset owner was accepted")
	}
	if err := ValidatePayload("user", "monitors", "monitor", []byte(`{"unknown":true}`)); err == nil {
		t.Fatal("unknown ProtoJSON field was accepted")
	}
	if err := ValidatePayload("user", "settings", "settings", []byte(`{`)); err == nil {
		t.Fatal("malformed ProtoJSON was accepted")
	}
	if err := ValidatePayload("user", "settings", "other", marshalProto(t, &clientpb.Settings{})); err == nil {
		t.Fatal("mismatched settings identity was accepted")
	}
	if err := ValidatePayload("user", "unknown-kind", "resource", []byte(`{}`)); err == nil {
		t.Fatal("unsupported resource kind was accepted")
	}
}

func validPresetPayload() *clientpb.Preset {
	preset := &clientpb.Preset{}
	preset.SetId("preset")
	preset.SetUserId("user")
	preset.SetName("Preset")
	preset.SetTheaterId("theater")
	preset.SetAuditoriumId("auditorium")
	preset.SetSeatCount(1)
	preset.SetSeatPreference(&clientpb.SeatPreference{})
	return preset
}

func validMonitorPayload() *clientpb.Monitor {
	mode := &clientpb.MonitorMode{}
	mode.SetOpening(&clientpb.OpeningMonitor{})
	state := &clientpb.MonitorState{}
	state.SetPending(&clientpb.MonitorPending{})
	date := &commonpb.LocalDate{}
	date.SetYear(2026)
	date.SetMonth(8)
	date.SetDay(12)
	monitor := &clientpb.Monitor{}
	monitor.SetId("monitor")
	monitor.SetUserId("user")
	monitor.SetPresetId("preset")
	monitor.SetMode(mode)
	monitor.SetMovieId("movie_1")
	monitor.SetTargetDates([]*commonpb.LocalDate{date})
	monitor.SetPollInterval(durationpb.New(2 * time.Second))
	monitor.SetMaximumPollInterval(durationpb.New(3 * time.Second))
	monitor.SetState(state)
	return monitor
}

func marshalProto(t *testing.T, value proto.Message) []byte {
	t.Helper()
	payload, err := protojson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
