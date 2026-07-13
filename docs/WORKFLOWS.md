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
- Architecture or protocol boundary changes: update `docs/ARCHITECTURE.md` and `docs/DECISIONS.md`
- Validation command or acceptance-criteria changes: update `docs/VALIDATION.md`
- Small implementation changes: update `docs/CHANGELOG.md` and link the entry to the milestone it advanced

## Demo and Experiment Flow

Local demos, local evaluator runs, and Docker Compose runs are preflight tools.
Use them to catch regressions before deployment. `eval-local` and `eval-suite`
also provide the clean local protocol baseline. The real distributed target for
current thesis metrics is VM/EC2-per-sidecar evidence.

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

When interpreting M1 results, remember that the integrated BTE path already
uses deterministic BEAT-MEV `Opt-2` sub-batching: `alpha = ceil(2*sqrt(B))`.
M1 is for integrated slot latency, not for comparing BTE optimization variants.
Use a separate M2 benchmark or evaluator sweep when the question is normal vs
`sqrt(B)` vs `2*sqrt(B)` vs parallel combine, or when testing batch sizes beyond
the M1 `BMax=128` profile.

For the current paper-versus-integrated combine discrepancy, use the dedicated
attribution command and EC2 wrapper. The wrapper holds batches, warmups, and
repetitions constant across two placements of both `t3.small` and `c7a.large`,
runs the inherited paper benchmark as a cross-check, captures CPU-credit data
for burstable hosts, and destroys its Terraform resources by default:

```powershell
.\deploy\ec2\run-bte-attribution.ps1 `
  -AdminCidrs @("203.0.113.10/32")
```

Review the Terraform plan before typing `APPLY`. Do not substitute instance
families after a capacity error in an evidence run; record the failed placement
and rerun only with an explicit campaign change. Campaign-relevant paths must
be committed unless `-AllowDirtyTree` is deliberately used for a non-evidence
smoke run. Results and the generated attribution report are written under
`results/ec2/<experiment-id>/`.

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

Copy `cluster.ec2.json` to `/etc/bloc/cluster.json` on every operator, copy
`deploy/ec2/operator-compose.yaml` to each operator, set the host's `NODE_ID`,
and start the sidecar with `docker compose up -d`. On the controller, create
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

For the immediate A1 pilot, use the Windows PowerShell runner from a shell with
AWS CLI, Terraform, Docker Desktop, OpenSSH, and SCP available:

```powershell
.\deploy\ec2\run-a1-pilot.ps1 `
  -AdminCidrs "<your-ip>/32" `
  -AwsProfile bloc `
  -ExperimentId bloc-ec2-a1-pilot-same-az-n4-step1
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
`-KeepResourcesOnFailure`; if a deploy or evaluator step fails after Terraform
apply, the artifact directory keeps the inventory, generated configs, Terraform
workdir, and ignored SSH key. Then patch code or scripts locally and rerun only
the image/deploy/evaluator part:

```powershell
.\deploy\ec2\rerun-a1-pilot-existing.ps1 `
  -ArtifactRoot .\results\ec2\<experiment-id> `
  -AwsProfile bloc `
  -FirstSlot 1000
```

Use a fresh `-FirstSlot` range for every rerun against the same long-running
sidecars, because each `eval-remote` invocation runs sequential slots and old
slot IDs remain resident in the sidecars. Destroy the Terraform workdir once
the kept-alive environment has either produced a clean pilot or is no longer
being actively debugged:

```powershell
terraform -chdir=.\results\ec2\<experiment-id>\generated\terraform-work destroy `
  -var-file=.\results\ec2\<experiment-id>\generated\terraform-work\a1-pilot.tfvars `
  -auto-approve
```

The pilot records controller-to-operator network characterization with HTTP
timing against each operator's `/healthz` endpoint, not ICMP ping. ICMP is not
opened in the default security groups, so ping loss is not a meaningful BLOC
traffic signal unless the security groups are intentionally changed.

For the first thesis-grade M3 same-AZ synthetic baseline, use the M3 wrapper.
It runs the EC2 phase runner sequentially for 4, 7, and 10 operators, using
batch sizes `8,32,128`, 5 warmups, and 30 measured repetitions:

```powershell
.\deploy\ec2\run-m3-same-az.ps1 `
  -AdminCidrs "<your-ip>/32" `
  -AwsProfile bloc `
  -AutoApprovePlan
```

By default the wrapper pauses after each successful node-count phase so the
phase summary can be inspected before launching the next, larger EC2 campaign.
Use `-AutoApprovePhases` only when intentionally running the full 4/7/10 matrix
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

For the same-region cross-AZ synthetic comparison, use the cross-AZ wrapper:

```powershell
.\deploy\ec2\run-m3-cross-az.ps1 `
  -AdminCidrs "<your-ip>/32" `
  -AwsProfile bloc `
  -AutoApprovePlan
```

By default it runs only `n=4` and `n=7`, using the same batch sizes, warmups,
repetitions, and instance types as the same-AZ M3 path. The Terraform phase
runner creates three public subnets in `us-east-1a`, `us-east-1b`, and
`us-east-1c`, then assigns operators round-robin across those subnet IDs while
keeping private-IP BLOC traffic inside one VPC. Do not run a `t3.small` `n=10`
EC2 phase unless the AWS account has enough vCPU quota for 10 operators plus
the controller.

Run `docker version --format '{{.Server.Version}}'` from the same PowerShell
session before launching the pilot. If that command cannot talk to the Docker
daemon, fix Docker Desktop access before creating AWS resources. A bash runner
also exists at `deploy/ec2/run-a1-pilot.sh` for a future Linux/WSL controller,
but the Windows runner is the current supported path for this workspace.

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
