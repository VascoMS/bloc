# Workflows

## Standard Development Flow

1. Understand the task.
2. Check `docs/STATUS.md` for the active milestone, immediate next actions, and the last known good state.
3. Read the minimum relevant context.
4. Implement the smallest useful change.
5. Run the required validation.
6. Update canonical docs if behavior, workflow, roadmap state, or rationale changed.
7. Update `docs/STATUS.md` if a milestone started, completed, blocked, or gained a stronger validated baseline.
8. Summarize outcome, validation, and remaining gaps.

## Codex Task Flow

For agent-driven work, use this read order:

1. [AGENTS.md](/bloc/AGENTS.md)
2. [docs/CODEX_GUIDE.md](/bloc/docs/CODEX_GUIDE.md)
3. [docs/STATUS.md](/bloc/docs/STATUS.md)
4. The relevant canonical doc such as architecture or validation
5. The relevant module README
6. Only the code files needed for the task

## Task-Specific Context Packets

A good task packet for Codex should include only:

- objective,
- active milestone,
- affected module,
- likely files,
- required validation,
- relevant constraints or invariants.

Do not repeatedly paste long protocol summaries or benchmark outputs when the same information already lives in canonical docs.

## Documentation Update Workflow

Update docs based on the kind of change:

- Milestone state or next-step changes: update `docs/STATUS.md`
- Workflow or developer process changes: update `docs/WORKFLOWS.md` or `docs/DEVELOPMENT.md`
- Architecture or protocol boundary changes: update `docs/ARCHITECTURE.md`, the
  affected `docs/modules/` deep dive, and `docs/DECISIONS.md`
- Validation command or acceptance-criteria changes: update `docs/VALIDATION.md`
- Small implementation changes: update `docs/CHANGELOG.md` and link the entry to the milestone it advanced

## Demo and Experiment Flow

Local demos, local evaluator runs, and Docker Compose runs are preflight tools.
Use them to catch regressions before deployment. `eval-local` and `eval-suite`
also provide the clean local protocol baseline. The real distributed target for
current thesis metrics is VM/EC2-per-sidecar evidence.

### Resource-limit configuration

Generated `bloc-cluster-v2` files explicitly include the shared `limits`
object. Older v2 files omit it safely and receive these defaults:

```json
{
  "limits": {
    "max_proposal_bytes": 8388608,
    "max_envelope_bytes": 16777216,
    "max_combine_attempts_per_sub_batch": 256
  }
}
```

Every operator must use the same public file. Proposal bytes may be raised only
to 32 MiB, envelopes only to 64 MiB, and envelopes must retain at least 64 KiB
of headroom over proposals. Recovery attempts may be configured from 1 to 4096.
Raise limits only for a recorded workload that cannot fit the defaults, then
rerun the adversarial resource tests and local matrix. A cap rejection is a
failed-closed protocol event, not permission to give individual operators
different overrides.

Both `gen-config` and `gen-ec2-config` accept
`--max-proposal-bytes`, `--max-envelope-bytes`, and
`--max-combine-attempts-per-sub-batch`. Prefer these flags over editing a
generated artifact so invalid relationships and hard ceilings are rejected at
generation time.

### Experiment and Result Naming

Use one campaign ID for the complete logical run. The same ID must name the
top-level result directory, chart directory, manifest `experiment_id`, and any
comparison input. Do not invent a second human-facing name for a phase or for
the generated charts.

Campaign IDs use this grammar:

```text
<milestone>-<topology>-<workload>[-<variant>]-<utc-timestamp>
```

Use lowercase ASCII, digits, and hyphens only. Format the UTC timestamp as
`yyyyMMdd't'HHmmss'z'`, for example `20260715t095117z`. Use the following
controlled terms where they apply:

- milestone: `m1`, `m2`, `m3`, `m4`, or `m5`;
- topology: `local`, `compose`, `same-az`, or `cross-az`;
- workload: `synthetic`, `replay`, `libp2p`, `bte`, or a short fault name;
- variant: `baseline`, `opt`, `probe`, or another short protocol variant that
  is meaningful in the final analysis.

Examples:

```text
m3-cross-az-synthetic-opt-20260715t095117z
m3-cross-az-synthetic-probe-20260715t093849z
m1-local-libp2p-baseline-20260715t081500z
m5-local-omit-proposal-20260715t140000z
```

Do not use suffixes such as `v2`, `fixed`, `final`, `free`, or `step1`. A rerun
gets a new timestamp. Record its relationship to an earlier run in the
manifest or comparison metadata. Reuse an existing campaign ID only when a
runner explicitly supports resuming that same incomplete campaign.

Store artifacts using this layout:

```text
results/<environment>/<campaign-id>/
  manifest.json
  run_measurements.csv
  n4/
  n7/
  comparison/
results/charts/<campaign-id>/
```

`<environment>` is `local`, `distributed`, or `ec2`. Node-count and scenario
directories are children of the campaign; they are not separate top-level
campaigns. Temporary low-level EC2 experiment IDs may include the required
`bloc-ec2-` prefix, but the wrapper's `-CampaignId` remains the canonical name.
For M3 wrappers, retain their full standard prefix (for example
`m3-cross-az-synthetic-`) and keep the optional variant short. The wrapper
removes that prefix when constructing AWS names, which keeps IAM role and
instance-profile names within AWS's 64-character limit.

Before starting any recorded run, choose the campaign ID once and pass it
unchanged to the runner. When reporting results, link the canonical campaign
root rather than any temporary phase-staging directory.

Use the `bloc-node` demo flow when you need a fast end-to-end prototype check:

```sh
cd bloc-node
./scripts/demo-local.sh
```

Use `eval-local` for targeted experiments:

```sh
cd bloc-node
go run ./cmd/bloc-node eval-local --nodes 4 --batch-sizes 8
```

Use persistent `eval-suite` runs when deliberately refreshing the clean local
M1 protocol baseline or debugging a local latency regression. The named M1
profile starts one cluster for each operator count and runs fresh slots through
it; use `--execution-mode isolated` only when per-sample process lifecycle
validation is the intended local check.

```sh
cd bloc-node
go run ./cmd/bloc-node eval-suite --profile m1-baseline --experiment-id m1-baseline --out-dir results/m1-local/baseline-persistent
```

Use BTE benchmarks for cryptographic full-path measurements:

```sh
cd bte/btd-impl-main
go test ./be -run '^$' -bench '^BenchmarkHybridFullPath'
```

For merge/plan attribution or optimization work, capture a baseline and an
optimized phase with the same campaign id. The Bash runner fixes
`GOMAXPROCS=1`, records ten one-second benchmark samples with allocations,
profiles the 7-node batch-128 overlap modes, runs the 4/7-node evaluator
matrix, and writes a comparison report without using AWS:

```sh
bash bloc-node/scripts/run-merge-plan-campaign.sh --phase baseline --campaign-id <id>
bash bloc-node/scripts/run-merge-plan-campaign.sh --phase optimized --campaign-id <id>
```

When interpreting M1 results, remember that the integrated BTE path already
uses deterministic BEAT-MEV `Opt-2` sub-batching: `alpha = ceil(2*sqrt(B))`.
M1 is for integrated slot latency, not for comparing BTE optimization variants.
Use a separate M2 benchmark or evaluator sweep when the question is normal vs
`sqrt(B)` vs `2*sqrt(B)` vs parallel combine, or when testing batch sizes beyond
the M1 `BMax=128` profile.

## Distributed Sidecar Workflow

Use Docker Compose as a local bridge before distributed deployment work.
Compose is a rehearsal environment; it does not replace distributed evidence:

```sh
cd deploy/docker-compose
docker compose up --build
```

Then run the remote evaluator from `bloc-node`:

```sh
go run ./cmd/bloc-node eval-remote --config ../deploy/docker-compose/remote-eval.compose.json --experiment-id compose-smoke --batch-size 8 --repetitions 1 --out-dir results/distributed/compose-smoke
```

For the main distributed thesis workflow, run one sidecar per VM/EC2 operator
host and run `eval-remote` from a separate controller. Record the environment,
image or binary version, git commit, generated config shape, endpoint mode,
result directory, and remaining deployment gaps in `docs/STATUS.md`.

Use host-local Docker Compose on EC2 rather than a single multi-host Compose
deployment. Each operator EC2 runs one `bloc-node` container from
`deploy/ec2/operator-compose.yaml`; the controller EC2 runs Prometheus/Grafana
from `deploy/ec2/controller-compose.yaml` and executes `eval-remote`.

The EC2 workflow is:

```sh
cd deploy/ec2/terraform
terraform init
terraform apply
terraform output -json inventory > ../inventory.json
```

Generate sidecar and evaluator configs from that inventory:

```sh
cd ../../../bloc-node
go run ./cmd/bloc-node gen-ec2-config \
  --inventory ../deploy/ec2/inventory.json \
  --cluster-out ../deploy/ec2/cluster.ec2.json \
  --remote-eval-out ../deploy/ec2/remote-eval.ec2.json \
  --cluster-id bloc-ec2 \
  --nodes 4 \
  --bmax 128
```

The generator also writes public `cluster.ec2.crs` and one file under
`secrets.ec2/` per operator. Copy the same JSON and CRS to every operator, but
copy only `secrets.ec2/operator-<id>.json` to that operator as
`/etc/bloc/operator.json` with mode `0600`. Never copy the secrets directory to
the controller or include it in experiment artifacts. Copy
`deploy/ec2/operator-compose.yaml`, set the host's `NODE_ID`, and start the
sidecar with `docker compose up -d`. On the controller, create
`prometheus.ec2.yml` from `prometheus.ec2.example.yml` using the operator
private IPs, then start `deploy/ec2/controller-compose.yaml`.

Run the distributed smoke from the controller:

```sh
go run ./cmd/bloc-node eval-remote \
  --config deploy/ec2/remote-eval.ec2.json \
  --experiment-id ec2-smoke \
  --batch-size 8 \
  --warmups 0 \
  --repetitions 1 \
  --out-dir results/distributed/ec2-smoke \
  --image-tag <image-tag> \
  --git-commit <git-commit>
```

For the immediate A1 pilot, use the Bash runner from macOS or Linux with AWS
CLI, Terraform, Docker, OpenSSH, SCP, Python 3, and `jq` available:

```sh
bash deploy/ec2/run-a1-pilot.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --experiment-id bloc-ec2-a1-pilot-same-az-n4-step1
```

By default, the runner launches the `T0-same-az` 4-operator pilot with
batch sizes `8,32,128`, one warmup, and three measured repetitions per batch.
It first verifies local Docker access and prebuilds the `bloc-node` image, then
prompts before `terraform apply`, collects artifacts under
`results/ec2/<experiment-id>/`, and destroys Terraform resources in its cleanup
block. Use `-KeepResourcesOnFailure` only while actively debugging a failed
deployment, and run `terraform destroy` immediately afterward.

During runner or evaluator debugging, prefer one provisioned EC2 environment
over repeated create/destroy loops. Launch the pilot with
`--keep-resources-on-failure`; if a deploy or evaluator step fails after Terraform
apply, the artifact directory keeps the inventory, generated configs, Terraform
workdir, and ignored SSH key. Then patch code or scripts locally and rerun only
the image/deploy/evaluator part:

```sh
bash deploy/ec2/rerun-a1-pilot-existing.sh \
  --artifact-root results/ec2/<experiment-id> \
  --aws-profile bloc \
  --batch-sizes 128 \
  --first-slot 1000
```

The recovery runner is unattended. It pulls the selected image on every
operator, stops the full sidecar cluster before restarting any node, clones the
existing generated configs with `FirstSlot` as the new initial slot, writes
UTF-8 JSON without a BOM, performs bounded health/metrics checks, validates
measured-run counts and consistency, and collects operator logs. Use
Use comma-separated `--batch-sizes` for one or more sizes. Destroy the Terraform workdir once the kept-alive
environment has either produced a clean pilot or is no longer being actively
debugged:

```sh
terraform -chdir=results/ec2/<experiment-id>/generated/terraform-work destroy \
  -var-file=results/ec2/<experiment-id>/generated/terraform-work/a1-pilot.tfvars \
  -auto-approve
```

The pilot records controller-to-operator network characterization with HTTP
timing against each operator's `/healthz` endpoint, not ICMP ping. ICMP is not
opened in the default security groups, so ping loss is not a meaningful BLOC
traffic signal unless the security groups are intentionally changed.

Before returning an ACS/BBA change to EC2, run the complete local safety gate:

```sh
bash bloc-node/scripts/run-acs-safety-campaign.sh
```

The runner is AWS-free and writes an ignored manifest, fixed-seed scheduler
description, command logs, evaluator outputs, failed slot status, and
`REPORT.md` under `results/local/acs-common-subset-safety/<campaign-id>/`. If a
tooling or evaluator stage stops after an earlier stage passed, resume without
repeating completed work by supplying the same `--campaign-id`, `--resume`, and
an explicit `--start-at race|gate|matrix|identity`.

For the first thesis-grade M3 same-AZ synthetic baseline, use the M3 wrapper.
It runs the EC2 phase runner sequentially for 4, 7, and 10 operators, using
batch sizes `8,32,128`, 5 warmups, and 30 measured repetitions:

```sh
bash deploy/ec2/run-m3-same-az.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --auto-approve-plan
```

By default the wrapper pauses after each successful node-count phase so the
phase summary can be inspected before launching the next, larger EC2 campaign.
Use `--auto-approve-phases` only when intentionally running the full 4/7/10 matrix
without manual review between phases. It writes phase artifacts under
`results/ec2/<campaign-id>/n<N>/`, merged campaign outputs under
`results/ec2/<campaign-id>/`, and combined charts under
`results/charts/<campaign-id>/`.

M3 same-AZ synthetic acceptance is stricter than a smoke test: every measured
run must be `success=true` and `consistent=true`, Prometheus must report all
targets up for every phase, chart generation must succeed, and cleanup
verification files must show no leftover EC2 instances, volumes, VPC, ECR
repository, temporary key pair, IAM role, or instance profile. Resource samples
from operator `docker stats --no-stream` are stored as `resource-samples.csv`
for context only; detailed overhead characterization remains M4.

For focused Merge + Plan attribution on the optimized image, use:

```sh
bash deploy/ec2/run-merge-plan-attribution.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --auto-approve-plan
```

For unattended repeated collection, use comma-separated matrix parameters:

```sh
bash deploy/ec2/run-m3-cross-az.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --node-counts 4,7 \
  --batch-sizes 8,32,128 \
  --campaign-id "m3-cross-az-synthetic-<label>" \
  --baseline-campaign-root "results/ec2/<baseline-campaign>" \
  --unattended
```

`--unattended` removes both approval prompts but does not bypass Terraform's
resource allowlist, instance-count and batch-size bounds, phase acceptance, or
cleanup checks. When `-BaselineCampaignRoot` is supplied, the completed campaign
automatically writes `comparison/comparison.csv`, `comparison/REPORT.md`, and a
before/after p50 chart. Canonical M3 wrappers reject node counts outside
`4,7,10` and batch sizes outside `8,32,128` before invoking AWS; the lower-level
phase runner independently rejects more than 10 operators and batches above
`BMax=128`.

The wrapper refuses relevant uncommitted sources, builds one image, and runs
Compute Flex `n=4`, Compute Flex `n=7`, and T3 burstable `n=7`
sequentially in one AZ. Each phase follows the shared three-block batch order,
records 30 samples per batch without restarting sidecars, destroys successful
infrastructure, and must retain the same image digest. The final analysis
contains node-level measurements, p50/p95 summaries, comparisons, a Markdown
report, and PNG/SVG charts. This 30-sample campaign does not support p99 claims.
Compute Flex `n=4/n=7` are the required attribution phases. T3 `n=7` is an
optional comparison phase and enters comparison tables only if it independently
passes the same 30-run acceptance checks. Preserve a failed T3 run as explicitly
invalid diagnostic evidence; never merge its partial observations into accepted
summary statistics.

For the same-region cross-AZ synthetic comparison, use the cross-AZ wrapper:

```sh
bash deploy/ec2/run-m3-cross-az.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --auto-approve-plan
```

By default it runs only `n=4` and `n=7`, using the same batch sizes, warmups,
repetitions, and instance types as the same-AZ M3 path. The Terraform phase
runner creates three public subnets in `us-east-1a`, `us-east-1b`, and
`us-east-1c`, then assigns operators round-robin across those subnet IDs while
keeping private-IP BLOC traffic inside one VPC. Do not run a `t3.small` `n=10`
EC2 phase unless the AWS account has enough vCPU quota for 10 operators plus
the controller.

For standalone three-region latency evidence, use the dedicated wrapper. It
creates one VPC in `us-east-1`, `eu-west-1`, and `eu-central-1`, directly peers
all three VPC pairs, runs the controller in `us-east-1`, and places operators
by `node_id % 3`:

The deploy identity policy used by this workflow is maintained at
`deploy/ec2/bloc-three-region-deployer-policy.json`. It includes direct VPC
peering, detailed-monitoring, regional teardown, and inline ECR-pull policy
lifecycle permissions for roles named `bloc-ec2-3r-*`.

```sh
bash deploy/ec2/run-m3-three-region.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --plan-only \
  --unattended
```

After committing the validated source, run the probe:

```sh
bash deploy/ec2/run-m3-three-region.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --node-counts 4 \
  --batch-sizes 8,128 \
  --warmups 1 \
  --repetitions 3
```

If the probe and its teardown are accepted, run the full defaults:

```sh
bash deploy/ec2/run-m3-three-region.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc
```

The canonical matrix is `n=4/7`, batches `8/32/128`, five warmups, and thirty
measurements on `t3.small`, with a 60-second slot timeout. Placement is `2/1/1`
for `n=4` and `3/2/2` for `n=7`. `--plan-only` checks both node-count plans and
the exact three-VPC/three-peering/six-peer-route allowlist without creating
resources. Real applies may be billable and require operator authorization.
The runner verifies 8/4/4 vCPU headroom, records pre/post five-attempt pairwise health matrices,
CPU/T3 credit metrics, resource samples, region/AZ attribution, and one image
digest. It always destroys, retries Terraform teardown, removes regional keys,
and authenticates empty cleanup for each node-count phase before continuing.
There is no preserve-on-failure mode. The report includes protocol p50/p95, four-stage, four
region-pair classes, and critical-node-region attribution. Inter-region data
transfer and T3 Unlimited surplus credits may be billable.

Run `docker version --format '{{.Server.Version}}'` from the same terminal
before launching the pilot. If it cannot talk to the Docker daemon, fix Docker
access before creating AWS resources. All campaign runners support
`--validate-only`, which performs argument/dependency validation without
writing evidence or allocating cloud resources.

Run the shared portability gate after changing any runner or artifact helper:

```sh
bash scripts/test-campaign-runners.sh
```

It checks Bash syntax, Python artifact fixtures, valid and invalid CLI paths,
paths containing spaces, UTF-8-without-BOM output, every entrypoint's
`--validate-only` path, and proves that validation does not invoke fake AWS,
Terraform, Docker, SSH, or SCP lifecycle commands. The same command is expected
to pass under the system macOS Bash 3.2 and Linux Bash 5.

The ECR-backed runner path intentionally creates IAM roles and instance
profiles named from the experiment id:

```text
<experiment-id>-ec2-ecr-readonly
```

Use experiment ids that begin with `bloc-ec2-`. The project IAM policy is
scoped to `arn:aws:iam::<account>:role/bloc-ec2-*` and
`arn:aws:iam::<account>:instance-profile/bloc-ec2-*`; ids such as
`a1-pilot-*` will fail at `iam:CreateRole`. The runner enforces this prefix
when `--image-distribution=ecr`, which is the default.

Before a fresh AWS account or policy change, test the IAM surface with the same
profile used by the runner:

```sh
aws iam create-role \
  --profile bloc \
  --role-name bloc-ec2-policy-test-ec2-ecr-readonly \
  --assume-role-policy-document '{
    "Version":"2012-10-17",
    "Statement":[{
      "Effect":"Allow",
      "Principal":{"Service":"ec2.amazonaws.com"},
      "Action":"sts:AssumeRole"
    }]
  }'

aws iam attach-role-policy \
  --profile bloc \
  --role-name bloc-ec2-policy-test-ec2-ecr-readonly \
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly

aws iam create-instance-profile \
  --profile bloc \
  --instance-profile-name bloc-ec2-policy-test-ec2-ecr-readonly

aws iam add-role-to-instance-profile \
  --profile bloc \
  --instance-profile-name bloc-ec2-policy-test-ec2-ecr-readonly \
  --role-name bloc-ec2-policy-test-ec2-ecr-readonly
```

Clean up the IAM preflight immediately:

```sh
aws iam remove-role-from-instance-profile \
  --profile bloc \
  --instance-profile-name bloc-ec2-policy-test-ec2-ecr-readonly \
  --role-name bloc-ec2-policy-test-ec2-ecr-readonly

aws iam delete-instance-profile \
  --profile bloc \
  --instance-profile-name bloc-ec2-policy-test-ec2-ecr-readonly

aws iam detach-role-policy \
  --profile bloc \
  --role-name bloc-ec2-policy-test-ec2-ecr-readonly \
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly

aws iam delete-role \
  --profile bloc \
  --role-name bloc-ec2-policy-test-ec2-ecr-readonly
```

## Scratchpads vs Durable Docs

- Scratchpads are temporary and task-scoped.
- Canonical docs are durable and cross-task.
- `docs/STATUS.md` is the live project-state surface and should stay short.
- Historical debugging notes belong in `docs/archive/` after their durable lessons are extracted.
