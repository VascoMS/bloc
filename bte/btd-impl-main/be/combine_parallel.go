package be

import (
	"fmt"
	"runtime"
	"sync"

	"go.dedis.ch/kyber/v4/share"
)

const defaultCombineWorkers = 1

type preparedSubBatch struct {
	id           int
	items        []BatchItem
	shares       []*share.PubShare
	attemptLimit int
}

type subBatchOutcome struct {
	id       int
	results  []positionedPlaintextResult
	attempts int
	err      error
}

func effectiveCombineWorkers(configured, subBatches int) (configuredWorkers, effectiveWorkers int, err error) {
	if configured < 0 {
		return 0, 0, fmt.Errorf("max combine workers must be non-negative")
	}
	if configured == 0 {
		configured = defaultCombineWorkers
	}
	if subBatches == 0 {
		return configured, 0, nil
	}
	return configured, max(1, min(configured, subBatches, runtime.GOMAXPROCS(0))), nil
}

func runSubBatchJobs(workerCount int, jobs []preparedSubBatch, combine func(preparedSubBatch) subBatchOutcome) ([]subBatchOutcome, error) {
	if workerCount < 0 {
		return nil, fmt.Errorf("combine worker count must be non-negative")
	}
	if len(jobs) == 0 {
		return []subBatchOutcome{}, nil
	}
	if workerCount == 0 {
		return nil, fmt.Errorf("combine worker count must be positive when jobs exist")
	}
	if combine == nil {
		return nil, fmt.Errorf("combine function is required")
	}

	outcomes := make([]subBatchOutcome, len(jobs))
	seen := make([]bool, len(jobs))
	for _, job := range jobs {
		if job.id < 0 || job.id >= len(jobs) {
			return nil, fmt.Errorf("sub-batch job id out of range: %d", job.id)
		}
		if seen[job.id] {
			return nil, fmt.Errorf("duplicate sub-batch job id: %d", job.id)
		}
		seen[job.id] = true
	}

	jobQueue := make(chan preparedSubBatch)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobQueue {
				outcome := combine(job)
				outcome.id = job.id
				outcomes[job.id] = outcome
			}
		}()
	}
	for _, job := range jobs {
		jobQueue <- job
	}
	close(jobQueue)
	workers.Wait()
	return outcomes, nil
}
