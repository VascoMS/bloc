package hbbft

import (
	"testing"
	"time"
)

func TestTraceRecorderBeginsAtProposalReadyAndReturnsDeepCopy(t *testing.T) {
	base := time.Unix(100, 0)
	now := base
	recorder := newTraceRecorder([]uint64{0, 1, 2, 3}, true, func() time.Time { return now })
	recorder.begin(base)

	now = base.Add(25 * time.Microsecond)
	recorder.recordAggregate(traceACSInputStarted)
	recorder.recordRBC(0, traceRBCProofAccepted)

	first := recorder.snapshot()
	if !first.Aggregate.InputStarted.Recorded || first.Aggregate.InputStarted.OffsetUS != 25 {
		t.Fatalf("input start = %+v", first.Aggregate.InputStarted)
	}
	first.RBC[0] = RBCTrace{}
	second := recorder.snapshot()
	if !second.Aggregate.InputStarted.Recorded || !second.RBC[0].ProofAccepted.Recorded {
		t.Fatal("snapshot mutation changed recorder state")
	}
}

func TestDisabledTraceRecorderReturnsDisabledEmptySnapshot(t *testing.T) {
	recorder := newTraceRecorder([]uint64{0, 1, 2, 3}, false, time.Now)
	recorder.begin(time.Now())
	recorder.recordAggregate(traceACSInputStarted)
	got := recorder.snapshot()
	if got.Enabled || got.SchemaVersion != "" || len(got.RBC) != 0 || len(got.BBA) != 0 || len(got.Messages) != 0 {
		t.Fatalf("disabled trace leaked state: %+v", got)
	}
}

func TestTraceRecorderBoundsKeysAndRecordsFirstOccurrence(t *testing.T) {
	base := time.Unix(300, 0)
	now := base
	recorder := newTraceRecorder([]uint64{2, 4, 6, 8}, true, func() time.Time { return now })
	recorder.begin(base)

	now = base.Add(10 * time.Microsecond)
	recorder.recordRBC(2, traceRBCProofAccepted)
	now = base.Add(20 * time.Microsecond)
	recorder.recordRBC(2, traceRBCProofAccepted)
	recorder.recordRBC(99, traceRBCProofAccepted)

	got := recorder.snapshot()
	if len(got.RBC) != 4 || len(got.BBA) != 4 {
		t.Fatalf("proposer bounds = rbc:%d bba:%d", len(got.RBC), len(got.BBA))
	}
	if _, ok := got.RBC[99]; ok {
		t.Fatal("unknown proposer created an RBC trace")
	}
	if got.RBC[2].ProofAccepted.OffsetUS != 10 {
		t.Fatalf("first proof offset = %d", got.RBC[2].ProofAccepted.OffsetUS)
	}
	wantSubtypes := []ACSMessageSubtype{
		ACSMessageProof,
		ACSMessageEcho,
		ACSMessageReady,
		ACSMessageBVAL,
		ACSMessageAUX,
	}
	if len(got.Messages) != len(wantSubtypes) {
		t.Fatalf("message subtype count = %d", len(got.Messages))
	}
	for _, subtype := range wantSubtypes {
		if _, ok := got.Messages[subtype]; !ok {
			t.Fatalf("missing message subtype %q", subtype)
		}
	}
}

func TestTraceRecorderIgnoresEventsBeforeItsOrigin(t *testing.T) {
	base := time.Unix(400, 0)
	now := base.Add(-time.Microsecond)
	recorder := newTraceRecorder([]uint64{0}, true, func() time.Time { return now })
	recorder.begin(base)
	recorder.recordAggregate(traceACSInputStarted)

	if got := recorder.snapshot().Aggregate.InputStarted; got.Recorded {
		t.Fatalf("negative offset was recorded: %+v", got)
	}
}
