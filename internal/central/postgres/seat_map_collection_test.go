package postgres

import (
	"testing"

	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
)

func TestSeatMapCollectionTransitionsRequireObjectiveReopen(t *testing.T) {
	clientTrigger := (&collectionpb.Trigger_builder{
		ClientRequest: (&collectionpb.ClientRequest_builder{}).Build(),
	}).Build()
	operatorTrigger := (&collectionpb.Trigger_builder{
		OperatorRequest: (&collectionpb.OperatorRequest_builder{}).Build(),
	}).Build()
	queued := (&collectionpb.State_builder{Queued: (&collectionpb.Queued_builder{}).Build()}).Build()
	collecting := (&collectionpb.State_builder{Collecting: (&collectionpb.Collecting_builder{}).Build()}).Build()
	waiting := (&collectionpb.State_builder{
		WaitingForShowtime: (&collectionpb.WaitingForShowtime_builder{}).Build(),
	}).Build()
	blocked := (&collectionpb.State_builder{Blocked: (&collectionpb.Blocked_builder{}).Build()}).Build()

	for name, test := range map[string]struct {
		from    *collectionpb.State
		to      *collectionpb.State
		trigger *collectionpb.Trigger
		valid   bool
	}{
		"idle to queued":              {from: nil, to: queued, trigger: clientTrigger, valid: true},
		"idle to collecting":          {from: nil, to: collecting, trigger: clientTrigger, valid: false},
		"queued to collecting":        {from: queued, to: collecting, trigger: clientTrigger, valid: true},
		"collecting to waiting":       {from: collecting, to: waiting, trigger: clientTrigger, valid: true},
		"blocked client reopen":       {from: blocked, to: queued, trigger: clientTrigger, valid: false},
		"blocked operator reopen":     {from: blocked, to: queued, trigger: operatorTrigger, valid: true},
		"blocked operator collecting": {from: blocked, to: collecting, trigger: operatorTrigger, valid: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := validSeatMapCollectionTransition(test.from, test.to, test.trigger); got != test.valid {
				t.Fatalf("validSeatMapCollectionTransition() = %t, want %t", got, test.valid)
			}
		})
	}
}

func TestSeatMapCollectionFailureReasonsDefineRetryPolicy(t *testing.T) {
	transport := &collectionpb.FailureReason{}
	transport.SetProviderTransportFailed(&collectionpb.ProviderTransportFailed{})
	identity := &collectionpb.FailureReason{}
	identity.SetIdentityMismatch(&collectionpb.IdentityMismatch{})

	if code, ok := seatMapCollectionFailureReasonCode(transport); !ok || code != "provider_transport_failed" {
		t.Fatalf("transport reason = %q, %t", code, ok)
	}
	if !seatMapCollectionRetryableFailureReason(transport) {
		t.Fatal("provider transport failure is not retryable")
	}
	if code, ok := seatMapCollectionFailureReasonCode(identity); !ok || code != "identity_mismatch" {
		t.Fatalf("identity reason = %q, %t", code, ok)
	}
	if seatMapCollectionRetryableFailureReason(identity) {
		t.Fatal("identity mismatch is retryable")
	}
}
