# Issue 8 Distributed-Campaign Preflight Design

## Objective

Issue #8 becomes a bounded local readiness gate for the final distributed
campaigns. It no longer collects a local p99 dataset or produces local
performance claims for the thesis.

The preflight proves that the frozen release candidate can execute every
planned primary and extension configuration, retain complete schema-v3
artifacts, and feed the validation and chart tooling before AWS resources are
allocated. Issue #15 becomes the first M5 task that collects thesis-performance
evidence.

## Rationale

The thesis evaluates BLOC as an independently operating distributed sidecar.
One VM per operator is the primary evidence environment because it preserves
independent machine, network, resource, and failure domains. A large local
campaign would produce statistically precise measurements of a deployment model
that the thesis does not intend to present.

Local execution remains valuable for correctness, configuration coverage, and
artifact-contract validation. Those goals need tens of observations, not the
6,000 primary measurements required for six p99-capable cells.

## Evidence Classification

The local preflight is validation evidence, not thesis-performance evidence.
Its output may establish:

- the frozen source, image, corpus, configuration, and schema identities;
- successful startup and bounded teardown for every planned configuration;
- identical materialized outputs at all correct nodes;
- complete attempt, failure, timing, and provenance fields;
- stage and substage additivity;
- compatibility with artifact validators and chart loaders; and
- whether an extension configuration has an immediate correctness,
  orchestration, or bounded-completion problem.

It must not support:

- local p50, p95, p99, maximum, throughput, or scaling claims;
- local-versus-VM performance comparisons;
- topology or network-overhead conclusions;
- operator CPU, memory, or network measurements; or
- a decision to merge local and distributed observations.

Any generated local summaries or figures are diagnostic only and are not
eligible for thesis tables or figures.

## Frozen Contract

The preflight uses the issue #14 release candidate without modification:

- executable source:
  `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`;
- image:
  `bloc-node@sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`;
- evaluator schema: `bloc-eval-suite/v3`;
- seed: `20260621`;
- completed-within-deadline boundary: 12 seconds;
- primary configurations: `n=4,t=3` and `n=7,t=5`, batches
  `8/32/128`; and
- extension configurations: `n=10,t=7`, batches `8/32/128`, plus batch
  `512` at `n=4/7/10`.

The exact committed full-protocol corpus and transaction-source configuration
used by the distributed campaigns must be bound in the preflight manifest.
The balanced issue #13 client-overhead corpus remains a separate experiment and
is not substituted for the full-protocol workload.

## Preflight Matrix

### Primary Coverage

Run all six primary cells with one warmup and one measured observation:

| Nodes | Threshold | Batches | Warmups per cell | Measured per cell |
|---:|---:|---|---:|---:|
| 4 | 3 | `8/32/128` | 1 | 1 |
| 7 | 5 | `8/32/128` | 1 | 1 |

Every measured primary observation must complete successfully, remain
cross-node consistent, meet the 12-second boundary, and satisfy artifact
validation.

### Extension Coverage

Run six unique extension cells with one warmup and three measured observations:

| Extension | Cells | Warmups per cell | Measured per cell |
|---|---:|---:|---:|
| `n=10`, batches `8/32/128` | 3 | 1 | 3 |
| batch `512` at `n=4/7/10` | 3 | 1 | 3 |

The short local extension pilot checks startup, bounded execution, consistency,
and complete diagnostics. It does not replace the distributed 30-observation
pilot or decide the final 1,000-observation versus 100-boundary-observation
continuation.

An extension may miss the 12-second boundary without invalidating the
preflight when it still terminates consistently and retains a complete outcome.
An inconsistent success, crash, unbounded hang, missing failure reason, corrupt
artifact, or configuration that cannot start fails the preflight.

## Execution Flow

1. Verify a clean checkout of the frozen executable source and record the
   source SHA, image digest, corpus hash, configuration, toolchain, host, and
   complete commands.
2. Run the four module suites, chart tests, campaign-runner validation, and
   side-effect-free deployment validation required by the release-candidate
   contract.
3. Run one plan or configuration-resolution check for every primary and
   extension cell.
4. Execute the primary matrix.
5. Execute the extension matrix.
6. Validate attempt counts, outcomes, provenance, cross-node consistency,
   timing additivity, failure retention, manifest identity, and schema
   completeness.
7. Load the artifact through the chart pipeline to prove compatibility. Retain
   any generated figures as ignored diagnostics with an explicit
   non-thesis-evidence label.
8. Promote the preflight only when every acceptance rule passes without manual
   artifact repair.

Artifacts live under:

```text
results/local/distributed-preflight-2bc8efc/
```

The artifact root includes a manifest or report that states:

```text
classification=validation-only
performance_claims_allowed=false
```

## Failure and Invalidation Rules

The release candidate is immutable during issue #8. If the preflight indicates
that code, configuration, corpus, evaluator schema, or campaign tooling must
change, issue #8 stops before making that change.

The issue records:

- the failing command and configuration;
- the retained diagnostic artifact;
- whether the failure is correctness, orchestration, performance-boundary, or
  artifact-contract related; and
- which frozen-contract component would need to change.

Changing a frozen component requires an explicit release-candidate invalidation
decision, a replacement source/image freeze, and rerunning every affected
preflight and campaign. No observation from different candidates may be merged.

## Resource Evidence

The local preflight does not collect operator resource measurements. It may
validate resource-runner configuration and artifact fixtures without starting a
measurement phase.

Issue #15 collects same-region CPU, memory, and network evidence in a dedicated
resource phase on independent operator VMs. Issue #16 does the same for the
three-region environment.

## Issue and Milestone Ordering

Implementation updates the tracker as follows:

1. Select M5 as the active milestone.
2. Rename and re-scope issue #8 to the distributed-campaign preflight.
3. Keep issue #8 in Area `evidence`, Priority `High`, Roadmap target `M5`, and
   set it to `In progress` when execution begins.
4. Add issue #8 as an explicit dependency of issue #15.
5. Make issue #15 the first M5 thesis-performance evidence task.
6. Keep issue #16 after #15 so matched manifests and configurations can be
   checked before topology interpretation.
7. Keep issue #17 as the separate BTE benchmark campaign; its samples are
   machine-specific cryptographic evidence rather than a local sidecar latency
   baseline.

Issue #8 closes only after the preflight artifacts pass and the issue contains
the exact validation outcome and artifact root. A successful preflight moves
the immediate next action to issue #15. It does not claim that any M5
performance evidence has been collected.

## Canonical Documentation Changes

The implementation updates existing owners rather than adding parallel
explanations:

- `docs/STATUS.md`: select M5, replace the final-local-campaign action with the
  bounded preflight, and keep evidence completeness open;
- `docs/ROADMAP.md`: classify local work as preflight and VM work as M5
  performance/resource evidence;
- `docs/VALIDATION.md`: remove local results from final RQ1/RQ2 performance
  claims, define the preflight matrix, and remove stale milestone-state prose;
- `docs/DECISIONS.md`: record that final sidecar performance evidence is
  VM-per-operator only and local evaluation is validation-only;
- `docs/CHANGELOG.md`: record the evidence-strategy change;
- `docs/superpowers/plans/2026-07-23-thesis-evaluation.md`: replace Task 9's
  local p99 campaign with the bounded preflight and make Task 10 the first
  thesis-performance campaign; and
- GitHub issues #8, #15, and #16 plus their Project fields and dependency text.

## Validation Strategy

The documentation and tracking implementation is accepted when:

- canonical documents agree that M5 is active, M4 is complete, local results
  are validation-only, and #15 is the first thesis-performance task;
- issue #8's title, objective, scope, acceptance criteria, and Project card
  describe the preflight rather than the superseded local p99 campaign;
- issue #15 explicitly depends on successful issue #8 preflight;
- issue #16 follows issue #15 for matched-campaign interpretation;
- all modified local Markdown links resolve;
- documented commands parse or pass their side-effect-free validation modes;
- `git diff --check` passes; and
- no AWS resource is allocated and no branch is pushed.

## Non-Goals

- No local p99 dataset is collected.
- No existing release-candidate code or image is modified.
- No new production protocol mode is introduced.
- No local resource evidence is promoted.
- No AWS plan, apply, instance, image push, or campaign run occurs.
- No issue #15 or #16 evidence is collected as part of the redesign.
