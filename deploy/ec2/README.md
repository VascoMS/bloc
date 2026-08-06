# VM/EC2 Deployment And Campaign Runbook

This runbook owns VM/EC2 operation for the BLOC prototype: manual deployment,
the A1 pilot, same-AZ and cross-AZ matrices, the three-region campaign, Merge +
Plan attribution, IAM prerequisites, and cleanup. Evidence meaning and acceptance
criteria remain canonical in [docs/VALIDATION.md](../../docs/VALIDATION.md).

AWS commands can allocate billable resources. Run real applies only with explicit
authorization. All maintained campaign entry points are Bash 3.2-compatible and
provide a side-effect-free `--validate-only` mode.

## Prerequisites

- Bash 3.2 or newer, AWS CLI, Terraform, Docker, OpenSSH/SCP, Python 3, and `jq`
- an authenticated AWS profile with the scoped EC2, VPC, ECR, IAM, and regional
  permissions required by the chosen runner
- Docker access from the same terminal
- a clean committed source tree for evidence campaigns that enforce source
  freezing

Verify Docker before provisioning:

```sh
docker version --format '{{.Server.Version}}'
```

Validate all campaign runner interfaces without allocating resources:

```sh
bash scripts/test-campaign-runners.sh
```

Individual runners also accept `--validate-only`.

## Deployment Model

Each operator EC2 host runs exactly one `bloc-node` container from
`operator-compose.yaml` plus one local immutable-corpus `mempool-il` container.
A separate controller host runs Prometheus/Grafana from
`controller-compose.yaml` and executes `eval-remote`.

Generated configuration separates:

- shared public cluster JSON and CRS, copied to every operator;
- one `secrets.ec2/operator-<id>.json` file per operator, installed only on that
  operator as `/etc/bloc/operator.json` with mode `0600`; and
- the controller's remote-evaluator configuration, which never receives
  operator secrets.

Never commit secret files or include them in experiment artifacts.

The issue #15 final runner validates or executes one topology, phase, and node
count at a time. Validation never sources the live adapter and therefore cannot
invoke AWS, Terraform lifecycle commands, Docker, SSH, SCP, or rsync:

```sh
bash deploy/ec2/run-same-az-campaign.sh \
  --phase readiness-pilot \
  --bundle-root <private-n4-bundle> --node-count 4 \
  --source-sha <replacement-source-sha> \
  --bloc-image <account>.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:<digest> \
  --mempool-image <account>.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:<digest> \
  --experiment-id bloc-ec2-i15-sa-n4-p1 \
  --admin-cidr <controller-public-ip>/32 --aws-profile <profile> \
  --validate-only
```

Replace only `--validate-only` with `--execute-live` after separate authorization.
`run-three-region-campaign.sh` has the same interface. The same-AZ adapter fixes
all `t3.small` hosts in `us-east-1a`; the three-region adapter keeps the
controller in `us-east-1` and maps operator `id % 3` to `us-east-1`,
`eu-west-1`, and `eu-central-1` through three peerings and six routes.

Evaluator invocations do not remain attached to a long-lived SSH session. The
runner stages `run-final-remote-job.sh` on the controller, atomically claims one
job identity per block/batch/first-slot tuple, starts that job at most once, and
polls its durable status through short reconnectable SSH calls. A repeated start
only observes the existing identity. Missing, ambiguous, lost, nonzero, or
poll-exhausted jobs fail closed and are never automatically re-executed.
Controller job commands, stdout, stderr, PID, and exit status are recovered
alongside evaluator artifacts.

Both adapters use two pre-existing private ECR repositories in `us-east-1`.
Instances receive repository-scoped pull access only. They pull and inspect the
exact digest and `linux/amd64` architecture before services start; the runner
never builds, tags, pushes, or substitutes an image. Each operator receives the
same encrypted corpus and only its own mode-0600 secret.

The fixed phases are `readiness-pilot` (n4 only: 1 warmup, 3 attempts, sampler
off), `latency` (10 warmups, 1,000 attempts, 10 blocks, sampler off), and
`resource` (0 warmups, 1,000 attempts, 10 blocks, sampler on). All use batches
8/32/128, seed 20260621, and the 12-second boundary. The extension phase remains
rejected until a later n10/batch-512 30-observation decision.

## Manual Deployment Recipe

Provision the single-region topology:

```sh
cd deploy/ec2/terraform
terraform init
terraform apply
terraform output -json inventory > ../inventory.json
```

Generate cluster and controller configuration from `bloc-node/`:

```sh
go run ./cmd/bloc-node gen-ec2-config \
  --inventory ../deploy/ec2/inventory.json \
  --cluster-out ../deploy/ec2/cluster.ec2.json \
  --remote-eval-out ../deploy/ec2/remote-eval.ec2.json \
  --cluster-id bloc-ec2 \
  --nodes 4 \
  --bmax 128
```

The generated cluster config uses the same 2,000 ms `mempool-http` request
default as `gen-config`. Override it with
`--mempool-timeout-ms <milliseconds>` when a deployment needs a different
bounded provider deadline.

Copy the shared JSON/CRS and only the matching secret to each operator, set
`NODE_ID`, and start `operator-compose.yaml`. On the controller, derive
`prometheus.ec2.yml` from `prometheus.ec2.example.yml`, start
`controller-compose.yaml`, and run `eval-remote` against
`remote-eval.ec2.json`.

Destroy the Terraform deployment immediately after artifact collection:

```sh
terraform -chdir=deploy/ec2/terraform destroy
```

## A1 Pilot And Kept-Environment Recovery

Use the cost-controlled A1 runner for a four-operator same-AZ readiness pilot:

```sh
bash deploy/ec2/run-a1-pilot.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --experiment-id bloc-ec2-a1-pilot-same-az-n4-20260719t120000z
```

The default pilot runs batches `8,32,128`, one warmup, and three measured
repetitions. It builds a `linux/amd64` image, prompts before apply, collects
artifacts under `results/ec2/<experiment-id>/`, and destroys resources in its
cleanup path. The runner accepts only `n=4/7/10` and batches
`8/32/128/512`, derives `BMax` from the largest requested batch, and accepts
`--repetition-blocks` plus `--seed` for stable balanced scheduling. Measured
repetitions must divide evenly across blocks.

While actively debugging a failed deployment, `--keep-resources-on-failure` may
retain the environment. Reuse it with:

```sh
bash deploy/ec2/rerun-a1-pilot-existing.sh \
  --artifact-root results/ec2/<experiment-id> \
  --aws-profile bloc \
  --batch-sizes 128 \
  --first-slot 1000
```

The rerun path pulls the selected image, restarts the complete sidecar cluster,
uses fresh slots, performs bounded health/metrics checks, validates consistency,
and collects logs. Destroy the retained Terraform workdir as soon as debugging
ends:

```sh
terraform -chdir=results/ec2/<experiment-id>/generated/terraform-work destroy \
  -var-file=results/ec2/<experiment-id>/generated/terraform-work/a1-pilot.tfvars \
  -auto-approve
```

## Same-AZ And Cross-AZ Matrices

Run the same-AZ wrapper:

```sh
bash deploy/ec2/run-m3-same-az.sh \
  --admin-cidr "<your-ip>/32" \
  --node-counts 4,7,10 \
  --batch-sizes 8,32,128
```

It defaults to `n=4,7,10`, batches `8,32,128`, five warmups, and thirty measured
repetitions. The runner prompts before allocating resources and between phases.
For a final p99 campaign, explicitly pass
`--warmups 10 --repetitions 1000 --repetition-blocks 10 --seed 20260621`; the
ordinary defaults remain a preflight-sized campaign.

Run the same-region cross-AZ wrapper:

```sh
bash deploy/ec2/run-m3-cross-az.sh \
  --admin-cidr "<your-ip>/32" \
  --node-counts 4,7 \
  --batch-sizes 8,32,128
```

It defaults to `n=4,7` and distributes operators across `us-east-1a/b/c`. The
runner prompts before allocating resources and between phases; acceptance gates
and cleanup verification remain mandatory.

## Three-Region Campaign

The dedicated runner creates directly peered VPCs in `us-east-1`, `eu-west-1`,
and `eu-central-1`, keeps the controller in the US, and assigns operators by
`node_id % 3`. Its deploy identity policy is
`bloc-three-region-deployer-policy.json`.

Run a no-allocation plan and allowlist check first:

```sh
bash deploy/ec2/run-m3-three-region.sh \
  --admin-cidr "127.0.0.1/32" \
  --aws-profile bloc \
  --plan-only \
  --unattended
```

For a separately authorized probe:

```sh
bash deploy/ec2/run-m3-three-region.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --node-counts 4 \
  --batch-sizes 8,128 \
  --warmups 1 \
  --repetitions 3
```

The canonical full defaults are `n=4,7`, batches `8,32,128`, five warmups,
thirty measurements, `t3.small`, and a 60-second evaluator timeout:

```sh
bash deploy/ec2/run-m3-three-region.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc
```

The runner requires verified 8/4/4 vCPU headroom, records pairwise health and
resource evidence, destroys on success or failure, retries Terraform teardown,
removes regional keys, and authenticates empty cleanup. It has no
preserve-on-failure mode. Final p99 collection must explicitly use 10 warmups,
1,000 measurements, balanced repetition blocks, and the predeclared seed.
Both same-region and three-region runners retain failed and timed-out attempts
as complete collection records while excluding them from successful latency
quantiles. The scale extension accepts `n=10` and batch `512` only after the
runbook's pilot/continuation decision and with generated `BMax` large enough.

## Resource Evidence

Same-region and three-region runners execute a separate `resource-measured`
evaluation pass after the primary latency phase. Each operator samples its own
container every 250 ms with cgroup-v2 CPU/memory counters and Docker fallback;
the sampler records neither credentials nor process configuration. The raw
`resource_timeseries.csv` retains timestamp, sample index, node/region,
scenario/phase, CPU microseconds, memory current/peak, network receive/transmit
bytes, restart count, and OOM state. `resource-summary.csv` reports per-node
configuration and cluster totals. Cluster memory fields are sums of per-node
maxima/peaks, not temporally synchronized cluster readings. A sampler must stay
live and write at least four data rows before its stop file is created; a single
sampling iteration permits at most four two-second Docker calls, while the
runner allows a bounded ten-second shutdown window. Host/container network
counters are separate from `bloc_protocol_message_bytes_total` protocol-message
metrics. Historical M3 `resource-samples.csv` artifacts retain only the coarse
running/restart/OOM stability gate and intentionally produce no host summary.

## EC2 Merge + Plan Attribution

Use the attribution runner only after relevant protocol, chart, deployment, and
canonical-document changes are committed:

```sh
bash deploy/ec2/run-merge-plan-attribution.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --auto-approve-plan
```

It runs Compute Flex `n=4`, Compute Flex `n=7`, and an independently accepted or
rejected T3 `n=7` comparison under one image digest. It requires at least 16
Standard On-Demand vCPUs, bounds phases to 90 minutes, and applies the runner's
conservative campaign cost ceiling. Partial or failed T3 observations remain
diagnostic and never enter accepted headline statistics.

## IAM And Naming Constraints

ECR-backed low-level experiment IDs must begin with `bloc-ec2-` because the
project policy scopes role and instance-profile names to `bloc-ec2-*`. Canonical
campaign wrappers keep human-facing IDs in the grammar defined by
[docs/WORKFLOWS.md](../../docs/WORKFLOWS.md) and derive shorter AWS names.
Final-campaign IDs are limited to 47 characters so the derived IAM role and
instance-profile names remain within AWS's 64-character limit.

Before using a new account or changed deploy policy, confirm the profile can
create and clean up the exact scoped role/profile surface. The three-region
workflow uses `bloc-three-region-deployer-policy.json`; do not broaden its
resource-name constraints merely to bypass a failed preflight.

## Cleanup Contract

Every real run must retain evidence that all scoped instances, volumes, VPCs,
peerings, campaign-created ECR repositories, temporary keys, IAM roles, and
instance profiles are absent after teardown. Successful results without
authenticated cleanup are not accepted evidence.

For the final campaign runner, the two shared ECR repositories are pre-existing
inputs and are never teardown targets; cleanup instead proves the exact regional
compute/network/key/IAM resources and Terraform state are empty. Historical
runners that created an experiment-scoped repository must still prove that
repository was removed.

Use preserve-on-failure only for a bounded active debugging window. The
three-region runner never preserves resources. Inter-region transfer and T3
Unlimited surplus credits can be billable even when an instance type is marked
Free Tier eligible.
