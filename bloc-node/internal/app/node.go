package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"bloc-node/internal/app/ethdemo"
	"bloc-node/internal/app/inclusion"
	"btd/be"
	"github.com/anthdm/hbbft"
	"go.dedis.ch/kyber/v4/share"
)

// newNode builds the full operator state from shared cluster config and the
// requested node id. It validates that the node has both membership metadata and
// a BTE secret share before creating ACS and transport instances.
func newNode(cfg ConfigFile, id uint64, faults FaultConfig) (*Node, error) {
	normalizeConfig(&cfg)
	var self NodeConfig
	foundSelf := false
	peers := make(map[uint64]NodeConfig)
	var ids []uint64
	for _, n := range cfg.Nodes {
		ids = append(ids, n.ID)
		peers[n.ID] = n
		if n.ID == id {
			self = n
			foundSelf = true
		}
	}
	if !foundSelf {
		return nil, fmt.Errorf("node id %d not found in config", id)
	}
	if cfg.Network.Mode != "libp2p" {
		return nil, fmt.Errorf("unsupported network mode %q: only libp2p is supported", cfg.Network.Mode)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	suite := newSuite()
	crsSeed, err := hex.DecodeString(strings.TrimPrefix(cfg.CRSSeedHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("decode crs seed: %w", err)
	}
	btd := be.NewBTDFromSeed(suite, cfg.BMax, crsSeed)
	pk, err := unmarshalPointHex(suite, cfg.PublicKeyHex)
	if err != nil {
		return nil, err
	}
	var secret be.SecretShare
	foundShare := false
	for _, s := range cfg.Shares {
		if s.OperatorID == int(id) {
			scalar, err := unmarshalScalarHex(suite, s.ScalarHex)
			if err != nil {
				return nil, err
			}
			secret = be.SecretShare{OperatorID: s.OperatorID, Share: &share.PriShare{I: uint32(s.OperatorID), V: scalar}}
			foundShare = true
			break
		}
	}
	if !foundShare {
		return nil, fmt.Errorf("secret share for node %d not found", id)
	}
	cluster := be.NewNode(btd, pk, secret, cfg.N, cfg.Threshold)
	node := &Node{
		cfg:           cfg,
		self:          self,
		nodeIDs:       ids,
		peers:         peers,
		cluster:       cluster,
		secret:        secret,
		suite:         suite,
		faults:        faults,
		lastSlot:      cfg.Slot,
		observability: newNodeMetrics(cfg.ClusterID, id),
	}
	node.slotState = node.newSlotState(cfg.Slot)
	node.transport = newLibP2PTransport(node, ProtoEnvelopeCodec{})
	return node, nil
}

func (n *Node) newSlotState(slotID uint64) *slotState {
	state := &slotState{
		id:          slotID,
		phase:       slotPrepared,
		seenPending: make(map[string]bool),
		seenShares:  make(map[string]bool),
		metrics: Metrics{
			SharesNeededPerSub: n.cfg.Threshold,
			MaxDecryptedGas:    n.cfg.Blockspace.MaxDecryptedGas,
			MaxDecryptedTxs:    inclusion.EffectiveMaxDecryptedTxs(n.cfg.Blockspace, n.cfg.BMax),
			OutboundMessages:   make(map[string]int),
			InboundMessages:    make(map[string]int),
			OutboundBytes:      make(map[string]int64),
			InboundBytes:       make(map[string]int64),
		},
		slot: hbbft.NewSlotACS(hbbft.SlotConfig{
			Config: hbbft.Config{N: n.cfg.N, F: (n.cfg.N - 1) / 3, ID: n.self.ID, Nodes: n.nodeIDs, BatchSize: n.cfg.BMax},
			Slot:   slotID,
		}),
	}
	if n.observability != nil {
		n.observability.setCurrentSlot(slotID)
		n.observability.setPhase(slotPrepared)
		n.observability.setResultAvailable(false)
		n.observability.setSelected(0, 0)
	}
	return state
}

// parseFaults converts evaluator fault names into runtime fault switches.
func parseFaults(raw string, delay time.Duration) FaultConfig {
	faults := FaultConfig{Delay: delay}
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(part) {
		case "":
		case "omit-proposal":
			faults.OmitProposal = true
		case "withhold-share":
			faults.WithholdShare = true
		case "corrupt-share":
			faults.CorruptShare = true
		default:
			log.Printf("ignoring unknown fault mode %q", part)
		}
	}
	return faults
}

// startTransport starts the configured node-to-node network backend.
func (n *Node) startTransport() error {
	return n.transport.Start(context.Background(), n.handleEnvelope)
}

// listenHTTP exposes the prototype control plane used by manual tests and the
// local evaluator.
func (n *Node) listenHTTP(outPath string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", n.instrumentHTTP("healthz", func(w http.ResponseWriter, _ *http.Request) {
		ready := true
		if transport, ok := n.transport.(interface{ Ready() bool }); ok {
			ready = transport.Ready()
		}
		if !ready {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "starting", "id": n.self.ID})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "id": n.self.ID})
	}))
	mux.HandleFunc("/tx", n.instrumentHTTP("tx", n.handleSubmitTx))
	mux.HandleFunc("/slot/prepare", n.instrumentHTTP("slot_prepare", n.handlePrepareSlot))
	mux.HandleFunc("/slot/status", n.instrumentHTTP("slot_status", n.handleSlotStatus))
	mux.HandleFunc("/metrics", n.instrumentHTTP("metrics", n.handleMetrics))
	mux.HandleFunc("/start", n.instrumentHTTP("start", func(w http.ResponseWriter, r *http.Request) {
		if err := n.validateRequestedSlot(r); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if err := n.startConsensus(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
	}))
	mux.HandleFunc("/result", n.instrumentHTTP("result", func(w http.ResponseWriter, r *http.Request) {
		n.lifecycleMu.RLock()
		defer n.lifecycleMu.RUnlock()
		if err := n.validateRequestedSlotLocked(r); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.result == nil {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
			return
		}
		writeJSON(w, http.StatusOK, n.result)
	}))
	server := &http.Server{Addr: n.self.httpListenAddr(), Handler: mux}
	log.Printf("event=http_listen node_id=%d listen_addr=%s advertise_url=%s", n.self.ID, n.self.httpListenAddr(), n.self.httpAdvertiseURL())
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http: %v", err)
		}
	}()
	if outPath != "" {
		go n.writeResultWhenReady(outPath)
	}
	return nil
}

type prepareSlotRequest struct {
	Slot uint64 `json:"slot"`
}

// handlePrepareSlot replaces a completed slot while retaining the process,
// cryptographic material, HTTP server, and libp2p mesh.
func (n *Node) handlePrepareSlot(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req prepareSlotRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := n.prepareSlot(req.Slot); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": slotPrepared, "slot": req.Slot})
}

func (n *Node) prepareSlot(slotID uint64) error {
	n.lifecycleMu.Lock()
	defer n.lifecycleMu.Unlock()
	if slotID <= n.lastSlot {
		return fmt.Errorf("slot %d must be greater than previous slot %d", slotID, n.lastSlot)
	}
	n.mu.Lock()
	phase := n.phase
	n.mu.Unlock()
	if phase != slotCompleted {
		return fmt.Errorf("slot %d is %s and cannot be replaced", n.id, phase)
	}
	n.slot.Close()
	n.cfg.Slot = slotID
	n.slotState = n.newSlotState(slotID)
	n.lastSlot = slotID
	return nil
}

func (n *Node) handleSlotStatus(w http.ResponseWriter, r *http.Request) {
	n.lifecycleMu.RLock()
	defer n.lifecycleMu.RUnlock()
	if err := n.validateRequestedSlotLocked(r); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	n.mu.Lock()
	status := map[string]any{
		"slot": n.id, "phase": n.phase, "node_id": n.self.ID,
		"pending": len(n.pending), "planned": n.planned,
		"shares": len(n.shares), "combine_in_flight": n.combineInFlight,
		"complete": n.result != nil,
	}
	n.mu.Unlock()
	if n.slot != nil {
		status["acs"] = n.slot.Progress()
	}
	writeJSON(w, http.StatusOK, status)
}

func (n *Node) validateRequestedSlot(r *http.Request) error {
	n.lifecycleMu.RLock()
	defer n.lifecycleMu.RUnlock()
	return n.validateRequestedSlotLocked(r)
}

func (n *Node) validateRequestedSlotLocked(r *http.Request) error {
	raw := r.URL.Query().Get("slot")
	if raw == "" {
		return nil
	}
	slotID, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid slot %q", raw)
	}
	if slotID != n.id {
		return fmt.Errorf("requested slot %d, active slot is %d", slotID, n.id)
	}
	return nil
}

// handleSubmitTx encrypts a submitted raw transaction into a placeholder and
// stores it in the node-local pending proposal buffer.
func (n *Node) handleSubmitTx(w http.ResponseWriter, r *http.Request) {
	n.lifecycleMu.RLock()
	defer n.lifecycleMu.RUnlock()
	n.inputMu.Lock()
	defer n.inputMu.Unlock()
	if err := n.validateRequestedSlotLocked(r); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	n.mu.Lock()
	phase := n.phase
	n.mu.Unlock()
	if phase != slotPrepared {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("slot %d is %s and no longer accepts transactions", n.id, phase)})
		return
	}
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req := SubmitTxRequest{
		Gas:                   n.cfg.Blockspace.DefaultTxGas,
		EffectiveFeePerGasWei: "0",
		Kind:                  "placeholder",
	}
	if strings.HasPrefix(r.Header.Get("content-type"), "application/json") {
		if err := json.Unmarshal(raw, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		raw, err = decodeHexMaybe(req.RawTx)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.Gas == 0 {
		req.Gas = n.cfg.Blockspace.DefaultTxGas
	}
	if req.Kind == "" {
		req.Kind = "placeholder"
	}
	if req.EffectiveFeePerGasWei == "" {
		req.EffectiveFeePerGasWei = "0"
	}
	if !validNonNegativeDecimal(req.EffectiveFeePerGasWei) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "effective_fee_per_gas_wei must be a non-negative integer"})
		return
	}
	n.mu.Lock()
	index := len(n.pending) % n.cfg.BMax
	n.mu.Unlock()
	ct, err := n.cluster.EncryptTx(raw, index, n.cfg.ClusterID, n.id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	encoded, err := ct.MarshalBinary()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	hash := hashHex(encoded)
	n.mu.Lock()
	if !n.seenPending[hash] {
		n.pending = append(n.pending, EncryptedPlaceholder{
			Hash:                  hash,
			Ciphertext:            encoded,
			Gas:                   req.Gas,
			EffectiveFeePerGasWei: req.EffectiveFeePerGasWei,
			From:                  strings.ToLower(req.From),
			Nonce:                 req.Nonce,
			Kind:                  req.Kind,
		})
		n.seenPending[hash] = true
		n.metrics.SubmittedTxs++
		n.metrics.SubmittedBytes += len(raw)
	}
	n.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"hash": hash, "index": index, "ciphertext_bytes": len(encoded), "gas": req.Gas})
}

// startConsensus builds this node's inclusion-list proposal, inputs it into the
// slot ACS instance, and drains any initial ACS messages.
func (n *Node) startConsensus() error {
	n.lifecycleMu.RLock()
	defer n.lifecycleMu.RUnlock()
	var err error
	n.startOnce.Do(func() {
		n.inputMu.Lock()
		defer n.inputMu.Unlock()
		start := time.Now()
		n.mu.Lock()
		n.phase = slotRunning
		n.mu.Unlock()
		if n.observability != nil {
			n.observability.slotStarted()
		}
		list, buildErr := n.buildInclusionList()
		if buildErr != nil {
			err = buildErr
			n.markSlotFailed("proposal")
			return
		}
		if n.faults.OmitProposal {
			list.Items = nil
			list.Hash = inclusion.HashInclusionList(list)
		}
		encodedList, encodeErr := inclusion.EncodeList(list)
		err = encodeErr
		if err != nil {
			n.markSlotFailed("proposal")
			return
		}
		proposalReady := time.Now()
		n.mu.Lock()
		n.metricTimes.slotStart = start
		n.metricTimes.proposalReady = proposalReady
		n.metrics.SlotStartUnixNano = start.UnixNano()
		n.metrics.ProposalReadyUnixNano = proposalReady.UnixNano()
		n.metrics.ProposalTxs = len(list.Items)
		n.metrics.ProposalHash = list.Hash
		n.refreshMetricsLocked()
		n.mu.Unlock()
		log.Printf("event=slot_start node_id=%d slot=%d proposal_hash=%s proposal_txs=%d", n.self.ID, n.id, list.Hash, len(list.Items))
		output, stepErr := n.stepACS(func() error {
			return n.slot.InputBatch(encodedList)
		})
		if stepErr != nil {
			err = stepErr
			n.markSlotFailed("acs")
			return
		}
		n.handleACSOutput(output)
	})
	return err
}

// handleEnvelope is the common inbound message path for every transport.
func (n *Node) handleEnvelope(env WireEnvelope, size int) {
	n.lifecycleMu.RLock()
	defer n.lifecycleMu.RUnlock()
	if env.Direct && env.To != n.self.ID {
		return
	}
	if env.Slot != n.id {
		log.Printf("ignoring %s envelope for stale slot %d; active slot is %d", env.Kind, env.Slot, n.id)
		return
	}
	n.recordInbound(env.Kind, size)
	switch env.Kind {
	case "acs":
		if env.ACS == nil {
			log.Printf("nil acs message from %d", env.From)
			return
		}
		output, err := n.stepACS(func() error {
			return n.slot.HandleMessage(env.From, env.ACS)
		})
		if err != nil {
			if isBenignDuplicate(err) {
				return
			}
			log.Printf("handle acs from %d: %v", env.From, err)
			return
		}
		n.handleACSOutput(output)
	case "share":
		if env.Share == nil {
			log.Printf("nil share from %d", env.From)
			return
		}
		if err := n.addWireShare(*env.Share); err != nil {
			log.Printf("share from %d: %v", env.From, err)
			return
		}
		n.tryCombine()
	default:
		log.Printf("unknown envelope kind %q from %d", env.Kind, env.From)
	}
}

// isBenignDuplicate recognizes duplicate ACS messages that can happen during
// local retries and are safe to ignore.
func isBenignDuplicate(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "received multiple readys") ||
		strings.Contains(msg, "received multiple echos") ||
		strings.Contains(msg, "received proof from") && strings.Contains(msg, "more the once")
}

// stepACS serializes the non-thread-safe slot ACS state machine. libp2p may
// deliver streams concurrently, but each node must drive ACS as a single local
// event loop: mutate protocol state, drain emitted messages, and consume output
// as one ordered transition.
func (n *Node) stepACS(step func() error) (*hbbft.SlotOutput, error) {
	n.acsMu.Lock()
	if err := step(); err != nil {
		n.acsMu.Unlock()
		return nil, err
	}
	messages := n.collectACSMessages()
	output := n.slot.Output()
	n.acsMu.Unlock()
	for _, env := range messages {
		n.sendEnvelope(env.to, env.envelope)
	}
	return output, nil
}

type pendingEnvelope struct {
	to       uint64
	envelope WireEnvelope
}

// collectACSMessages drains pending HoneyBadger ACS messages emitted by the
// local ACS state machine. Callers must hold acsMu.
func (n *Node) collectACSMessages() []pendingEnvelope {
	var out []pendingEnvelope
	for _, msg := range n.slot.Messages() {
		slotMsg, ok := msg.Payload.(*hbbft.SlotMessage)
		if !ok {
			log.Printf("unexpected slot payload %T", msg.Payload)
			continue
		}
		out = append(out, pendingEnvelope{
			to:       msg.To,
			envelope: WireEnvelope{From: n.self.ID, Kind: "acs", Slot: n.id, ACS: slotMsg},
		})
	}
	return out
}

// handleACSOutput processes an ACS decision. Once ACS has decided, every correct node
// canonicalizes the decided inclusion lists, applies the deterministic merge
// rule, plans BTE sub-batches, and gossips decryption shares.
func (n *Node) handleACSOutput(out *hbbft.SlotOutput) {
	if out == nil {
		return
	}
	decisionAt := time.Now()
	lists, err := decodeAcceptedLists(out.OrderedBatches)
	if err != nil {
		log.Printf("%v", err)
		n.markSlotFailed("decode")
		return
	}
	decodedAt := time.Now()
	agreed := inclusion.NewAgreedSet(n.id, lists)
	agreedAt := time.Now()
	merged := inclusion.Merge(n.id, lists, n.cfg.Blockspace, n.cfg.BMax)
	mergedAt := time.Now()
	encodedCiphertexts := make([][]byte, 0, len(merged.Items))
	for _, item := range merged.Items {
		encodedCiphertexts = append(encodedCiphertexts, item.Ciphertext)
	}
	decodedBatch, err := n.cluster.DecodeBatch(encodedCiphertexts)
	if err != nil {
		log.Printf("decode ciphertext batch: %v", err)
		n.markSlotFailed("decode")
		return
	}
	encrypted := decodedBatch.Ciphertexts()
	ciphertextsAt := time.Now()
	if len(encrypted) == 0 {
		n.finishEmptyMaterializedSet(decisionAt, decodedAt, agreedAt, mergedAt, ciphertextsAt, agreed, merged)
		return
	}
	plan, err := n.cluster.PlanDecodedBatch(decodedBatch)
	if err != nil {
		log.Printf("plan batch: %v", err)
		n.markSlotFailed("planning")
		return
	}
	planAt := time.Now()
	n.mu.Lock()
	if n.planned {
		n.mu.Unlock()
		return
	}
	n.plan = plan
	n.metricTimes.acsDecision = decisionAt
	n.metricTimes.acsOutputDecoded = decodedAt
	n.metricTimes.agreedSetDone = agreedAt
	n.metricTimes.mergeDone = mergedAt
	n.metricTimes.ciphertextsDecoded = ciphertextsAt
	n.metricTimes.planDone = planAt
	n.metricTimes.shareGenerationStart = planAt
	n.material = MaterializedTransactionSet{
		Slot:            n.id,
		AgreedSetHash:   agreed.Hash,
		MergedSetHash:   merged.Hash,
		SelectedGas:     merged.SelectedGas,
		EncryptedHashes: inclusion.EncryptedHashes(merged.Items),
	}
	n.planned = true
	n.metrics.ACSDecisionUnixNano = decisionAt.UnixNano()
	n.metrics.PlanDoneUnixNano = planAt.UnixNano()
	n.metrics.ShareGenerationStartUnixNano = planAt.UnixNano()
	n.metrics.AgreedLists = len(agreed.Lists)
	n.metrics.AgreedSetHash = agreed.Hash
	n.metrics.AgreedCiphertexts = agreed.TotalItems
	n.metrics.MergedSetHash = merged.Hash
	n.metrics.SelectedCiphertexts = len(merged.Items)
	n.metrics.SkippedCiphertexts = merged.SkippedItems
	n.metrics.SelectedGas = merged.SelectedGas
	n.metrics.SubBatches = len(plan.SubBatches)
	n.refreshMetricsLocked()
	n.mu.Unlock()
	log.Printf("event=acs_decision node_id=%d slot=%d agreed_lists=%d agreed_candidates=%d selected_txs=%d selected_gas=%d batch_id=%s sub_batches=%d", n.self.ID, n.id, len(agreed.Lists), agreed.TotalItems, len(encrypted), merged.SelectedGas, hex.EncodeToString(plan.BatchID[:]), len(plan.SubBatches))
	for subBatchID := range plan.SubBatches {
		if n.faults.WithholdShare {
			continue
		}
		d, err := n.cluster.MakeShare(n.secret, plan, subBatchID)
		if err != nil {
			log.Printf("make share %d: %v", subBatchID, err)
			n.markSlotFailed("share")
			return
		}
		if err := n.addShare(d); err != nil {
			log.Printf("add own share: %v", err)
			return
		}
		wire, err := n.marshalShare(d)
		if err != nil {
			log.Printf("marshal share: %v", err)
			n.markSlotFailed("share")
			return
		}
		if n.faults.CorruptShare {
			wire.PointHex = corruptHex(wire.PointHex)
		}
		for _, peer := range n.nodeIDs {
			if peer != n.self.ID {
				n.sendEnvelope(peer, WireEnvelope{From: n.self.ID, Kind: "share", Slot: n.id, Share: &wire})
			}
		}
	}
	n.mu.Lock()
	sharesDone := time.Now()
	n.shareGenerationDone = true
	n.metricTimes.sharesDone = sharesDone
	n.metrics.SharesDoneUnixNano = sharesDone.UnixNano()
	n.refreshMetricsLocked()
	if n.result != nil {
		n.result.Metrics = n.metrics.snapshot()
	}
	n.mu.Unlock()
	n.tryCombine()
}

func decodeAcceptedLists(batches []hbbft.AcceptedBatch) ([]InclusionList, error) {
	lists := make([]InclusionList, 0, len(batches))
	for _, accepted := range batches {
		list, err := inclusion.DecodeList(accepted.Batch)
		if err != nil {
			return nil, fmt.Errorf("decode accepted inclusion list from %d: %w", accepted.ProposerID, err)
		}
		lists = append(lists, list)
	}
	return lists, nil
}

// finishEmptyMaterializedSet records a successful slot result when ACS decides
// no decryptable ciphertexts.
func (n *Node) finishEmptyMaterializedSet(decisionAt, decodedAt, agreedAt, mergedAt, ciphertextsAt time.Time, agreed AgreedInclusionSet, merged MergedEncryptedSet) {
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.result != nil || n.planned {
		return
	}
	n.planned = true
	n.metricTimes.acsDecision = decisionAt
	n.metricTimes.acsOutputDecoded = decodedAt
	n.metricTimes.agreedSetDone = agreedAt
	n.metricTimes.mergeDone = mergedAt
	n.metricTimes.ciphertextsDecoded = ciphertextsAt
	n.metricTimes.planDone = ciphertextsAt
	n.metricTimes.shareGenerationStart = ciphertextsAt
	n.metricTimes.sharesDone = ciphertextsAt
	n.metricTimes.threshold = ciphertextsAt
	n.metricTimes.combineDone = ciphertextsAt
	n.metricTimes.materialized = now
	n.material = MaterializedTransactionSet{
		Slot:            n.id,
		AgreedSetHash:   agreed.Hash,
		MergedSetHash:   merged.Hash,
		SelectedGas:     merged.SelectedGas,
		EncryptedHashes: inclusion.EncryptedHashes(merged.Items),
		PlaintextHashes: []string{},
		PlaintextsHex:   []string{},
	}
	n.metrics.ACSDecisionUnixNano = decisionAt.UnixNano()
	n.metrics.PlanDoneUnixNano = ciphertextsAt.UnixNano()
	n.metrics.ShareGenerationStartUnixNano = ciphertextsAt.UnixNano()
	n.metrics.SharesDoneUnixNano = ciphertextsAt.UnixNano()
	n.metrics.ThresholdUnixNano = ciphertextsAt.UnixNano()
	n.metrics.CombineDoneUnixNano = ciphertextsAt.UnixNano()
	n.metrics.MaterializedUnixNano = now.UnixNano()
	n.metrics.AgreedLists = len(agreed.Lists)
	n.metrics.AgreedSetHash = agreed.Hash
	n.metrics.AgreedCiphertexts = agreed.TotalItems
	n.metrics.MergedSetHash = merged.Hash
	n.metrics.SelectedCiphertexts = 0
	n.metrics.SkippedCiphertexts = merged.SkippedItems
	n.metrics.SelectedGas = merged.SelectedGas
	n.refreshMetricsLocked()
	n.result = &Result{
		Slot:         n.id,
		NodeID:       n.self.ID,
		BatchID:      "",
		Ciphertexts:  0,
		Plaintexts:   []string{},
		Materialized: n.material,
		LatencyMS:    n.metrics.TotalSlotMS,
		Metrics:      n.metrics.snapshot(),
	}
	n.phase = slotCompleted
	n.completedSlots++
	if n.observability != nil {
		n.observability.slotCompleted(n.metrics.snapshot())
	}
	log.Printf("event=result_available node_id=%d slot=%d selected_txs=0 selected_gas=%d empty=true", n.self.ID, n.id, merged.SelectedGas)
}

// tryCombine attempts BTE reconstruction once the node has threshold shares for
// every planned sub-batch.
func (n *Node) tryCombine() {
	attempt, ok := n.claimCombine()
	if !ok {
		return
	}
	shares, rejected := matchingSharesForPlan(attempt.shares, attempt.plan)
	if rejected > 0 {
		log.Printf("ignored %d shares for other batches or sub-batches while waiting for batch %s", rejected, hex.EncodeToString(attempt.plan.BatchID[:]))
	}
	thresholdAt := time.Now()
	log.Printf("event=threshold_reached node_id=%d slot=%d batch_id=%s shares=%d", n.self.ID, n.id, hex.EncodeToString(attempt.plan.BatchID[:]), len(shares))
	results, err := n.cluster.CombineShares(attempt.plan, shares)
	if err != nil {
		log.Printf("combine shares: %v", err)
		if n.finishFailedCombine(attempt.shareVersion) {
			n.tryCombine()
		}
		return
	}
	combineAt := time.Now()
	plaintexts := make([]string, len(results))
	plaintextHashes := make([]string, len(results))
	ethereumTxHashes := make([]string, len(results))
	ethereumTxs := make([]EthereumTxSummary, len(results))
	for i, r := range results {
		if r.Err != nil || !r.HashOK {
			plaintexts[i] = "ERROR:" + r.Err.Error()
			plaintextHashes[i] = ""
			continue
		}
		plaintexts[i] = "0x" + hex.EncodeToString(r.RawTx)
		plaintextHashes[i] = hashHex(r.RawTx)
		txSummary, err := ethdemo.Parse(r.RawTx)
		if err != nil {
			plaintexts[i] = "ERROR:invalid ethereum transaction:" + err.Error()
			continue
		}
		ethereumTxHashes[i] = txSummary.Hash
		ethereumTxs[i] = txSummary
	}
	attempt.material.PlaintextsHex = plaintexts
	attempt.material.PlaintextHashes = plaintextHashes
	attempt.material.EthereumTxHashes = ethereumTxHashes
	attempt.material.EthereumTxs = ethereumTxs
	materializedAt := time.Now()
	result := &Result{
		Slot:         n.id,
		NodeID:       n.self.ID,
		BatchID:      hex.EncodeToString(attempt.plan.BatchID[:]),
		Ciphertexts:  len(results),
		Plaintexts:   plaintexts,
		Materialized: attempt.material,
	}
	n.mu.Lock()
	n.combineInFlight = false
	if n.result == nil {
		n.metricTimes.threshold = thresholdAt
		n.metricTimes.combineDone = combineAt
		n.metricTimes.materialized = materializedAt
		n.metrics.ThresholdUnixNano = thresholdAt.UnixNano()
		n.metrics.CombineDoneUnixNano = combineAt.UnixNano()
		n.metrics.MaterializedUnixNano = materializedAt.UnixNano()
		n.refreshMetricsLocked()
		result.LatencyMS = n.metrics.TotalSlotMS
		result.Metrics = n.metrics.snapshot()
		n.result = result
		n.phase = slotCompleted
		n.completedSlots++
		if n.observability != nil {
			n.observability.slotCompleted(result.Metrics)
		}
		log.Printf("event=result_available node_id=%d slot=%d batch_id=%s selected_txs=%d selected_gas=%d", n.self.ID, n.id, result.BatchID, len(result.Plaintexts), result.Materialized.SelectedGas)
	}
	n.mu.Unlock()
}

type combineAttempt struct {
	plan         be.BatchPlan
	material     MaterializedTransactionSet
	shares       []be.DecryptionShare
	shareVersion uint64
}

// claimCombine admits at most one expensive reconstruction per node. Share
// receipt remains independent, and a snapshot version allows a failed attempt
// to retry only when it can include newly accepted shares.
func (n *Node) claimCombine() (combineAttempt, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.result != nil || !n.planned || !n.shareGenerationDone || n.combineInFlight {
		return combineAttempt{}, false
	}
	shares := append([]be.DecryptionShare(nil), n.shares...)
	matching, _ := matchingSharesForPlan(shares, n.plan)
	if !hasThresholdPerSubBatch(matching, n.plan, n.cfg.Threshold) {
		return combineAttempt{}, false
	}
	n.combineInFlight = true
	n.metrics.CombineAttempts++
	return combineAttempt{
		plan:         n.plan,
		material:     n.material,
		shares:       shares,
		shareVersion: n.shareVersion,
	}, true
}

// finishFailedCombine releases the single-flight claim and reports whether a
// newer share set makes exactly one immediate retry useful.
func (n *Node) finishFailedCombine(attemptVersion uint64) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.combineInFlight = false
	return n.result == nil && n.shareVersion > attemptVersion
}

// addWireShare validates and converts a transport share into the BTE library
// type before deduplication.
func (n *Node) addWireShare(w WireShare) error {
	batchID, err := hex.DecodeString(w.BatchIDHex)
	if err != nil {
		return err
	}
	if len(batchID) != 32 {
		return fmt.Errorf("batch id has %d bytes", len(batchID))
	}
	point, err := unmarshalPointHex(n.suite, w.PointHex)
	if err != nil {
		return err
	}
	var id [32]byte
	copy(id[:], batchID)
	return n.addShare(be.DecryptionShare{
		OperatorID: w.OperatorID,
		BatchID:    id,
		SubBatchID: w.SubBatchID,
		Share:      &share.PubShare{I: uint32(w.OperatorID), V: point},
	})
}

// addShare deduplicates a BTE share and updates share-related metrics.
func (n *Node) addShare(d be.DecryptionShare) error {
	key := fmt.Sprintf("%x/%d/%d", d.BatchID, d.SubBatchID, d.OperatorID)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seenShares[key] {
		return nil
	}
	n.seenShares[key] = true
	n.shares = append(n.shares, d)
	n.shareVersion++
	n.metrics.SharesAccepted++
	if d.OperatorID == int(n.self.ID) {
		n.metrics.SharesGenerated++
	}
	if n.metrics.FirstShareUnixNano == 0 {
		n.metrics.FirstShareUnixNano = time.Now().UnixNano()
	}
	return nil
}

// marshalShare converts a BTE decryption share into transport-safe hex fields.
func (n *Node) marshalShare(d be.DecryptionShare) (WireShare, error) {
	pointHex, err := marshalPointHex(d.Share.V)
	if err != nil {
		return WireShare{}, err
	}
	return WireShare{
		OperatorID: d.OperatorID,
		BatchIDHex: hex.EncodeToString(d.BatchID[:]),
		SubBatchID: d.SubBatchID,
		PointHex:   pointHex,
	}, nil
}

// sendEnvelope fills in local routing fields and sends one direct envelope in a
// goroutine so ACS state-machine progress is not blocked by network I/O.
func (n *Node) sendEnvelope(to uint64, env WireEnvelope) {
	go func() {
		n.lifecycleMu.RLock()
		defer n.lifecycleMu.RUnlock()
		if env.Slot != n.id {
			return
		}
		if n.faults.Delay > 0 {
			time.Sleep(n.faults.Delay)
		}
		env.From = n.self.ID
		env.To = to
		env.Direct = true
		size, err := n.transport.Send(context.Background(), to, env)
		if err != nil {
			log.Printf("send %s to %d failed: %v", env.Kind, to, err)
			return
		}
		n.recordOutbound(env.Kind, size)
	}()
}

func (n *Node) recordInbound(kind string, size int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.metrics.InboundMessages[kind]++
	n.metrics.InboundBytes[kind] += int64(size)
	if n.observability != nil {
		n.observability.recordProtocol("inbound", kind, size)
	}
}

func (n *Node) recordOutbound(kind string, size int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.metrics.OutboundMessages[kind]++
	n.metrics.OutboundBytes[kind] += int64(size)
	if n.observability != nil {
		n.observability.recordProtocol("outbound", kind, size)
	}
}

func (n *Node) markSlotFailed(reason string) {
	n.mu.Lock()
	n.failedSlots++
	n.mu.Unlock()
	if n.observability != nil {
		n.observability.slotFailed(reason)
	}
}

// refreshMetricsLocked derives durations from monotonic readings. Several
// stages overlap (notably local share generation and threshold waiting), so
// these fields describe event intervals rather than additive accounting.
func (n *Node) refreshMetricsLocked() {
	t := n.metricTimes
	n.metrics.ProposalPreparationUS = durationUS(t.slotStart, t.proposalReady)
	n.metrics.ACSUS = durationUS(t.proposalReady, t.acsDecision)
	n.metrics.MergePlanUS = durationUS(t.acsDecision, t.planDone)
	n.metrics.ACSOutputDecodeUS = durationUS(t.acsDecision, t.acsOutputDecoded)
	n.metrics.AgreedSetUS = durationUS(t.acsOutputDecoded, t.agreedSetDone)
	n.metrics.MergeUS = durationUS(t.agreedSetDone, t.mergeDone)
	n.metrics.CiphertextDecodeUS = durationUS(t.mergeDone, t.ciphertextsDecoded)
	n.metrics.BatchPlanUS = durationUS(t.ciphertextsDecoded, t.planDone)
	n.metrics.ShareGenerationUS = durationUS(t.shareGenerationStart, t.sharesDone)
	n.metrics.ThresholdWaitUS = durationUS(t.planDone, t.threshold)
	n.metrics.CombineUS = durationUS(t.threshold, t.combineDone)
	n.metrics.MaterializationUS = durationUS(t.combineDone, t.materialized)
	n.metrics.CommitToPlaintextUS = durationUS(t.acsDecision, t.materialized)
	n.metrics.TotalSlotUS = durationUS(t.slotStart, t.materialized)
	n.metrics.MetricsFinalized = !t.materialized.IsZero() && !t.sharesDone.IsZero()

	n.metrics.ACSMS = n.metrics.ACSUS / 1000
	n.metrics.PlanMS = n.metrics.MergePlanUS / 1000
	n.metrics.ShareGenerationMS = n.metrics.ShareGenerationUS / 1000
	n.metrics.CommitToPlaintextMS = n.metrics.CommitToPlaintextUS / 1000
	n.metrics.TotalSlotMS = n.metrics.TotalSlotUS / 1000
}

func durationUS(start, end time.Time) int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Microseconds()
}

func (m Metrics) snapshot() Metrics {
	out := m
	out.OutboundMessages = cloneIntMap(m.OutboundMessages)
	out.InboundMessages = cloneIntMap(m.InboundMessages)
	out.OutboundBytes = cloneInt64Map(m.OutboundBytes)
	out.InboundBytes = cloneInt64Map(m.InboundBytes)
	return out
}

func cloneIntMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneInt64Map(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func corruptHex(s string) string {
	if s == "" {
		return s
	}
	if s[0] == '0' {
		return "1" + s[1:]
	}
	return "0" + s[1:]
}

func (n *Node) writeResultWhenReady(path string) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		n.lifecycleMu.RLock()
		n.mu.Lock()
		result := n.result
		n.mu.Unlock()
		n.lifecycleMu.RUnlock()
		if result == nil {
			continue
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			log.Printf("marshal result: %v", err)
			return
		}
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Printf("create result dir: %v", err)
				return
			}
		}
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			log.Printf("write result: %v", err)
		}
		return
	}
}

func hasThresholdPerSubBatch(shares []be.DecryptionShare, plan be.BatchPlan, threshold int) bool {
	counts := make(map[int]map[int]bool)
	for _, d := range shares {
		if counts[d.SubBatchID] == nil {
			counts[d.SubBatchID] = make(map[int]bool)
		}
		counts[d.SubBatchID][d.OperatorID] = true
	}
	for i := range plan.SubBatches {
		if len(counts[i]) < threshold {
			return false
		}
	}
	return true
}

func matchingSharesForPlan(shares []be.DecryptionShare, plan be.BatchPlan) ([]be.DecryptionShare, int) {
	matching := make([]be.DecryptionShare, 0, len(shares))
	rejected := 0
	for _, d := range shares {
		if d.BatchID != plan.BatchID || d.SubBatchID < 0 || d.SubBatchID >= len(plan.SubBatches) {
			rejected++
			continue
		}
		matching = append(matching, d)
	}
	return matching, rejected
}
