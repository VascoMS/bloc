package hbbft

import (
	"errors"
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

func TestTraceWaitTransitionsAreExclusive(t *testing.T) {
	base := time.Unix(450, 0)
	now := base
	recorder := newTraceRecorder([]uint64{0, 1, 2, 3}, true, func() time.Time { return now })
	recorder.begin(base)
	recorder.transitionWait("waiting_for_n_minus_f_true_bba_results")
	now = base.Add(50 * time.Microsecond)
	recorder.transitionWait("waiting_for_all_bba_results")
	now = base.Add(80 * time.Microsecond)
	recorder.transitionWait("waiting_for_truthy_rbc_outputs")
	now = base.Add(100 * time.Microsecond)
	recorder.finishWait()

	got := recorder.snapshot().Wait
	if got.TrueBBAQuorumUS != 50 || got.AllBBAUS != 30 || got.TruthyRBCUS != 20 {
		t.Fatalf("wait attribution = %+v", got)
	}
}

func TestACSOutboundAggregatesSuccessfulSendPhases(t *testing.T) {
	slot := NewSlotACS(SlotConfig{
		Config: Config{N: 4, F: 1, ID: 0, Nodes: []uint64{0, 1, 2, 3}},
		Slot:   1,
		Trace:  TraceOptions{Enabled: true},
	})
	defer slot.Close()
	slot.BeginTrace(time.Now())

	slot.RecordACSOutbound(ACSMessageReady, ACSSendObservation{
		Size: 512, Total: 9 * time.Millisecond,
		Encode: time.Millisecond, QueueWait: 2 * time.Millisecond,
		Write: 6 * time.Millisecond, Reused: true,
	})
	slot.RecordACSOutbound(ACSMessageReady, ACSSendObservation{
		Size: 256, Total: 15 * time.Millisecond,
		Encode: 2 * time.Millisecond, StreamOpen: 4 * time.Millisecond,
		Write: 7 * time.Millisecond, Finalize: 2 * time.Millisecond,
	})
	slot.RecordACSOutbound(ACSMessageReady, ACSSendObservation{
		Total: 5 * time.Millisecond, StreamOpen: 5 * time.Millisecond,
		Err: errors.New("open failed"),
	})

	trace := slot.Trace()
	if trace.SchemaVersion != ACSTraceSchemaV2 || ACSTraceSchemaVersion != ACSTraceSchemaV2 {
		t.Fatalf("trace schema = %q, current = %q", trace.SchemaVersion, ACSTraceSchemaVersion)
	}
	got := trace.Messages[ACSMessageReady]
	if got.OutboundCount != 2 || got.OutboundBytes != 768 || got.SendCount != 2 || got.SendFailureCount != 1 {
		t.Fatalf("send accounting = %+v", got)
	}
	if got.SendTotalUS != 24000 || got.SendMaxUS != 15000 {
		t.Fatalf("send duration accounting = %+v", got)
	}
	assertSendPhase(t, "encode", got.Encode, 2, 3000, 2000)
	assertSendPhase(t, "queue wait", got.QueueWait, 2, 2000, 2000)
	assertSendPhase(t, "stream open", got.StreamOpen, 2, 4000, 4000)
	assertSendPhase(t, "write", got.Write, 2, 13000, 7000)
	assertSendPhase(t, "finalize", got.Finalize, 2, 2000, 2000)
	if got.StreamOpenCount != 1 || got.StreamReuseCount != 1 {
		t.Fatalf("stream reuse accounting = %+v", got)
	}
}

func assertSendPhase(t *testing.T, name string, got ACSSendPhaseTrace, count uint64, totalUS, maxUS int64) {
	t.Helper()
	if got.Count != count || got.TotalUS != totalUS || got.MaxUS != maxUS {
		t.Fatalf("%s phase = %+v, want count=%d total_us=%d max_us=%d", name, got, count, totalUS, maxUS)
	}
}
