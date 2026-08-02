# Issue 15 Same-Region Campaign Execution Plan

AWS execution remains separately authorized. This plan does not authorize AWS
API calls, Terraform plan/apply, registry writes, images pushes, or billable
resources.

## Replacement Contract

The 2026-07-29 invalidation decision superseded source
`2bc8efc9269798a7f7ab58021f8b9bda1012ae5d` and image
`sha256:ee99ceb…`. They remain historical evidence only and must not be
executed or combined with replacement observations.

The replacement must freeze:

- one clean source commit containing `bloc-cluster-v3` and `bte-tx-v2`;
- digest-addressed `linux/amd64` node and mempool images;
- separate BMax-128 public configs and fully self-checked encrypted corpora for
  `n=4,t=3` and `n=7,t=5`;
- the 512-row nested plaintext master corpus and exact 8/32/128 prefix
  identities;
- `tx_source=mock-encrypted-corpus`, with exact requested/received counts;
- same-AZ `us-east-1a`, `t3.small` operators and controller;
- batches 8/32/128, 10 warmups, 1,000 measured attempts per cell, 10 balanced
  blocks, seed 20260621, and the 12-second completion boundary.

Latency and resource collection are separate phases. `n=10` and batch 512 are
excluded from the primary launch and begin only with a later 30-observation
pilot/continuation decision.

## Checkpoint 1: Frozen Provenance Readiness

- Complete all four module suites, focused races, ciphertext/corpus contract
  tests, Compose resolution, and campaign `--validate-only`.
- Generate cluster configuration first; encrypt and self-check each corpus
  offline; bind public/plaintext/encrypted/prefix identities into the node and
  remote evaluator configs.
- Inspect both local images by immutable digest. Do not rebuild, retag,
  substitute, or distribute until both references are locally addressable.
- Freeze the clean source SHA only after validation. Record exact image and
  corpus file SHA-256 values.
- Bound quotas (at least eight concurrent instances and the corresponding
  Standard On-Demand vCPUs), public IPv4 requirements, wall-clock deadline,
  cost ceiling, artifact recovery, failure retention, and authenticated cleanup.
- Implement and test immutable two-image plus per-cluster corpus distribution.

Exit gate: `run-same-az-campaign.sh --validate-only` passes against real frozen
identities and artifacts, and the live path has authenticated cleanup. Current
state: lifecycle, shared pull-only ECR distribution, recovery, and cleanup are
implemented; local n4/n7 crypto/corpus fixtures and all primary validate-only
permutations pass. Remaining gates are the final source freeze, two published
and inspected linux/amd64 image digests, final manifests, chart-test dependencies,
the extended bloc-node race check, account capacity, and live authorization.

## Checkpoint 2: Primary n=4 Pilot/Readiness

- `n=4,t=3`, exact 8/32/128 prefixes, one warmup and three measured attempts
  per cell.
- No resource sampler.
- Verify endpoint/config/evaluator identities, exact materialized prefix,
  target health, retained failures, artifact recovery, and empty authenticated
  cleanup.
- Recalculate the primary duration and cost ceiling from this pilot.

## Checkpoint 3: Primary n=4/7 p99 Collection

- Run n=4 first; validate, recover, checksum, and clean it before n=7.
- For each node count run exact 8/32/128 prefixes, 10 warmups, 1,000 measured
  attempts per cell, 10 blocks, seed 20260621.
- Retain all failures, inconsistencies, and timeouts. Only successful,
  consistent attempts enter latency distributions.
- Keep the resource sampler disabled.

## Checkpoint 4: Separate Resource Collection

- Start fresh infrastructure from the same frozen source, images, configs, and
  corpora.
- Run only the documented `resource-measured` schedule with the dedicated
  sampler; do not merge its latencies into the p99 dataset.
- Require complete per-node CPU, RSS/memory, network, disk, restart/OOM, and
  sampler-cadence coverage.

## Checkpoint 5: Extension Pilot Decision

- Only after primary acceptance, generate and freeze distinct BMax-512 configs
  and corpora.
- Run 30 observations per authorized n=10 or batch-512 extension cell.
- Continue to the documented 1,000-observation or 100-boundary-observation path
  only after recording a continuation decision.

## Checkpoint 6: Validation, Analysis, Cleanup, Documentation

- Validate manifests, raw runs, per-node rows, requested/received counts,
  provenance, attempt completeness, timing additivity, summaries, confidence
  intervals, and chart compatibility.
- Authenticate zero remaining instances, volumes, network resources, key pairs,
  IAM objects, and registries/repositories created by the campaign.
- Update issue #15 with exact evidence and cleanup results. Update
  `STATUS.md` only when the active blocker, accepted evidence, baseline, or
  immediate next action changes.

## Current No-AWS Commands

```sh
cd bte/btd-impl-main && go test ./...
cd mempool-il && go test ./...
cd bloc-node && go test ./...
cd sbc/hbbft && go test ./...
docker compose -f deploy/docker-compose/compose.yaml config
bash deploy/ec2/run-same-az-campaign.sh \
  --phase readiness-pilot --bundle-root <private-n4-bundle> --node-count 4 \
  --source-sha <replacement-sha> --bloc-image <private-ecr-bloc@digest> \
  --mempool-image <private-ecr-mempool@digest> \
  --experiment-id issue-15-same-az-n4-pilot \
  --admin-cidr 127.0.0.1/32 --aws-profile <profile> --validate-only
bash deploy/ec2/run-three-region-campaign.sh \
  --phase readiness-pilot --bundle-root <private-n4-bundle> --node-count 4 \
  --source-sha <replacement-sha> --bloc-image <same-private-ecr-bloc@digest> \
  --mempool-image <same-private-ecr-mempool@digest> \
  --experiment-id issue-15-three-region-n4-pilot \
  --admin-cidr 127.0.0.1/32 --aws-profile <profile> --validate-only
```

After Checkpoint 1 closes and the user separately authorizes AWS execution, the
first live command is the same-AZ n4 readiness command above with the real
controller `/32` and `--execute-live` replacing `--validate-only`. No latency,
resource, n7, or three-region continuation is implied by that authorization.
