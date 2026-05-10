package app

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"btd/be"
	"github.com/anthdm/hbbft"
	"go.dedis.ch/kyber/v4/share"
)

func newNode(cfg ConfigFile, id uint64, faults FaultConfig) (*Node, error) {
	normalizeConfig(&cfg)
	var self NodeConfig
	peers := make(map[uint64]NodeConfig)
	var ids []uint64
	for _, n := range cfg.Nodes {
		ids = append(ids, n.ID)
		peers[n.ID] = n
		if n.ID == id {
			self = n
		}
	}
	if self.ConsensusAddr == "" {
		return nil, fmt.Errorf("node id %d not found in config", id)
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
		cfg:         cfg,
		self:        self,
		nodeIDs:     ids,
		peers:       peers,
		cluster:     cluster,
		secret:      secret,
		suite:       suite,
		faults:      faults,
		seenPending: make(map[string]bool),
		seenShares:  make(map[string]bool),
		metrics: Metrics{
			SharesNeededPerSub: cfg.Threshold,
			MaxDecryptedGas:    cfg.Blockspace.MaxDecryptedGas,
			MaxDecryptedTxs:    effectiveMaxDecryptedTxs(cfg.Blockspace, cfg.BMax),
			OutboundMessages:   make(map[string]int),
			InboundMessages:    make(map[string]int),
			OutboundBytes:      make(map[string]int64),
			InboundBytes:       make(map[string]int64),
		},
	}
	node.slot = hbbft.NewSlotACS(hbbft.SlotConfig{
		Config: hbbft.Config{N: cfg.N, F: (cfg.N - 1) / 3, ID: id, Nodes: ids, BatchSize: cfg.BMax},
		Slot:   cfg.Slot,
	})
	return node, nil
}

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

func (n *Node) listenConsensus() error {
	ln, err := net.Listen("tcp", n.self.ConsensusAddr)
	if err != nil {
		return err
	}
	log.Printf("node %d consensus listening on %s", n.self.ID, n.self.ConsensusAddr)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("accept: %v", err)
				continue
			}
			go n.handleConn(conn)
		}
	}()
	return nil
}

func (n *Node) listenHTTP(outPath string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": n.self.ID})
	})
	mux.HandleFunc("/tx", n.handleSubmitTx)
	mux.HandleFunc("/start", func(w http.ResponseWriter, _ *http.Request) {
		if err := n.startConsensus(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
	})
	mux.HandleFunc("/result", func(w http.ResponseWriter, _ *http.Request) {
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.result == nil {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
			return
		}
		writeJSON(w, http.StatusOK, n.result)
	})
	server := &http.Server{Addr: n.self.HTTPAddr, Handler: mux}
	log.Printf("node %d http listening on http://%s", n.self.ID, n.self.HTTPAddr)
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

func (n *Node) handleSubmitTx(w http.ResponseWriter, r *http.Request) {
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
	if _, ok := parseBigInt(req.EffectiveFeePerGasWei); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "effective_fee_per_gas_wei must be a non-negative integer"})
		return
	}
	n.mu.Lock()
	index := len(n.pending) % n.cfg.BMax
	n.mu.Unlock()
	ct, err := n.cluster.EncryptTx(raw, index, n.cfg.ClusterID, n.cfg.Slot)
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

func (n *Node) startConsensus() error {
	var err error
	n.startOnce.Do(func() {
		start := time.Now()
		list, buildErr := n.buildInclusionList()
		if buildErr != nil {
			err = buildErr
			return
		}
		if n.faults.OmitProposal {
			list.Items = nil
			list.Hash = hashInclusionList(list)
		}
		n.mu.Lock()
		n.metrics.SlotStartUnixNano = start.UnixNano()
		n.metrics.ProposalTxs = len(list.Items)
		n.metrics.ProposalHash = list.Hash
		n.mu.Unlock()
		var buf bytes.Buffer
		err = gob.NewEncoder(&buf).Encode(list)
		if err != nil {
			return
		}
		log.Printf("node %d starting slot %d with inclusion list %s (%d items)", n.self.ID, n.cfg.Slot, list.Hash, len(list.Items))
		err = n.slot.InputBatch(buf.Bytes())
		if err != nil {
			return
		}
		n.drainACSMessages()
		n.tryOutput()
	})
	return err
}

func (n *Node) handleConn(conn net.Conn) {
	defer conn.Close()
	data, err := io.ReadAll(conn)
	if err != nil {
		log.Printf("read envelope: %v", err)
		return
	}
	var env WireEnvelope
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&env); err != nil {
		log.Printf("decode envelope: %v", err)
		return
	}
	n.recordInbound(env.Kind, len(data))
	switch env.Kind {
	case "acs":
		if env.ACS == nil {
			log.Printf("nil acs message from %d", env.From)
			return
		}
		if err := n.slot.HandleMessage(env.From, env.ACS); err != nil {
			if isBenignDuplicate(err) {
				return
			}
			log.Printf("handle acs from %d: %v", env.From, err)
			return
		}
		n.drainACSMessages()
		n.tryOutput()
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

func isBenignDuplicate(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "received multiple readys") ||
		strings.Contains(msg, "received multiple echos")
}

func (n *Node) drainACSMessages() {
	for _, msg := range n.slot.Messages() {
		slotMsg, ok := msg.Payload.(*hbbft.SlotMessage)
		if !ok {
			log.Printf("unexpected slot payload %T", msg.Payload)
			continue
		}
		n.sendEnvelope(msg.To, WireEnvelope{From: n.self.ID, Kind: "acs", Slot: n.cfg.Slot, ACS: slotMsg})
	}
}

func (n *Node) tryOutput() {
	out := n.slot.Output()
	if out == nil {
		return
	}
	decisionAt := time.Now()
	lists := make([]InclusionList, 0, len(out.OrderedBatches))
	for _, accepted := range out.OrderedBatches {
		var list InclusionList
		if err := gob.NewDecoder(bytes.NewReader(accepted.Batch)).Decode(&list); err != nil {
			log.Printf("decode accepted inclusion list from %d: %v", accepted.ProposerID, err)
			return
		}
		list.Hash = hashInclusionList(list)
		lists = append(lists, list)
	}
	agreed := newAgreedInclusionSet(n.cfg.Slot, lists)
	merged := mergeInclusionLists(n.cfg.Slot, lists, n.cfg.Blockspace, n.cfg.BMax)
	var encrypted []be.Ciphertext
	for _, item := range merged.Items {
		ct, err := n.cluster.UnmarshalCiphertext(item.Ciphertext)
		if err != nil {
			log.Printf("decode ciphertext %s: %v", item.Hash, err)
			return
		}
		encrypted = append(encrypted, ct)
	}
	if len(encrypted) == 0 {
		n.finishEmptyMaterializedSet(decisionAt, agreed, merged)
		return
	}
	plan, err := n.cluster.PlanBatch(encrypted)
	if err != nil {
		log.Printf("plan batch: %v", err)
		return
	}
	planAt := time.Now()
	n.mu.Lock()
	if n.planned {
		n.mu.Unlock()
		return
	}
	n.plan = plan
	n.material = MaterializedTransactionSet{
		Slot:            n.cfg.Slot,
		AgreedSetHash:   agreed.Hash,
		MergedSetHash:   merged.Hash,
		SelectedGas:     merged.SelectedGas,
		EncryptedHashes: encryptedHashes(merged.Items),
	}
	n.planned = true
	n.metrics.ACSDecisionUnixNano = decisionAt.UnixNano()
	n.metrics.PlanDoneUnixNano = planAt.UnixNano()
	n.metrics.AgreedLists = len(agreed.Lists)
	n.metrics.AgreedSetHash = agreed.Hash
	n.metrics.AgreedCiphertexts = agreed.TotalItems
	n.metrics.MergedSetHash = merged.Hash
	n.metrics.SelectedCiphertexts = len(merged.Items)
	n.metrics.SkippedCiphertexts = merged.SkippedItems
	n.metrics.SelectedGas = merged.SelectedGas
	n.metrics.SubBatches = len(plan.SubBatches)
	if n.metrics.SlotStartUnixNano != 0 {
		n.metrics.ACSMS = decisionAt.Sub(time.Unix(0, n.metrics.SlotStartUnixNano)).Milliseconds()
	}
	n.metrics.PlanMS = planAt.Sub(decisionAt).Milliseconds()
	n.mu.Unlock()
	log.Printf("node %d ACS decided %d lists/%d candidates; selected %d txs (%d gas); batch %s has %d sub-batches", n.self.ID, len(agreed.Lists), agreed.TotalItems, len(encrypted), merged.SelectedGas, hex.EncodeToString(plan.BatchID[:]), len(plan.SubBatches))
	shareStart := time.Now()
	for subBatchID := range plan.SubBatches {
		if n.faults.WithholdShare {
			continue
		}
		d, err := n.cluster.MakeShare(n.secret, plan, subBatchID)
		if err != nil {
			log.Printf("make share %d: %v", subBatchID, err)
			return
		}
		if err := n.addShare(d); err != nil {
			log.Printf("add own share: %v", err)
			return
		}
		wire, err := n.marshalShare(d)
		if err != nil {
			log.Printf("marshal share: %v", err)
			return
		}
		if n.faults.CorruptShare {
			wire.PointHex = corruptHex(wire.PointHex)
		}
		for _, peer := range n.nodeIDs {
			if peer != n.self.ID {
				n.sendEnvelope(peer, WireEnvelope{From: n.self.ID, Kind: "share", Slot: n.cfg.Slot, Share: &wire})
			}
		}
	}
	n.mu.Lock()
	n.metrics.SharesDoneUnixNano = time.Now().UnixNano()
	n.metrics.ShareGenerationMS = time.Since(shareStart).Milliseconds()
	n.mu.Unlock()
	n.tryCombine()
}

func (n *Node) finishEmptyMaterializedSet(decisionAt time.Time, agreed AgreedInclusionSet, merged MergedEncryptedSet) {
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.result != nil || n.planned {
		return
	}
	n.planned = true
	n.material = MaterializedTransactionSet{
		Slot:            n.cfg.Slot,
		AgreedSetHash:   agreed.Hash,
		MergedSetHash:   merged.Hash,
		SelectedGas:     merged.SelectedGas,
		EncryptedHashes: encryptedHashes(merged.Items),
		PlaintextHashes: []string{},
		PlaintextsHex:   []string{},
	}
	n.metrics.ACSDecisionUnixNano = decisionAt.UnixNano()
	n.metrics.PlanDoneUnixNano = now.UnixNano()
	n.metrics.CombineDoneUnixNano = now.UnixNano()
	n.metrics.AgreedLists = len(agreed.Lists)
	n.metrics.AgreedSetHash = agreed.Hash
	n.metrics.AgreedCiphertexts = agreed.TotalItems
	n.metrics.MergedSetHash = merged.Hash
	n.metrics.SelectedCiphertexts = 0
	n.metrics.SkippedCiphertexts = merged.SkippedItems
	n.metrics.SelectedGas = merged.SelectedGas
	if n.metrics.SlotStartUnixNano != 0 {
		slotStart := time.Unix(0, n.metrics.SlotStartUnixNano)
		n.metrics.ACSMS = decisionAt.Sub(slotStart).Milliseconds()
		n.metrics.TotalSlotMS = now.Sub(slotStart).Milliseconds()
	}
	n.metrics.PlanMS = now.Sub(decisionAt).Milliseconds()
	n.result = &Result{
		Slot:         n.cfg.Slot,
		NodeID:       n.self.ID,
		BatchID:      "",
		Ciphertexts:  0,
		Plaintexts:   []string{},
		Materialized: n.material,
		LatencyMS:    n.metrics.TotalSlotMS,
		Metrics:      n.metrics.snapshot(),
	}
	log.Printf("node %d materialized empty transaction set after ACS (%d lists/%d candidates)", n.self.ID, len(agreed.Lists), agreed.TotalItems)
}

func (n *Node) tryCombine() {
	n.mu.Lock()
	if n.result != nil || !n.planned {
		n.mu.Unlock()
		return
	}
	plan := n.plan
	material := n.material
	shares := append([]be.DecryptionShare(nil), n.shares...)
	n.mu.Unlock()
	if !hasThresholdPerSubBatch(shares, plan, n.cfg.Threshold) {
		return
	}
	thresholdAt := time.Now()
	results, err := n.cluster.CombineShares(plan, shares)
	if err != nil {
		log.Printf("combine shares: %v", err)
		return
	}
	combineAt := time.Now()
	plaintexts := make([]string, len(results))
	plaintextHashes := make([]string, len(results))
	for i, r := range results {
		if r.Err != nil || !r.HashOK {
			plaintexts[i] = "ERROR:" + r.Err.Error()
			plaintextHashes[i] = ""
			continue
		}
		plaintexts[i] = "0x" + hex.EncodeToString(r.RawTx)
		plaintextHashes[i] = hashHex(r.RawTx)
	}
	material.PlaintextsHex = plaintexts
	material.PlaintextHashes = plaintextHashes
	result := &Result{
		Slot:         n.cfg.Slot,
		NodeID:       n.self.ID,
		BatchID:      hex.EncodeToString(plan.BatchID[:]),
		Ciphertexts:  len(results),
		Plaintexts:   plaintexts,
		Materialized: material,
	}
	n.mu.Lock()
	if n.result == nil {
		n.metrics.ThresholdUnixNano = thresholdAt.UnixNano()
		n.metrics.CombineDoneUnixNano = combineAt.UnixNano()
		if n.metrics.ACSDecisionUnixNano != 0 {
			n.metrics.CommitToPlaintextMS = combineAt.Sub(time.Unix(0, n.metrics.ACSDecisionUnixNano)).Milliseconds()
		}
		if n.metrics.SlotStartUnixNano != 0 {
			n.metrics.TotalSlotMS = combineAt.Sub(time.Unix(0, n.metrics.SlotStartUnixNano)).Milliseconds()
		}
		result.LatencyMS = n.metrics.TotalSlotMS
		result.Metrics = n.metrics.snapshot()
		n.result = result
		log.Printf("node %d decrypted batch %s with %d plaintext txs", n.self.ID, result.BatchID, len(result.Plaintexts))
	}
	n.mu.Unlock()
}

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

func (n *Node) addShare(d be.DecryptionShare) error {
	key := fmt.Sprintf("%x/%d/%d", d.BatchID, d.SubBatchID, d.OperatorID)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seenShares[key] {
		return nil
	}
	n.seenShares[key] = true
	n.shares = append(n.shares, d)
	n.metrics.SharesAccepted++
	if d.OperatorID == int(n.self.ID) {
		n.metrics.SharesGenerated++
	}
	if n.metrics.FirstShareUnixNano == 0 {
		n.metrics.FirstShareUnixNano = time.Now().UnixNano()
	}
	return nil
}

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

func (n *Node) sendEnvelope(to uint64, env WireEnvelope) {
	peer, ok := n.peers[to]
	if !ok {
		log.Printf("unknown peer %d", to)
		return
	}
	go func() {
		if n.faults.Delay > 0 {
			time.Sleep(n.faults.Delay)
		}
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(env); err != nil {
			log.Printf("encode %s to %d failed: %v", env.Kind, to, err)
			return
		}
		data := buf.Bytes()
		var lastErr error
		for attempt := 0; attempt < 20; attempt++ {
			conn, err := net.DialTimeout("tcp", peer.ConsensusAddr, 500*time.Millisecond)
			if err == nil {
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				_, err = conn.Write(data)
				_ = conn.Close()
				if err == nil {
					n.recordOutbound(env.Kind, len(data))
					return
				}
			}
			lastErr = err
			time.Sleep(100 * time.Millisecond)
		}
		log.Printf("send %s to %d failed: %v", env.Kind, to, lastErr)
	}()
}

func (n *Node) recordInbound(kind string, size int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.metrics.InboundMessages[kind]++
	n.metrics.InboundBytes[kind] += int64(size)
}

func (n *Node) recordOutbound(kind string, size int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.metrics.OutboundMessages[kind]++
	n.metrics.OutboundBytes[kind] += int64(size)
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
		n.mu.Lock()
		result := n.result
		n.mu.Unlock()
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
		if d.BatchID != plan.BatchID {
			continue
		}
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
