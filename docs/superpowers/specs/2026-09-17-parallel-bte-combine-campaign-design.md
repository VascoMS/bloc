# Bounded Parallel BTE Combine Development Campaign

## Objective

Replace the integrated serial Opt-2 sub-batch combine loop with deterministic,
bounded parallel reconstruction, then evaluate the new architecture in the
three missing B=512 preliminary cells without rerunning the successful BMax-128
pilots.

GitHub issue [#33](https://github.com/VascoMS/bloc/issues/33) owns task-level
scope, progress, acceptance, and evidence. This design fixes the architecture
and campaign contract that the implementation plan must realize.

## Evidence And Motivation

The corrected issue #30 n=4/B=512 pilot retained all 30 measured attempts but
only 23 completed within the 12-second deadline. In successful observations,
combine p50 was about 7.45 seconds and p95 about 8.58 seconds, while ACS p50 was
below one second. Each successful run planned 46 Opt-2 sub-batches and used one
valid share-subset attempt per sub-batch. The dominant cost was therefore the
serial cryptographic work across independent sub-batches, not subset-recovery
explosion.

The inherited BEAT-MEV benchmark already demonstrates the intended algorithmic
shape by launching independent sub-batch combines concurrently. The integrated
cluster path cannot copy that benchmark literally: it must retain proof and
AEAD validation, deterministic invalid-share recovery, attempt budgets,
original-position restoration, node-level single-flight, configuration
compatibility, and artifact provenance.

The current operators are `t3.small` instances with two vCPUs. This campaign
therefore evaluates two bounded combine workers before considering a different
instance class. It does not claim that two workers is universally optimal.

## Selected Architecture

### Parallelism boundary

Only planned sub-batches execute concurrently. A node still admits at most one
slot-level combine attempt through its existing `combineInFlight` claim, and a
single sub-batch still enumerates threshold subsets serially and
deterministically.

`CombineSharesBounded` is divided into three phases:

1. validate the plan, options, candidates, ownership, and per-sub-batch budgets
   serially; group and sort candidates exactly as today;
2. submit sub-batch IDs in ascending order to a fixed worker pool; and
3. collect indexed outcomes, choose the lowest failing sub-batch, commit
   serial-equivalent statistics, and restore successful plaintexts to original
   positions.

Workers receive immutable-use sub-batch items, sorted share candidates, and one
attempt limit. Each worker writes only its uniquely indexed outcome. No worker
mutates the published plan, node slot state, another result, or another
sub-batch's budget.

### Configuration

The BTE interface gains a bounded worker option and reports both configured and
effective workers. `bloc-node` exposes it as:

```json
{
  "limits": {
    "max_combine_workers": 2
  }
}
```

An omitted field defaults to `1`, preserving historical configuration and
serial behavior. Explicit zero and negative values fail configuration
validation. The effective value is:

```text
min(configured workers, planned sub-batches, GOMAXPROCS)
```

and is at least one for every nonempty plan. A repository constant supplies a
finite upper configuration bound. The B=512 development bundles set the value
to `2`; accepted BMax-128 artifacts remain historical and are not regenerated
or relabeled.

### Deterministic success and failure semantics

Valid inputs must return the same plaintext bytes in the same original
positions for workers 1 and 2. `BatchID`, Opt-2 planning, ciphertexts, shares,
proof checks, AEAD checks, and Ethereum materialization do not change.

Parallel work may finish out of order, but observable failure is selected only
after collection. The returned error is the error from the lowest numbered
failing sub-batch, matching the serial loop. To preserve cumulative recovery
semantics, reported attempts are committed through that failing sub-batch and
are reported as zero for later sub-batches, even if speculative work there
already completed. This keeps node-visible budgets and retry behavior
equivalent to serial execution.

Pre-worker validation errors remain serial and retain their current text and
zero-attempt statistics. Worker=1 is the compatibility oracle for all result,
error, and statistics tests.

### Concurrency safety

The combine path reads shared BTD, ElGamal, PRF, CRS, ciphertext, and public
share state while constructing fresh point/scalar/hash objects for intermediate
results. The design does not assume that this is safe merely because the
current code appears read-only. Focused two-worker stress and Go race tests are
an acceptance gate.

If shared-state execution exposes a race or mutable backend dependency, the
implementation must create worker-local reconstruction state from the same
immutable public parameters. Adding a global mutex around the cryptographic
body would preserve correctness but defeat the feature and is not an accepted
fallback.

### Observability and provenance

`CombineStats` reports configured and effective workers in addition to attempts
by sub-batch. Node result metrics retain those values, and evaluator run/node
artifacts expose them alongside `combine_us` and attempt counts. The campaign
manifest, public cluster config, evaluator config, run rows, node rows, and
artifact validator must agree on configured worker count. Node rows must report
the expected effective count for a nonempty B=512 plan.

`combine_us` keeps its current wall-clock boundary: threshold availability
through completion of the bounded parallel combine. It is not divided by worker
count and does not become CPU time. The existing subset-attempt counter remains
the sum of committed cryptographic attempts, not goroutine executions.

## Alternatives Considered

### One goroutine per sub-batch

The inherited benchmark uses this shape, but B=512 produces 46 sub-batches and
the deployment has two vCPUs. Unbounded fan-out adds scheduling and memory
pressure without providing a stable resource contract. A fixed worker pool is
selected.

### Parallel threshold-subset enumeration within each sub-batch

This complicates deterministic attempt order and cumulative budgets, while the
observed successful B=512 runs use one subset attempt per sub-batch. It does not
target the measured bottleneck and is excluded.

### Increase the EC2 instance class first

A larger fixed-performance instance could reduce combine time, but it would not
evaluate the strongest software architecture and would confound algorithm and
hardware effects. Instance-class comparison remains a later, separately scoped
decision.

### Enable selective/hash-only ECHO at the same time

That would change dissemination and attribution while the observed B=512
bottleneck is post-ACS combine. The campaign retains broadcast ECHO and disables
the optimization.

## Local Development And Validation Campaign

Implementation follows regression-first development:

1. add worker=1/2 equivalence, deterministic multi-failure, concurrency-bound,
   stress, and race coverage;
2. implement the BTE worker pool and indexed collector;
3. add strict compatible node configuration and generator/materializer
   propagation;
4. add result/evaluator/campaign provenance and fail-closed artifact checks;
5. add B=512 workers 1/2/`GOMAXPROCS` combine benchmarks; and
6. update canonical architecture, module, validation, decision, changelog,
   status, and EC2 documentation.

Minimum gates are the complete normal and race suites in both affected Go
modules, focused repeated concurrency tests, B=512 benchmark output suitable
for `benchstat`, an n=4/7/10 B=512 local validation-only smoke with two workers,
campaign artifact fixtures, runner portability, both Terraform validations,
and exact live-runner `--validate-only` coverage. Local evaluator output proves
correctness and artifact shape only; it is not latency evidence.

A review checkpoint occurs after all local gates and before image publication.
It must confirm deterministic failure/statistics behavior, backend race safety,
bounded concurrency, compatibility defaults, provenance completeness, and the
absence of unrelated protocol changes.

## Preliminary AWS Campaign

The live campaign keeps the issue #30 workload and deployment architecture:

- topology: three region only;
- evaluator execution: `persistent`;
- stream mode: `persistent-lanes`;
- RBC ECHO: broadcast;
- selective/hash-only ECHO: disabled;
- ACS tracing: disabled;
- resource sampler: disabled;
- instance type: `t3.small`;
- seed: `20260621`;
- deadline: 12 seconds; and
- schedule: 5 warmups, 30 measured attempts, 3 balanced blocks per cell.

Only these new worker=2 cells run:

| Operators | Threshold | Batch | BMax | Measured attempts |
| ---: | ---: | ---: | ---: | ---: |
| 4 | 3 | 512 | 512 | 30 |
| 7 | 5 | 512 | 512 | 30 |
| 10 | 7 | 512 | 512 | 30 |

The accepted n=10/B=8, 32, and 128 pilots are not rerun. The earlier serial
n=4/B=512 artifact remains complete negative performance evidence and is not
pooled with the worker=2 distribution.

Unlike the prior cross-cell gate, all three B=512 pilots are independent. A
deadline or failure boundary in n=4 prevents only an n=4 continuation; it does
not suppress n=7 or n=10. Every cell is classified independently as viable
preliminary architecture evidence, complete mixed/boundary evidence, or invalid
deployment/harness evidence.

Each cell retains every attempt and reports attempted, successful, consistent,
deadline-met, failed, and timed-out counts plus Type-7 p50/p95 for qualifying
successful observations. Thirty observations never support p99. A 1,000-run
continuation, a worker-count change, resource sampling, or an instance-class
comparison requires a new decision.

## Freeze, Cost, And Cleanup Gate

No AWS resources are allocated during implementation. Before the first live
cell, the task must publish one clean source, immutable linux/amd64 image
digests, fresh BMax-512 bundle manifests carrying `max_combine_workers=2`, exact
validate-only commands, current regional quota/offerings evidence, per-phase
cost ceilings, and cleanup targets to issue #33.

The existing verified three-region footprint is retained: n=10 uses 10/6/6
regional vCPUs including its controller and fits the current 16-vCPU regional
quotas. Capacity must still be rechecked immediately before live work. Each
deployment is recovered, destroyed, and independently audited for empty
regional EC2/network/key, IAM, and Terraform state before the next cell starts.

## Documentation And Evidence Ownership

Durable implementation behavior belongs in the BTE and node deep dives;
operator commands belong in module and EC2 READMEs; evidence semantics and
acceptance belong in `docs/VALIDATION.md`; the architectural selection belongs
in `docs/DECISIONS.md`; implementation history belongs in
`docs/CHANGELOG.md`; and live milestone/blocker/next-action state belongs in
`docs/STATUS.md`. GitHub issue #33 owns granular progress and validation notes.
Generated benchmark, smoke, freeze, and cloud artifacts remain in ignored
`results/` directories.

## Non-Goals

- Changing Opt-2 planning, `BatchID`, ciphertext/share wire formats, proofs,
  AEAD, ACS, RBC, BBA, or materialization semantics.
- Adding public decryption-share proofs, secure CRS generation, DKG, a
  cryptographic common coin, or a remote combiner.
- Enabling selective/hash-only ECHO, GossipSub, tracing, or resource sampling.
- Running same-AZ cells or rerunning successful BMax-128 preliminary cells.
- Claiming p99 or final thesis performance from the 30-run campaign.
