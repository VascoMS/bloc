package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthdm/hbbft"
)

func TestWriteACSTraceArtifactOrdersBoundedNodeRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acs_trace.jsonl")
	runs := []EvalRun{
		{
			RunID: "run-b", MeasurementBlock: 2, Slot: 9,
			Results: []Result{
				{NodeID: 1, ACSTrace: artifactTestTrace(20)},
				{NodeID: 0, ACSTrace: artifactTestTrace(10)},
			},
		},
		{
			RunID: "run-a", MeasurementBlock: 1, Slot: 8,
			Results: []Result{{NodeID: 1, ACSTrace: artifactTestTrace(5)}},
		},
	}

	if err := writeACSTraceArtifact(path, runs); err != nil {
		t.Fatal(err)
	}
	records, err := readACSTraceArtifact(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("record count = %d, want 3", len(records))
	}
	wantKeys := []acsTraceArtifactKey{
		{MeasurementBlock: 1, RunID: "run-a", NodeID: 1, Slot: 8},
		{MeasurementBlock: 2, RunID: "run-b", NodeID: 0, Slot: 9},
		{MeasurementBlock: 2, RunID: "run-b", NodeID: 1, Slot: 9},
	}
	for i, want := range wantKeys {
		if records[i].Key != want {
			t.Fatalf("record %d key = %+v, want %+v", i, records[i].Key, want)
		}
	}
	if got, want := proposerIDs(records[0].RBC), []uint64{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RBC proposer order = %v, want %v", got, want)
	}
	if got, want := proposerIDs(records[0].BBA), []uint64{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BBA proposer order = %v, want %v", got, want)
	}
	gotSubtypes := make([]hbbft.ACSMessageSubtype, len(records[0].Messages))
	for i, message := range records[0].Messages {
		gotSubtypes[i] = message.Subtype
	}
	wantSubtypes := []hbbft.ACSMessageSubtype{
		hbbft.ACSMessageProof,
		hbbft.ACSMessageEcho,
		hbbft.ACSMessageReady,
		hbbft.ACSMessageBVAL,
		hbbft.ACSMessageAUX,
	}
	if !reflect.DeepEqual(gotSubtypes, wantSubtypes) {
		t.Fatalf("message subtype order = %v, want %v", gotSubtypes, wantSubtypes)
	}
}

func TestValidateACSTraceArtifactLegacyManifestDoesNotRequireFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	if err := validateACSTraceArtifact(suiteManifest{}, nil, missing); err != nil {
		t.Fatalf("legacy artifact unexpectedly required ACS trace JSONL: %v", err)
	}
}

func TestValidateACSTraceArtifactAcceptsCompleteArtifact(t *testing.T) {
	manifest, runs, records := validACSTraceArtifactFixture()
	path := writeACSTraceRecords(t, records)
	if err := validateACSTraceArtifact(manifest, runs, path); err != nil {
		t.Fatalf("valid ACS trace artifact rejected: %v", err)
	}
}

func TestValidateACSTraceArtifactAcceptsHistoricalV1(t *testing.T) {
	manifest, runs, records := validACSTraceArtifactFixture()
	manifest.ACSTraceSchema = hbbft.ACSTraceSchemaV1
	for runIndex := range runs {
		for resultIndex := range runs[runIndex].Results {
			runs[runIndex].Results[resultIndex].ACSTrace.SchemaVersion = hbbft.ACSTraceSchemaV1
		}
	}
	for index := range records {
		records[index].SchemaVersion = hbbft.ACSTraceSchemaV1
	}
	path := writeACSTraceRecords(t, records)
	if err := validateACSTraceArtifact(manifest, runs, path); err != nil {
		t.Fatalf("historical v1 artifact rejected: %v", err)
	}
}

func TestValidateACSTraceArtifactAcceptsHistoricalV2WithoutV3Lifecycle(t *testing.T) {
	manifest, runs, _ := validACSTraceArtifactFixture()
	manifest.ACSTraceSchema = hbbft.ACSTraceSchemaV2
	for runIndex := range runs {
		for resultIndex := range runs[runIndex].Results {
			trace := runs[runIndex].Results[resultIndex].ACSTrace
			trace.SchemaVersion = hbbft.ACSTraceSchemaV2
			trace.Transport = hbbft.ACSTransportTrace{}
			proof := trace.Messages[hbbft.ACSMessageProof]
			proof.OutboundCount = 1
			proof.SendCount = 1
			proof.SendTotalUS = 10
			proof.SendMaxUS = 10
			proof.Encode = hbbft.ACSSendPhaseTrace{Count: 1, TotalUS: 1, MaxUS: 1}
			proof.QueueWait = hbbft.ACSSendPhaseTrace{Count: 1}
			proof.StreamOpen = hbbft.ACSSendPhaseTrace{Count: 1, TotalUS: 2, MaxUS: 2}
			proof.Write = hbbft.ACSSendPhaseTrace{Count: 1, TotalUS: 7, MaxUS: 7}
			proof.Finalize = hbbft.ACSSendPhaseTrace{Count: 1}
			proof.StreamOpenCount = 1
			trace.Messages[hbbft.ACSMessageProof] = proof
			runs[runIndex].Results[resultIndex].ACSTrace = trace
		}
	}
	records := []acsTraceArtifactRecord{
		newACSTraceArtifactRecord(runs[0], runs[0].Results[0]),
		newACSTraceArtifactRecord(runs[0], runs[0].Results[1]),
	}

	if err := validateACSTraceArtifact(manifest, runs, writeACSTraceRecords(t, records)); err != nil {
		t.Fatalf("historical v2 artifact without v3 lifecycle rejected: %v", err)
	}
}

func TestACSTraceSchemaForRunsRequiresV3ForNewRuns(t *testing.T) {
	_, runs, _ := validACSTraceArtifactFixture()
	schema, err := acsTraceSchemaForRuns(runs)
	if err != nil {
		t.Fatalf("v3 evaluator runs rejected: %v", err)
	}
	if schema != hbbft.ACSTraceSchemaV3 {
		t.Fatalf("new-run schema = %q, want %q", schema, hbbft.ACSTraceSchemaV3)
	}
}

func TestValidateACSTraceArtifactRejectsV2PhaseCountMismatch(t *testing.T) {
	manifest, runs, records := validACSTraceArtifactFixture()
	message := hbbft.ACSMessageTrace{
		OutboundCount: 1, OutboundBytes: 10, SendCount: 1, SendTotalUS: 10, SendMaxUS: 10,
		Encode:          hbbft.ACSSendPhaseTrace{Count: 1, TotalUS: 1, MaxUS: 1},
		QueueWait:       hbbft.ACSSendPhaseTrace{Count: 1},
		StreamOpen:      hbbft.ACSSendPhaseTrace{Count: 1, TotalUS: 2, MaxUS: 2},
		Write:           hbbft.ACSSendPhaseTrace{Count: 1, TotalUS: 7, MaxUS: 7},
		Finalize:        hbbft.ACSSendPhaseTrace{Count: 1},
		StreamOpenCount: 1,
	}
	runs[0].Results[0].ACSTrace.Messages[hbbft.ACSMessageProof] = message
	records[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
	records[0].Messages[0].Trace.Encode.Count = 0
	path := writeACSTraceRecords(t, records)
	err := validateACSTraceArtifact(manifest, runs, path)
	if err == nil || !strings.Contains(err.Error(), "phase count") {
		t.Fatalf("validate error = %v, want phase count failure", err)
	}
}

func TestValidateACSTraceArtifactAllowsRemoteRBCOutputBeforeLocalInput(t *testing.T) {
	manifest, runs, _ := validACSTraceArtifactFixture()
	trace := runs[0].Results[0].ACSTrace
	trace.Aggregate.InputStarted.OffsetUS = 2
	trace.Aggregate.FirstRBCOutput.OffsetUS = 1
	localRBC := trace.RBC[0]
	localRBC.OutputStored = hbbft.TracePoint{Recorded: true, OffsetUS: 3}
	trace.RBC[0] = localRBC
	runs[0].Results[0].ACSTrace = trace
	records := []acsTraceArtifactRecord{
		newACSTraceArtifactRecord(runs[0], runs[0].Results[0]),
		newACSTraceArtifactRecord(runs[0], runs[0].Results[1]),
	}

	path := writeACSTraceRecords(t, records)
	if err := validateACSTraceArtifact(manifest, runs, path); err != nil {
		t.Fatalf("remote RBC output before local input rejected: %v", err)
	}
}

func TestValidateACSTraceArtifactRejectsLocalRBCOutputBeforeLocalInput(t *testing.T) {
	manifest, runs, _ := validACSTraceArtifactFixture()
	trace := runs[0].Results[0].ACSTrace
	trace.Aggregate.InputStarted.OffsetUS = 2
	trace.Aggregate.FirstRBCOutput.OffsetUS = 1
	localRBC := trace.RBC[0]
	localRBC.OutputStored = hbbft.TracePoint{Recorded: true, OffsetUS: 1}
	trace.RBC[0] = localRBC
	runs[0].Results[0].ACSTrace = trace
	records := []acsTraceArtifactRecord{
		newACSTraceArtifactRecord(runs[0], runs[0].Results[0]),
		newACSTraceArtifactRecord(runs[0], runs[0].Results[1]),
	}

	path := writeACSTraceRecords(t, records)
	err := validateACSTraceArtifact(manifest, runs, path)
	if err == nil || !strings.Contains(err.Error(), "local input start occurs after local RBC output") {
		t.Fatalf("validate error = %v, want local RBC causality failure", err)
	}
}

func TestValidateACSTraceArtifactAllowsCrossEpochBBAOffsetsAfterTraceOrigin(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*hbbft.BBATrace)
	}{
		{
			name: "later epoch BIN follows recorded quorum",
			mutate: func(trace *hbbft.BBATrace) {
				trace.FirstBinValue.OffsetUS = 5
				trace.FirstAux.OffsetUS = 1
				trace.ValidAuxQuorum.OffsetUS = 2
				trace.Decision.OffsetUS = 3
			},
		},
		{
			name: "later epoch AUX follows recorded quorum",
			mutate: func(trace *hbbft.BBATrace) {
				trace.FirstBinValue.OffsetUS = 1
				trace.FirstAux.OffsetUS = 5
				trace.ValidAuxQuorum.OffsetUS = 2
				trace.Decision.OffsetUS = 3
			},
		},
		{
			name: "later epoch quorum follows recorded decision",
			mutate: func(trace *hbbft.BBATrace) {
				trace.FirstBinValue.OffsetUS = 1
				trace.FirstAux.OffsetUS = 1
				trace.ValidAuxQuorum.OffsetUS = 5
				trace.Decision.OffsetUS = 3
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, runs, _ := validACSTraceArtifactFixture()
			trace := runs[0].Results[0].ACSTrace
			bba := trace.BBA[1]
			bba.Input = hbbft.TracePoint{}
			bba.FirstBinValue.Recorded = true
			bba.FirstAux.Recorded = true
			bba.ValidAuxQuorum.Recorded = true
			bba.Decision.Recorded = true
			bba.MaxEpoch = 2
			test.mutate(&bba)
			trace.BBA[1] = bba
			runs[0].Results[0].ACSTrace = trace
			records := []acsTraceArtifactRecord{
				newACSTraceArtifactRecord(runs[0], runs[0].Results[0]),
				newACSTraceArtifactRecord(runs[0], runs[0].Results[1]),
			}

			path := writeACSTraceRecords(t, records)
			if err := validateACSTraceArtifact(manifest, runs, path); err != nil {
				t.Fatalf("valid cross-epoch BBA trace rejected: %v", err)
			}
		})
	}
}

func TestWriteSuiteOutputsGatesACSTraceArtifactWithManifest(t *testing.T) {
	manifest, runs, _ := validACSTraceArtifactFixture()
	enabledDir := t.TempDir()
	if err := writeSuiteOutputs(enabledDir, nil, runs, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(enabledDir, "acs_trace.jsonl")); err != nil {
		t.Fatalf("enabled suite did not write ACS trace artifact: %v", err)
	}

	legacyDir := t.TempDir()
	for runIndex := range runs {
		runs[runIndex].StreamMode = ""
	}
	if err := writeSuiteOutputs(legacyDir, nil, runs, suiteManifest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "acs_trace.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("legacy suite unexpectedly wrote ACS trace artifact: %v", err)
	}
}

func TestValidateACSTraceArtifactFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*suiteManifest, []EvalRun, *[]acsTraceArtifactRecord)
	}{
		{
			name: "missing node", want: "missing node trace",
			mutate: func(_ *suiteManifest, _ []EvalRun, records *[]acsTraceArtifactRecord) {
				*records = (*records)[:1]
			},
		},
		{
			name: "duplicate key", want: "duplicate ACS trace key",
			mutate: func(_ *suiteManifest, _ []EvalRun, records *[]acsTraceArtifactRecord) {
				*records = append(*records, (*records)[0])
			},
		},
		{
			name: "unknown manifest schema", want: "unsupported ACS trace schema",
			mutate: func(manifest *suiteManifest, _ []EvalRun, _ *[]acsTraceArtifactRecord) {
				manifest.ACSTraceSchema = "bloc-acs-trace/v999"
			},
		},
		{
			name: "negative offset", want: "negative offset",
			mutate: func(_ *suiteManifest, _ []EvalRun, records *[]acsTraceArtifactRecord) {
				(*records)[0].Aggregate.CoreDecision.OffsetUS = -1
			},
		},
		{
			name: "core decision after node receipt", want: "core decision occurs after node output receipt",
			mutate: func(_ *suiteManifest, _ []EvalRun, records *[]acsTraceArtifactRecord) {
				(*records)[0].Aggregate.CoreDecision.OffsetUS = 11
			},
		},
		{
			name: "BBA decision after done", want: "impossible BBA completion ordering",
			mutate: func(_ *suiteManifest, _ []EvalRun, records *[]acsTraceArtifactRecord) {
				(*records)[0].BBA[0].Trace.Decision = hbbft.TracePoint{Recorded: true, OffsetUS: 4}
				(*records)[0].BBA[0].Trace.Done = hbbft.TracePoint{Recorded: true, OffsetUS: 3}
			},
		},
		{
			name: "unknown proposer", want: "unknown RBC proposer",
			mutate: func(_ *suiteManifest, _ []EvalRun, records *[]acsTraceArtifactRecord) {
				(*records)[0].RBC = append((*records)[0].RBC, acsRBCTraceRecord{ProposerID: 99})
			},
		},
		{
			name: "missing fixed subtype", want: "missing ACS message subtype",
			mutate: func(_ *suiteManifest, _ []EvalRun, records *[]acsTraceArtifactRecord) {
				(*records)[0].Messages = (*records)[0].Messages[:4]
			},
		},
		{
			name: "aggregate detail count mismatch", want: "aggregate/detail message mismatch",
			mutate: func(_ *suiteManifest, _ []EvalRun, records *[]acsTraceArtifactRecord) {
				(*records)[0].Messages[0].Trace.InboundCount++
			},
		},
		{
			name: "legacy ACS mismatch", want: "legacy ACS reconciliation",
			mutate: func(_ *suiteManifest, runs []EvalRun, _ *[]acsTraceArtifactRecord) {
				runs[0].Results[0].Metrics.ACSUS = 100_000
			},
		},
		{
			name: "v3 trace not finalized", want: "transport trace is not sealed and finalized",
			mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
				runs[0].Results[0].ACSTrace.Transport.Finalized = false
				(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
			},
		},
		{
			name: "scheduled terminal mismatch", want: "scheduled count",
			mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
				message := runs[0].Results[0].ACSTrace.Messages[hbbft.ACSMessageReady]
				message.ScheduledCount++
				runs[0].Results[0].ACSTrace.Messages[hbbft.ACSMessageReady] = message
				(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
			},
		},
		{
			name: "terminal outcome mismatch", want: "terminal count",
			mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
				message := runs[0].Results[0].ACSTrace.Messages[hbbft.ACSMessageReady]
				message.TerminalCount++
				runs[0].Results[0].ACSTrace.Messages[hbbft.ACSMessageReady] = message
				(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
			},
		},
		{
			name: "missing READY trigger", want: "missing READY trigger",
			mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
				ready := runs[0].Results[0].ACSTrace.RBC[0]
				ready.ReadySent = hbbft.TracePoint{Recorded: true, OffsetUS: 1}
				ready.ReadyTrigger = ""
				runs[0].Results[0].ACSTrace.RBC[0] = ready
				(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
			},
		},
		{
			name: "echo READY trigger below threshold", want: "READY trigger threshold",
			mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
				ready := runs[0].Results[0].ACSTrace.RBC[0]
				ready.ReadySent = hbbft.TracePoint{Recorded: true, OffsetUS: 1}
				ready.ReadyTrigger = hbbft.RBCReadyTriggerEchoQuorum
				ready.ReadyTriggerEchoCount = 1
				runs[0].Results[0].ACSTrace.RBC[0] = ready
				(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
			},
		},
		{
			name: "echo READY trigger above threshold", want: "READY trigger threshold",
			mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
				ready := runs[0].Results[0].ACSTrace.RBC[0]
				ready.ReadySent = hbbft.TracePoint{Recorded: true, OffsetUS: 1}
				ready.ReadyTrigger = hbbft.RBCReadyTriggerEchoQuorum
				ready.ReadyTriggerEchoCount = 3
				runs[0].Results[0].ACSTrace.RBC[0] = ready
				(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
			},
		},
		{
			name: "relay READY trigger below threshold", want: "READY trigger threshold",
			mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
				ready := runs[0].Results[0].ACSTrace.RBC[0]
				ready.ReadySent = hbbft.TracePoint{Recorded: true, OffsetUS: 1}
				ready.ReadyTrigger = hbbft.RBCReadyTriggerRelay
				ready.ReadyTriggerReadyCount = 0
				runs[0].Results[0].ACSTrace.RBC[0] = ready
				(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
			},
		},
		{
			name: "relay READY trigger above threshold", want: "READY trigger threshold",
			mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
				ready := runs[0].Results[0].ACSTrace.RBC[0]
				ready.ReadySent = hbbft.TracePoint{Recorded: true, OffsetUS: 1}
				ready.ReadyTrigger = hbbft.RBCReadyTriggerRelay
				ready.ReadyTriggerReadyCount = 2
				runs[0].Results[0].ACSTrace.RBC[0] = ready
				(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, runs, records := validACSTraceArtifactFixture()
			test.mutate(&manifest, runs, &records)
			path := writeACSTraceRecords(t, records)
			err := validateACSTraceArtifact(manifest, runs, path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validate error = %v, want category %q", err, test.want)
			}
		})
	}
}

func TestValidateACSTraceArtifactAcceptsExactV3READYTriggers(t *testing.T) {
	manifest, runs, _ := validACSTraceArtifactFixture()
	echo := runs[0].Results[0].ACSTrace.RBC[0]
	echo.ReadySent = hbbft.TracePoint{Recorded: true, OffsetUS: 1}
	echo.ReadyTrigger = hbbft.RBCReadyTriggerEchoQuorum
	echo.ReadyTriggerEchoCount = 2
	runs[0].Results[0].ACSTrace.RBC[0] = echo

	relay := runs[0].Results[1].ACSTrace.RBC[1]
	relay.ReadySent = hbbft.TracePoint{Recorded: true, OffsetUS: 1}
	relay.ReadyTrigger = hbbft.RBCReadyTriggerRelay
	relay.ReadyTriggerReadyCount = 1
	runs[0].Results[1].ACSTrace.RBC[1] = relay

	records := []acsTraceArtifactRecord{
		newACSTraceArtifactRecord(runs[0], runs[0].Results[0]),
		newACSTraceArtifactRecord(runs[0], runs[0].Results[1]),
	}
	if err := validateACSTraceArtifact(manifest, runs, writeACSTraceRecords(t, records)); err != nil {
		t.Fatalf("exact v3 READY triggers rejected: %v", err)
	}
}

func readACSTraceArtifact(path string) ([]acsTraceArtifactRecord, error) {
	handle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	var records []acsTraceArtifactRecord
	scanner := bufio.NewScanner(handle)
	for scanner.Scan() {
		var record acsTraceArtifactRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}

func writeACSTraceRecords(t *testing.T, records []acsTraceArtifactRecord) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "acs_trace.jsonl")
	handle, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(handle)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			_ = handle.Close()
			t.Fatal(err)
		}
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func validACSTraceArtifactFixture() (suiteManifest, []EvalRun, []acsTraceArtifactRecord) {
	runs := []EvalRun{{
		RunID: "run", MeasurementBlock: 3, Slot: 9, Nodes: 2, StreamMode: streamModeFresh,
		Results: []Result{
			{NodeID: 0, Metrics: Metrics{ACSUS: 10}, ACSTrace: artifactTestTrace(10)},
			{NodeID: 1, Metrics: Metrics{ACSUS: 20}, ACSTrace: artifactTestTrace(20)},
		},
	}}
	records := []acsTraceArtifactRecord{
		newACSTraceArtifactRecord(runs[0], runs[0].Results[0]),
		newACSTraceArtifactRecord(runs[0], runs[0].Results[1]),
	}
	return suiteManifest{ACSTraceSchema: hbbft.ACSTraceSchemaVersion, StreamMode: streamModeFresh}, runs, records
}

func proposerIDs[T interface{ proposerID() uint64 }](records []T) []uint64 {
	ids := make([]uint64, len(records))
	for i, record := range records {
		ids[i] = record.proposerID()
	}
	return ids
}

func artifactTestTrace(nodeReceiptUS int64) hbbft.ACSTrace {
	point := func(offset int64) hbbft.TracePoint {
		return hbbft.TracePoint{Recorded: true, OffsetUS: offset}
	}
	trace := hbbft.ACSTrace{
		SchemaVersion: hbbft.ACSTraceSchemaVersion,
		Enabled:       true,
		Transport:     hbbft.ACSTransportTrace{Sealed: true, Finalized: true},
		Aggregate: hbbft.ACSAggregateTrace{
			InputStarted:    point(0),
			FirstRBCOutput:  point(1),
			RBCOutputQuorum: point(2),
			FirstTrueBBA:    point(3),
			TrueBBAQuorum:   point(4),
			AllBBADecided:   point(5),
			TruthyRBCReady:  point(6),
			CoreDecision:    point(7),
		},
		Adapter: hbbft.ACSAdapterTrace{
			CommonSubsetDecoded: point(8),
			BlockBodyBuilt:      point(9),
			NodeOutputReceived:  point(nodeReceiptUS),
		},
		RBC: map[uint64]hbbft.RBCTrace{
			1: {ProofAccepted: point(1)},
			0: {ProofAccepted: point(1)},
		},
		BBA: map[uint64]hbbft.BBATrace{
			1: {Input: point(2)},
			0: {Input: point(2)},
		},
		Messages: map[hbbft.ACSMessageSubtype]hbbft.ACSMessageTrace{},
	}
	for _, subtype := range []hbbft.ACSMessageSubtype{
		hbbft.ACSMessageProof,
		hbbft.ACSMessageEcho,
		hbbft.ACSMessageReady,
		hbbft.ACSMessageBVAL,
		hbbft.ACSMessageAUX,
	} {
		trace.Messages[subtype] = hbbft.ACSMessageTrace{}
	}
	return trace
}
