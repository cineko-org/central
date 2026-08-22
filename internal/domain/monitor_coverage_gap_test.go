package domain

import (
	"testing"
	"time"
)

func TestMonitorScheduleRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, location)
	start := now.Add(time.Hour)
	monitor := validMonitorProto()
	cases := []struct {
		name string
		got  bool
	}{
		{name: "nil monitor", got: MonitorMatchesSchedule(nil, start, now, location)},
		{name: "nil location", got: MonitorMatchesSchedule(monitor, start, now, nil)},
		{name: "zero start", got: MonitorMatchesSchedule(monitor, time.Time{}, now, location)},
		{name: "date nil monitor", got: MonitorMatchesScheduleDate(nil, "2026-08-10", start, now, location)},
		{name: "date nil location", got: MonitorMatchesScheduleDate(monitor, "2026-08-10", start, now, nil)},
		{name: "date zero start", got: MonitorMatchesScheduleDate(monitor, "2026-08-10", time.Time{}, now, location)},
		{name: "empty target date", got: MonitorMatchesScheduleDate(monitor, "", start, now, location)},
		{name: "target date outside monitor", got: MonitorMatchesScheduleDate(monitor, "2099-01-01", start, now, location)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if test.got {
				t.Fatal("invalid schedule input matched")
			}
		})
	}
}
