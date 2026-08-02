# Matched Final-Campaign Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the issue #15 same-AZ and issue #16 three-region metric runners
consume the same frozen BTE identities, encrypted corpora, source, images, and
measurement contract while changing only topology-specific placement.

**Architecture:** Generate one network-independent campaign identity and
encrypted corpus for each primary node count, bind them into a checksummed
bundle, and materialize addresses only after Terraform produces an inventory.
Thin topology entrypoints use one shared lifecycle that pulls two ECR images by
digest, stages the frozen corpus, runs one explicit measurement phase, retains
failures, and authenticates cleanup.

**Tech Stack:** Go 1.24, BTE/Kyber, libp2p identities, JSON, Python 3, portable
Bash 3.2, Docker Compose, Terraform, private Amazon ECR, EC2, and the existing
evaluator/resource sampler.

## Global Constraints

- Work only on `codex/issue-15-same-region-campaign`; preserve unrelated work.
- During implementation do not call AWS, run Terraform plan/apply, build or
  push images, contact ECR, push Git commits, or create billable resources.
- The old source `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d` and old image
  digest `ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`
  remain historical only.
- Final inputs accept only `bloc-cluster-v3`, `bte-tx-v2`,
  `bloc-encrypted-corpus-v1`, `mock-encrypted-corpus`, and full private-ECR
  `repository@sha256:<64 lowercase hex>` references.
- Primary bundles are exactly `n=4,t=3,BMax=128` and
  `n=7,t=5,BMax=128`, with coordinated indexes and exact prefixes 8/32/128.
- Same-AZ is `us-east-1a`; three-region is `us-east-1`, `eu-west-1`, and
  `eu-central-1`; every operator/controller is `t3.small`.
- Primary latency uses 10 warmups, 1,000 measurements per cell, 10 blocks,
  seed `20260621`, and the 12-second boundary.
- Resource collection uses fresh infrastructure, zero warmups, and 1,000
  attempts per primary cell. Its latency rows never enter p99 evidence.
- `n=10` and batch 512 remain disabled pending a recorded 30-observation pilot
  decision.
- Final runners never build, tag, push, SSH-load, substitute images, or retain
  infrastructure on failure.
- `--validate-only` must not invoke AWS, Terraform lifecycle commands, Docker
  registry operations, SSH, SCP, or rsync.
- Follow red-green-refactor for every behavior change.

---

## File Structure

- `bloc-node/internal/app/campaign_identity.go`: network-independent identity,
  generation, strict loading, and public/private matching.
- `bloc-node/internal/app/campaign_bundle.go`: bundle manifest, image refs,
  checksums, and verification.
- `bloc-node/internal/app/campaign_materialize.go`: inventory-driven public
  cluster and remote-evaluator output without rekeying.
- Matching `campaign_*_test.go` files: focused Go contract tests.
- `mempool-il/internal/mempool/encrypted_corpus.go`: accept campaign identity
  as an offline encryption input.
- `scripts/lib/final-campaign-contract.sh`: portable runner arguments, frozen
  schedules, and side-effect-free validation.
- `scripts/lib/final-campaign-lifecycle.sh`: common staging, execution,
  recovery, and cleanup sequencing.
- `deploy/ec2/final-topology-{same-az,three-region}.sh`: topology hooks only.
- `deploy/ec2/run-{final,same-az,three-region}-campaign.sh`: generic entrypoint
  and thin wrappers.
- `scripts/tests/test-final-campaign-{contract,lifecycle,terraform}.sh`: local
  behavior tests with external-boundary fakes.
- `scripts/lib/campaign_artifacts.py` and its tests: final phase and cleanup
  evidence validation.
- Both EC2 Terraform modules: pull-only IAM for two pre-existing ECR repos.

---

### Task 1: Generate A Network-Independent Campaign Identity

**Files:**
- Create: `bloc-node/internal/app/campaign_identity.go`
- Create: `bloc-node/internal/app/campaign_identity_test.go`
- Modify: `bloc-node/internal/app/app.go`

**Interfaces:**

```go
const campaignIdentityVersion = "bloc-campaign-identity-v1"

type campaignOperatorIdentity struct {
	ID uint64 `json:"id"`
	P2PPeerID string `json:"p2p_peer_id"`
}
type campaignIdentity struct {
	Version string `json:"version"`
	ClusterID string `json:"cluster_id"`
	N int `json:"n"`
	Threshold int `json:"threshold"`
	BMax int `json:"bmax"`
	CRSFile string `json:"crs_file"`
	CRSSHA256 string `json:"crs_sha256"`
	PublicKeyHex string `json:"public_key_hex"`
	Blockspace BlockspaceConfig `json:"blockspace"`
	Limits ResourceLimits `json:"limits"`
	Operators []campaignOperatorIdentity `json:"operators"`
}
type campaignIdentityOptions struct {
	ClusterID, IdentityOut, CRSOut, SecretsDir string
	N, Threshold, BMax int
	Blockspace BlockspaceConfig
	Limits ResourceLimits
}
func buildCampaignIdentity(campaignIdentityOptions) (campaignIdentity, []byte, []NodeSecretConfig, error)
func readCampaignIdentity(path string) (campaignIdentity, []byte, error)
func verifyCampaignSecrets(campaignIdentity, []byte, string) error
func genCampaignIdentity(args []string) error
```

- [ ] **Step 1: Write failing tests**

Test that an n4 identity has no address/region fields, uses t=3/BMax=128,
contains four unique peer IDs, writes public files 0644 and secrets 0600 under
a 0700 directory, refuses overwrite, and rejects a secret whose private key
derives a different peer ID or whose share set does not reconstruct the public
key.

```go
func TestBuildCampaignIdentityContainsNoDeploymentAddresses(t *testing.T) {
	identity, _, secrets, err := buildCampaignIdentity(campaignIdentityOptions{
		ClusterID: "final-n4", N: 4, Threshold: 3, BMax: 128,
		Limits: defaultResourceLimits(),
	})
	if err != nil { t.Fatal(err) }
	encoded, _ := json.Marshal(identity)
	for _, forbidden := range []string{"private_ip", "region", "http_addr", "p2p_addr"} {
		if bytes.Contains(encoded, []byte(forbidden)) { t.Fatalf("contains %q", forbidden) }
	}
	if len(identity.Operators) != 4 || len(secrets) != 4 { t.Fatal("wrong operators") }
}
```

- [ ] **Step 2: Verify red**

```sh
cd bloc-node
go test ./internal/app -run 'Test(Build|Read|Verify|Gen)CampaignIdentity'
```

Expected: compile failure because the API does not exist.

- [ ] **Step 3: Implement minimal identity behavior**

Extract only CRS, BTE key/share, and libp2p identity generation from the
current config generator. Validate n/t/BMax, limits, CRS hash, unique
consecutive IDs, strict JSON, file modes, and no overwrite. In verification,
derive peer IDs from private keys and reconstruct/compare the BTE public key.

- [ ] **Step 4: Register the CLI**

Add `gen-campaign-identity` to `app.Run` and usage. Require `--cluster-id`,
`--nodes`, `--threshold`, `--bmax`, `--identity-out`, `--crs-out`, and
`--secrets-dir`.

- [ ] **Step 5: Verify green and commit**

```sh
cd bloc-node && go test ./internal/app -run 'Test(Build|Read|Verify|Gen)CampaignIdentity' && go test ./...
git add bloc-node/internal/app/app.go bloc-node/internal/app/campaign_identity.go bloc-node/internal/app/campaign_identity_test.go
git commit -m "feat: generate frozen campaign identities"
```

### Task 2: Encrypt The Static Corpus From A Campaign Identity

**Files:**
- Modify: `mempool-il/internal/mempool/encrypted_corpus.go`
- Modify: `mempool-il/internal/mempool/encrypted_corpus_test.go`
- Modify: `mempool-il/cmd/encrypt-corpus/main.go`

**Interfaces:**

```go
type EncryptedCorpusOptions struct {
	PlaintextPath, ClusterConfigPath, CampaignIdentityPath string
	SecretPaths []string
	Limit int
	OutputPath string
}
```

Exactly one of `ClusterConfigPath` and `CampaignIdentityPath` is accepted.

- [ ] **Step 1: Write failing tests**

Generate 128 encrypted entries from an n4 identity and three secrets. Assert
the existing schema, BMax, all 8/32/128 IDs, proof/self-decrypt success, and
target hashes. Reject both config inputs, neither input, wrong identity version,
bad CRS hash, wrong public key, wrong cluster ID, and insufficient shares.

- [ ] **Step 2: Verify red**

```sh
cd mempool-il
go test ./internal/mempool -run 'TestGenerateEncryptedCorpus(FromCampaignIdentity|RejectsCampaignIdentity|RejectsTwoConfigurationInputs)'
```

- [ ] **Step 3: Implement one shared encryption path**

Strictly load `bloc-campaign-identity-v1`, resolve/verify its CRS, and convert
it into the existing internal replay-cluster shape. Reuse the existing
encryption, proof validation, threshold self-decryption, Ethereum comparison,
prefix computation, and atomic writer.

- [ ] **Step 4: Expose the flag and verify green**

Add `--campaign-identity`; pass it through the options struct.

```sh
cd mempool-il && go test ./internal/mempool -run 'TestGenerateEncryptedCorpus' && go test ./...
git add mempool-il/cmd/encrypt-corpus/main.go mempool-il/internal/mempool/encrypted_corpus.go mempool-il/internal/mempool/encrypted_corpus_test.go
git commit -m "feat: encrypt corpora from campaign identities"
```

### Task 3: Bind And Verify A Frozen Campaign Bundle

**Files:**
- Create: `bloc-node/internal/app/campaign_bundle.go`
- Create: `bloc-node/internal/app/campaign_bundle_test.go`
- Modify: `bloc-node/internal/app/app.go`

**Interfaces:**

```go
const campaignBundleVersion = "bloc-campaign-bundle-v1"
type campaignBundleManifest struct {
	Version, SourceSHA, BlocImage, MempoolImage string
	ClusterConfigVersion, CiphertextWireVersion string
	N, Threshold, BMax int
	PublicConfigID, PlaintextMasterCorpusID string
	PlaintextPrefixSetIDs map[string]string
	EncryptedCorpusID string
	EncryptedPrefixSetIDs map[string]string
	IndexAssignment string
	FileSHA256 map[string]string
}
type campaignBundle struct {
	Root, IdentityPath, CRSPath, CorpusPath, SecretDir string
	Identity campaignIdentity
	Corpus corpusProvenance
	Manifest campaignBundleManifest
}
func loadCampaignBundle(root string) (campaignBundle, error)
func buildCampaignBundleManifest(root, sourceSHA, blocImage, mempoolImage string) (campaignBundleManifest, error)
func verifyCampaignBundle(args []string) error
```

- [ ] **Step 1: Write failing mutation tests**

Build one valid fixture, then independently change source SHA, image reference,
CRS bytes, public key, corpus public ID, each prefix ID, index assignment,
threshold, and one share. Every copy must reject. Explicitly reject
`repo:latest`, non-ECR registries, uppercase/malformed digests, absolute paths,
and symlinks escaping the root.

- [ ] **Step 2: Verify red**

```sh
cd bloc-node
go test ./internal/app -run 'Test(Build|Load|Verify)CampaignBundle'
```

- [ ] **Step 3: Implement exact primary validation**

Require n4/t3 or n7/t5, BMax/availability 128, exact 8/32/128 prefix keys,
`coordinated-position-v1`, matching public IDs, and valid private secrets. Hash
only `cluster-identity.json`, `cluster.crs`, and `encrypted-corpus.json`; never
serialize secret paths or hashes.

- [ ] **Step 4: Add write/verify CLI**

```text
bloc-node verify-campaign-bundle --bundle-root DIR
  [--source-sha SHA --bloc-image ECR@DIGEST --mempool-image ECR@DIGEST --write-manifest]
```

Without `--write-manifest`, require an existing manifest. With it, require all
frozen inputs and refuse overwrite.

- [ ] **Step 5: Verify green and commit**

```sh
cd bloc-node && go test ./internal/app -run 'Test(Build|Load|Verify)CampaignBundle' && go test ./...
git add bloc-node/internal/app/app.go bloc-node/internal/app/campaign_bundle.go bloc-node/internal/app/campaign_bundle_test.go
git commit -m "feat: verify frozen campaign bundles"
```

### Task 4: Materialize Topology Config Without Rekeying

**Files:**
- Create: `bloc-node/internal/app/campaign_materialize.go`
- Create: `bloc-node/internal/app/campaign_materialize_test.go`
- Modify: `bloc-node/internal/app/app.go`
- Reuse: `bloc-node/internal/app/ec2_config.go`

**Interfaces:**

```go
type campaignMaterializeOptions struct {
	BundleRoot, InventoryPath, ClusterOut, CRSOut, RemoteEvalOut string
	Topology, MempoolURL, PrometheusURL, GrafanaURL, ControllerURL string
	HTTPPort, P2PPort int
	HTTPHostMode, P2PHostMode string
}
func buildMaterializedCampaignConfigs(campaignBundle, ec2Inventory, campaignMaterializeOptions) (ConfigFile, []byte, remoteEvalConfig, error)
func materializeCampaignConfig(args []string) error
```

- [ ] **Step 1: Write the failing matched-topology test**

Materialize the same n4 fixture against hand-written same-AZ and three-region
inventories. Assert identical public key, CRS hash, provider IDs, peer IDs,
limits, and remote corpus IDs, but different advertise addresses/placement.
Reject missing/duplicate IDs, wrong n, wrong instance type, wrong same-AZ, and
wrong modulo-three placement.

- [ ] **Step 2: Verify red**

```sh
cd bloc-node && go test ./internal/app -run 'TestMaterializeCampaignConfig'
```

- [ ] **Step 3: Implement materialization**

Copy crypto, peer IDs, limits, and provider provenance from the bundle. Derive
only addresses and deployment labels from inventory. Set the provider to local
`http://mempool-il:8080`, exact-count mode, and all expected corpus/prefix IDs.
Validate `T0-same-az` and `T2-three-region` exactly as defined globally.

- [ ] **Step 4: Register atomic CLI output**

Require `--bundle-root`, `--inventory`, `--topology`, `--cluster-out`,
`--crs-out`, and `--remote-eval-out`; refuse overwrite and never copy secrets.

- [ ] **Step 5: Verify green and commit**

```sh
cd bloc-node && go test ./internal/app -run 'TestMaterializeCampaignConfig' && go test ./...
git add bloc-node/internal/app/app.go bloc-node/internal/app/campaign_materialize.go bloc-node/internal/app/campaign_materialize_test.go
git commit -m "feat: materialize matched campaign topologies"
```

### Task 5: Define Final Runner Arguments And Phase Gates

**Files:**
- Create: `scripts/lib/final-campaign-contract.sh`
- Create: `scripts/tests/test-final-campaign-contract.sh`
- Replace: `deploy/ec2/run-final-campaign.sh`
- Replace: `deploy/ec2/run-same-az-campaign.sh`
- Create: `deploy/ec2/run-three-region-campaign.sh`
- Modify: `scripts/test-campaign-runners.sh`

**Interface:**

```text
run-final-campaign.sh --topology same-az|three-region
  --phase readiness-pilot|latency|resource|extension-pilot
  --bundle-root DIR --node-count 4|7 --source-sha SHA
  --bloc-image ECR@DIGEST --mempool-image ECR@DIGEST
  --experiment-id ID --admin-cidr CIDR --aws-profile PROFILE
  [--validate-only | --execute-live]
```

- [ ] **Step 1: Write failing executable tests**

Invoke real scripts against temp fixtures. Cover valid n4/n7, source mismatch,
image mismatch, mutable images, pilot n7, non-primary n, schedule overrides,
extension without authorization, neither execution switch, both switches,
unknown flags, missing values, and wrapper topology injection. Put fake
`aws/terraform/docker/ssh/scp/rsync` executables on PATH; successful validation
must leave their call log empty.

- [ ] **Step 2: Verify red**

```sh
bash scripts/tests/test-final-campaign-contract.sh
```

- [ ] **Step 3: Implement portable schedules**

Use Bash 3.2 only. Freeze:

```text
readiness-pilot n4: warmups=1, repetitions=3, blocks=1, sampler=off
latency n4|n7:      warmups=10, repetitions=1000, blocks=10, sampler=off
resource n4|n7:     warmups=0, repetitions=1000, blocks=10, sampler=on
extension-pilot:    fail closed
```

All use batches 8/32/128, seed 20260621, deadline 12s. Require local HEAD and
bundle source/images to equal supplied values.

- [ ] **Step 4: Implement thin wrappers**

Each wrapper execs the generic runner with only its topology injected.

- [ ] **Step 5: Verify and commit**

```sh
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/test-campaign-runners.sh
git add scripts/lib/final-campaign-contract.sh scripts/tests/test-final-campaign-contract.sh scripts/test-campaign-runners.sh deploy/ec2/run-final-campaign.sh deploy/ec2/run-same-az-campaign.sh deploy/ec2/run-three-region-campaign.sh
git commit -m "feat: enforce final campaign phase contracts"
```

### Task 6: Use Pre-Existing ECR With Pull-Only IAM

**Files:**
- Modify: `deploy/ec2/terraform/{main,variables,outputs}.tf`
- Modify: `deploy/ec2/terraform-three-region/{main,variables,outputs}.tf`
- Create: `scripts/tests/test-final-campaign-terraform.sh`

**Interface:** both modules require two `us-east-1` ECR repository ARNs.
Token permission is `*`; layer/check/get permissions are scoped to those ARNs.
No ECR repository resource or write action remains.

- [ ] **Step 1: Write the failing semantic test**

Run `terraform fmt -check -diff` and `terraform validate`; assert the required
list variable exists, repository outputs/resources are absent, and IAM action
literals contain only `GetAuthorizationToken`, `BatchCheckLayerAvailability`,
`BatchGetImage`, and `GetDownloadUrlForLayer`.

- [ ] **Step 2: Verify red**

```sh
bash scripts/tests/test-final-campaign-terraform.sh
```

Expected: current repository creation and write-capable three-region policy
fail the test.

- [ ] **Step 3: Implement minimal Terraform changes**

Remove ECR resources/creation variables/outputs. Replace broad managed ECR IAM
with explicit token/pull policy. Keep the campaign-created role/profile because
instances need credentials and cleanup owns them.

- [ ] **Step 4: Validate without planning and commit**

```sh
terraform -chdir=deploy/ec2/terraform fmt -check -diff
terraform -chdir=deploy/ec2/terraform validate
terraform -chdir=deploy/ec2/terraform-three-region fmt -check -diff
terraform -chdir=deploy/ec2/terraform-three-region validate
bash scripts/tests/test-final-campaign-terraform.sh
git add deploy/ec2/terraform deploy/ec2/terraform-three-region scripts/tests/test-final-campaign-terraform.sh
git commit -m "refactor: pull frozen images from shared ECR"
```

### Task 7: Implement Shared Staging, Health, And Measurement Lifecycle

**Files:**
- Create: `scripts/lib/final-campaign-lifecycle.sh`
- Create: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `deploy/ec2/run-final-campaign.sh`
- Modify: `deploy/ec2/operator-compose.yaml`

**Adapter hooks:**

```bash
final_topology_prepare "$artifact_root" "$node_count"
final_topology_apply "$artifact_root"        # writes inventory.json
final_topology_key_for_host "$host_json"      # prints key path
final_topology_destroy "$artifact_root"
final_topology_verify_absent "$artifact_root" # writes cleanup-topology.json
```

- [ ] **Step 1: Write failure-injection tests**

Source the real library and fake only external boundaries. Use a temp remote
filesystem so SCP assertions inspect actual files. Prove success ordering;
corpus checksum failure before start; image digest/architecture failure before
start; health/evaluator failure still recovers/destroys/verifies; no sampler in
pilot/latency; sampler only in resource; cleanup failure invalidates; no secret
under the public artifact root.

- [ ] **Step 2: Verify red**

```sh
bash scripts/tests/test-final-campaign-lifecycle.sh
```

- [ ] **Step 3: Implement exact staging and image pulls**

Stage public config, CRS, corpus, one matching 0600 secret, operator Compose,
and sampler script. Verify checksums. Log in to the region encoded in the ECR
ref, pull exact refs, verify RepoDigests and `amd64`, and start with only
`NODE_ID`, `BLOC_IMAGE`, and `MEMPOOL_IMAGE`.

- [ ] **Step 4: Implement controller/health gates**

Stage remote evaluator and Prometheus config, pull the exact bloc image, then
require node health/metrics, local mempool health, one exact corpus-prefix
probe, and all Prometheus targets up.

- [ ] **Step 5: Implement measurement paths**

For latency, reuse deterministic balanced orders; each of 10 blocks contains
all batches once. Warm each cell only at first occurrence. Invoke `eval-remote`
with `mock-encrypted-corpus`, 12s timeout, exact source/image/corpus provenance,
and planned count 1000. Recover/validate each block without requiring success.

For resource, start the existing 250 ms sampler on every node, run zero-warmup
1,000-attempt cells, stop/recover with existing cadence/coverage/OOM gates, and
classify evaluator latency as resource-only.

- [ ] **Step 6: Verify and commit**

```sh
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/test-campaign-runners.sh
git add scripts/lib/final-campaign-lifecycle.sh scripts/tests/test-final-campaign-lifecycle.sh deploy/ec2/run-final-campaign.sh deploy/ec2/operator-compose.yaml
git commit -m "feat: stage and execute frozen campaign workloads"
```

### Task 8: Validate Final Artifacts And Cleanup

**Files:**
- Modify: `scripts/lib/campaign_artifacts.py`
- Modify: `scripts/tests/test_campaign_artifacts.py`
- Modify: `scripts/lib/final-campaign-lifecycle.sh`

**Interfaces:**

```text
assert-final-phase --phase-root PATH --expected-topology T --expected-phase P
assert-final-cleanup --cleanup PATH --regions CSV
```

- [ ] **Step 1: Write failing fixtures**

Create literal valid latency/resource fixtures. Mutate source/image/bundle,
attempt count, requested/received count, prefix ID, phase classification,
failed row retention, secret leakage, placement, checksum, cleanup region,
each leftover resource category, and Terraform state. Each mutation must fail
for its named reason.

- [ ] **Step 2: Verify red**

```sh
python3 -m unittest scripts.tests.test_campaign_artifacts.FinalCampaignArtifactTests
```

- [ ] **Step 3: Implement new-schema validation**

Keep historical schemas unchanged. Require complete measured schedules,
including failures/timeouts; only successful consistent rows feed latency.
Require 1,000 primary or three pilot attempts per cell, exact block coverage,
corpus counts/IDs, placement, and phase separation.

- [ ] **Step 4: Implement cleanup validation**

Every recorded region/category must exist and contain an empty list; every
query must report success; Terraform state must be empty. Missing is not empty.

- [ ] **Step 5: Integrate, verify, and commit**

```sh
python3 -m unittest scripts.tests.test_campaign_artifacts
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/test-campaign-runners.sh
git add scripts/lib/campaign_artifacts.py scripts/tests/test_campaign_artifacts.py scripts/lib/final-campaign-lifecycle.sh
git commit -m "feat: validate final campaign evidence and cleanup"
```

### Task 9: Implement The Same-AZ Adapter

**Files:**
- Create: `deploy/ec2/final-topology-same-az.sh`
- Modify: `deploy/ec2/run-same-az-campaign.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`

- [ ] **Step 1: Write the failing adapter test**

Use literal n4/n7 Terraform-output fixtures. Assert generated tfvars select
`us-east-1a`, t3.small, unlimited credits, encrypted/delete-on-termination
volumes, IMDSv2, and two ECR ARNs. Assert normalized inventory materializes.
Inject a leftover tagged volume after fake destroy and require failure with its
ID recorded.

- [ ] **Step 2: Verify red**

```sh
bash scripts/tests/test-final-campaign-lifecycle.sh same-az
```

- [ ] **Step 3: Implement hooks**

Copy the single-region module to generated work, write explicit tfvars, record
commands, write inventory after live apply, select the one SSH key, run bounded
destroy, and query recorded/tagged instances, volumes, VPC/network resources,
key, role, profile, and empty state in us-east-1. Never query/delete shared ECR.

- [ ] **Step 4: Verify and commit**

```sh
bash scripts/tests/test-final-campaign-lifecycle.sh same-az
bash scripts/test-campaign-runners.sh
git add deploy/ec2/final-topology-same-az.sh deploy/ec2/run-same-az-campaign.sh scripts/tests/test-final-campaign-lifecycle.sh
git commit -m "feat: add matched same-AZ campaign topology"
```

### Task 10: Implement The Three-Region Adapter

**Files:**
- Create: `deploy/ec2/final-topology-three-region.sh`
- Modify: `deploy/ec2/run-three-region-campaign.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`

- [ ] **Step 1: Write failing placement/cleanup tests**

With literal n4/n7 fixtures assert `id%3` maps to US/Ireland/Frankfurt,
controller is US, all instances are t3.small, three peerings/six routes exist,
and the same US ECR ARNs are used. Inject leftovers in each region and require
exact regional attribution.

- [ ] **Step 2: Verify red**

```sh
bash scripts/tests/test-final-campaign-lifecycle.sh three-region
```

- [ ] **Step 3: Implement hooks**

Port only topology behavior from the historical three-region runner: workdir,
regional providers/keys, peering, inventory normalization, key selection,
bounded destroy, and three-region absence checks. Do not port build/push,
synthetic source, runtime rekey/corpus generation, or combined phases.

- [ ] **Step 4: Verify and commit**

```sh
bash scripts/tests/test-final-campaign-lifecycle.sh three-region
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/test-campaign-runners.sh
git add deploy/ec2/final-topology-three-region.sh deploy/ec2/run-three-region-campaign.sh scripts/tests/test-final-campaign-lifecycle.sh
git commit -m "feat: add matched three-region campaign topology"
```

### Task 11: Full Local Validation And Replacement Preflight

**Files:** Generated outputs only under ignored `results/local/` and a private
mode-0700 ignored bundle directory.

- [ ] **Step 1: Run focused and full suites**

```sh
cd bte/btd-impl-main && go test ./...
cd mempool-il && go test ./...
cd bloc-node && go test ./...
cd sbc/hbbft && go test ./...
cd latency-charts && python3 -m pytest
python3 -m unittest scripts.tests.test_campaign_artifacts
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-terraform.sh
bash scripts/test-campaign-runners.sh
```

- [ ] **Step 2: Run focused race suites**

```sh
cd bte/btd-impl-main && go test -race ./be
cd mempool-il && go test -race ./internal/mempool ./internal/api
cd bloc-node && go test -race ./internal/app
```

- [ ] **Step 3: Resolve Compose without pulling**

Use syntactically valid fixture digests with `docker compose ... config`; do
not pull or build.

- [ ] **Step 4: Generate real n4/n7 crypto/corpus inputs**

After implementation commits stabilize, generate identities and 128-entry
corpora from the committed master corpus under ignored private storage. Run
bundle/corpus checks without writing final manifests because real ECR digests
are not authorized or available.

- [ ] **Step 5: Exercise every validate-only contract**

Use temporary bundle manifests containing syntactically valid but unpublished
ECR digest references to exercise same-AZ/three-region pilot, n4/n7 latency,
and n4/n7 resource validation. Record
`registry_images_verified=false` only in the separate local-preflight summary,
not in `bloc-campaign-bundle-v1`. Verify all fake lifecycle call logs remain
empty. Do not retain these manifests as frozen inputs or claim AWS
readiness/performance.

- [ ] **Step 6: Retain local logs**

Write ignored logs below
`results/local/final-campaign-readiness-<source-sha>/validation/`, explicitly
separating implemented runner readiness from unresolved ECR publication.

### Task 12: Update Canonical Documentation And Issue #15

**Files:**
- Modify: `deploy/ec2/README.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/STATUS.md`
- Modify: `docs/DECISIONS.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `bloc-node/README.md`
- Modify: `mempool-il/README.md`
- Modify: `docs/superpowers/plans/2026-07-28-issue-15-same-region-campaign.md`

- [ ] **Step 1: Document exact commands and invariants**

Document identity generation, identity-based encryption, bundle verification,
materialization, both validate-only runners, ECR digest-only behavior, matched
identity invariants, separate phases, failure retention, and cleanup.

- [ ] **Step 2: Update status precisely**

Remove the tooling blocker only if Tasks 1-11 pass. Retain this external gate:
publish/inspect two linux/amd64 ECR images, bind real refs into n4/n7 manifests,
re-run validation, confirm account capacity, and separately authorize n4 pilot.
Do not claim accepted AWS evidence.

- [ ] **Step 3: Validate docs and commit**

```sh
git diff --check
rg -n 'm3-(same-az|three-region)-synthetic' docs/STATUS.md docs/VALIDATION.md deploy/ec2/README.md
git status --short
git add deploy/ec2/README.md docs/VALIDATION.md docs/STATUS.md docs/DECISIONS.md docs/CHANGELOG.md bloc-node/README.md mempool-il/README.md docs/superpowers/plans/2026-07-28-issue-15-same-region-campaign.md
git commit -m "docs: document matched final campaign operation"
```

Review each remaining synthetic reference and retain only historical ones.

- [ ] **Step 4: Post the local implementation checkpoint**

Using authenticated `gh`, comment on issue #15 with branch, source commit,
validation, unresolved ECR/live-pilot gate, and confirmation that no AWS action
or push occurred. Do not close #15 or move #16 from Backlog.

### Task 13: Final Verification And Freeze Handoff

**Files:** None unless verification reveals a task-owned defect.

- [ ] **Step 1: Re-run completion gates from a clean tree**

```sh
git status --short
git diff --check
cd bte/btd-impl-main && go test ./...
cd mempool-il && go test ./...
cd bloc-node && go test ./...
cd sbc/hbbft && go test ./...
cd latency-charts && python3 -m pytest
bash scripts/test-campaign-runners.sh
bash scripts/tests/test-final-campaign-terraform.sh
```

- [ ] **Step 2: Check current remote ancestry without pushing**

```sh
git fetch origin --prune
git merge-base --is-ancestor origin/main HEAD
git rev-list --left-right --count origin/main...HEAD
git status --short --branch
```

- [ ] **Step 3: Produce the authorization handoff**

Report branch/commit, validation, local public bundle identities without secret
contents, exact separately authorized image publication/freeze commands without
executing them, exact validate-only and first live n4 commands, unresolved real
ECR gate, `STATUS.md` outcome, and confirmation that nothing was pushed and no
AWS resources were created.
