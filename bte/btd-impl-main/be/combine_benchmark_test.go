package be

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
)

type combineBenchmarkFixture struct {
	cluster *ClusterBTE
	plan    BatchPlan
	shares  []DecryptionShare
	raw     [][]byte
}

func newCombineBenchmarkFixture(b *testing.B, batchSize int) combineBenchmarkFixture {
	b.Helper()
	cluster := newTestCluster(b, batchSize, 4, 3)
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
	fixture := newCombineBenchmarkFixture(b, 512)
	tests := []struct {
		name    string
		workers int
	}{
		{name: "workers-1", workers: 1},
		{name: "workers-2", workers: 2},
		{name: fmt.Sprintf("workers-gomaxprocs-%d", runtime.GOMAXPROCS(0)), workers: runtime.GOMAXPROCS(0)},
	}
	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				results, stats, err := fixture.cluster.CombineSharesBounded(
					fixture.plan,
					fixture.shares,
					CombineOptions{MaxAttemptsPerSubBatch: 256, MaxWorkers: test.workers},
				)
				b.StopTimer()
				fixture.validate(b, test.workers, results, stats, err)
				if i+1 < b.N {
					b.StartTimer()
				}
			}
		})
	}
}
