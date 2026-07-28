# Issue 15 Same-Region Campaign Execution Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:executing-plans to execute this plan checkpoint by checkpoint.
> AWS execution remains separately authorized and must not start while any
> checkpoint is blocked.

**Goal:** Collect matched same-AZ `us-east-1a` p99 and dedicated resource
evidence for issue #15 without changing or obscuring the frozen evaluation
contract.

**Architecture:** Keep issue documentation and planning on
`codex/issue-15-same-region-campaign`, but execute campaign commands from a
clean detached worktree at
`2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`. Distribute a previously frozen
image without rebuilding it, run `n=4` readiness before the primary matrix,
separate latency and resource measurement, and require complete provenance,
artifact, failure-retention, and authenticated-cleanup gates before evidence is
accepted.

**Tech Stack:** Git worktrees, Bash 3.2 campaign runners, Docker, Terraform,
AWS EC2/ECR or SSH image transfer, Go evaluator schema `bloc-eval-suite/v3`,
and Python artifact/chart validation.

**Plan status:** **BLOCKED. No live AWS command is currently valid.** The frozen
same-AZ tooling cannot satisfy the frozen transaction-source, image, or bounded
execution requirements described below. Per the release-candidate contract,
the tooling is not patched in this task.

## Global Constraints

- Executable source is exactly
  `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`.
- Image identity is exactly
  `bloc-node@sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`.
- Protocol transaction source is `mock-placeholder`.
- Protocol corpus is
  `deploy/docker-compose/corpus/mock-targets.jsonl`, SHA-256
  `52121653abf114f7230b19794433e2a71da8726df3472acd1ce184f4d4131cc2`,
  100 rows, distribution `28/50/12/8/2`.
- The 500-target balanced client-overhead corpus must not be substituted.
- Primary topology is same-AZ `us-east-1a`, with `t3.small` operators and
  controller.
- Primary matrix is `n=4,t=3` and `n=7,t=5`, batches `8/32/128`, 10 warmups,
  1,000 measured attempts per cell, 10 balanced blocks, seed `20260621`, and a
  12-second completed-within-deadline boundary.
- Every attempt is retained. Only successful, consistent attempts enter
  latency quantiles. A positive conclusion additionally requires at least 99%
  consistent completion within 12 seconds and empirical p99 below 12 seconds.
- Primary latency/p99 collection must run without the host resource sampler.
  Resource collection is a later dedicated `resource-measured` phase.
- `n=10,t=7` and batch `512` are excluded from the initial launch. Each
  extension cell starts with a separate 30-observation pilot after primary
  evidence is accepted.
- No source, image, corpus, schema, campaign-tooling, or configuration-semantic
  change is allowed without an explicit invalidation and replacement-freeze
  decision.
- No AWS API call, Terraform plan/apply, ECR push, billable resource, Git push,
  or pull request is authorized by this plan.

---

## File And State Map

- Task-branch plan:
  `docs/superpowers/plans/2026-07-28-issue-15-same-region-campaign.md`.
- Frozen execution checkout: a clean detached worktree outside the task
  checkout, at the exact executable source SHA.
- Intended primary root:
  `results/ec2/issue-15-same-az-primary-rc-2bc8efc-<UTC>/`.
- Intended readiness root:
  `results/ec2/issue-15-same-az-n4-readiness-rc-2bc8efc-<UTC>/`.
- Intended extension root, only after a continuation decision:
  `results/ec2/issue-15-same-az-extension-rc-2bc8efc-<UTC>/`.
- Each root must contain commands, top-level and per-phase manifests, raw run
  and node rows, scenario summaries, network pre/post observations, dedicated
  resource time series and summaries when applicable, logs, checksums,
  Terraform metadata/state evidence, and authenticated cleanup verification.
- Failed infrastructure runs retain all locally collected files and a manifest
  explaining the failure. Resource preservation is allowed only under a
  separately approved, time-bounded debugging window with an explicit destroy
  command and owner.

## Readiness Findings That Block Live Execution

1. `deploy/ec2/run-a1-pilot.sh` and `scripts/lib/m3-matrix.sh` hard-code
   `tx_source="synthetic"` in their manifests.
2. The same-AZ runner generates a direct inclusion-list provider, does not
   deploy `mempool-il`, does not distribute
   `deploy/docker-compose/corpus/mock-targets.jsonl`, and does not pass
   `--tx-source mock-placeholder` or `--mempool-url` to `eval-remote`.
3. `deploy/ec2/run-m3-same-az.sh` does not accept or forward
   `--prebuilt-image-tag` or `--image-distribution`; it therefore invokes the
   lower-level runner's rebuild path.
4. Local Docker inspection resolves `bloc-node:rc-2bc8efc` to image ID and
   repo digest
   `sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`
   with the expected `linux/amd64`, `10001:10001`, entrypoint, and command.
   Direct inspection of the canonical
   `bloc-node@sha256:ee99…` reference nevertheless returns `No such image`.
5. The lower-level `ssh-load` path could transfer the exact tagged local image
   without ECR, but its retained metadata records the mutable tag rather than
   verifying and recording the remote image ID/digest. The ECR path records the
   pushed digest but does not compare it with the frozen digest before starting
   instances.
6. The matrix wrapper does not expose a wall-clock ceiling. The lower-level
   `--max-runtime-minutes` check is not forwarded and is checked only between
   primary scenarios, not during the dedicated resource phase. Terraform,
   readiness, artifact recovery, and cleanup also lack one enclosing deadline.

Resolving any of items 1-6 requires changing campaign tooling, configuration
semantics, provenance schema, or the frozen-source boundary. That triggers the
invalidation rule and prevents a live command from being recommended now.

---

## Checkpoint 1: Frozen Provenance Readiness

**Current state:** **BLOCKED** by the findings above.

- [x] Create the issue branch from current local `main`.
- [x] Refresh remote refs and verify local `main` is 23 commits ahead and zero
  behind `origin/main`.
- [x] Create a temporary detached worktree at the exact source SHA.
- [x] Run the exact primary matrix CLI through `--validate-only`.
- [x] Run all campaign-runner portability and validation-only paths.
- [x] Verify the protocol corpus hash and row count.
- [x] Inspect the local frozen image without rebuilding or running it.
- [ ] Obtain an explicit invalidation decision choosing one of:
  1. update the same-region and three-region orchestration for
     `mock-placeholder`, immutable-image verification, and hard runtime bounds;
     freeze a replacement source/image; rerun issue #8; or
  2. formally redefine the freeze as separate executable-image and
     orchestration/configuration identities, freeze both identities, and rerun
     issue #8 before issue #15.
- [ ] Reject changing the final protocol campaigns to synthetic unless the
  transaction-source/corpus contract is explicitly invalidated for both
  same-region and three-region evidence.
- [ ] With separately authorized read-only AWS calls, verify caller identity,
  `us-east-1a` availability, at least 16 Standard On-Demand vCPUs, at least
  eight simultaneously addressable public IPv4 assignments, IAM permissions,
  and cleanup-query permissions.
- [ ] Pin the AMI/root-volume assumptions and an enforceable wall-clock/cost
  ceiling before requesting resource creation.

**Exit gate:** source, image, corpus, configuration, schema, distribution,
quota, timeout, and cleanup identities are immutable and mechanically
verified. Until then, no live command exists.

## Checkpoint 2: Primary `n=4` Pilot And Readiness

**Current state:** Not reachable until Checkpoint 1 passes.

- [ ] Run `n=4,t=3`, batches `8/32/128`, with the final topology, instance
  class, image, transaction source, corpus, configuration, seed, and schema.
- [ ] Keep this readiness pilot below p99 scope: one retained warmup and three
  retained measured attempts per batch is sufficient to verify deployment,
  corpus materialization, attempt retention, target health, artifact transfer,
  and cleanup.
- [ ] Do not run the resource sampler during the readiness latency observations.
- [ ] Verify every node materializes the expected corpus-derived hashes,
  manifests record `tx_source=mock-placeholder`, and the remote immutable image
  identity equals the frozen identity.
- [ ] Retain any failed attempt and logs. Destroy by default; preserve resources
  only under a separately authorized debugging deadline.
- [ ] Authenticate empty cleanup for instances, volumes, VPC/subnet/route
  resources, key pair, ECR/IAM resources when used, and local temporary keys.

**Exit gate:** the complete `n=4` readiness artifact is valid, cleanup is empty,
and the observed attempt rate supports a revised, deterministic duration and
cost ceiling for the primary campaign.

## Checkpoint 3: Primary `n=4/7` P99 Collection

**Current state:** Not reachable until Checkpoint 2 passes.

- [ ] Run `n=4` first. Stop before `n=7` if provenance, artifact integrity,
  completion, cost, or cleanup fails.
- [ ] Run `n=7` only after the `n=4` phase is copied locally, checksummed,
  validated, and cleaned up.
- [ ] For each node count, collect batches `8/32/128`, 10 retained warmups,
  1,000 measured attempts per cell, 10 balanced blocks, seed `20260621`.
- [ ] Enforce the 12-second classification boundary independently of the
  larger harness timeout.
- [ ] Retain failures, inconsistencies, and timeouts as complete negative
  evidence; exclude them from successful latency and stage distributions.
- [ ] Do not start the host resource sampler.

**Exit gate:** all six primary cells have complete provenance and raw data,
p50/p95/p99/max and order-statistic intervals are generated where eligible,
and authenticated cleanup is empty after both phases.

## Checkpoint 4: Separate Resource Collection

**Current state:** Not reachable until Checkpoint 3 passes.

- [ ] Launch a dedicated `resource-measured` phase with no latency/p99 claim.
- [ ] Use the same source, image, transaction source/corpus, topology,
  instance class, configuration, and primary scenarios.
- [ ] Sample every operator at 250 ms; require contiguous indexes, monotonic
  CPU/network counters or explicit reset rejection, at least four rows per
  node/configuration, and no restart/OOM signal.
- [ ] Report per-node and cluster summaries. Treat cluster memory as sums of
  per-node peaks, not a synchronized peak, and keep host/container network
  bytes distinct from protocol-message metrics.
- [ ] Copy, checksum, validate, and then authenticate cleanup.

**Exit gate:** dedicated resource artifacts are complete for every primary
node/configuration without contaminating the latency collection.

## Checkpoint 5: Extension Pilot Decision

**Current state:** Deliberately deferred; not part of the initial launch.

- [ ] After primary acceptance, run a separate 30-observation pilot for
  `n=10,t=7` at batches `8/32/128` and batch `512` at `n=4/7/10`.
- [ ] Require generated `BMax` to cover 512 and retain all outcomes.
- [ ] If a cell is viable, authorize 1,000 independent final observations.
  If it clearly exceeds 12 seconds or fails frequently, retain 100 independent
  boundary observations and make no p99-feasibility claim.
- [ ] Record the continuation decision per cell before any extension launch.

**Exit gate:** every extension cell has an explicit continue/boundary decision;
no primary and extension observations are silently pooled.

## Checkpoint 6: Artifact Validation, Analysis, Cleanup, And Documentation

- [ ] Verify exact source, image, corpus hash/distribution, schema, seed,
  schedule, topology, instance class, thresholds, BMax, transaction source,
  and attempted/completed/failed/timed-out counts.
- [ ] Verify cross-node consistency, selected-ciphertext counts, timing
  additivity, target health, resource coverage, and checksum completeness.
- [ ] Load the aggregate through the chart pipeline without manual repair.
- [ ] Report same-region p50/p95/p99/max, confidence intervals, completion
  fractions, throughput, stage attribution, and resource summaries only within
  the accepted claim boundaries.
- [ ] Authenticate empty cleanup from Terraform state and exact AWS queries.
- [ ] Update issue #15 with evidence and decisions. Update
  `docs/VALIDATION.md`, `docs/CHANGELOG.md`, and `docs/STATUS.md` only after an
  accepted or rejected live campaign changes durable evidence state.
- [ ] End with branch, commit, validation, publication, cleanup, and retained
  divergent-work status. Do not push without explicit authorization.

---

## Duration, Cost, Quota, And Distribution Bounds

The current runner executes 3,030 latency attempts and 3,000 resource attempts
per node-count phase. At the 12-second evidence boundary this is 20.1 hours per
phase and 40.2 hours for sequential `n=4` and `n=7`; at the runner's 30-second
attempt timeout it is 50.25 hours per phase and 100.5 hours total, before
provisioning, artifact transfer, analysis, and cleanup. These are planning
envelopes, not a hard wall-clock bound, because the frozen runner lacks an
enclosing deadline.

Pricing model dated 2026-07-28, before EBS, transfer, ECR storage, or taxes:

- Linux `t3.small` in `us-east-1`: assumed `$0.0208` per instance-hour.
- Public IPv4: `$0.005` per address-hour.
- T3 Unlimited surplus credits: up to `$0.05` per vCPU-hour; each `t3.small`
  has two vCPUs.
- `n=4` uses five instances and 10 Standard On-Demand vCPUs:
  `$0.1290/hour` before surplus, up to `$0.6290/hour` at the modeled maximum.
- `n=7` uses eight instances and 16 Standard On-Demand vCPUs:
  `$0.2064/hour` before surplus, up to `$1.0064/hour` at the modeled maximum.
- Combined modeled compute plus IPv4 is `$6.74-$32.87` at the 12-second
  envelope and `$16.85-$82.18` at the 30-second envelope. A live authorization
  should use at least a `$100` stop ceiling after AMI/EBS and data assumptions
  are pinned.

The minimum EC2 quota is 16 running Standard On-Demand vCPUs because `n=4` and
`n=7` run sequentially. Capacity must exist for eight `t3.small` instances in
`us-east-1a`. The preferred immutable-image method is SSH load from a verified
local tag to avoid an ECR manifest transformation; this remains blocked until
remote digest verification and immutable manifest recording exist. ECR is an
alternative only if the pushed digest is compared with the frozen digest
before any service starts.

## Exact Commands

The only currently recommended exact command is the no-AWS validation command,
which has already passed from the detached frozen worktree:

```bash
bash deploy/ec2/run-m3-same-az.sh \
  --admin-cidr 127.0.0.1/32 \
  --node-counts 4,7 \
  --batch-sizes 8,32,128 \
  --warmups 10 \
  --repetitions 1000 \
  --repetition-blocks 10 \
  --seed 20260621 \
  --validate-only
```

There is no recommended live command. The superficially corresponding command
would rebuild the image, execute a synthetic/direct transaction source, omit
the committed protocol corpus, record mutable image metadata in SSH-load mode,
and lack an enforceable end-to-end time/cost ceiling. Executing it would create
evidence that violates issue #15 and `docs/VALIDATION.md`.
