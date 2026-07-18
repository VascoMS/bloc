package app

import (
	"sync"
	"testing"

	"btd/be"
	"go.dedis.ch/kyber/v4/share"
)

func TestClaimCombineRequiresGenerationAndThreshold(t *testing.T) {
	node := combineTestNode(2)
	node.shareGenerationDone = false
	if _, ok := node.claimCombine(); ok {
		t.Fatal("combine claimed before local share generation completed")
	}

	node.shareGenerationDone = true
	setCombineTestShares(node, combineTestShares(node.plan.BatchID, 1))
	if _, ok := node.claimCombine(); ok {
		t.Fatal("combine claimed without threshold shares")
	}

	setCombineTestShares(node, combineTestShares(node.plan.BatchID, 2))
	if _, ok := node.claimCombine(); !ok {
		t.Fatal("combine not claimed after generation and threshold")
	}
}

func TestClaimCombineIsSingleFlight(t *testing.T) {
	node := combineTestNode(3)
	const callers = 64
	start := make(chan struct{})
	claimed := make(chan bool, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, ok := node.claimCombine()
			claimed <- ok
		}()
	}
	close(start)
	wg.Wait()
	close(claimed)

	count := 0
	for ok := range claimed {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("claimed attempts = %d, want 1", count)
	}
	if node.metrics.CombineAttempts != 1 {
		t.Fatalf("combine attempts = %d, want 1", node.metrics.CombineAttempts)
	}
}

func TestFailedCombineRetriesOnlyForNewShares(t *testing.T) {
	node := combineTestNode(2)
	attempt, ok := node.claimCombine()
	if !ok {
		t.Fatal("initial combine was not claimed")
	}
	if node.finishFailedCombine(attempt.shareVersion) {
		t.Fatal("failed combine requested retry without newer shares")
	}

	attempt, ok = node.claimCombine()
	if !ok {
		t.Fatal("second combine was not claimed")
	}
	if err := node.addShare(be.DecryptionShare{OperatorID: 2, BatchID: node.plan.BatchID, SubBatchID: 0, Share: &share.PubShare{I: 2, V: newSuite().G1().Point().Base()}}); err != nil {
		t.Fatal(err)
	}
	if !node.finishFailedCombine(attempt.shareVersion) {
		t.Fatal("failed combine did not request retry after a newer share")
	}
}

func TestSuccessfulResultPreventsFurtherCombine(t *testing.T) {
	node := combineTestNode(2)
	if _, ok := node.claimCombine(); !ok {
		t.Fatal("initial combine was not claimed")
	}
	node.mu.Lock()
	node.combineInFlight = false
	node.result = &Result{}
	node.mu.Unlock()
	if _, ok := node.claimCombine(); ok {
		t.Fatal("combine claimed after successful result")
	}
}

func TestCombineAttemptBudgetIsCumulativeAcrossRetries(t *testing.T) {
	node := combineTestNode(2)
	node.combineAttemptsLeft[0] = 4
	node.recordCombineStats(be.CombineStats{AttemptsBySubBatch: []int{2}})
	node.recordCombineStats(be.CombineStats{AttemptsBySubBatch: []int{2}})
	if node.combineAttemptsLeft[0] != 0 {
		t.Fatalf("remaining attempts = %d, want 0", node.combineAttemptsLeft[0])
	}
	if node.metrics.ShareSubsetAttempts != 4 {
		t.Fatalf("recorded subset attempts = %d, want 4", node.metrics.ShareSubsetAttempts)
	}
	if _, ok := node.claimCombine(); ok {
		t.Fatal("combine claimed after cumulative budget exhaustion")
	}
}

func combineTestNode(threshold int) *Node {
	var batchID [32]byte
	batchID[0] = 1
	plan := be.BatchPlan{
		BatchID: batchID,
		SubBatches: [][]be.BatchItem{
			{{OriginalPosition: 0}},
		},
	}
	shares := combineTestShares(batchID, threshold)
	node := &Node{
		cfg:  ConfigFile{N: 4, BMax: 8, Threshold: threshold, Limits: defaultResourceLimits()},
		self: NodeConfig{ID: 0},
		peers: map[uint64]NodeConfig{
			0: {ID: 0}, 1: {ID: 1}, 2: {ID: 2}, 3: {ID: 3},
		},
		nodeIDs: []uint64{0, 1, 2, 3},
		slotState: &slotState{
			planned:             true,
			shareGenerationDone: true,
			plan:                plan,
			shareVersion:        uint64(len(shares)),
			shareCandidates:     make(map[int]*operatorShareCandidates),
			combineAttemptsLeft: []int{defaultMaxCombineAttemptsPerSubBatch},
		},
	}
	setCombineTestShares(node, shares)
	return node
}

func combineTestShares(batchID [32]byte, count int) []be.DecryptionShare {
	shares := make([]be.DecryptionShare, count)
	suite := newSuite()
	for i := range shares {
		shares[i] = be.DecryptionShare{OperatorID: i, BatchID: batchID, SubBatchID: 0, Share: &share.PubShare{I: uint32(i), V: suite.G1().Point().Base()}}
	}
	return shares
}

func setCombineTestShares(node *Node, shares []be.DecryptionShare) {
	node.shareCandidates = make(map[int]*operatorShareCandidates)
	for _, candidate := range shares {
		encoded, _ := candidate.Share.V.MarshalBinary()
		node.shareCandidates[candidate.OperatorID] = &operatorShareCandidates{
			batchID:  candidate.BatchID,
			batchSet: true,
			shares:   map[int]retainedShare{candidate.SubBatchID: {value: candidate, encoded: encoded}},
		}
	}
}
