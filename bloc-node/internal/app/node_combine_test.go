package app

import (
	"sync"
	"testing"

	"btd/be"
)

func TestClaimCombineRequiresGenerationAndThreshold(t *testing.T) {
	node := combineTestNode(2)
	node.shareGenerationDone = false
	if _, ok := node.claimCombine(); ok {
		t.Fatal("combine claimed before local share generation completed")
	}

	node.shareGenerationDone = true
	node.shares = node.shares[:1]
	if _, ok := node.claimCombine(); ok {
		t.Fatal("combine claimed without threshold shares")
	}

	node.shares = combineTestShares(node.plan.BatchID, 2)
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
	if err := node.addShare(be.DecryptionShare{OperatorID: 2, BatchID: node.plan.BatchID, SubBatchID: 0}); err != nil {
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
	return &Node{
		cfg: ConfigFile{Threshold: threshold},
		slotState: &slotState{
			planned:             true,
			shareGenerationDone: true,
			plan:                plan,
			shares:              shares,
			shareVersion:        uint64(len(shares)),
			seenShares:          make(map[string]bool),
		},
	}
}

func combineTestShares(batchID [32]byte, count int) []be.DecryptionShare {
	shares := make([]be.DecryptionShare, count)
	for i := range shares {
		shares[i] = be.DecryptionShare{OperatorID: i, BatchID: batchID, SubBatchID: 0}
	}
	return shares
}
