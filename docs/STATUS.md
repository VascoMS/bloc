# Status

- Last reviewed: `2026-08-30`
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
Prometheus metrics, local evaluators, VM/EC2 remote evaluation, and opt-in
bounded `bloc-acs-trace/v1` diagnostics with fail-closed evaluator artifacts.
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

- **Issue #23 attribution and phase-one local stream experiment are accepted;
  distributed canaries remain:** the
  matched n4 same-AZ and three-region latency phases are complete on source
  `d6bf2c0d62d5e4e039952ace117a6ab9a08b8cc0` and immutable BLOC image
  `sha256:ab4bf84da397f379ba5e22820ddd53ba532702b1bc7da3d709e3c847e1e5eaf1`.
  Each topology retained 90/90 successful, consistent measured attempts and
  360 validated node traces for batches `8/32/128`; the matched-contract loader
  accepted identical source, images, identities, corpus, seed, schedule, and
  `bloc-acs-trace/v1` schema. ACS p50 changed from
  `15.908/26.381/59.482 ms` same-AZ to `185.691/235.464/500.357 ms`
  three-region. The post-BBA adapter handoff stayed below `1 ms` for batches 8
  and 32 and near `4--5 ms` for batch 128, while per-message send maxima moved
  from roughly `3--12 ms` to `70--422 ms`. Send failures remained zero, BBA
  epoch depth stayed at median `1` and p95 `2`, and WAN BVAL/AUX counts did not
  increase. The first RBC output is especially payload-sensitive at batch 128
  (`30.581 ms` versus `389.078 ms` p50); at smaller batches, successive RBC and
  BBA quorum waits account for the remaining WAN delay. This supports a core
  ACS transport/round-trip attribution rather than adapter work, retries, or
  extra agreement epochs. The transport mechanism is an inference to test:
  each addressed ACS message opens a fresh libp2p logical stream over a
  persistent peer connection, so stream setup, WAN RTT, write completion, and
  backpressure may amplify the protocol's broadcast/quorum pattern. Both
  campaigns completed recovery and cleanup; Terraform destroyed 15 and 39
  resources respectively, all regional EC2/EBS/VPC/key/IAM checks are empty,
  and the three peering records are `deleted`. Retained roots are
  `results/ec2/bloc-ec2-i23-sa-n4-c4/`,
  `results/ec2/bloc-ec2-i23-tr-n4-c2/`, and
  `results/analysis/issue-23-acs-n4-c4-c2/`. Phase one on source
  `857d5024b9db6d6a9ec78b726ca5af921181f197` keeps v1 fresh streams as the
  compatibility arm and adds opt-in v2 persistent framed streams with one
  capacity-one writer per peer, prewarm/readiness gates, bounded deadlines,
  reset-without-replay failure handling, and `bloc-acs-trace/v2` transport
  phases. The matched local n4 experiment retained 30/30 successful,
  consistent measurements for each mode and batch. Persistent streams reduced
  ACS p50 from `4.363/4.463/5.197 ms` to `1.798/2.002/2.943 ms` for batches
  `8/32/128`; p50 and p95 confidence intervals were non-overlapping in every
  cell, ACS send failures stayed zero, and median persistent queue wait was
  `0.211/0.240/0.384 ms`. The analyzer classified all three cells and the
  cross-batch result as `acs-signal`. This is accepted local mechanism evidence,
  not proof of the WAN effect. Ignored roots are
  `bloc-node/results/phase1-streams/local-fresh/`,
  `bloc-node/results/phase1-streams/local-persistent/`, and
  `bloc-node/results/phase1-streams/local-comparison/`. The phase-one cloud
  campaign is explicitly authorized. Source
  `fbebd778c76cd0c8e3a16a5673cb1803bec2f090` and private ECR image
  `sha256:730771174d5cd6a79ff19271c65fddf7fd28a6da2f74698cd4ad08ce9ab70a50`
  passed the four-arm no-allocation contract. Same-AZ fresh attempt
  `bloc-ec2-i23-p1-sa-fr-c2` is accepted: all nine cells completed, retaining
  90/90 measured runs and 420 trace-v2 records. Terraform destroyed all 15
  resources and retained plus independent authenticated cleanup checks are
  empty. Same-AZ persistent attempt `bloc-ec2-i23-p1-sa-ps-c1` is rejected:
  slot 1 timed out with zero node results, its prewarmed streams reset at the
  ten-second write deadlines, and later slots returned `409 Conflict` because
  slot 1 remained active. Its 0/90 completed measured runs are not evidence.
  The materializer marked the phase invalid, Terraform destroyed all 15
  resources, and retained plus independent cleanup checks are empty. The
  campaign remains halted before multi-region. A bounded 32-frame FIFO handoff
  now keeps each persistent stream's read loop draining while one dispatcher
  invokes the protocol handler in order. The failure was reproduced before the
  change, and the regression, focused/full/race transport checks, a four-slot
  smoke run, and the exact 105-slot local matrix now pass: 90/90 measured runs,
  420 trace-v2 records, zero send failures, and no recovery. Freeze a new
  source/image/bundle and rerun the matched same-AZ pair before allocating
  multi-region. Do not expand to n7 or publish p99 from 30 observations.
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
  empty. The approved staging-helper correction now gives SCP the same bounded
  connection/server-alive settings as SSH and retries each transfer at most
  three times. Recovery and exhaustion regressions, the complete local campaign
  runner suite, byte-identical frozen-worktree overlay, and n7 p3
  `--validate-only` passed. Authorized retry
  `bloc-ec2-i15-sa-n7-latency-p3` then provisioned the expected 18 resources and
  passed materialization, all seven corpus transfers, exact image verification,
  startup, and health. Measurement stopped during block 2 when the foreground
  controller SSH session timed out. The recovered controller artifact proves
  the remote batch-128 command nevertheless completed all 100 measured rows
  after terminal output stopped at row 71; block 1 is complete and block 2
  retains complete batch-32/128 cells, for 500 measured rows total. A blind
  command retry is unsafe because it can re-execute completed work against
  terminalized slots. The phase is invalid and incomplete. Terraform destroyed
  all 18 resources and the temporary key; independent cleanup validation passes,
  Terraform state is empty, and no private key remains. Resource phases are
  paused pending an explicit execution-tooling decision. The user approved the
  reconnectable option. The task branch now stages a controller-local helper,
  atomically claims one block/batch/first-slot job identity, launches it at most
  once, and polls durable status through short SSH connections. Tests prove a
  lost start response plus transient poll failures reconnect without duplicate
  execution, exhausted polling fails closed, a completed job can be reattached,
  nonzero/ambiguous/lost states remain terminal, job state is recovered, and the
  monotonic slot schedule is unchanged. Focused lifecycle/helper/contract,
  29 artifact tests, Bash syntax, diff hygiene, and complete runner portability
  pass. The helper and lifecycle were overlaid byte-for-byte onto frozen
  `cf36eb` with SHA-256
  `247158b5b9cdf0d4b7bc78b2907fd5c530e02266e3b8cbf9963ca7e8f7c84082`
  and `0c2e8d943636a1b9d4856f9d8a75f839d99fec7e173461fd8133e47ad033ebcf`;
  exact n7 latency p4 `--validate-only` passes. Authorized live p4 then passed
  provisioning, staging, exact image checks, startup, and health, and the
  reconnectable helper retained 30 unique job identities without a duplicate
  launch. The controller's unchanged 8 GiB root volume filled during the final
  block-10 batch-128 job: 29 jobs exited zero, the final job retained 74 of 100
  measured attempts, its `runs.jsonl` ends mid-JSON, and its atomic exit-status
  file is empty. The phase is invalid with 2,974 complete measured records and
  26 missing records; none are accepted or combined with n4. Recovery retained
  about 4.7 GiB of evaluator output but an optional resource-directory `rsync`
  ran despite the latency sampler being off and hung for more than nine hours;
  terminating only that subprocess allowed automatic teardown to continue.
  Terraform destroyed all 18 resources and deleted the key pair. Independent
  authenticated cleanup validation passes, Terraform state is empty, and no
  private key remains. Before a from-zero p5, the task requires an explicit
  decision to increase controller-only storage and to bound or skip optional
  resource recovery; source, images, corpus, schema, schedule, and protocol
  remain unchanged. The lifecycle also reports successful destroy and cleanup
  as failed after any earlier phase failure because its event labels use the
  cumulative status; the independent cleanup artifact is authoritative for p4.
  That decision is now approved and implemented on the task branch and frozen
  overlay: both final topologies allocate a 16 GiB encrypted controller root
  volume while operator volumes remain unchanged, recovery uses bounded rsync
  and omits resource directories when the sampler is off, and post-measurement
  lifecycle stages record independent outcomes. Regression-first lifecycle and
  adapter tests, both direct Terraform validations, static Terraform contract,
  29 artifact tests, race-gate contract, full runner portability, exact-overlay
  hashes, and exact frozen n7 p5 same-AZ and three-region `--validate-only`
  checks pass. Authorized live p5 then provisioned the expected eight
  `t3.small` instances in `us-east-1a` with 8 GiB encrypted operator volumes
  and the corrected 16 GiB encrypted controller volume. Materialization and
  staging passed, but the controller stopped answering SSH during its final
  digest-pinned image pull and verification. It was reachable again during
  recovery about one minute later. No service started and no measurement was
  attempted, so `bloc-ec2-i15-sa-n7-latency-p5` is rejected rather than metric
  evidence. Recovery, destroy, and authenticated cleanup all passed;
  Terraform destroyed all 18 resources, its state is empty, the AWS and local
  keys are absent, and the independent cleanup validator passes. The approved
  tooling correction now gives each exact digest-pinned image operation at
  most three attempts over bounded SSH and propagates every operator/controller
  failure immediately. Regression-first tests prove third-attempt recovery,
  three-attempt exhaustion, unchanged digest/architecture verification, and
  that the first operator failure cannot be masked by later hosts. The focused
  and complete lifecycle suites, both topology adapters, final contract, 29
  artifact tests, race-gate contract, runner portability, Bash syntax, both
  Terraform validations, task/frozen byte equality, and exact frozen n7 p6
  same-AZ `--validate-only` pass without AWS access. The task and frozen helper
  SHA-256 is
  `ac64399ec838e82ca70bfb183fa5e41026b758d9c1366e97ead3139fce8fb666`.
  Frozen source, image digests, corpus, schema, schedule, configuration, and
  protocol semantics remain unchanged. Authorized from-zero attempt
  `bloc-ec2-i15-sa-n7-latency-p6` then completed the primary n7 same-AZ
  latency phase in 3h10m06s. Each batch retained 1,000 measured attempts over
  all 10 balanced blocks, and all 3,000 attempts were successful, consistent,
  and within the 12-second completion boundary. Type-7 total-slot latency was
  p50/p95/p99 143.081/156.662/301.749 ms for batch 8,
  388.567/484.247/1,026.110 ms for batch 32, and
  1,730.534/2,064.372/3,232.609 ms for batch 128. The manifest binds the run
  to frozen source `cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f`, the exact
  BLOC and mempool image digests above, encrypted corpus
  `5506222c7dfba596817276ea1d919e976bbfe6c1cfeb5cda9d66c4066c8be528`,
  seven `t3.small` operators in `us-east-1a`, seed `20260621`, 10 warmups,
  1,000 measured attempts per batch, 10 blocks, and resource sampling off.
  Final-phase and cleanup validators pass. Terraform destroyed all 18
  resources and the temporary key; its state is empty, the local PEM is
  absent, and direct authenticated EC2/IAM queries found no scoped residual
  resources. The n7 latency artifact is accepted primary evidence. Separate
  n4/n7 resource phases remained the next gated work. Authorized n4 resource
  attempt `bloc-ec2-i15-sa-n4-resource-p1` then passed provisioning, staging,
  and exact image verification but was rejected before measurement. Operator
  1's service-start SSH call timed out; the per-host startup loop did not
  propagate that failure, so later host success masked it. Node 0 consequently
  remained at HTTP 503 while retrying the absent libp2p peer. No resource
  sampler or evaluator measurement ran. Terraform destroyed all 15 resources
  and deleted the temporary key. A transient credential rejection invalidated
  the wrapper's first cleanup-verification event, but a fresh authenticated
  check passes with empty instances, volumes, VPC/network objects, key pairs,
  IAM role/profile objects, and Terraform state. The approved fail-closed
  correction now propagates every per-host service-start and sampler
  start/stop failure immediately. Its focused regression, complete lifecycle,
  contract, Terraform, runner-portability, 29 artifact, and full node/BTE/
  mempool race suites pass; task and frozen lifecycle helpers are byte-identical
  at SHA-256
  `0ae2371303f027bd226d273c337870d42a9b1490170ac78d780a50fa70026296`,
  and exact n4/n7 resource `--validate-only` passes. Authorized from-zero n4
  resource attempt `bloc-ec2-i15-sa-n4-resource-p2` then cleared every
  provisioning, staging, image, service, health, and sampler gate. It was
  rejected during block 2, batch 128 after the reconnectable supervisor
  reported the cell `LOST`. Recovered durable evidence proves the cell instead
  completed normally: `exit.status` is `0`, stdout contains all 100 successful
  attempts for slots 401--500, and stderr is empty. The status helper has a
  check-then-act race: `exit.status` can appear after its initial file test but
  before its dead-PID test, causing a false `LOST` without rechecking the now
  durable status. Measurement stopped fail-closed, sampler stop and recovery
  passed, Terraform destroyed all 15 resources and deleted the key, and fresh
  authenticated cleanup is fully empty. The approved supervision correction
  now rechecks `exit.status` after a failed PID liveness test before declaring
  `LOST`. Its deterministic regression failed against the old helper with the
  exact false-`LOST` result and passes after the four-line correction. The
  focused helper, complete lifecycle, contract, Terraform, runner-portability,
  29 artifact, race-gate-contract, and Bash syntax suites pass; task and frozen
  helpers are byte-identical at SHA-256
  `a0bfb2e28e04bed69c2a7394b3be16440f89c35051146efa1899d20869949cac`.
  Frozen protocol/source/image/corpus/configuration/schedule/schema remain
  unchanged. Authorized n4 resource p3 then completed all 3,000 evaluator
  attempts with 30/30 controller jobs exiting zero, but is rejected as resource
  evidence. Every recovered operator sampler log contains only the required-
  argument error because `final_sampler_start` omits the node's `--region`; no
  resource CSV exists. The lifecycle recorded sampler start/stop as successful
  because it checks only asynchronous SSH launch/touch completion, resource
  recovery suppresses missing-file failures, and `assert-final-phase` checks
  the sampler manifest flag without invoking resource coverage/cadence/counter/
  restart/OOM validation. The retained p3 manifest is reclassified `invalid`.
  Terraform destroyed all 15 resources and the key, and authenticated cleanup
  plus state are empty. A safe retry requires an explicit frozen-tooling
  invalidation covering per-node region and configuration labels, bounded
  sampler startup/minimum-row/shutdown gates, mandatory resource recovery, and
  final resource evidence validation/summary generation. That approved tooling
  correction is now implemented and overlaid without changing frozen protocol,
  source, images, corpus, configuration, evaluator schema, or schedule. Each
  block/batch cell has a region-bound sampler segment; startup requires a live PID, exact
  header, and four rows; shutdown is bounded and attempts every node; recovery
  requires every operator directory; acceptance requires the exact 30-cell
  coverage per node and regenerates segment and per-batch summaries while
  checking cadence, counters, restart, and OOM state. The focused regressions,
  33 artifact tests, lifecycle/contract/remote-job/race-gate/Terraform/runner
  suites, both direct Terraform validations, task/frozen helper equality, and
  exact n4 p4/n7 p1 resource `--validate-only` contracts pass. Authorized n4
  resource p4 then passed provisioning, immutable staging/images, service
  startup, and health, but failed closed before measurement at the first
  block-1/batch-8 sampler gate. Three operators retained 7--9 samples at about
  2.0-second intervals, which the 250 ms cadence contract correctly rejects;
  node 2 exited after two samples, below the four-row minimum, with an empty
  sampler log. The sampler's nominal 250 ms loop executes bounded
  `docker stats --no-stream` network collection on every sample, and its
  `set -e` path exits on a transient sample failure without identifying the
  failed sub-read. P4 is invalid and contains no evaluator observation.
  Terraform destroyed all 15 resources and the key; the cleanup artifact,
  fresh instance/VPC queries, absent IAM role, empty state, and missing local
  key confirm complete cleanup. N7 resource collection has not launched. A
  further retry requires an explicit decision either to replace per-sample
  Docker network reads with a non-blocking container-network counter source and
  add failure diagnostics, preserving 250 ms evidence, or to relax the accepted
  cadence contract. The user has deferred that decision and all further
  resource collection; issue #15 remains open for the paused resource scope,
  while its accepted n4/n7 same-AZ latency evidence is the matched control for
  issue #16. Issue #16's authorized n4 three-region latency run
  `bloc-ec2-i16-tr-n4-latency-p1` completed and is accepted without a live
  rerun: all 3,000 measured attempts were successful, consistent, and within
  12 seconds across 10 balanced blocks. Type-7 p50/p95/p99 was
  299.452/323.761/539.382 ms for batch 8,
  517.925/627.276/1,039.952 ms for batch 32, and
  2,016.202/2,238.784/3,131.810 ms for batch 128. Its original post-destroy
  cleanup event failed because the three-region Bash adapter appended an extra
  closing brace while passing valid region JSON to `jq`; measurement,
  recovery, and destroy had already passed. A regression-first serializer fix
  now passes the full lifecycle and runner-portability suites and is
  byte-identical in the task and frozen worktrees. Fresh authenticated cleanup
  queries are empty for all regional EC2/network/key categories, IAM
  role/profile categories, and Terraform state; final cleanup and phase
  validators pass, and replay events preserve the original failure and later
  acceptance. Issue #16's authorized n7 three-region latency run
  `bloc-ec2-i16-tr-n7-latency-p1` is also accepted: all 3,000 measured attempts
  were successful, consistent, and within 12 seconds across 10 balanced blocks,
  and all 30 durable controller jobs exited zero. Type-7 p50/p95/p99 was
  366.012/457.844/654.747 ms for batch 8,
  745.101/876.041/1,384.713 ms for batch 32, and
  2,183.349/2,954.622/3,800.734 ms for batch 128. The complete manifest binds
  the same frozen source and immutable images, the n7 BMax-128 corpus, seed,
  deadline, and sampler-off latency contract. Measurement, recovery, destroy,
  and cleanup lifecycle events all passed. The final phase and cleanup
  validators pass; all regional EC2/network/key categories, IAM role/profile
  categories, and Terraform state are empty both in the retained cleanup
  artifact and in independent authenticated API queries. Issue #16's amended
  latency-only scope is complete and the issue is closed as completed. Resource
  collection remains paused under issue #15, and extension and economic
  analysis remain deferred.
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

1. Freeze the corrected issue #23 source/image/bundle, rerun same-AZ fresh then
   persistent, and advance to the authorized multi-region pair only if both
   same-AZ arms and their independent cleanup checks are accepted.
2. Leave issue #15 open and paused for resource collection. Do not admit any
   resource-phase latency rows or rejected campaign attempts into the p99
   dataset.
3. Do not combine measurements from different source, image, corpus,
   configuration, or schema revisions into one final campaign.
4. Keep the existing issue #23 live authorization scoped to the fail-closed n4
   four-arm campaign; do not advance to multi-region until the corrected
   same-AZ pair is accepted and cleanup is independently empty.
5. Track granular work in the [BLOC Thesis Prototype GitHub
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
- Issue #23 local image freeze:
  `results/local/acs-common-subset-safety/issue-23-local-gate-87ae409/local-image-manifest.json`
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
