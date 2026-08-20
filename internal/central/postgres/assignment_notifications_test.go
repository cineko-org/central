package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cineko-org/central/internal/central"
	"github.com/cineko-org/central/internal/central/reconcile"
	"github.com/cineko-org/central/internal/observation/planning"
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
	raw, err := marshalAssignmentTask(reconcile.NewAssignment{
		Lane:                 planning.LaneHot,
		HotTargetFingerprint: "fingerprint",
		Task:                 central.AssignmentTask{Kind: "cgv.schedule.capture.v2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var lane planning.Lane
	if err := json.Unmarshal(fields[planning.TaskDataLaneKey], &lane); err != nil {
		t.Fatal(err)
	}
	if lane != planning.LaneHot {
		t.Fatalf("persisted lane = %q", lane)
	}
	var fingerprint string
	if err := json.Unmarshal(fields[planning.TaskDataHotFingerprintKey], &fingerprint); err != nil {
		t.Fatal(err)
	}
	if fingerprint != "fingerprint" {
		t.Fatalf("persisted hot fingerprint = %q", fingerprint)
	}
	var task central.AssignmentTask
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	if task.Kind != "cgv.schedule.capture.v2" {
		t.Fatalf("task kind = %q", task.Kind)
	}
}
