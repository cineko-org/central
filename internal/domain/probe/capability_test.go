package probe

import (
	"reflect"
	"testing"

	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
)

func TestCapabilityKeysCanonicalizesOrder(t *testing.T) {
	t.Parallel()

	seatMap := &observationpb.Capability{}
	seatMap.SetSeatMapCapture(&observationpb.SeatMapCapture{})
	schedule := &observationpb.Capability{}
	schedule.SetScheduleCapture(&observationpb.ScheduleCapture{})
	catalog := &observationpb.Capability{}
	catalog.SetCatalogCapture(&observationpb.CatalogCapture{})
	availability := &observationpb.Capability{}
	availability.SetSeatAvailabilityCapture(&observationpb.SeatAvailabilityCapture{})

	keys, err := CapabilityKeys([]*observationpb.Capability{seatMap, availability, schedule, catalog})
	if err != nil {
		t.Fatalf("CapabilityKeys() error = %v", err)
	}
	want := []string{
		CapabilityCGVCatalogCapture,
		CapabilityCGVScheduleCapture,
		CapabilityCGVSeatAvailabilityCapture,
		CapabilityCGVSeatMapCapture,
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("CapabilityKeys() = %v, want %v", keys, want)
	}
}
