package be

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCombineSharesBoundedNormalizesWorkersBeforePreflight(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	cluster := newTestCluster(t, 8, 4, 3)
	for _, test := range []struct {
		name       string
		workers    int
		subBatches int
		configured int
		effective  int
		wantError  string
	}{
		{"omitted", 0, 6, 1, 1, "max attempts per sub-batch must be positive"},
		{"two", 2, 6, 2, 2, "max attempts per sub-batch must be positive"},
		{"cpu-bound", 8, 6, 8, 2, "max attempts per sub-batch must be positive"},
		{"plan-bound", 8, 1, 8, 1, "max attempts per sub-batch must be positive"},
		{"empty", 2, 0, 2, 0, "max attempts per sub-batch must be positive"},
		{"negative", -1, 6, 0, 0, "max combine workers must be non-negative"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := BatchPlan{SubBatches: make([][]BatchItem, test.subBatches)}
			results, stats, err := cluster.CombineSharesBounded(plan, nil, CombineOptions{MaxWorkers: test.workers})
			require.Nil(t, results)
			require.EqualError(t, err, test.wantError)
			require.Equal(t, make([]int, test.subBatches), stats.AttemptsBySubBatch)
			require.Equal(t, test.configured, stats.ConfiguredWorkers)
			require.Equal(t, test.effective, stats.EffectiveWorkers)
		})
	}
}

func TestEffectiveCombineWorkers(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })

	tests := []struct {
		name       string
		configured int
		subBatches int
		wantConfig int
		wantActive int
		wantErr    bool
	}{
		{"omitted-defaults-to-one", 0, 46, 1, 1, false},
		{"negative-rejected", -1, 46, 0, 0, true},
		{"bounded-by-plan", 8, 1, 8, 1, false},
		{"bounded-by-gomaxprocs", 8, 46, 8, 2, false},
		{"empty-plan", 2, 0, 2, 0, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configured, active, err := effectiveCombineWorkers(test.configured, test.subBatches)
			if test.wantErr {
				if err == nil {
					t.Fatal("effectiveCombineWorkers returned no error")
				}
				return
			}
			if err != nil {
				t.Fatalf("effectiveCombineWorkers returned error: %v", err)
			}
			if configured != test.wantConfig || active != test.wantActive {
				t.Fatalf("effectiveCombineWorkers(%d, %d) = (%d, %d), want (%d, %d)", test.configured, test.subBatches, configured, active, test.wantConfig, test.wantActive)
			}
		})
	}
}

func TestRunSubBatchJobsBoundsConcurrencyAndIndexesOutcomes(t *testing.T) {
	jobs := []preparedSubBatch{{id: 3}, {id: 2}, {id: 1}, {id: 0}}
	release := make(chan struct{})
	started := make(chan int, len(jobs))
	completed := make(chan int, len(jobs))
	finish := make([]chan struct{}, len(jobs))
	for id := range finish {
		finish[id] = make(chan struct{})
	}

	var current atomic.Int32
	var peak atomic.Int32
	combine := func(job preparedSubBatch) subBatchOutcome {
		active := current.Add(1)
		for {
			seen := peak.Load()
			if active <= seen || peak.CompareAndSwap(seen, active) {
				break
			}
		}
		started <- job.id
		<-release
		<-finish[job.id]
		current.Add(-1)
		completed <- job.id
		return subBatchOutcome{id: job.id, attempts: job.id + 1}
	}

	var (
		outcomes []subBatchOutcome
		err      error
	)
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		outcomes, err = runSubBatchJobs(2, jobs, combine)
	}()

	for range 2 {
		<-started
	}
	if got := peak.Load(); got != 2 {
		t.Fatalf("peak concurrency = %d, want 2", got)
	}

	release <- struct{}{}
	release <- struct{}{}
	close(finish[2])
	if got := <-completed; got != 2 {
		t.Fatalf("first completed job = %d, want 2", got)
	}
	close(finish[3])
	if got := <-completed; got != 3 {
		t.Fatalf("second completed job = %d, want 3", got)
	}

	for range 2 {
		<-started
	}
	release <- struct{}{}
	release <- struct{}{}
	close(finish[0])
	if got := <-completed; got != 0 {
		t.Fatalf("third completed job = %d, want 0", got)
	}
	close(finish[1])
	if got := <-completed; got != 1 {
		t.Fatalf("fourth completed job = %d, want 1", got)
	}
	done.Wait()

	if err != nil {
		t.Fatalf("runSubBatchJobs returned error: %v", err)
	}
	for id, outcome := range outcomes {
		if outcome.id != id {
			t.Fatalf("outcomes[%d].id = %d, want %d", id, outcome.id, id)
		}
		if outcome.attempts != id+1 {
			t.Fatalf("outcomes[%d].attempts = %d, want %d", id, outcome.attempts, id+1)
		}
	}
}

func TestRunSubBatchJobsRejectsInvalidIDs(t *testing.T) {
	tests := []struct {
		name string
		jobs []preparedSubBatch
	}{
		{"duplicate", []preparedSubBatch{{id: 0}, {id: 0}}},
		{"out-of-range", []preparedSubBatch{{id: 0}, {id: 2}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			_, err := runSubBatchJobs(1, test.jobs, func(job preparedSubBatch) subBatchOutcome {
				called = true
				return subBatchOutcome{id: job.id}
			})
			if err == nil {
				t.Fatal("runSubBatchJobs returned no error")
			}
			if called {
				t.Fatal("runSubBatchJobs invoked combine for invalid jobs")
			}
		})
	}
}
