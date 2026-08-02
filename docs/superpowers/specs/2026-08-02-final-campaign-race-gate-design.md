# Final Campaign Race-Gate Design

## Objective

Replace the impractical package-wide `go test -race ./internal/app` readiness
command with one automated gate that preserves relevant race coverage and all
deterministic campaign-bundle validation.

This is a validation-workflow change only. It must not change protocol code,
cryptographic verification, bundle schemas, corpus semantics, source/image
identity rules, or the 12-second campaign completion boundary.

## Evidence Behind The Change

The full package race command timed out first at 10 minutes and again at 30
minutes without reporting a race. A single
`TestBuildAndLoadCampaignBundle` run took about 185 seconds under the race
detector and about 18 seconds normally. Both timeout stacks showed active
BMax-128 CRS parsing and BLS12-381 pairing work, not blocked goroutines.

The slow tests exercise deterministic identity, bundle, secret, and
materialization validation. The BTE library already has a focused `-race` gate,
while concurrency-sensitive node lifecycle, transport, evaluator, and provider
tests live elsewhere in `internal/app`.

## Selected Design

Add one portable Bash preflight entrypoint under `scripts/tests/`. It runs, in
order:

1. the complete `bloc-node/internal/app` suite normally, including all campaign
   identity, bundle mutation, secret, and topology-materialization tests;
2. `bte/btd-impl-main/be` under the race detector;
3. `mempool-il/internal/mempool` and `mempool-il/internal/api` under the race
   detector; and
4. `bloc-node/internal/app` under the race detector while skipping only the
   explicitly enumerated deterministic campaign identity, bundle, secret, and
   materialization tests.

The skip expression is an allowlisted set of exact test names, not a broad
substring such as `Campaign`, so final-campaign evaluator and other ordinary
application tests continue to run under `-race`.

## Automation And Failure Semantics

The script uses `set -Eeuo pipefail`, accepts no behavior-changing options, and
stops at the first failed command. It uses caller-provided `GOCACHE` when set
and otherwise selects task-specific `/tmp` caches. It performs no network,
Docker, registry, Terraform, GitHub, or AWS operation.

A contract test places a fake `go` executable first on `PATH` and asserts the
four exact invocations. This proves the intended split without executing the
expensive suites. The real verification then executes the new entrypoint.

## Acceptance

- the contract test fails before the entrypoint exists and passes afterward;
- every excluded bloc-node test is also covered by the complete normal suite;
- BTE and mempool focused race suites pass;
- the remaining bloc-node race suite passes without a detected race;
- all existing campaign runner, artifact, Terraform, Compose, and chart checks
  remain unchanged and green;
- canonical validation, status, changelog, and issue #15 record the refined
  gate and remove the obsolete full-package race-timeout blocker; and
- no AWS API, Terraform plan/apply, registry write, resource creation, or Git
  push occurs.
