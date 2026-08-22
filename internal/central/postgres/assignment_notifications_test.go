package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cineko-org/central/internal/central/reconcile"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	"github.com/cineko-org/central/internal/observation/planning"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestAssignmentNotificationContractCoversClaimEligibility(t *testing.T) {
	for _, fragment := range []string{
		"probe.status = 'online'",
		"NOT probe.draining",
		"probe.health = 'healthy'",
		"probe.available_slots > 0",
		"assignment.status = 'queued'",
		"assignment.not_before <= CURRENT_TIMESTAMP",
		"assignment.deadline > CURRENT_TIMESTAMP",
		"assignment.task_kind = ANY(probe.available_capabilities)",
		"eligible.network_id = probe.network_id",
		"assignment_attempts",
		"active.status = 'leased'",
	} {
		if !strings.Contains(assignmentWakeQuery, fragment) {
			t.Errorf("assignment wake query is missing %q", fragment)
		}
	}
	if assignmentNotifyChannel == "" || !strings.Contains(assignmentNotifyChannel, "assignment") {
		t.Fatalf("assignment notification channel = %q", assignmentNotifyChannel)
	}
}

func TestMarshalAssignmentTaskPersistsLaneWithoutChangingTaskShape(t *testing.T) {
	theater := &catalogpb.Theater{}
	theater.SetId("theater")
	theater.SetProviderId("cgv")
	catalogdomain.SetTheaterSourceKey(theater, "0056")
	theater.SetRegion("서울")
	theater.SetName("용산아이파크몰")
	schedule := &observationpb.ScheduleTask{}
	schedule.SetTheater(theater)
	schedule.SetTargetDates([]*commonpb.LocalDate{{}})
	schedule.GetTargetDates()[0].SetYear(2026)
	schedule.GetTargetDates()[0].SetMonth(8)
	schedule.GetTargetDates()[0].SetDay(22)
	schedule.SetLocale("ko-KR")
	schedule.SetTimeZone("Asia/Seoul")
	task := &observationpb.AssignmentTask{}
	egress := &commonpb.EgressPolicy{}
	egress.SetManagedScan(&commonpb.ManagedScanEgress{})
	task.SetEgress(egress)
	task.SetSchedule(schedule)
	raw, err := marshalAssignmentTask(reconcile.NewAssignment{
		Lane:                 planning.LaneHot,
		HotTargetFingerprint: "fingerprint",
		Task:                 task,
	})
	if err != nil {
		t.Fatal(err)
	}
	var persisted observationpb.AssignmentTask
	if err := protojson.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.GetSchedule() == nil || persisted.GetSchedule().GetTheater().GetId() != "theater" {
		t.Fatalf("persisted task = %s", raw)
	}
	if persisted.GetEgress().GetManagedScan() == nil {
		t.Fatalf("persisted egress = %s", raw)
	}
	var taskShape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &taskShape); err != nil {
		t.Fatal(err)
	}
	if _, exists := taskShape[string(planning.TaskDataLaneKey)]; exists {
		t.Fatalf("persisted task unexpectedly contains lane metadata: %s", raw)
	}
	if _, exists := taskShape[string(planning.TaskDataHotFingerprintKey)]; exists {
		t.Fatalf("persisted task unexpectedly contains fingerprint metadata: %s", raw)
	}
}
