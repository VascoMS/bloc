# Status

- Last reviewed: `2026-09-06`
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
Prometheus metrics, local evaluators, VM/EC2 remote evaluation, opt-in finalized
`bloc-acs-trace/v3` diagnostics with fail-closed evaluator artifacts, and a
locally validated experimental `persistent-lanes` mode that isolates control
and data application streams while retaining fresh and single-stream modes.
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

- **Persistent control/data lanes remain experimental pending matched VM
  evidence:** issue #25's finalized `bloc-acs-trace/v3` implementation now
  synchronously admits sends, seals at local ACS output, publishes only after
  terminal accounting completes, and records immutable pending-at-decision and
  READY trigger context. The combined lane smoke retained four sealed/finalized
  v3 records with balanced lifecycles and zero send failures, removing the
  accepted-v2 right-censoring blocker. The ten-sample observer result remains a
  warning: trace-on medians were `+7.673%` (`+0.120 ms`) at `n4/batch8` and
  `+2.015%` (`+1.419 ms`) at `n7/batch128`. Those absolute deltas are accepted
  as negligible for the matched multi-region objective, where both arms use the
  same tracing; the relative values remain visible.

  Issue #26's local gate passed complete normal/race suites, the lane smoke,
  and the ACS safety campaign. Matched persistent/lane diagnostics each
  retained 90/90 successful, consistent, deadline-met measured slots, 420
  finalized v3 traces including warmups, balanced lifecycles, and zero send
  failures. READY queue-wait p50 changed by
  `-24.3%/-36.1%/-16.7%` for batches `8/32/128`, while ACS p50 changed only
  `+0.016/+0.033/+0.043 ms`. This validates the local mechanism without
  demonstrating an ACS or WAN improvement and does not adopt the mode. Ignored
  artifacts are under `bloc-node/results/local/acs-persistent-lanes/` and
  `results/local/acs-common-subset-safety/acs-safety-20260906t09581788688726z/`.

  The user has now authorized the short matched n4 three-region control/treatment
  campaign: batches `8/32/128`, five warmups, 30 measured attempts, three
  balanced blocks, seed `20260621`, deadline `12s`, and sampler off. Preflight
  found that the EC2 wrapper and final recovered-artifact validator had not been
  extended for the already implemented v3 lane mode. The bounded correction now
  accepts persistent/v3 and persistent-lanes/v3, retains historical contracts,
  and requires sealed/finalized balanced v3 traces. The live control arm remains
  pending immutable source/image/bundle and authenticated empty-environment
  preflight; no campaign resources were allocated during the correction.

- **Issue #23 phase one is accepted; GossipSub phase two is deferred:** the final
  n4 four-arm campaign used source
  `7720b1f5bfce1997f611c1db95cead394b0349c4`, immutable BLOC image
  `sha256:87da6c9a447f73b8090e3b257dce94c357d64b97d644ea57e1150d4137426a34`,
  one identity/corpus bundle, batches `8/32/128`, five warmups, 30 measured
  repetitions, three balanced blocks, seed `20260621`, a 12-second deadline,
  and `bloc-acs-trace/v2`. Same-AZ fresh/persistent and three-region
  fresh/persistent each retained all 105 planned slots, 90/90 successful,
  consistent measured runs, and 420 node traces with zero ACS send failures.
  Fresh mode opened 21,174 same-AZ and 16,366 three-region measured logical
  streams; persistent mode recorded zero measured opens and 23,938/23,497
  same-AZ/three-region reuses. The matched analyzer accepted provenance and
  schedule identity in both topology pairs.

  Persistent streams reduced same-AZ ACS p50 from
  `17.534/27.715/63.439 ms` to `8.293/18.333/50.861 ms` for batches
  `8/32/128` (`-52.7%/-33.9%/-19.8%`). All three cells classify
  `acs-signal`; p50 intervals are non-overlapping, while the batch-128 p95
  intervals overlap. In three-region, p50 changed only from
  `233.209/237.621/515.948 ms` to `232.449/259.736/523.570 ms`
  (`-0.3%/+9.3%/+1.5%`), with overlapping p50 intervals in every cell. All
  three WAN cells and the cross-batch result classify
  `sender-finalization-only`: persistent mode removes fresh stream-finalize
  time, but does not produce an ACS latency signal. At batch 128 it also exposes
  head-of-line backpressure from the one-stream-per-peer writer: median
  per-node-trace queue wait rises to `545.796 ms`, versus
  `0.220/0.224 ms` at batches 8/32.

  Fresh logical-stream churn therefore contributes real same-AZ overhead but
  does not explain the cross-region increase. The WAN critical path remains
  RBC payload dissemination and successive RBC/BBA quorum dependencies:
  persistent first-RBC-output p50 is
  `72.434/144.454/428.540 ms`, and true-BBA-quorum p50 is
  `209.100/235.958/508.608 ms`. Direct persistent streams neither reduce the
  all-to-all message pattern nor remove those WAN-dependent rounds, and a
  single per-peer stream can serialize large concurrent messages. The locally
  validated experimental lane mode isolates that application-level mechanism;
  a separately authorized matched same-AZ/three-region campaign is still needed
  before any adoption decision. GossipSub remains a possible later
  dissemination experiment but is not an immediate action. No n7 or p99 claim
  is made from this 30-observation diagnostic.

  All four runs completed recovery and teardown. Terraform destroyed 15
  resources per same-AZ arm and 39 per three-region arm; retained and
  independent authenticated EC2/EBS/VPC/key/IAM checks are empty, with peering
  records visible only as terminal `deleted` tombstones. Retained roots are
  `results/ec2/bloc-ec2-i23-p1-sa-fr-c4/`,
  `results/ec2/bloc-ec2-i23-p1-sa-ps-c4/`,
  `results/ec2/bloc-ec2-i23-p1-tr-fr-c4/`, and
  `results/ec2/bloc-ec2-i23-p1-tr-ps-c4/`; ignored flattened analysis views
  and outputs are under
  `results/local/acs-latency-attribution/issue-23-7720b1f/aws-analysis/`.
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

1. Run the separately authorized matched same-AZ/three-region `persistent`
   versus `persistent-lanes` campaign under the finalized v3 trace contract.
   Keep current `persistent` as the control; do not claim WAN improvement or
   adopt the experimental lane mode before that evidence is accepted.
2. Defer the Merkle construction, GossipSub, alternate-RBC, and serialization
   proposals. Do not include them in the focused READY/stream-lane program.
3. Leave issue #15 open and paused for resource collection. Do not admit any
   resource-phase latency rows or rejected campaign attempts into the p99
   dataset.
4. Do not combine measurements from different source, image, corpus,
   configuration, or schema revisions into one final campaign.
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
