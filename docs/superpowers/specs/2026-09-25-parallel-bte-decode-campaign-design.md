# Bounded Parallel BTE Ciphertext Decode Campaign

## Objective

Replace the integrated serial ciphertext-decoding loop with deterministic,
bounded parallel decoding, then measure its isolated and end-to-end effect for
the B=512 n=4, n=7, and n=10 configurations.

This campaign evaluates the strongest practical BLOC architecture while keeping
the accepted ACS, stream, combine, and deployment choices unchanged. It does
not authorize AWS activity. A GitHub issue and project item will own execution
after this specification and its implementation plan are approved.

## Evidence And Motivation

In the retained issue #33 three-region worker-two pilots, ciphertext decoding
dominates the phase currently named `merge_plan`:

| Configuration | `merge_plan` p50 | Ciphertext decode p50 | Decode share |
| --- | ---: | ---: | ---: |
| n=4, B=512 | 1468.150 ms | 1412.174 ms | 96% |
| n=7, B=512 | 1114.599 ms | 1000.376 ms | 90% |
| n=10, B=512 | 1266.049 ms | 1106.274 ms | 87% |

Batch planning itself is about 0.27 ms. `ClusterBTE.DecodeBatch` currently
walks the 512 encodings serially; each item reconstructs curve points, scalars,
and encrypted bytes and validates their shape. The ciphertexts are independent,
so bounded item-level parallelism directly targets the measured bottleneck.

## Selected Architecture

### API and compatibility

The BTE library adds a bounded API while preserving the existing serial entry
point:

```go
type DecodeOptions struct {
	MaxWorkers int
}

type DecodeStats struct {
	ConfiguredWorkers int
	EffectiveWorkers  int
}

func (c *ClusterBTE) DecodeBatch(encoded [][]byte) (DecodedBatch, error)

func (c *ClusterBTE) DecodeBatchBounded(
	encoded [][]byte,
	options DecodeOptions,
) (DecodedBatch, DecodeStats, error)
```

`DecodeBatch` remains a one-worker compatibility wrapper. At the library
boundary, zero means the omitted default of one and negative values are
invalid. For a nonempty batch, the effective worker count is:

```text
min(configured workers, ciphertext count, GOMAXPROCS)
```

An empty batch succeeds with zero effective workers.

### Node configuration

`bloc-node` exposes a separate limit:

```json
{
  "limits": {
    "max_decode_workers": 2
  }
}
```

Omission defaults to one. Explicit zero and negative values fail node
configuration validation; accepted explicit values are 1 through 64. Decode
and combine worker limits remain independent so experiments can attribute
their effects without changing the established meaning of
`max_combine_workers`.

### Bounded execution

The bounded decoder performs these steps:

1. validate the batch-level `BMax` constraint before starting workers or
   parsing any ciphertext;
2. submit indexed ciphertext jobs in ascending order to a fixed worker pool;
3. collect indexed outcomes through a single owner;
4. place successful results at their original indices; and
5. after complete success, compute `BatchID` serially from the original ordered
   encodings.

Workers create fresh points, scalars, readers, and ciphertext state. They treat
the cluster parameters and input buffers as immutable. Callers may not mutate
input buffers while decoding is in progress.

### Deterministic failure semantics

Parallel completion order does not alter observable behavior. If several
ciphertexts fail, the method returns `decode ciphertext <index>: ...` for the
lowest failing input index.

After observing an error, the dispatcher stops admitting unnecessary
higher-index jobs, joins all work already started, and awaits every lower index
needed to establish the deterministic error. Speculative work remains bounded
by the effective worker count. The method never returns while a worker is live
or blocked on a channel.

Worker one is the compatibility oracle for successful bytes, output order,
`BatchID`, error selection, and empty/oversized behavior.

### Concurrency safety

The design does not assume that the shared Kyber/kilic suite and BTD state are
safe merely because decoding appears read-only. Repeated parallel equivalence,
stress, and Go race tests on the same `ClusterBTE` are acceptance gates.

If those tests expose mutable shared state, the implementation must use
worker-local decoder contexts constructed from the same immutable parameters,
provided the backend supports that safely. A global mutex around ciphertext
decoding is not an acceptable performance fallback; failure to establish safe
parallelism stops the campaign and is recorded as negative architectural
evidence.

## Alternatives Considered

### Reuse `max_combine_workers`

This minimizes configuration changes but silently broadens a combine-specific
setting and makes phase attribution ambiguous. It is rejected.

### Introduce `max_crypto_workers`

A shared crypto budget could eventually coordinate overlapping cryptographic
work. The current combine and decode phases are sequential, so a shared
semaphore adds no resource protection while requiring migration and precedence
rules for the existing combine limit. It is deferred.

### Unbounded goroutine per ciphertext

B=512 would create 512 decoding goroutines on two-vCPU deployment nodes. That
does not provide a stable resource contract and is rejected in favor of a
fixed pool.

## Configuration, Metrics, And Provenance

The node records configured and effective decode workers immediately after the
decode call, including the empty-batch case. Evaluator scenarios, campaign
identity, frozen inputs, bundle manifests, generated public node configuration,
remote configuration, run rows, and node rows carry the requested value.
Node-level results also carry the effective value.

Artifact validation fails closed when requested, materialized, and reported
values disagree. Historical artifacts that omit the field normalize to one;
the existing schema does not need a version change. The strict mempool campaign
identity reader must recognize the added limit while continuing to reject
unknown fields.

The existing `ciphertext_decode_us` timing remains wall-clock duration. It is
not divided by worker count and does not become CPU time. `merge_plan_us`
continues to contain its documented decode, agreed-set, merge, and planning
subphases until a separately approved metric-schema change.

## Regression-First Implementation Sequence

### 1. BTE behavior

Write failing tests for defaults and invalid options, empty input, `BMax`
preflight, worker and CPU caps, ordered serial/parallel equivalence, inverted
multi-error completion, peak concurrency, full worker drain, repeated
success/failure stress, and same-instance race safety. Then add the bounded
executor and compatibility wrapper.

### 2. Node integration

Add strict `max_decode_workers` parsing and generation, pass the option into the
BTE call, and expose configured/effective values in metrics and results. Cover
omission, invalid values, propagation, and integrated decode behavior.

### 3. Evaluator and campaign artifacts

Propagate the field through local, suite, persistent, and remote evaluators;
campaign identity and materialization; bundle lifecycle helpers; CSV/JSON
outputs; and fail-closed artifact checks. Add mismatch and historical-omission
fixtures.

### 4. Canonical documentation

Update the BTE and node READMEs and deep dives, architecture, validation,
decision record, changelog, EC2 runbook, and live status. `ROADMAP.md` remains
unchanged because the work stays within M5.

## Local Evaluation Campaign

Add `BenchmarkDecodeBatchBoundedB512` with these 12 cells:

| Operators / threshold | Decode workers |
| --- | --- |
| n=4 / t=3 | 1, 2, 4, 8 |
| n=7 / t=5 | 1, 2, 4, 8 |
| n=10 / t=7 | 1, 2, 4, 8 |

CRS construction, key generation, encryption, and canonical encoding happen
outside the timer. Each measured operation decodes exactly 512 ciphertexts.
After timing, validation requires ordered canonical re-encoding, the serial
`BatchID`, no error, and exact configured/effective worker counts.

The retained development run uses `GOMAXPROCS=8`, one timed operation per
sample, 30 samples per cell, and allocation reporting. Committee size should
not materially affect decoding; retaining all three fixtures tests that claim
and aligns the evidence with the deployment campaign.

Integrated local smokes cover n=4/7/10 at B=512 with decode workers two and
the existing combine workers two. They require successful and consistent
materialization, 512 selected ciphertexts, exact worker provenance, unchanged
identities and order, and additive phase timings. Local evaluator output is
correctness and integration evidence, not multi-region latency evidence.

Worker two becomes the deployment candidate only if retained local results
show improved decode p50 for all three fixtures, no material allocation growth,
and no integrated pipeline regression greater than 5%. Workers four and eight
remain local scaling evidence because the present `t3.small` nodes expose two
vCPUs. Negative performance is retained and reported rather than filtered.

## Validation Gates

Before deployment review, the campaign requires:

- complete BTE normal and race suites;
- a focused parallel-decode race/stress gate with at least 20 repetitions;
- complete `bloc-node` and `mempool-il` normal suites;
- the repository's split final-campaign race gate;
- all 12 retained isolated benchmark cells and the three integrated smokes;
- campaign identity, bundle, materialization, and artifact-mutation tests;
- runner portability, both Terraform validations, and exact
  `--validate-only` checks;
- documentation/link review and `git diff --check`; and
- review of the implementation diff and retained local evidence.

## Optional AWS Stage

AWS work requires explicit authorization after local evidence review. If
authorized, publish a clean source and immutable images, then generate fresh
BMax-512 bundles carrying `max_decode_workers=2` and
`max_combine_workers=2`.

Keep the issue #33 three-region topology, corpus, `t3.small` instances,
trace-off `persistent-lanes`, broadcast ECHO, seed, 12-second deadline, and
resource-sampler-off settings. Run an independent n=4 canary with 30 measured
attempts first. Only after reviewing it may n=7 and n=10 run as independent
30-attempt pilots.

Decode-campaign artifacts remain separately labeled from issue #33 and are
never pooled with it. Thirty observations support preliminary p50/p95 evidence,
not p99 or a full-continuation claim. Artifact completeness, cleanup, and
provenance are acceptance requirements even when performance is negative.

## Tracking And Documentation Ownership

After written-spec approval, create one repository issue and project item under
M5 before implementation. The issue names every canonical document it may
change and owns granular progress, blockers, validation, and evidence decisions.

Implementation behavior belongs in the BTE and node deep dives; commands in
module and EC2 READMEs; evidence semantics in `docs/VALIDATION.md`; the selected
architecture in `docs/DECISIONS.md`; implementation history in
`docs/CHANGELOG.md`; and live campaign state and immediate actions in
`docs/STATUS.md`. Generated benchmarks, smokes, and deployment results remain
under ignored `results/` directories.

## Non-Goals

- Changing ciphertext formats, encryption, `BatchID`, Opt-2 planning, combine,
  threshold-share exchange, proofs, AEAD, or materialization semantics.
- Changing ACS, RBC, BBA, stream mode, ECHO mode, tracing, or resource sampling.
- Renaming phase metrics or changing their timing boundaries.
- Re-running successful BMax-128 campaigns or same-AZ deployments.
- Changing the EC2 instance class in the first decode comparison.
- Claiming p99, production readiness, or final thesis performance from the
  30-attempt campaign.
