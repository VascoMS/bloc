# Status

- Last reviewed: `2026-08-05`
- Active milestone: `M5. Performance, Scaling, And Resource Evidence`
- Latest completed milestone: `M4. Evaluation Readiness And Prototype Hardening`
- Last known good source: `cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f`

## Current Prototype State

The implemented prototype supports the complete local BLOC path: operators
construct encrypted inclusion-list proposals, reach slot-scoped ACS agreement,
deterministically merge and plan the accepted ciphertext set, exchange bounded
threshold-decryption shares, and materialize the same ordered plaintext
transactions.

The active integration uses libp2p-authenticated operator messaging, scoped BTE
decoding, deterministic BEAT-MEV `Opt-2` sub-batching, bounded proposal and
envelope sizes, root-bound RBC reconstruction with post-decode commitment
verification, bounded share retention/recovery, split public configuration and
operator-local secrets, durable bounded terminal slot failures, node-owned
bounded mempool HTTP requests, non-contaminating host resource sampling and
validation, p99-capable Type-7/order-statistic reporting, balanced seeded
long-campaign scheduling, explicit terminal-attempt accounting, a deterministic
500-transaction balanced client corpus with one plaintext/encrypted measurement
per target, a separate nested 512-target representative protocol corpus,
Prometheus metrics, local evaluators, and VM/EC2 remote evaluation.
The source-led protocol review and current module boundaries are documented in
[ARCHITECTURE.md](ARCHITECTURE.md), the module deep dives, and the [PIR evidence
register](archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md).

This remains a research prototype. It does not provide production DKG or key
custody, public decryption-share verification, a cryptographic common coin,
execution-client validation, Builder API compatibility, proposer signing,
slashing protection, or PBS prefix enforcement.

## Latest Completed Milestone

M4 is complete. Its now-superseded evaluation candidate was source
`2bc8efc9269798a7f7ab58021f8b9bda1012ae5d` and local immutable image
`bloc-node@sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`.
That historical image contract was `linux/amd64`, user `10001:10001`, entrypoint
`["bloc-node"]`, and default command
`["run","--config","/config/cluster.json"]`.

Validation logs are retained under the ignored root
`results/release-candidate/2bc8efc9269798a7f7ab58021f8b9bda1012ae5d/validation/`.
All four Go module suites, chart tests, normal and targeted race suites, both
BTE decoder fuzz targets, Terraform validation, runner portability, local
evaluation, full-path BTE benchmarks, and standard/mock-placeholder Compose
rehearsals passed. The exact-source ACS campaign
`results/local/acs-common-subset-safety/rc-2bc8efc/` passed 1,000 delivery
schedules, the 100-slot sustained gate, the 180-slot compatibility matrix,
identity checks, and Merge/Plan attribution.

Issue #8's accepted validation-only distributed preflight is retained at
`results/local/distributed-preflight-2bc8efc/`. It exercised all primary and
extension configurations from the frozen source with complete, consistent
artifacts and chart-loader compatibility. It collected no resource evidence and
supports no local performance claim.

The accepted M3 three-region evidence remains at
`results/ec2/m3-three-region-synthetic-accepted-20260718-1/`. Issue #13's
balanced 500-target client-overhead artifact remains separate from the
`28/50/12/8/2` 100-target full-protocol workload. Their scoped claims and the
release-candidate configuration contract are defined in
[VALIDATION.md](VALIDATION.md).

## Open Blockers And Risks

- **Replacement campaign execution:** the epochless hybrid-ciphertext wire,
  deterministic 512-target master corpus, immutable cluster-specific encrypted
  prefixes, and exact-count provider path are implemented. Network-independent
  n4/n7 identity generation, primary corpus binding, frozen-bundle verification,
  topology materialization, pull-only ECR access, immutable staging, health
  gates, separate latency/resource phases, failure recovery, and authenticated
  cleanup are implemented locally on issue #15's task branch. Real local n4/n7
  BMax-128 identities and 128-entry encrypted corpora are bound by final
  manifests to source `cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f`, BLOC image
  `sha256:a58d8ef4ef5a674ce89341538798b47a422ffdc66d72637d8b3f4351282a2eec`,
  and mempool image
  `sha256:3c0c147a92d66c89293f9bda89967bded2ae22795bd37de09fa466ca4dbe38aa`.
  Both images were pulled back from immutable, scan-on-push private ECR
  repositories by digest and inspected as `linux/amd64` with their expected
  non-root users and entrypoints. Final bundle verification and every primary
  `--validate-only` permutation passed. The isolated chart environment passes
  37/37 tests, and the automated split race gate passes the complete normal
  application suite plus focused BTE, mempool,
  and concurrency-relevant application races. Earlier 10- and 30-minute
  package-wide race attempts exhausted their process time while actively parsing
  BMax-128 CRS material and reported no race. A clean detached execution
  worktree remains anchored at the frozen SHA while task documentation advances.
  Regional `t3.small` offerings and Standard On-Demand quotas satisfy the
  documented 16/4/4 vCPU requirement. The authorized n4 same-AZ pilot attempt
  `issue-15-same-az-n4-pilot-20260802T144509Z` was rejected before instance
  creation because IAM user `bloc` lacked `iam:CreateRole` for the generated
  `issue-15-*` role. Its seven partial network resources and temporary key were
  removed; direct authenticated EC2 checks and Terraform state are empty. The
  repository deployer policy now aligns role creation and inline pull-policy
  management under the `bloc-ec2-*` namespace and permits cleanup's global
  read-only `iam:ListRoles` and `iam:ListInstanceProfiles` calls. That policy was
  applied to IAM user `bloc`, and both list operations passed. Authorized retry
  `bloc-ec2-issue-15-same-az-n4-pilot-20260802T150441Z` then stopped during
  Terraform planning because its derived 68-character role name exceeds IAM's
  64-character limit; Terraform applied no resources and the temporary key was
  deleted. Authenticated cleanup output is fully empty, but the frozen same-AZ
  adapter's final jq assertion mis-groups the empty-array expression and rejects
  that valid document. The approved minimal tooling correction now rejects IDs
  longer than 47 characters and correctly accepts empty cleanup for both final
  topologies. Short-ID retry `bloc-ec2-i15-sa-n4-p1` created the expected 15
  resources but failed during staging before measurement because the controller
  setup created `/opt/bloc/ec2` without elevated permissions; an operator's CRS
  transfer also reset while cloud-init was still completing. Terraform destroyed
  all 15 resources and the key, and authenticated cleanup plus Terraform state
  are empty. The approved staging correction then added a bounded host-readiness
  gate, fail-fast transfers, and controller ownership. Retry
  `bloc-ec2-i15-sa-n4-p2` stopped before apply on a transient Terraform provider
  download failure and left no AWS resources. Retry `bloc-ec2-i15-sa-n4-p3`
  reused the exact locked provider, created the expected 15 resources, and passed
  materialization, staging, immutable-image verification, and service startup.
  Its health gate checked each node only once immediately after startup and
  failed before the BMax-128 services became ready. Failure recovery also omitted
  the required Compose image variables, so the retained logs contain an
  interpolation error instead of container output. Terraform destroyed all 15
  resources and the key; authenticated cleanup and Terraform state are empty.
  The approved correction now performs 60 ten-second readiness attempts and
  supplies both immutable image variables during log recovery; its focused and
  topology regressions pass. Retry `bloc-ec2-i15-sa-n4-p4` passed provisioning,
  materialization, staging, digest verification, and Compose startup, then the
  new gate exposed restart loops rather than slow initialization. The frozen
  config requires `/config/cluster.crs`, but `operator-compose.yaml` mounts the
  CRS at `/config/cluster.ec2.crs`. The encrypted corpus is staged with mode
  `0600`, so the non-root mempool image receives `permission denied` on
  `/corpus/encrypted-corpus.json`. The unrecoverable attempt was interrupted to
  avoid extended SSH retries; Terraform destroyed all 15 resources, the EC2 and
  local temporary keys were deleted, direct authenticated EC2/IAM queries are
  empty, and Terraform state contains no resources. The approved correction now
  exposes the canonical CRS mount read-only, stages public config/corpus inputs
  with mode `0644`, and bounds SSH connection attempts. Retry
  `bloc-ec2-i15-sa-n4-p5` passed provisioning, materialization, staging, exact
  digest verification, and Compose startup. Its mempool became healthy, proving
  the public-input correction, while BLOC restarted because the mode-`0600`
  `/run/secrets/operator.json` remained owned by the host `ubuntu` user and is
  unreadable by the frozen image's runtime identity `10001:10001`. No
  measurement began. Terraform destroyed all 15 resources, the EC2 and local
  temporary keys were deleted, authenticated resource queries are empty, and
  Terraform state contains no resources. The approved secret correction now
  retains mode `0600` and assigns only `operator.json` to `10001:10001`; its
  red/green and topology regressions pass. Retry `bloc-ec2-i15-sa-n4-p6` was
  interrupted during host readiness by the local execution channel's
  five-minute boundary before the ownership correction was exercised; its
  authenticated cleanup and state are empty. Persistent-session retry
  `bloc-ec2-i15-sa-n4-p7` passed provisioning, materialization, staging, secret
  ownership (`10001:10001 0600`), exact image verification, and startup. Both
  containers remained running, but the health gate cannot pass: Compose keeps
  mempool port 8080 internal while the gate probes host
  `127.0.0.1:8080`. No measurement began. The five instances and every exact
  p7 network/IAM/key resource were manually removed after the interrupted
  wrapper cleanup; authenticated queries, Terraform state, and local key checks
  are empty. The approved loopback-only binding and its resolved-Compose
  regression now pass on the task branch and were overlaid byte-for-byte onto
  the detached frozen worktree. Retry `bloc-ec2-i15-sa-n4-p8` passed frozen
  validation, provisioning, materialization, staging, exact image verification,
  and Compose startup. The loopback probe reached mempool successfully, then
  readiness failed because the operator host does not install `jq`, although
  the unchanged health command uses it to validate `returned_count=8`. No
  measurement began. Terraform destroyed all 15 resources, the EC2 and local
  temporary keys were deleted, and authenticated resource queries plus
  Terraform state are empty. The approved `jq` dependency was then added to
  both EC2 user-data variants with a real command-boundary regression and
  overlaid exactly onto the frozen worktree. Retry
  `bloc-ec2-i15-sa-n4-p9` passed validation, provisioning, staging, exact image
  verification, startup, and every readiness gate, proving the `jq` correction.
  The controller evaluator then failed all three batch invocations because the
  frozen runtime UID `10001` cannot create the requested output below the
  host-owned `/opt/bloc/ec2/results` bind mount. The measurement loop failed to
  propagate those SSH errors, artifact validation was not enabled, and the
  runner consequently wrote an incorrect `complete` manifest despite retaining
  no run measurements. Recovery logs also omitted required Compose `NODE_ID`,
  and automatic cleanup left the local private key after deleting its AWS key
  pair. The exact local key was removed manually. Terraform destroyed all 15
  resources; authenticated resource queries and Terraform state are empty. All
  attempts through p9 remain invalid and contain no metric observation. The
  approved fail-closed correction set then passed five independent red/green
  boundaries and was overlaid exactly onto the frozen worktree. Retry
  `bloc-ec2-i15-sa-n4-p10` passed provisioning, staging, exact image checks,
  startup, health, controller output, recovery, mandatory artifact validation,
  and automatic local/cloud cleanup. It retained nine measured attempts: all
  three batch-8 attempts completed consistently within the boundary, while all
  six batch-32/128 attempts failed before protocol execution. Each separate
  evaluator invocation restarted at slots 1-4, but the persistent nodes had
  already terminalized those slots during batch 8 and correctly returned
  `409 Conflict` (`slot N must be greater than previous slot 4`). The p10
  artifact is complete negative readiness evidence, not an accepted readiness
  pilot and not primary metric evidence. Terraform destroyed all 15 resources;
  authenticated resource queries and state are empty, recovery logs are usable,
  and the local temporary key is absent. The approved monotonic slot allocator
  now passes a red/green readiness and full-primary regression, is committed on
  the task branch, and is overlaid byte-for-byte onto the frozen worktree.
  Authorized retry `bloc-ec2-i15-sa-n4-p11` then passed the complete live
  readiness lifecycle. All nine measured attempts were successful, consistent,
  and within 12 seconds: batch 8 was 89.350--99.365 ms, batch 32 was
  312.324--319.261 ms, and batch 128 was 1.625--1.698 s. Slots were monotonic
  across batch invocations (`1--4`, `5--8`, and `9--12` including warmups), all
  four recovery logs are usable, the manifest and artifact validators pass,
  Terraform destroyed all 15 resources, authenticated local/cloud cleanup and
  state are empty, and the temporary key is absent. P11 is accepted readiness
  evidence. The authorized primary n4 same-AZ latency attempt
  `bloc-ec2-i15-sa-n4-latency-p1` then completed all 3,000 measured attempts:
  each batch retained 1,000 successful, consistent, deadline-met rows across
  all 10 blocks. Cleanup destroyed all 15 resources, authenticated queries and
  Terraform state are empty, all recovery logs are present, and no private key
  remains. The user approved invalidating the frozen validator's incorrect
  globally unique `run_id` assumption. Final attempt identity is now explicitly
  `(measurement_block, run_id)`; a 10-block regression accepts block-local IDs
  and still rejects duplicate pairs. The corrected final-phase and cleanup gates
  pass the retained artifact, so n4 latency is accepted without a live rerun.
  Two subsequent n7 latency launches stopped before measurement during staging.
  `bloc-ec2-i15-sa-n7-latency-p1` lost its one-shot SCP connection while
  transferring the immutable corpus to operator 4. The unchanged p2 retry also
  recorded `stage=failed` after three instances took about 17 minutes to become
  ready. Both attempts destroyed all 18 resources and their temporary keys;
  independent authenticated cleanup validation passes and Terraform state is
  empty. The shared lifecycle bounds and retries SSH, but `final_scp` performs
  one transfer with no connection/server-alive options or retry. A third launch
  is paused pending explicit approval for a bounded SCP-only tooling correction
  and regression; source, images, corpus, schema, and protocol remain unchanged.
- **Evidence completeness:** the final evidence contract requires p99-capable
  same-region and three-region VM performance campaigns, complete per-operator
  VM resource measurements, RQ3 fault campaigns, and operator cost synthesis.
  The accepted M3 campaign remains valid historical p50/p95 evidence but is not
  the final release-candidate campaign. Local issue #8 output is
  validation-only preflight evidence, not local performance or resource
  evidence.

## Bounded Prototype Limitations

- BBA uses a deterministic rather than cryptographic common coin; RQ3 liveness
  claims will be restricted to the exercised schedules and fault model.
- The CRS retains inherited insecure diagonal elements and a trusted generator
  creates all shares; secure setup and DKG are future work.
- Shares have no public correctness proof. Admission and subset recovery remain
  bounded, and invalid shares are detected through reconstruction failure.
- The current prototype assigns BTE puncture indexes from local/corpus
  position. This coordinated model is retained for the thesis metrics;
  paper-aligned independent index sampling and collision-distribution evidence
  are deferred to issue #22.
- Builder API integration, DVT/SSV signing, execution payload construction, and
  block publication are future integration work and outside the measured path.

## Immediate Next Actions

1. Decide whether to invalidate the frozen staging helper so SCP uses the same
   bounded connection/server-alive settings as SSH and retries a transfer at
   most three times, with red/green exhaustion and recovery regressions.
2. If approved, overlay the exact tested helper onto the frozen worktree and
   retry n7 latency only after validate-only and authenticated empty cleanup.
3. After accepted n7 latency, collect the separate n4/n7 resource phases without
   admitting their latency rows into the p99 dataset.
4. Validate and accept issue #15 artifacts, then run issue `#16` using matched
   manifests and configurations.
5. Do not combine measurements from different source, image, corpus,
   configuration, or schema revisions into one final campaign.
6. Track granular work in the [BLOC Thesis Prototype GitHub
   Project](https://github.com/users/VascoMS/projects/1) while keeping this file
   limited to milestone state, major blockers, accepted evidence, and next
   actions.

## Last Known Good Baseline

- Date: `2026-08-02`
- Source: `cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f`
- BLOC image:
  `632783683536.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:a58d8ef4ef5a674ce89341538798b47a422ffdc66d72637d8b3f4351282a2eec`
- Mempool image:
  `632783683536.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:3c0c147a92d66c89293f9bda89967bded2ae22795bd37de09fa466ca4dbe38aa`
- Replacement-candidate validation:
  `results/local/final-campaign-readiness-cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f/validation/`
- Historical M4 local safety evidence:
  `results/local/acs-common-subset-safety/rc-2bc8efc/`
- Historical M4 accepted distributed-campaign preflight:
  `results/local/distributed-preflight-2bc8efc/`
- Linux RBC/ACS/BBA race evidence:
  `results/release-candidate/2bc8efc9269798a7f7ab58021f8b9bda1012ae5d/validation/logs/hbbft-linux-amd64-race.log`
- Accepted distributed evidence:
  `results/ec2/m3-three-region-synthetic-accepted-20260718-1/`
- Canonical validation commands and evidence semantics:
  [VALIDATION.md](VALIDATION.md)
- Reproduction and deployment procedures: [WORKFLOWS.md](WORKFLOWS.md) and the
  module-local deployment runbooks.

## Planned And Deferred Work

- Active: `M5. Performance, Scaling, And Resource Evidence`
- Planned after M5: `M6. Fault And Adversarial Robustness Evidence`
- Planned after M6: `M7. Cost Analysis And Thesis Evidence Synthesis`
- Deferred: Builder API boundary, SSV/DVT signing integration, secure CRS/DKG,
  public share proofs, cryptographic common coin, and PBS prefix enforcement.

See [ROADMAP.md](ROADMAP.md) for milestone objectives and done criteria.
