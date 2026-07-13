#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash deploy/ec2/run-a1-pilot.sh --admin-cidr CIDR [options]

Required:
  --admin-cidr CIDR              CIDR allowed to SSH/Prometheus/Grafana, e.g. 1.2.3.4/32.

Options:
  --aws-profile NAME             AWS profile to use. Default: bloc
  --aws-region REGION            AWS region. Default: us-east-1
  --availability-zone AZ         Single-AZ pilot placement. Default: us-east-1a
  --node-count N                 Operator count. Default: 4
  --operator-instance-type TYPE  Operator instance type. Default: t3.small
  --controller-instance-type TYPE Controller instance type. Default: t3.small
  --batch-sizes LIST             Comma-separated batch sizes. Default: 8,32,128
  --warmups N                    Warmups per batch. Default: 1
  --repetitions N                Measured repetitions per batch. Default: 3
  --experiment-id ID             Stable experiment id. Default: bloc-ec2-a1-pilot-same-az-n<N>-<UTC stamp>
                                  ECR mode requires a bloc-ec2-* id to match the scoped IAM policy.
  --image-distribution MODE      ecr or ssh-load. Default: ecr
  --auto-approve-plan            Apply Terraform without interactive APPLY prompt.
  --keep-resources-on-failure    Do not destroy Terraform resources after a failure.
  --skip-chart-generation        Do not run latency chart generation.
  -h, --help                     Show this help.

This script creates AWS resources. By default it destroys them in a cleanup trap.
EOF
}

aws_profile="bloc"
aws_region="us-east-1"
availability_zone="us-east-1a"
node_count="4"
operator_instance_type="t3.small"
controller_instance_type="t3.small"
batch_sizes_csv="8,32,128"
warmups="1"
repetitions="3"
experiment_id=""
auto_approve_plan="0"
keep_resources_on_failure="0"
skip_chart_generation="0"
image_distribution="ecr"
admin_cidrs=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --admin-cidr) admin_cidrs+=("$2"); shift 2 ;;
    --aws-profile) aws_profile="$2"; shift 2 ;;
    --aws-region) aws_region="$2"; shift 2 ;;
    --availability-zone) availability_zone="$2"; shift 2 ;;
    --node-count) node_count="$2"; shift 2 ;;
    --operator-instance-type) operator_instance_type="$2"; shift 2 ;;
    --controller-instance-type) controller_instance_type="$2"; shift 2 ;;
    --batch-sizes) batch_sizes_csv="$2"; shift 2 ;;
    --warmups) warmups="$2"; shift 2 ;;
    --repetitions) repetitions="$2"; shift 2 ;;
    --experiment-id) experiment_id="$2"; shift 2 ;;
    --image-distribution) image_distribution="$2"; shift 2 ;;
    --auto-approve-plan) auto_approve_plan="1"; shift ;;
    --keep-resources-on-failure) keep_resources_on_failure="1"; shift ;;
    --skip-chart-generation) skip_chart_generation="1"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ ${#admin_cidrs[@]} -eq 0 ]]; then
  echo "--admin-cidr is required; use a /32 for your current admin IP." >&2
  exit 2
fi
if [[ "$image_distribution" != "ecr" && "$image_distribution" != "ssh-load" ]]; then
  echo "--image-distribution must be ecr or ssh-load" >&2
  exit 2
fi

IFS=',' read -r -a batch_sizes <<< "$batch_sizes_csv"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
terraform_dir="$script_dir/terraform"
stamp="$(date -u +%Y%m%dt%H%M%sz)"
if [[ -z "$experiment_id" ]]; then
  experiment_id="bloc-ec2-a1-pilot-same-az-n${node_count}-${stamp}"
fi
if [[ "$image_distribution" == "ecr" && "$experiment_id" != bloc-ec2-* ]]; then
  echo "--experiment-id must start with bloc-ec2- when --image-distribution=ecr" >&2
  echo "This keeps generated IAM roles and instance profiles inside the scoped bloc IAM policy." >&2
  exit 2
fi
ecr_repository_name="$(printf 'bloc-node-%s' "$experiment_id" | tr '[:upper:]' '[:lower:]' | sed 's#[^a-z0-9._/-]#-#g')"

artifact_root="$repo_root/results/ec2/$experiment_id"
key_name="${experiment_id}-key"
terraform_work_dir="$artifact_root/generated/terraform-work"
key_path="${TMPDIR:-/tmp}/${key_name}.pem"
tfvars_path="$terraform_work_dir/a1-pilot.tfvars"
plan_path="$terraform_work_dir/a1-pilot.tfplan"
image_tar_path="$artifact_root/generated/bloc-node-image.tar"
docker_context_dir="${TMPDIR:-/tmp}/${experiment_id}-docker-context"
campaign_started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

terraform_applied="0"
terraform_started="0"
key_created="0"
git_commit=""
local_image_uri=""
ecr_repository_url=""
image_uri=""
registry=""
invalid_reason=""
campaign_status="invalid"

mkdir -p "$artifact_root"/{generated,logs,scenarios}

log() {
  printf '\n==> %s\n' "$*"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 2
  }
}

json_escape_array() {
  local first="1"
  printf '['
  for item in "$@"; do
    if [[ "$first" == "0" ]]; then printf ', '; fi
    first="0"
    printf '"%s"' "$item"
  done
  printf ']'
}

ssh_ec2() {
  local host="$1"
  shift
  ssh -i "$key_path"     -o StrictHostKeyChecking=no     -o UserKnownHostsFile=/dev/null     -o ConnectTimeout=10     "ubuntu@$host" "$@"
}

scp_ec2() {
  scp -i "$key_path"     -o StrictHostKeyChecking=no     -o UserKnownHostsFile=/dev/null     "$@"
}

retry_ssh_ec2() {
  local host="$1"
  local attempts="$2"
  local delay="$3"
  local description="$4"
  shift 4

  local attempt
  for ((attempt = 1; attempt <= attempts; attempt++)); do
    if ssh_ec2 "$host" "$@"; then
      return 0
    fi
    if [[ "$attempt" -lt "$attempts" ]]; then
      printf 'waiting for %s (%d/%d)
' "$description" "$attempt" "$attempts" >&2
      sleep "$delay"
    fi
  done

  printf 'timed out waiting for %s after %d attempts
' "$description" "$attempts" >&2
  return 1
}

write_prometheus_config() {
  local inventory="$1"
  local out="$2"
  {
    echo "global:"
    echo "  scrape_interval: 2s"
    echo "scrape_configs:"
    echo "  - job_name: 'bloc-sidecars'"
    echo "    metrics_path: /metrics"
    echo "    static_configs:"
    echo "      - targets:"
    jq -r '.nodes | sort_by(.id)[] | "          - " + .private_ip + ":8000"' "$inventory"
  } > "$out"
}

collect_network_matrix() {
  local phase="$1"
  local out="$2"
  local controller_public_ip="$3"
  local inventory="$4"

  echo "phase,source,target_node_id,target_private_ip,transmitted,received,loss_percent,avg_rtt_ms" > "$out"
  jq -c '.nodes | sort_by(.id)[]' "$inventory" | while read -r node; do
    local node_id private_ip
    node_id="$(jq -r '.id' <<< "$node")"
    private_ip="$(jq -r '.private_ip' <<< "$node")"
    if ! ping_output="$(ssh_ec2 "$controller_public_ip" "ping -c 5 '$private_ip' || true")"; then
      line="$phase,controller,$node_id,$private_ip,error,error,error,error"
    elif [[ -z "$ping_output" ]]; then
      line="$phase,controller,$node_id,$private_ip,error,error,error,error"
    else
      line="$(awk -v phase="$phase" -v node_id="$node_id" -v ip="$private_ip" '
        /packets transmitted/ { tx=$1; rx=$4; loss=$6; gsub(/%/, "", loss) }
        /rtt/ { split($4, a, "/"); avg=a[2] }
        END { printf "%s,controller,%s,%s,%s,%s,%s,%s", phase, node_id, ip, tx, rx, loss, avg }
      ' <<< "$ping_output")"
    fi
    echo "$line" >> "$out"
  done
}

merge_csv_outputs() {
  local root="$1"
  local csv_name source dest first
  for csv_name in run_measurements.csv node_measurements.csv scenario_summary.csv; do
    dest="$root/$csv_name"
    rm -f "$dest"
    first="1"
    for batch in "${batch_sizes[@]}"; do
      source="$root/scenarios/batch-$batch/results/$csv_name"
      [[ -f "$source" ]] || {
        echo "missing scenario output: $source" >&2
        return 1
      }
      if [[ "$first" == "1" ]]; then
        cat "$source" >> "$dest"
        first="0"
      else
        tail -n +2 "$source" >> "$dest"
      fi
    done
  done

  jq -s 'add' "$root"/scenarios/batch-*/results/scenario_summary.json > "$root/scenario_summary.json"
}

write_manifest() {
  local status="$1"
  local reason="${2:-}"
  local inventory_json="null"
  local cleanup_json="{}"
  local terraform_json="{}"

  [[ -f "$artifact_root/inventory.json" ]] && inventory_json="$(cat "$artifact_root/inventory.json")"
  [[ -f "$artifact_root/cleanup-verification.json" ]] && cleanup_json="$(cat "$artifact_root/cleanup-verification.json")"
  [[ -f "$artifact_root/terraform-metadata.json" ]] && terraform_json="$(cat "$artifact_root/terraform-metadata.json")"

  jq -n \
    --arg schema "bloc-ec2-campaign/v1" \
    --arg experiment_id "$experiment_id" \
    --arg campaign "A1-pilot-same-az" \
    --arg status "$status" \
    --arg invalid_reason "$reason" \
    --arg started_at "$campaign_started_at" \
    --arg finished_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg git_commit "$git_commit" \
    --arg docker_image "$image_uri" \
    --arg ecr_repository_name "$ecr_repository_name" \
    --arg aws_region "$aws_region" \
    --arg availability_zone "$availability_zone" \
    --arg node_count "$node_count" \
    --arg operator_instance_type "$operator_instance_type" \
    --arg controller_instance_type "$controller_instance_type" \
    --arg warmups "$warmups" \
    --arg repetitions "$repetitions" \
    --argjson batch_sizes "$(printf '%s\n' "${batch_sizes[@]}" | jq -R 'tonumber' | jq -s '.')" \
    --argjson inventory "$inventory_json" \
    --argjson terraform "$terraform_json" \
    --argjson cleanup "$cleanup_json" \
    '{
      schema_version: $schema,
      experiment_id: $experiment_id,
      campaign: $campaign,
      status: $status,
      invalid_reason: (if $invalid_reason == "" then null else $invalid_reason end),
      started_at: $started_at,
      finished_at: $finished_at,
      git_commit: $git_commit,
      docker_image: $docker_image,
      ecr_repository_name: $ecr_repository_name,
      aws_region: $aws_region,
      availability_zone: $availability_zone,
      node_count: ($node_count | tonumber),
      operator_instance_type: $operator_instance_type,
      controller_instance_type: $controller_instance_type,
      topology: "T0-same-az",
      tx_source: "synthetic",
      batch_sizes: $batch_sizes,
      warmups: ($warmups | tonumber),
      repetitions: ($repetitions | tonumber),
      terraform: $terraform,
      inventory: $inventory,
      cleanup_checks: $cleanup
    }' > "$artifact_root/manifest.json"
}

cleanup() {
  local exit_code=$?
  set +e

  if [[ "$exit_code" -ne 0 ]]; then
    invalid_reason="${invalid_reason:-runner failed with exit code $exit_code}"
    campaign_status="invalid"
  fi

  if [[ ("$terraform_applied" == "1" || "$terraform_started" == "1") && "$keep_resources_on_failure" != "1" ]]; then
    log "terraform destroy"
    (
      cd "$terraform_work_dir"
      if [[ -f "$tfvars_path" ]]; then
        AWS_PROFILE="$aws_profile" terraform destroy -var-file="$tfvars_path" -auto-approve
      else
        AWS_PROFILE="$aws_profile" terraform destroy -auto-approve
      fi
      terraform state list > "$artifact_root/terraform-state-after-destroy.txt" 2>/dev/null || true
    )
  elif [[ ("$terraform_applied" == "1" || "$terraform_started" == "1") && "$keep_resources_on_failure" == "1" ]]; then
    echo "keeping AWS resources because --keep-resources-on-failure was supplied" >&2
  fi

  if [[ "$key_created" == "1" ]]; then
    aws ec2 delete-key-pair --profile "$aws_profile" --region "$aws_region" --key-name "$key_name" >/dev/null 2>&1
  fi
  rm -f "$key_path" "$tfvars_path" "$plan_path"
  rm -rf "$docker_context_dir"

  local instance_check vpc_check key_check ecr_check
  instance_check="$(aws ec2 describe-instances --profile "$aws_profile" --region "$aws_region" \
    --filters "Name=tag:Name,Values=$experiment_id-controller,$experiment_id-operator-*" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
    --query 'Reservations[].Instances[].InstanceId' --output text 2>/dev/null)"
  vpc_check="$(aws ec2 describe-vpcs --profile "$aws_profile" --region "$aws_region" \
    --filters "Name=tag:Name,Values=$experiment_id-vpc" \
    --query 'Vpcs[].VpcId' --output text 2>/dev/null)"
  key_check="$(aws ec2 describe-key-pairs --profile "$aws_profile" --region "$aws_region" \
    --key-names "$key_name" --query 'KeyPairs[].KeyName' --output text 2>/dev/null)"
  ecr_check="$(aws ecr describe-repositories --profile "$aws_profile" --region "$aws_region" \
    --repository-names "$ecr_repository_name" --query 'repositories[].repositoryUri' --output text 2>/dev/null)"

  jq -n \
    --arg instances "$instance_check" \
    --arg vpc "$vpc_check" \
    --arg key_pair "$key_check" \
    --arg ecr_repository "$ecr_check" \
    '{instances:$instances, vpc:$vpc, key_pair:$key_pair, ecr_repository:$ecr_repository}' \
    > "$artifact_root/cleanup-verification.json"

  write_manifest "$campaign_status" "$invalid_reason"
  exit "$exit_code"
}
trap cleanup EXIT

require_cmd aws
require_cmd terraform
require_cmd docker
require_cmd git
require_cmd jq
require_cmd ssh
require_cmd scp
require_cmd gzip
require_cmd rsync
require_cmd timeout

cd "$repo_root"

log "local preflight"
git_commit="$(git rev-parse --short=12 HEAD)"
local_image_uri="bloc-node:${git_commit}"
aws sts get-caller-identity --profile "$aws_profile" --output json > "$artifact_root/aws-caller-identity.json"

log "prebuild Docker image"
timeout 20 docker version --format '{{.Server.Version}}' >/dev/null
rm -rf "$docker_context_dir"
mkdir -p "$docker_context_dir"
rsync -a --delete \
  --exclude '.git' \
  --exclude '.gocache' \
  --exclude '.cache' \
  --exclude 'bin' \
  --exclude 'results' \
  --exclude 'results-*' \
  --exclude '.terraform' \
  --exclude 'terraform.tfstate*' \
  --exclude '*.tfplan' \
  --exclude '*.pem' \
  "$repo_root/bte" "$repo_root/sbc" "$repo_root/bloc-node" "$docker_context_dir/"
docker build -f "$docker_context_dir/bloc-node/Dockerfile" -t "$local_image_uri" "$docker_context_dir"

log "create temporary EC2 key pair"
aws ec2 create-key-pair \
  --profile "$aws_profile" \
  --region "$aws_region" \
  --key-name "$key_name" \
  --key-type rsa \
  --key-format pem \
  --output json \
  | jq -r '.KeyMaterial' > "$key_path"
chmod 600 "$key_path"
key_created="1"

log "prepare isolated Terraform workdir"
rm -rf "$terraform_work_dir"
mkdir -p "$terraform_work_dir"
cp "$terraform_dir"/{main.tf,outputs.tf,variables.tf,user-data.sh} "$terraform_work_dir/"
if [[ -f "$terraform_dir/.terraform.lock.hcl" ]]; then
  cp "$terraform_dir/.terraform.lock.hcl" "$terraform_work_dir/"
fi

admin_cidr_tf="$(json_escape_array "${admin_cidrs[@]}")"
create_ecr_repository="true"
create_iam_instance_profile="true"
if [[ "$image_distribution" == "ssh-load" ]]; then
  create_ecr_repository="false"
  create_iam_instance_profile="false"
fi
cat > "$tfvars_path" <<EOF
aws_region               = "$aws_region"
availability_zone        = "$availability_zone"
name_prefix              = "$experiment_id"
node_count               = $node_count
operator_instance_type   = "$operator_instance_type"
controller_instance_type = "$controller_instance_type"
create_ecr_repository    = $create_ecr_repository
create_iam_instance_profile = $create_iam_instance_profile
ecr_repository_name      = "$ecr_repository_name"
key_name                 = "$key_name"
admin_cidrs              = $admin_cidr_tf
EOF

log "terraform plan"
(
  cd "$terraform_work_dir"
  AWS_PROFILE="$aws_profile" terraform init -input=false
  terraform fmt
  terraform fmt -check -diff
  terraform validate
  AWS_PROFILE="$aws_profile" terraform plan -var-file="$tfvars_path" -out="$plan_path" -input=false
  terraform show -no-color "$plan_path" > "$artifact_root/terraform-plan.txt"
)

for forbidden in aws_nat_gateway aws_lb aws_eks_cluster aws_db_instance aws_eip aws_autoscaling_group; do
  if grep -q "$forbidden" "$artifact_root/terraform-plan.txt"; then
    invalid_reason="Terraform plan contains forbidden expensive resource: $forbidden"
    echo "$invalid_reason" >&2
    exit 1
  fi
done

if [[ "$auto_approve_plan" != "1" ]]; then
  echo "Terraform plan saved at: $artifact_root/terraform-plan.txt"
  echo "Expected only low-cost EC2/VPC/ECR/IAM resources."
  read -r -p "Type APPLY to create AWS resources for this A1 pilot: " answer
  if [[ "$answer" != "APPLY" ]]; then
    invalid_reason="operator declined terraform apply"
    exit 1
  fi
fi

log "terraform apply"
terraform_started="1"
(
  cd "$terraform_work_dir"
  AWS_PROFILE="$aws_profile" terraform apply -input=false "$plan_path"
  AWS_PROFILE="$aws_profile" terraform output -json inventory > "$script_dir/inventory.json"
  cp "$script_dir/inventory.json" "$artifact_root/inventory.json"
  AWS_PROFILE="$aws_profile" terraform state pull > "$artifact_root/terraform-state-after-apply.json"
)
terraform_applied="1"
if [[ "$image_distribution" == "ecr" ]]; then
  ecr_repository_url="$(cd "$terraform_work_dir" && AWS_PROFILE="$aws_profile" terraform output -raw ecr_repository_url)"
fi

if [[ "$image_distribution" == "ecr" ]]; then
  image_uri="${ecr_repository_url}:${git_commit}"
  log "tag Docker image"
  docker tag "$local_image_uri" "$image_uri"
else
  image_uri="$local_image_uri"
fi

if [[ "$image_distribution" == "ecr" ]]; then
  log "push Docker image"
  registry="${ecr_repository_url%%/*}"
  aws ecr get-login-password --profile "$aws_profile" --region "$aws_region" \
    | docker login --username AWS --password-stdin "$registry"
  docker push "$image_uri"
  image_digest="$(aws ecr describe-images --profile "$aws_profile" --region "$aws_region" \
    --repository-name "$ecr_repository_name" \
    --image-ids imageTag="$git_commit" \
    --query 'imageDetails[0].imageDigest' --output text)"
  jq -n \
    --arg image_distribution "$image_distribution" \
    --arg ecr_repository_url "$ecr_repository_url" \
    --arg docker_image_digest "$image_digest" \
    '{image_distribution:$image_distribution,ecr_repository_url:$ecr_repository_url,docker_image_digest:$docker_image_digest}' \
    > "$artifact_root/terraform-metadata.json"
else
  log "export Docker image"
  docker save -o "$image_tar_path" "$image_uri"
  jq -n \
    --arg image_distribution "$image_distribution" \
    --arg docker_image "$image_uri" \
    '{image_distribution:$image_distribution,docker_image:$docker_image}' \
    > "$artifact_root/terraform-metadata.json"
fi

log "generate deployment configs"
inventory_path="$script_dir/inventory.json"
controller_private_ip="$(jq -r '.controller.private_ip' "$inventory_path")"
controller_public_ip="$(jq -r '.controller.public_ip' "$inventory_path")"
write_prometheus_config "$inventory_path" "$script_dir/prometheus.ec2.yml"
(
  cd "$repo_root/bloc-node"
  GOCACHE="$PWD/.gocache" go run ./cmd/bloc-node gen-ec2-config \
    --inventory ../deploy/ec2/inventory.json \
    --cluster-out ../deploy/ec2/cluster.ec2.json \
    --remote-eval-out ../deploy/ec2/remote-eval.ec2.json \
    --cluster-id "$experiment_id" \
    --nodes "$node_count" \
    --bmax 128 \
    --prometheus-url "http://${controller_private_ip}:9090" \
    --grafana-url "http://${controller_private_ip}:3000" \
    --controller-url "$controller_private_ip"
)
cp "$script_dir/cluster.ec2.json" "$artifact_root/generated/cluster.ec2.json"
cp "$script_dir/remote-eval.ec2.json" "$artifact_root/generated/remote-eval.ec2.json"
cp "$script_dir/prometheus.ec2.yml" "$artifact_root/generated/prometheus.ec2.yml"

log "copy configs and start services"
mapfile -t public_hosts < <(jq -r '.controller.public_ip, (.nodes | sort_by(.id)[] | .public_ip)' "$inventory_path")
for host in "${public_hosts[@]}"; do
  ssh_ec2 "$host" "sudo mkdir -p /etc/bloc /opt/bloc/ec2 /opt/bloc/docker-compose/grafana && sudo chown -R ubuntu:ubuntu /opt/bloc /etc/bloc"
done

if [[ "$image_distribution" == "ssh-load" ]]; then
  log "load Docker image on EC2 hosts"
  for host in "${public_hosts[@]}"; do
    scp_ec2 "$image_tar_path" "ubuntu@$host:/opt/bloc/ec2/bloc-node-image.tar"
    ssh_ec2 "$host" "docker load -i /opt/bloc/ec2/bloc-node-image.tar"
  done
fi

jq -c '.nodes | sort_by(.id)[]' "$inventory_path" | while read -r node; do
  node_id="$(jq -r '.id' <<< "$node")"
  public_ip="$(jq -r '.public_ip' <<< "$node")"
  scp_ec2 "$script_dir/cluster.ec2.json" "ubuntu@$public_ip:/etc/bloc/cluster.json"
  scp_ec2 "$script_dir/operator-compose.yaml" "ubuntu@$public_ip:/opt/bloc/ec2/operator-compose.yaml"
  if [[ "$image_distribution" == "ecr" ]]; then
    ssh_ec2 "$public_ip" "set -e; aws ecr get-login-password --region '$aws_region' | docker login --username AWS --password-stdin '$registry'; cd /opt/bloc/ec2; NODE_ID='$node_id' BLOC_IMAGE='$image_uri' docker compose -f operator-compose.yaml up -d"
  else
    ssh_ec2 "$public_ip" "set -e; cd /opt/bloc/ec2; NODE_ID='$node_id' BLOC_IMAGE='$image_uri' docker compose -f operator-compose.yaml up -d"
  fi
done

scp_ec2 "$script_dir/controller-compose.yaml" "ubuntu@$controller_public_ip:/opt/bloc/ec2/controller-compose.yaml"
scp_ec2 "$script_dir/prometheus.ec2.yml" "ubuntu@$controller_public_ip:/opt/bloc/ec2/prometheus.ec2.yml"
scp_ec2 "$script_dir/remote-eval.ec2.json" "ubuntu@$controller_public_ip:/opt/bloc/ec2/remote-eval.ec2.json"
scp_ec2 -r "$repo_root/deploy/docker-compose/grafana/." "ubuntu@$controller_public_ip:/opt/bloc/docker-compose/grafana/"
ssh_ec2 "$controller_public_ip" "set -e; cd /opt/bloc/ec2; docker compose -f controller-compose.yaml up -d"

log "readiness checks"
jq -c '.nodes | sort_by(.id)[]' "$inventory_path" | while read -r node; do
  node_id="$(jq -r '.id' <<< "$node")"
  private_ip="$(jq -r '.private_ip' <<< "$node")"
  retry_ssh_ec2 "$controller_public_ip" 30 5 "operator $node_id /healthz"     "curl -fsS 'http://$private_ip:8000/healthz'"
  retry_ssh_ec2 "$controller_public_ip" 12 5 "operator $node_id /metrics"     "curl -fsS 'http://$private_ip:8000/metrics' | head -n 5"
done
retry_ssh_ec2 "$controller_public_ip" 12 5 "Prometheus targets API"   "curl -fsS http://127.0.0.1:9090/api/v1/targets > /opt/bloc/ec2/prometheus-targets-before.json"
scp_ec2 "ubuntu@$controller_public_ip:/opt/bloc/ec2/prometheus-targets-before.json" "$artifact_root/prometheus-targets-before.json"

log "pre-campaign network characterization"
collect_network_matrix "pre" "$artifact_root/network-pre.csv" "$controller_public_ip" "$inventory_path"

log "run A1 pilot scenarios"
for batch in "${batch_sizes[@]}"; do
  scenario_dir="/opt/bloc/ec2/results/$experiment_id/batch-$batch"
  if [[ "$image_distribution" == "ecr" ]]; then
    ssh_ec2 "$controller_public_ip" \
      "set -e; aws ecr get-login-password --region '$aws_region' | docker login --username AWS --password-stdin '$registry'; sudo mkdir -p '$scenario_dir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$image_uri' eval-remote --config remote-eval.ec2.json --experiment-id '${experiment_id}-b${batch}' --batch-size '$batch' --warmups '$warmups' --repetitions '$repetitions' --out-dir 'results/$experiment_id/batch-$batch' --image-tag '$image_uri' --git-commit '$git_commit' --timeout 30s"
  else
    ssh_ec2 "$controller_public_ip" \
      "set -e; sudo mkdir -p '$scenario_dir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$image_uri' eval-remote --config remote-eval.ec2.json --experiment-id '${experiment_id}-b${batch}' --batch-size '$batch' --warmups '$warmups' --repetitions '$repetitions' --out-dir 'results/$experiment_id/batch-$batch' --image-tag '$image_uri' --git-commit '$git_commit' --timeout 30s"
  fi
  mkdir -p "$artifact_root/scenarios/batch-$batch"
  scp_ec2 -r "ubuntu@$controller_public_ip:$scenario_dir" "$artifact_root/scenarios/batch-$batch/results"
done

log "post-campaign artifact collection"
collect_network_matrix "post" "$artifact_root/network-post.csv" "$controller_public_ip" "$inventory_path"
ssh_ec2 "$controller_public_ip" "curl -fsS http://127.0.0.1:9090/api/v1/targets > /opt/bloc/ec2/prometheus-targets-after.json"
scp_ec2 "ubuntu@$controller_public_ip:/opt/bloc/ec2/prometheus-targets-after.json" "$artifact_root/prometheus-targets.json"

jq -c '.nodes | sort_by(.id)[]' "$inventory_path" | while read -r node; do
  node_id="$(jq -r '.id' <<< "$node")"
  public_ip="$(jq -r '.public_ip' <<< "$node")"
  ssh_ec2 "$public_ip" "docker logs --tail=500 ec2-bloc-node-1 2>&1" > "$artifact_root/logs/operator-${node_id}.log"
done
ssh_ec2 "$controller_public_ip" "docker logs --tail=500 ec2-prometheus-1 2>&1" > "$artifact_root/logs/prometheus.log" || true
ssh_ec2 "$controller_public_ip" "docker logs --tail=500 ec2-grafana-1 2>&1" > "$artifact_root/logs/grafana.log" || true

merge_csv_outputs "$artifact_root"

if [[ "$skip_chart_generation" != "1" ]]; then
  log "generate charts"
  if [[ -x "$repo_root/latency-charts/.venv/bin/python" ]]; then
    (cd "$repo_root/latency-charts" && .venv/bin/python -m bloc_latency_charts "$artifact_root")
  else
    echo "latency-charts .venv not found; skipping chart generation" >&2
  fi
fi

campaign_status="complete"
write_manifest "complete" ""
