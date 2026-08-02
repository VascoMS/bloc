# Matched Final-Campaign Readiness Design

## Objective

Make the issue #15 same-AZ and issue #16 three-region protocol-metric
campaigns executable from one frozen replacement candidate without changing
the measured cryptographic workload between topologies.

This design completes the missing local campaign tooling. It does not authorize
AWS API calls, Terraform plan/apply, ECR publication, image builds, image
pushes, or billable resources. Those actions remain separate operator-approved
checkpoints.

## Scope

The implementation will provide:

- one frozen, network-independent BTE identity and encrypted corpus for
  `n=4,t=3,BMax=128`;
- one frozen, network-independent BTE identity and encrypted corpus for
  `n=7,t=5,BMax=128`;
- topology-specific materialization that preserves those identities while
  changing only deployment addresses and placement metadata;
- digest-only private-ECR image distribution for `bloc-node` and `mempool-il`;
- a shared final-campaign lifecycle used by thin same-AZ and three-region
  entrypoints;
- explicit readiness-pilot, primary-latency, and separate resource phases;
- complete measurement retention, artifact recovery, and authenticated
  cleanup; and
- side-effect-free local validation of every frozen input and live-runner
  contract.

The implementation will not add an estimator, pricing model, cost-report
artifact, or general-purpose deployment framework. It will not add a legacy
synthetic fallback. `n=10` and batch 512 remain a later extension-pilot
decision.

## Frozen Campaign Identities

The replacement candidate has four identity layers:

1. one clean source commit;
2. one `linux/amd64` `bloc-node` ECR manifest digest and one `linux/amd64`
   `mempool-il` ECR manifest digest built from that source;
3. one frozen campaign bundle for each primary node count; and
4. one topology overlay produced from an AWS inventory for each deployment.

Layers 1-3 remain identical between the same-AZ and three-region measurements.
Only layer 4 changes.

The source commit and both image references are campaign-wide. The BTE setup,
operator identities, and encrypted corpus are node-count-specific because
`n=4,t=3` and `n=7,t=5` use different threshold configurations.

## Network-Independent Campaign Bundle

Each bundle uses schema `bloc-campaign-bundle-v1` and contains:

```text
<bundle-root>/
├── bundle-manifest.json
├── cluster-identity.json
├── cluster.crs
├── encrypted-corpus.json
└── secrets/
    ├── operator-0.json
    └── ...
```

`cluster-identity.json` uses schema `bloc-campaign-identity-v1`. It contains:

- cluster ID;
- `n`, threshold, and `BMax`;
- CRS filename and SHA-256;
- BTE public key;
- blockspace and resource limits;
- operator IDs and libp2p peer IDs; and
- no IP address, DNS name, region, availability zone, controller location, or
  mempool endpoint.

The existing `bloc-node-secret-v1` operator files retain the matching BTE
share and libp2p private key. They are created under a mode-0700 directory and
written with mode 0600. They are never committed, copied into evidence roots,
uploaded to GitHub, or included in public checksum manifests.

`encrypted-corpus.json` remains `bloc-encrypted-corpus-v1`. It contains 128
ordered `bte-tx-v2` ciphertexts produced once from the committed 512-entry
plaintext master corpus. Its nested prefixes are exactly 8, 32, and 128
transactions. The plaintext master is an offline generator input and is not
installed on EC2 hosts.

`bundle-manifest.json` binds:

- schema version;
- frozen source SHA;
- complete digest-addressed ECR references for both images;
- `bloc-cluster-v3` and `bte-tx-v2` schema identities;
- `n`, threshold, and `BMax`;
- public configuration ID;
- plaintext master and plaintext-prefix IDs;
- encrypted corpus and encrypted-prefix IDs;
- `index_assignment=coordinated-position-v1`; and
- SHA-256 checksums of the identity, CRS, and encrypted-corpus files.

The manifest deliberately excludes secret contents and secret hashes. Bundle
validation reads the private files locally to prove that every operator ID,
cluster ID, share, and libp2p private key matches the public identity.

## Bundle Commands

The node command surface will add:

```text
bloc-node gen-campaign-identity
bloc-node materialize-campaign-config
bloc-node verify-campaign-bundle
```

`gen-campaign-identity` generates the network-independent identity, CRS, and
private operator files. It refuses to overwrite any output.

The existing offline generator will accept the network-independent identity:

```text
mempool-il encrypt-corpus --campaign-identity <path> ...
```

It keeps its proof verification, threshold self-decryption, Ethereum decoding,
transaction-hash comparison, prefix validation, and atomic output behavior.

`verify-campaign-bundle` accepts the identity, CRS, corpus, secret directory,
source SHA, and two image digest references. It validates their cross-file
contract and writes `bundle-manifest.json` atomically only after all checks
pass. A separate verification invocation validates an existing manifest
without modifying it.

`materialize-campaign-config` accepts one verified bundle plus a Terraform
inventory and writes:

- topology-specific `bloc-cluster-v3` public configuration;
- topology-specific `remote-eval` configuration; and
- Prometheus target metadata through the existing deployment helper.

Materialization copies the frozen BTE public key, CRS identity, limits,
operator peer IDs, provider corpus identities, and exact-count policy without
regenerating them. It derives only listen/advertise addresses, evaluator URLs,
regions, zones, controller attribution, and deployment labels from inventory.
It fails if inventory node IDs are missing, duplicated, or incompatible with
the bundle.

## Matched-Topology Invariant

Materializing the same bundle against a same-AZ inventory and a three-region
inventory must produce configurations with identical:

- source and image identities;
- BTE public configuration ID;
- CRS and public key;
- operator IDs and libp2p peer IDs;
- BTE shares and libp2p private keys;
- encrypted corpus and prefix IDs;
- exact ciphertext bytes and ordering;
- blockspace and resource limits; and
- transaction source, schema, schedule, and timeout contract.

Only peer addresses, evaluator URLs, region/AZ placement, topology label,
controller location, and experiment ID may differ. The same-AZ campaign is
completed and cleaned before the three-region campaign begins, so the same
prototype secret material is not intentionally active in both topologies at
once.

## ECR Image Distribution

Private ECR is the sole final-campaign image source. Image building and
publication are a separate, explicitly authorized freeze action outside the
campaign runner.

The bundle manifest records full references of the form:

```text
<account>.dkr.ecr.us-east-1.amazonaws.com/<repository>@sha256:<digest>
```

The campaign runner never builds, tags, pushes, substitutes, or falls back to
an SSH-loaded image. Operator hosts pull both images. The controller pulls only
the `bloc-node` image for `eval-remote`. Each host verifies the requested repo
digest and `linux/amd64` architecture before use.

Instances receive only the private-ECR read permissions needed to obtain an
authorization token and pull layers. A single `us-east-1` registry origin is
used by all three regions so every host consumes the same manifests. The ECR
repositories pre-exist the campaign and are not campaign cleanup targets.

## Corpus Staging And Measured Data Flow

Before starting containers, the runner copies the node-count-specific
`encrypted-corpus.json` to every operator host and verifies its SHA-256 against
the bundle manifest. The file is mounted read-only into the host-local
`mempool-il` container.

The corpus is staged once per deployment. It is not copied or regenerated per
slot. During measurement each node requests the exact prefix through its local
sidecar:

```text
batch 8   -> first 8 encrypted candidates
batch 32  -> first 32 encrypted candidates
batch 128 -> all 128 encrypted candidates
```

Every operator receives byte-identical candidates and ordering. No campaign
encryption or plaintext-corpus loading occurs in the measured interval. The
local mempool HTTP request and subsequent encrypted proposal processing remain
part of the implemented protocol path.

## Shared Runner Architecture

Thin entrypoints select topology:

```text
deploy/ec2/run-same-az-campaign.sh
deploy/ec2/run-three-region-campaign.sh
```

Both use one shared final-campaign lifecycle. The lifecycle owns:

- CLI and frozen-input validation;
- bundle and schedule validation;
- Terraform lifecycle sequencing;
- topology materialization;
- ECR digest pulls and verification;
- corpus/config/secret staging;
- sidecar/controller startup and health gates;
- evaluation scheduling;
- artifact collection and validation;
- failure retention; and
- cleanup and cleanup verification.

Topology adapters own only Terraform directory selection, regional placement,
inventory normalization, SSH-key selection, and scoped cleanup queries.

The same-AZ adapter fixes all operators and the controller to `us-east-1a` on
`t3.small`. The three-region adapter retains `us-east-1`, `eu-west-1`, and
`eu-central-1`, controller placement in the US, and operator assignment by
`node_id % 3`, also on `t3.small`.

The legacy synthetic same-AZ, cross-AZ, and three-region entrypoints remain
historical utilities and are not valid final-campaign inputs. They must not be
called by either new runner.

## Explicit Campaign Phases

The live runner accepts exactly one phase per invocation:

- `readiness-pilot`: `n=4,t=3`; batches 8/32/128; one warmup and three measured
  attempts per cell; no resource sampler;
- `latency`: one of `n=4,t=3` or `n=7,t=5`; batches 8/32/128; 10 warmups and
  1,000 measured attempts per cell; 10 balanced blocks; seed `20260621`;
  12-second completion boundary; resource sampler disabled;
- `resource`: fresh infrastructure for one primary node count and the same
  three cells; zero warmups and 1,000 measured attempts per cell so the
  existing issue #15/#16 resource-evidence contract is not weakened; no
  latency observation from this invocation can enter the p99 dataset; and
- `extension-pilot`: unavailable until a later recorded decision authorizes
  the 30-observation `n=10` or batch-512 extension.

The primary operational sequence is pilot, same-AZ n4 latency, same-AZ n7
latency, separate same-AZ resources, issue #15 acceptance, then the matched
three-region invocations for issue #16. Each `n` deployment is recovered,
validated, and cleaned before the next begins.

`--validate-only` performs local CLI, source, bundle, image-reference,
topology, and schedule checks. It must not invoke AWS, Terraform lifecycle
commands, Docker registry operations, SSH, SCP, or rsync. There is no
`estimate-only` mode and no pricing or cost-report contract.

A live invocation still requires a separate explicit operator authorization.
The runner requires a deliberate live-execution switch so an omitted
`--validate-only` does not allocate infrastructure accidentally.

## Artifacts And Secret Boundary

Each invocation writes an independent ignored artifact root:

```text
results/ec2/<campaign-id>/
├── frozen-inputs.json
├── lifecycle.jsonl
├── commands.txt
├── generated-public/
├── scenarios/
├── logs/
├── checksums.sha256
├── cleanup-verification.json
└── manifest.json
```

The artifact records source, both image digests, bundle and corpus identities,
topology, placement, phase, schedule, every attempted outcome, public generated
configuration, evaluator rows, resource rows when applicable, logs, artifact
checksums, and cleanup results.

Secrets are staged from the private bundle directory directly to the matching
operator and are not copied through the public artifact root. Remote secret
files use owner `10001:10001` and mode 0600. The local private bundle remains
outside `results/ec2/<campaign-id>`.

Latency and resource invocations use distinct campaign IDs, phase labels, and
manifests. Artifact merging rejects mixed phase labels, source/image/bundle
identities, topology identities, or schema versions.

## Failure Retention And Cleanup

Every measured attempt is retained as completed, failed, or timed out. Failed
or inconsistent measurements are not silently retried or replaced. A runner
may retry a bounded infrastructure operation, but it records each lifecycle
attempt separately from protocol observations.

On failure, the exit path:

1. records the failed lifecycle step and exit status;
2. attempts bounded recovery of partial evaluator output, node/mempool logs,
   sampler output, inventory, and public configuration;
3. stops active samplers and containers where reachable;
4. runs bounded Terraform destroy retries;
5. removes only campaign-created keys and scoped IAM/network resources; and
6. performs authenticated absence checks in every recorded region.

Final runners do not support preserving resources on failure. Cleanup evidence
must cover recorded instances, volumes, VPCs, subnets, security groups,
peerings, temporary keys, roles, and instance profiles, plus an empty Terraform
state. Incomplete artifact recovery is recorded; incomplete authenticated
cleanup makes the campaign invalid.

## Validation Strategy

All behavior changes follow test-first development. Required checkpoints are:

1. identity and bundle schema tests, including overwrite and corruption
   rejection;
2. private-secret/public-identity matching tests;
3. encrypted-corpus generation from the network-independent identity;
4. same-bundle materialization against same-AZ and three-region fixtures,
   proving frozen identities and ciphertext bytes do not change;
5. rejection of inventory, source, image, checksum, schema, corpus, prefix,
   schedule, phase, and topology mismatches;
6. fake-command lifecycle tests proving `--validate-only` invokes no external
   lifecycle command;
7. fake AWS/Terraform/SSH/ECR failure injection proving artifact recovery and
   cleanup always run in the required order;
8. shell portability on macOS Bash 3.2 and Linux Bash 5;
9. Compose resolution with two digest-addressed images and the read-only
   corpus mount;
10. side-effect-free Terraform formatting and validation;
11. full BTE, mempool, node, hbbft, and chart suites plus focused affected race
    suites; and
12. replacement local distributed preflight using real n4 and n7 bundles with
    no AWS and no resource-performance claim.

## Readiness Boundary

Local implementation is complete when both new entrypoints pass
`--validate-only` against real n4/n7 bundles, all required local validation
passes, the source tree is clean, and the live path contains no synthetic,
build, push, substitution, secret-leakage, preserve-on-failure, or
resource-cleanup bypass.

Actual AWS readiness additionally requires the separately authorized image
publication and read-only operational confirmation that the supplied ECR
digests and required account capacity are available. The first billable action
remains the separately authorized same-AZ n4 readiness pilot.
