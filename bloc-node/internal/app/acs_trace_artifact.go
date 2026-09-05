package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"

	"github.com/anthdm/hbbft"
)

var orderedACSMessageSubtypes = []hbbft.ACSMessageSubtype{
	hbbft.ACSMessageProof,
	hbbft.ACSMessageEcho,
	hbbft.ACSMessageReady,
	hbbft.ACSMessageBVAL,
	hbbft.ACSMessageAUX,
}

// acsTraceLegacyToleranceUS allows for the adjacent monotonic reads used by
// the legacy ACS timer and the trace recorder without masking a protocol-scale
// discrepancy between them.
const acsTraceLegacyToleranceUS int64 = 5_000

type acsTraceArtifactKey struct {
	MeasurementBlock int    `json:"measurement_block"`
	RunID            string `json:"run_id"`
	NodeID           uint64 `json:"node_id"`
	Slot             uint64 `json:"slot"`
}

type acsTraceRunKey struct {
	MeasurementBlock int
	RunID            string
	Slot             uint64
}

type acsRBCTraceRecord struct {
	ProposerID uint64         `json:"proposer_id"`
	Trace      hbbft.RBCTrace `json:"trace"`
}

func (r acsRBCTraceRecord) proposerID() uint64 { return r.ProposerID }

type acsBBATraceRecord struct {
	ProposerID uint64         `json:"proposer_id"`
	Trace      hbbft.BBATrace `json:"trace"`
}

func (r acsBBATraceRecord) proposerID() uint64 { return r.ProposerID }

type acsMessageTraceRecord struct {
	Subtype hbbft.ACSMessageSubtype `json:"subtype"`
	Trace   hbbft.ACSMessageTrace   `json:"trace"`
}

type acsTraceArtifactRecord struct {
	Key           acsTraceArtifactKey     `json:"key"`
	SchemaVersion string                  `json:"schema_version,omitempty"`
	Enabled       bool                    `json:"enabled"`
	Transport     hbbft.ACSTransportTrace `json:"transport"`
	Aggregate     hbbft.ACSAggregateTrace `json:"aggregate"`
	Wait          hbbft.ACSWaitTrace      `json:"wait_us"`
	Adapter       hbbft.ACSAdapterTrace   `json:"adapter"`
	RBC           []acsRBCTraceRecord     `json:"rbc"`
	BBA           []acsBBATraceRecord     `json:"bba"`
	Messages      []acsMessageTraceRecord `json:"messages"`
}

type expectedACSTraceArtifact struct {
	record      acsTraceArtifactRecord
	legacyACSUS int64
}

func acsTraceSchemaForRuns(runs []EvalRun) (string, error) {
	var schema string
	for _, run := range runs {
		for _, result := range run.Results {
			trace := result.ACSTrace
			if !trace.Enabled {
				continue
			}
			if trace.SchemaVersion == "" {
				return "", fmt.Errorf("run %q node %d enabled ACS trace has no schema", run.RunID, result.NodeID)
			}
			if schema == "" {
				schema = trace.SchemaVersion
				continue
			}
			if trace.SchemaVersion != schema {
				return "", fmt.Errorf("mixed ACS trace schemas %q and %q", schema, trace.SchemaVersion)
			}
		}
	}
	if schema != "" && schema != hbbft.ACSTraceSchemaVersion {
		return "", fmt.Errorf("new evaluator runs require ACS trace schema %q, got %q", hbbft.ACSTraceSchemaVersion, schema)
	}
	return schema, nil
}

func writeACSTraceArtifact(path string, runs []EvalRun) error {
	records := make([]acsTraceArtifactRecord, 0)
	for _, run := range runs {
		for _, result := range run.Results {
			records = append(records, newACSTraceArtifactRecord(run, result))
		}
	}
	sort.Slice(records, func(i, j int) bool {
		left, right := records[i].Key, records[j].Key
		if left.MeasurementBlock != right.MeasurementBlock {
			return left.MeasurementBlock < right.MeasurementBlock
		}
		if left.RunID != right.RunID {
			return left.RunID < right.RunID
		}
		if left.NodeID != right.NodeID {
			return left.NodeID < right.NodeID
		}
		return left.Slot < right.Slot
	})

	handle, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(handle)
	encoder := json.NewEncoder(writer)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			_ = handle.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = handle.Close()
		return err
	}
	return handle.Close()
}

func validateACSTraceArtifact(manifest suiteManifest, runs []EvalRun, path string) error {
	if manifest.ACSTraceSchema == "" {
		return nil
	}
	if !supportedACSTraceSchema(manifest.ACSTraceSchema) {
		return fmt.Errorf("unsupported ACS trace schema %q", manifest.ACSTraceSchema)
	}

	expected := make(map[acsTraceArtifactKey]expectedACSTraceArtifact)
	memberships := make(map[acsTraceRunKey]map[uint64]struct{})
	for _, run := range runs {
		members := make(map[uint64]struct{}, run.Nodes)
		for nodeID := 0; nodeID < run.Nodes; nodeID++ {
			members[uint64(nodeID)] = struct{}{}
		}
		runKey := acsTraceRunKey{MeasurementBlock: run.MeasurementBlock, RunID: run.RunID, Slot: run.Slot}
		memberships[runKey] = members
		for _, result := range run.Results {
			if !result.ACSTrace.Enabled {
				return fmt.Errorf("missing enabled ACS trace in run %q node %d", run.RunID, result.NodeID)
			}
			if result.ACSTrace.SchemaVersion != manifest.ACSTraceSchema {
				return fmt.Errorf("run %q node %d ACS trace schema %q does not match manifest %q", run.RunID, result.NodeID, result.ACSTrace.SchemaVersion, manifest.ACSTraceSchema)
			}
			record := newACSTraceArtifactRecord(run, result)
			if _, exists := expected[record.Key]; exists {
				return fmt.Errorf("duplicate result ACS trace key %+v", record.Key)
			}
			expected[record.Key] = expectedACSTraceArtifact{record: record, legacyACSUS: result.Metrics.ACSUS}
		}
	}

	handle, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open ACS trace artifact: %w", err)
	}
	defer handle.Close()
	decoder := json.NewDecoder(bufio.NewReader(handle))
	seen := make(map[acsTraceArtifactKey]struct{}, len(expected))
	var previous *acsTraceArtifactKey
	for {
		var record acsTraceArtifactRecord
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode ACS trace artifact: %w", err)
		}
		if _, duplicate := seen[record.Key]; duplicate {
			return fmt.Errorf("duplicate ACS trace key %+v", record.Key)
		}
		seen[record.Key] = struct{}{}
		if previous != nil && acsTraceKeyLess(record.Key, *previous) {
			return fmt.Errorf("ACS trace records are not in deterministic key order: %+v follows %+v", record.Key, *previous)
		}
		keyCopy := record.Key
		previous = &keyCopy
		expectedRecord, exists := expected[record.Key]
		if !exists {
			return fmt.Errorf("unexpected ACS node trace key %+v", record.Key)
		}
		if record.SchemaVersion != manifest.ACSTraceSchema {
			return fmt.Errorf("record %+v ACS trace schema %q does not match manifest %q", record.Key, record.SchemaVersion, manifest.ACSTraceSchema)
		}
		if !record.Enabled {
			return fmt.Errorf("record %+v has disabled ACS trace", record.Key)
		}
		runKey := acsTraceRunKey{MeasurementBlock: record.Key.MeasurementBlock, RunID: record.Key.RunID, Slot: record.Key.Slot}
		if err := validateACSTraceRecord(record, memberships[runKey], expectedRecord); err != nil {
			return fmt.Errorf("record %+v: %w", record.Key, err)
		}
	}
	for key := range expected {
		if _, exists := seen[key]; !exists {
			return fmt.Errorf("missing node trace for ACS key %+v", key)
		}
	}
	return nil
}

func validateACSTraceRecord(record acsTraceArtifactRecord, members map[uint64]struct{}, expected expectedACSTraceArtifact) error {
	if record.SchemaVersion == hbbft.ACSTraceSchemaV3 &&
		(!record.Transport.Sealed || !record.Transport.Finalized) {
		return fmt.Errorf("transport trace is not sealed and finalized")
	}
	points := []struct {
		name  string
		point hbbft.TracePoint
	}{
		{"input_started", record.Aggregate.InputStarted},
		{"first_rbc_output", record.Aggregate.FirstRBCOutput},
		{"rbc_output_quorum", record.Aggregate.RBCOutputQuorum},
		{"first_true_bba", record.Aggregate.FirstTrueBBA},
		{"true_bba_quorum", record.Aggregate.TrueBBAQuorum},
		{"false_input_injected", record.Aggregate.FalseInputInjected},
		{"all_bba_decided", record.Aggregate.AllBBADecided},
		{"truthy_rbc_ready", record.Aggregate.TruthyRBCReady},
		{"core_decision", record.Aggregate.CoreDecision},
		{"common_subset_decoded", record.Adapter.CommonSubsetDecoded},
		{"block_body_built", record.Adapter.BlockBodyBuilt},
		{"node_output_received", record.Adapter.NodeOutputReceived},
	}
	for _, item := range points {
		if item.point.Recorded && item.point.OffsetUS < 0 {
			return fmt.Errorf("negative offset for %s: %d", item.name, item.point.OffsetUS)
		}
	}
	for _, required := range points {
		if required.name == "false_input_injected" {
			continue
		}
		if !required.point.Recorded {
			return fmt.Errorf("missing required ACS trace point %s", required.name)
		}
	}
	for _, pair := range []struct {
		leftName  string
		left      hbbft.TracePoint
		rightName string
		right     hbbft.TracePoint
	}{
		{"first RBC output", record.Aggregate.FirstRBCOutput, "RBC output quorum", record.Aggregate.RBCOutputQuorum},
		{"first RBC output", record.Aggregate.FirstRBCOutput, "first true BBA", record.Aggregate.FirstTrueBBA},
		{"first true BBA", record.Aggregate.FirstTrueBBA, "true BBA quorum", record.Aggregate.TrueBBAQuorum},
		{"true BBA quorum", record.Aggregate.TrueBBAQuorum, "all BBA decided", record.Aggregate.AllBBADecided},
		{"all BBA decided", record.Aggregate.AllBBADecided, "core decision", record.Aggregate.CoreDecision},
		{"truthy RBC ready", record.Aggregate.TruthyRBCReady, "core decision", record.Aggregate.CoreDecision},
	} {
		if err := requireTracePointOrder(pair.leftName, pair.left, pair.rightName, pair.right); err != nil {
			return err
		}
	}
	if record.Aggregate.FalseInputInjected.Recorded {
		if err := requireTracePointOrder("true BBA quorum", record.Aggregate.TrueBBAQuorum, "false input injection", record.Aggregate.FalseInputInjected); err != nil {
			return err
		}
	}
	if err := requireTracePointOrder("core decision", record.Aggregate.CoreDecision, "node output receipt", record.Adapter.NodeOutputReceived); err != nil {
		return err
	}
	if err := requireTracePointOrder("core decision", record.Aggregate.CoreDecision, "common subset decode", record.Adapter.CommonSubsetDecoded); err != nil {
		return err
	}
	if err := requireTracePointOrder("common subset decode", record.Adapter.CommonSubsetDecoded, "block body build", record.Adapter.BlockBodyBuilt); err != nil {
		return err
	}
	if err := requireTracePointOrder("block body build", record.Adapter.BlockBodyBuilt, "node output receipt", record.Adapter.NodeOutputReceived); err != nil {
		return err
	}
	if record.Wait.TrueBBAQuorumUS < 0 || record.Wait.AllBBAUS < 0 || record.Wait.TruthyRBCUS < 0 {
		return fmt.Errorf("negative ACS wait duration: %+v", record.Wait)
	}

	if err := validateRBCRecords(record.RBC, members, record.SchemaVersion); err != nil {
		return err
	}
	for _, rbc := range record.RBC {
		if rbc.ProposerID != record.Key.NodeID || !rbc.Trace.OutputStored.Recorded {
			continue
		}
		if err := requireTracePointOrder("local input start", record.Aggregate.InputStarted, "local RBC output", rbc.Trace.OutputStored); err != nil {
			return err
		}
		break
	}
	if err := validateBBARecords(record.BBA, members); err != nil {
		return err
	}
	messages, err := validateMessageRecords(record.Messages, record.SchemaVersion)
	if err != nil {
		return err
	}
	expectedMessages, err := validateMessageRecords(expected.record.Messages, expected.record.SchemaVersion)
	if err != nil {
		return fmt.Errorf("invalid source result messages: %w", err)
	}
	if !reflect.DeepEqual(messages, expectedMessages) {
		return fmt.Errorf("aggregate/detail message mismatch")
	}
	if !reflect.DeepEqual(record.Aggregate, expected.record.Aggregate) ||
		!reflect.DeepEqual(record.Transport, expected.record.Transport) ||
		!reflect.DeepEqual(record.Wait, expected.record.Wait) ||
		!reflect.DeepEqual(record.Adapter, expected.record.Adapter) ||
		!reflect.DeepEqual(record.RBC, expected.record.RBC) ||
		!reflect.DeepEqual(record.BBA, expected.record.BBA) {
		return fmt.Errorf("artifact/result trace mismatch")
	}
	delta := record.Adapter.NodeOutputReceived.OffsetUS - expected.legacyACSUS
	if delta < -acsTraceLegacyToleranceUS || delta > acsTraceLegacyToleranceUS {
		return fmt.Errorf("legacy ACS reconciliation exceeds %d us tolerance: trace=%d legacy=%d", acsTraceLegacyToleranceUS, record.Adapter.NodeOutputReceived.OffsetUS, expected.legacyACSUS)
	}
	return nil
}

func requireTracePointOrder(leftName string, left hbbft.TracePoint, rightName string, right hbbft.TracePoint) error {
	if !left.Recorded || !right.Recorded {
		return fmt.Errorf("missing required ACS trace point %s or %s", leftName, rightName)
	}
	if left.OffsetUS > right.OffsetUS {
		return fmt.Errorf("%s occurs after %s", leftName, rightName)
	}
	return nil
}

func validateRBCRecords(records []acsRBCTraceRecord, members map[uint64]struct{}, schema string) error {
	seen := make(map[uint64]struct{}, len(records))
	for _, record := range records {
		if _, known := members[record.ProposerID]; !known {
			return fmt.Errorf("unknown RBC proposer %d", record.ProposerID)
		}
		if _, duplicate := seen[record.ProposerID]; duplicate {
			return fmt.Errorf("duplicate RBC proposer %d", record.ProposerID)
		}
		seen[record.ProposerID] = struct{}{}
		if err := validateTracePoints("RBC", []hbbft.TracePoint{record.Trace.ProofAccepted, record.Trace.EchoSent, record.Trace.ReadySent, record.Trace.DecodeEligible, record.Trace.ReconstructionStarted, record.Trace.ReconstructionFinished, record.Trace.OutputStored}); err != nil {
			return err
		}
		if schema == hbbft.ACSTraceSchemaV3 {
			if err := validateV3RBCReadyTrigger(record.Trace, len(members)); err != nil {
				return fmt.Errorf("RBC proposer %d: %w", record.ProposerID, err)
			}
		}
		for _, pair := range [][2]hbbft.TracePoint{
			{record.Trace.ProofAccepted, record.Trace.EchoSent},
			{record.Trace.DecodeEligible, record.Trace.ReconstructionStarted},
			{record.Trace.ReconstructionStarted, record.Trace.ReconstructionFinished},
			{record.Trace.ReconstructionFinished, record.Trace.OutputStored},
		} {
			if pair[0].Recorded && pair[1].Recorded && pair[0].OffsetUS > pair[1].OffsetUS {
				return fmt.Errorf("impossible RBC milestone ordering for proposer %d", record.ProposerID)
			}
		}
	}
	if len(seen) != len(members) {
		return fmt.Errorf("missing RBC proposer trace: got %d, want %d", len(seen), len(members))
	}
	return nil
}

func validateV3RBCReadyTrigger(trace hbbft.RBCTrace, members int) error {
	if trace.ReadyTrigger == "" {
		if !trace.ReadySent.Recorded && trace.ReadyTriggerEchoCount == 0 && trace.ReadyTriggerReadyCount == 0 {
			return nil
		}
		return fmt.Errorf("missing READY trigger")
	}
	if trace.ReadyTrigger != hbbft.RBCReadyTriggerEchoQuorum && trace.ReadyTrigger != hbbft.RBCReadyTriggerRelay {
		return fmt.Errorf("invalid READY trigger %q", trace.ReadyTrigger)
	}
	if trace.ReadyTriggerEchoCount < 0 || trace.ReadyTriggerReadyCount < 0 ||
		trace.ReadyTriggerEchoCount > members || trace.ReadyTriggerReadyCount > members {
		return fmt.Errorf("invalid READY trigger counts echo=%d ready=%d", trace.ReadyTriggerEchoCount, trace.ReadyTriggerReadyCount)
	}
	faults := (members - 1) / 3
	switch trace.ReadyTrigger {
	case hbbft.RBCReadyTriggerEchoQuorum:
		if trace.ReadyTriggerEchoCount != members-faults {
			return fmt.Errorf("READY trigger threshold for echo_quorum is %d, got %d", members-faults, trace.ReadyTriggerEchoCount)
		}
		if trace.ReadyTriggerReadyCount >= faults+1 {
			return fmt.Errorf("READY trigger history for echo_quorum has ready count %d at relay threshold %d", trace.ReadyTriggerReadyCount, faults+1)
		}
	case hbbft.RBCReadyTriggerRelay:
		if trace.ReadyTriggerReadyCount != faults+1 {
			return fmt.Errorf("READY trigger threshold for ready_relay is %d, got %d", faults+1, trace.ReadyTriggerReadyCount)
		}
		if trace.ReadyTriggerEchoCount >= members-faults {
			return fmt.Errorf("READY trigger history for ready_relay has echo count %d at echo threshold %d", trace.ReadyTriggerEchoCount, members-faults)
		}
	}
	return nil
}

func validateBBARecords(records []acsBBATraceRecord, members map[uint64]struct{}) error {
	seen := make(map[uint64]struct{}, len(records))
	for _, record := range records {
		if _, known := members[record.ProposerID]; !known {
			return fmt.Errorf("unknown BBA proposer %d", record.ProposerID)
		}
		if _, duplicate := seen[record.ProposerID]; duplicate {
			return fmt.Errorf("duplicate BBA proposer %d", record.ProposerID)
		}
		seen[record.ProposerID] = struct{}{}
		if err := validateTracePoints("BBA", []hbbft.TracePoint{record.Trace.Input, record.Trace.FirstBinValue, record.Trace.FirstAux, record.Trace.ValidAuxQuorum, record.Trace.Decision, record.Trace.Done}); err != nil {
			return err
		}
		// BIN, AUX, and valid-AUX quorum recur across epochs. If remote BBA
		// progress predates the local proposal-ready trace origin, their first
		// recorded occurrences can belong to different epochs and have no
		// pairwise causal order in this epochless summary.
		if record.Trace.Decision.Recorded && record.Trace.Done.Recorded && record.Trace.Decision.OffsetUS > record.Trace.Done.OffsetUS {
			return fmt.Errorf("impossible BBA completion ordering for proposer %d", record.ProposerID)
		}
	}
	if len(seen) != len(members) {
		return fmt.Errorf("missing BBA proposer trace: got %d, want %d", len(seen), len(members))
	}
	return nil
}

func validateTracePoints(kind string, points []hbbft.TracePoint) error {
	for _, point := range points {
		if point.Recorded && point.OffsetUS < 0 {
			return fmt.Errorf("negative offset in %s trace: %d", kind, point.OffsetUS)
		}
	}
	return nil
}

func validateMessageRecords(records []acsMessageTraceRecord, schema string) (map[hbbft.ACSMessageSubtype]hbbft.ACSMessageTrace, error) {
	known := make(map[hbbft.ACSMessageSubtype]struct{}, len(orderedACSMessageSubtypes))
	for _, subtype := range orderedACSMessageSubtypes {
		known[subtype] = struct{}{}
	}
	seen := make(map[hbbft.ACSMessageSubtype]hbbft.ACSMessageTrace, len(records))
	for _, record := range records {
		if _, ok := known[record.Subtype]; !ok {
			return nil, fmt.Errorf("unknown ACS message subtype %q", record.Subtype)
		}
		if _, duplicate := seen[record.Subtype]; duplicate {
			return nil, fmt.Errorf("duplicate ACS message subtype %q", record.Subtype)
		}
		if record.Trace.SendTotalUS < 0 || record.Trace.SendMaxUS < 0 {
			return nil, fmt.Errorf("negative send duration for ACS message subtype %q", record.Subtype)
		}
		if schema == hbbft.ACSTraceSchemaV2 || schema == hbbft.ACSTraceSchemaV3 {
			if err := validateV2MessagePhases(record.Subtype, record.Trace); err != nil {
				return nil, err
			}
		}
		if schema == hbbft.ACSTraceSchemaV3 {
			if err := validateV3MessageLifecycle(record.Subtype, record.Trace); err != nil {
				return nil, err
			}
		}
		seen[record.Subtype] = record.Trace
	}
	for _, subtype := range orderedACSMessageSubtypes {
		if _, exists := seen[subtype]; !exists {
			return nil, fmt.Errorf("missing ACS message subtype %q", subtype)
		}
	}
	return seen, nil
}

func validateV3MessageLifecycle(subtype hbbft.ACSMessageSubtype, trace hbbft.ACSMessageTrace) error {
	if trace.ScheduledCount != trace.TerminalCount {
		return fmt.Errorf("ACS message subtype %q scheduled count %d does not match terminal count %d", subtype, trace.ScheduledCount, trace.TerminalCount)
	}
	if trace.OutboundCount > trace.TerminalCount || trace.SendFailureCount != trace.TerminalCount-trace.OutboundCount {
		return fmt.Errorf("ACS message subtype %q terminal count %d does not match successful plus failed outcomes", subtype, trace.TerminalCount)
	}
	if trace.SendCount != trace.OutboundCount || trace.PendingAtDecision > trace.ScheduledCount {
		return fmt.Errorf("ACS message subtype %q has inconsistent successful or pending counts", subtype)
	}
	return nil
}

func validateV2MessagePhases(subtype hbbft.ACSMessageSubtype, trace hbbft.ACSMessageTrace) error {
	for name, phase := range map[string]hbbft.ACSSendPhaseTrace{
		"encode": trace.Encode, "queue_wait": trace.QueueWait, "stream_open": trace.StreamOpen,
		"write": trace.Write, "finalize": trace.Finalize,
	} {
		if phase.Count != trace.SendCount {
			return fmt.Errorf("ACS message subtype %q phase count for %s is %d, want send count %d", subtype, name, phase.Count, trace.SendCount)
		}
		if phase.TotalUS < 0 || phase.MaxUS < 0 || phase.MaxUS > phase.TotalUS {
			return fmt.Errorf("invalid %s phase duration for ACS message subtype %q", name, subtype)
		}
	}
	if trace.StreamOpenCount > trace.SendCount || trace.StreamReuseCount != trace.SendCount-trace.StreamOpenCount {
		return fmt.Errorf("ACS message subtype %q open/reuse count does not match send count %d", subtype, trace.SendCount)
	}
	return nil
}

func supportedACSTraceSchema(schema string) bool {
	return schema == hbbft.ACSTraceSchemaV1 || schema == hbbft.ACSTraceSchemaV2 || schema == hbbft.ACSTraceSchemaV3
}

func acsTraceKeyLess(left, right acsTraceArtifactKey) bool {
	if left.MeasurementBlock != right.MeasurementBlock {
		return left.MeasurementBlock < right.MeasurementBlock
	}
	if left.RunID != right.RunID {
		return left.RunID < right.RunID
	}
	if left.NodeID != right.NodeID {
		return left.NodeID < right.NodeID
	}
	return left.Slot < right.Slot
}

func newACSTraceArtifactRecord(run EvalRun, result Result) acsTraceArtifactRecord {
	trace := result.ACSTrace
	record := acsTraceArtifactRecord{
		Key: acsTraceArtifactKey{
			MeasurementBlock: run.MeasurementBlock,
			RunID:            run.RunID,
			NodeID:           result.NodeID,
			Slot:             run.Slot,
		},
		SchemaVersion: trace.SchemaVersion,
		Enabled:       trace.Enabled,
		Transport:     trace.Transport,
		Aggregate:     trace.Aggregate,
		Wait:          trace.Wait,
		Adapter:       trace.Adapter,
	}
	proposers := make([]uint64, 0, len(trace.RBC))
	for proposerID := range trace.RBC {
		proposers = append(proposers, proposerID)
	}
	sort.Slice(proposers, func(i, j int) bool { return proposers[i] < proposers[j] })
	for _, proposerID := range proposers {
		record.RBC = append(record.RBC, acsRBCTraceRecord{ProposerID: proposerID, Trace: trace.RBC[proposerID]})
	}
	proposers = proposers[:0]
	for proposerID := range trace.BBA {
		proposers = append(proposers, proposerID)
	}
	sort.Slice(proposers, func(i, j int) bool { return proposers[i] < proposers[j] })
	for _, proposerID := range proposers {
		record.BBA = append(record.BBA, acsBBATraceRecord{ProposerID: proposerID, Trace: trace.BBA[proposerID]})
	}

	subtypes := make([]hbbft.ACSMessageSubtype, 0, len(trace.Messages))
	for subtype := range trace.Messages {
		subtypes = append(subtypes, subtype)
	}
	sort.Slice(subtypes, func(i, j int) bool {
		leftOrder, leftKnown := acsMessageSubtypeOrder(subtypes[i])
		rightOrder, rightKnown := acsMessageSubtypeOrder(subtypes[j])
		if leftKnown && rightKnown {
			return leftOrder < rightOrder
		}
		if leftKnown != rightKnown {
			return leftKnown
		}
		return subtypes[i] < subtypes[j]
	})
	for _, subtype := range subtypes {
		record.Messages = append(record.Messages, acsMessageTraceRecord{Subtype: subtype, Trace: trace.Messages[subtype]})
	}
	return record
}

func acsMessageSubtypeOrder(subtype hbbft.ACSMessageSubtype) (int, bool) {
	for i, known := range orderedACSMessageSubtypes {
		if subtype == known {
			return i, true
		}
	}
	return 0, false
}
