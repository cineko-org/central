package domain

import (
	"strings"
	"testing"
	"time"
)

func TestSeatMapValidateRequiresMatchingVisualAndSnapshotEvidence(t *testing.T) {
	t.Parallel()

	seatMap := validSeatMap()
	if err := seatMap.Validate(); err != nil {
		t.Fatalf("valid seat map rejected: %v", err)
	}

	seatMap.Evidence.ScreenshotSHA256 = ""
	if err := seatMap.Validate(); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("missing screenshot hash error = %v", err)
	}

	seatMap = validSeatMap()
	seatMap.Evidence.DOMSeatCount = 2
	if err := seatMap.Validate(); err == nil || !strings.Contains(err.Error(), "counts") {
		t.Fatalf("mismatched count error = %v", err)
	}
}

func TestAnalyzeSeatMapCountsTheaterSpecificTypesAndZones(t *testing.T) {
	t.Parallel()

	seatMap := validSeatMap()
	seatMap.Seats = append(seatMap.Seats, Seat{
		ID: "seat-2", AuditoriumID: "auditorium-1", Label: "A2", Row: "A", Number: 2,
		X: 0.6, Y: 0.3, Type: SeatTypeWheelchair, ZoneName: "Light존", SaleFormName: "이동식",
	})
	analysis := AnalyzeSeatMap(seatMap)
	if analysis.SeatTypes[SeatTypeWheelchair] != 1 || analysis.Zones["Light존"] != 1 || analysis.SaleForms["이동식"] != 1 {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func validSeatMap() SeatMap {
	return SeatMap{
		AuditoriumID: "auditorium-1", Version: "version-1",
		Seats: []Seat{{
			ID: "seat-1", AuditoriumID: "auditorium-1", Label: "A1", Row: "A", Number: 1,
			X: 0.4, Y: 0.3, Type: SeatTypeStandard, ZoneName: "일반존", SaleFormName: "일반",
		}},
		Evidence: LayoutEvidence{
			ScreenshotPath: "/tmp/layout.png", ScreenshotSHA256: "screen-hash",
			SnapshotSHA256: "snapshot-hash", DOMSeatCount: 1, SnapshotSeatCount: 1,
			SourceShowtimeID: "showtime-1", CaptureTrigger: "refresh-button", CapturedAt: time.Now(),
		},
	}
}
