package be

import (
	"bytes"
	"fmt"
	"testing"
)

type combineBenchmarkCase struct {
	name      string
	n         int
	threshold int
	workers   int
}

type combineBenchmarkCommittee struct {
	name      string
	n         int
	threshold int
}

func combineBenchmarkCommittees() []combineBenchmarkCommittee {
	return []combineBenchmarkCommittee{
		{name: "n4-t3", n: 4, threshold: 3},
		{name: "n7-t5", n: 7, threshold: 5},
		{name: "n10-t7", n: 10, threshold: 7},
	}
}

func combineBenchmarkWorkers() []int {
	return []int{1, 2, 4, 8}
}

func combineBenchmarkCases() []combineBenchmarkCase {
	workers := combineBenchmarkWorkers()
	committees := combineBenchmarkCommittees()
	cases := make([]combineBenchmarkCase, 0, len(committees)*len(workers))
	for _, committee := range committees {
		for _, workerCount := range workers {
			cases = append(cases, combineBenchmarkCase{
				name:      fmt.Sprintf("%s/workers-%d", committee.name, workerCount),
				n:         committee.n,
				threshold: committee.threshold,
				workers:   workerCount,
			})
		}
	}
	return cases
}

func TestCombineBenchmarkCasesCoverWorkerScalingMatrix(t *testing.T) {
	want := []combineBenchmarkCase{
		{name: "n4-t3/workers-1", n: 4, threshold: 3, workers: 1},
		{name: "n4-t3/workers-2", n: 4, threshold: 3, workers: 2},
		{name: "n4-t3/workers-4", n: 4, threshold: 3, workers: 4},
		{name: "n4-t3/workers-8", n: 4, threshold: 3, workers: 8},
		{name: "n7-t5/workers-1", n: 7, threshold: 5, workers: 1},
		{name: "n7-t5/workers-2", n: 7, threshold: 5, workers: 2},
		{name: "n7-t5/workers-4", n: 7, threshold: 5, workers: 4},
		{name: "n7-t5/workers-8", n: 7, threshold: 5, workers: 8},
		{name: "n10-t7/workers-1", n: 10, threshold: 7, workers: 1},
		{name: "n10-t7/workers-2", n: 10, threshold: 7, workers: 2},
		{name: "n10-t7/workers-4", n: 10, threshold: 7, workers: 4},
		{name: "n10-t7/workers-8", n: 10, threshold: 7, workers: 8},
	}
	got := combineBenchmarkCases()
	if len(got) != len(want) {
		t.Fatalf("benchmark matrix has %d leaf cases, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("benchmark case %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

type combineBenchmarkFixture struct {
	cluster *ClusterBTE
	plan    BatchPlan
	shares  []DecryptionShare
	raw     [][]byte
}

func newCombineBenchmarkFixture(b *testing.B, batchSize, n, threshold int) combineBenchmarkFixture {
	b.Helper()
	cluster := newTestCluster(b, batchSize, n, threshold)
	raw := make([][]byte, batchSize)
	ciphertexts := make([]Ciphertext, batchSize)
	for i := range ciphertexts {
		raw[i] = []byte(fmt.Sprintf("combine benchmark transaction %d", i))
		ciphertext, err := cluster.EncryptTx(raw[i], i)
		if err != nil {
			b.Fatalf("encrypt transaction %d: %v", i, err)
		}
		ciphertexts[i] = ciphertext
	}
	plan, err := cluster.PlanBatch(ciphertexts)
	if err != nil {
		b.Fatalf("plan batch: %v", err)
	}
	if len(plan.SubBatches) != 46 {
		b.Fatalf("planned sub-batches = %d, want 46", len(plan.SubBatches))
	}
	shares := make([]DecryptionShare, 0, cluster.btd.T*len(plan.SubBatches))
	for subBatchID := range plan.SubBatches {
		for _, secret := range cluster.Shares[:cluster.btd.T] {
			candidate, shareErr := cluster.MakeShare(secret, plan, subBatchID)
			if shareErr != nil {
				b.Fatalf("make share for sub-batch %d: %v", subBatchID, shareErr)
			}
			shares = append(shares, candidate)
		}
	}
	return combineBenchmarkFixture{cluster: cluster, plan: plan, shares: shares, raw: raw}
}

func (fixture combineBenchmarkFixture) validate(b *testing.B, workers int, results []PlaintextResult, stats CombineStats, combineErr error) {
	b.Helper()
	if combineErr != nil {
		b.Fatalf("combine with %d workers: %v", workers, combineErr)
	}
	configured, effective, err := effectiveCombineWorkers(workers, len(fixture.plan.SubBatches))
	if err != nil {
		b.Fatalf("normalize %d workers: %v", workers, err)
	}
	if stats.ConfiguredWorkers != configured || stats.EffectiveWorkers != effective {
		b.Fatalf("worker stats = configured %d effective %d, want %d and %d", stats.ConfiguredWorkers, stats.EffectiveWorkers, configured, effective)
	}
	if len(stats.AttemptsBySubBatch) != len(fixture.plan.SubBatches) {
		b.Fatalf("attempt vector length = %d, want %d", len(stats.AttemptsBySubBatch), len(fixture.plan.SubBatches))
	}
	for subBatchID, attempts := range stats.AttemptsBySubBatch {
		if attempts != 1 {
			b.Fatalf("sub-batch %d attempts = %d, want 1", subBatchID, attempts)
		}
	}
	if len(results) != len(fixture.raw) {
		b.Fatalf("result count = %d, want %d", len(results), len(fixture.raw))
	}
	for position, result := range results {
		if result.Err != nil {
			b.Fatalf("result %d: %v", position, result.Err)
		}
		if !bytes.Equal(result.Plaintext, fixture.raw[position]) {
			b.Fatalf("result %d plaintext mismatch", position)
		}
	}
}

func BenchmarkCombineSharesBoundedB512(b *testing.B) {
	for _, committee := range combineBenchmarkCommittees() {
		b.Run(committee.name, func(b *testing.B) {
			fixture := newCombineBenchmarkFixture(b, 512, committee.n, committee.threshold)
			for _, workerCount := range combineBenchmarkWorkers() {
				b.Run(fmt.Sprintf("workers-%d", workerCount), func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						results, stats, err := fixture.cluster.CombineSharesBounded(
							fixture.plan,
							fixture.shares,
							CombineOptions{MaxAttemptsPerSubBatch: 256, MaxWorkers: workerCount},
						)
						b.StopTimer()
						fixture.validate(b, workerCount, results, stats, err)
						b.ReportMetric(float64(stats.ConfiguredWorkers), "configured_workers")
						b.ReportMetric(float64(stats.EffectiveWorkers), "effective_workers")
						b.ReportMetric(float64(len(fixture.plan.SubBatches)), "sub_batches")
						if i+1 < b.N {
							b.StartTimer()
						}
					}
				})
			}
		})
	}
}
