#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$repo_root/scripts/lib/final-campaign-lifecycle.sh"

fixture="$(mktemp -d "${TMPDIR:-/tmp}/bloc-final-lifecycle.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT

empty_cleanup="$fixture/empty-cleanup.json"
nonempty_cleanup="$fixture/nonempty-cleanup.json"
printf '%s\n' '{"regions":{"us-east-1":{"query_succeeded":true,"instances":[],"volumes":[],"vpcs":[],"subnets":[],"security_groups":[],"route_tables":[],"key_pairs":[],"peering_connections":[]}},"iam":{"query_succeeded":true,"roles":[],"instance_profiles":[]},"terraform_state":[]}' >"$empty_cleanup"
printf '%s\n' '{"regions":{"us-east-1":{"query_succeeded":true,"instances":["i-leftover"],"volumes":[],"vpcs":[],"subnets":[],"security_groups":[],"route_tables":[],"key_pairs":[],"peering_connections":[]}},"iam":{"query_succeeded":true,"roles":[],"instance_profiles":[]},"terraform_state":[]}' >"$nonempty_cleanup"
final_assert_cleanup_empty "$empty_cleanup"
if final_assert_cleanup_empty "$nonempty_cleanup"; then
  echo "cleanup assertion accepted a retained instance" >&2
  exit 1
fi

make_fixture() {
  local root="$1"
  mkdir -p "$root/bundle/secrets"
  printf 'identity\n' >"$root/bundle/cluster-identity.json"
  printf 'crs\n' >"$root/bundle/cluster.crs"
  printf 'corpus\n' >"$root/bundle/encrypted-corpus.json"
  printf 'secret\n' >"$root/bundle/secrets/operator-0.json"
  chmod 600 "$root/bundle/secrets/operator-0.json"
  printf '{}\n' >"$root/bundle/bundle-manifest.json"
}

install_fakes() {
  FINAL_TEST_ROOT="$1"
  FINAL_EVENT_LOG="$FINAL_TEST_ROOT/events"
  : >"$FINAL_EVENT_LOG"
  FINAL_BUNDLE_ROOT="$FINAL_TEST_ROOT/bundle"
  FINAL_NODE_COUNT=4
  FINAL_TOPOLOGY=same-az
  FINAL_EXPERIMENT_ID=test-campaign
  FINAL_SOURCE_SHA=cccccccccccccccccccccccccccccccccccccccc
  FINAL_BLOC_IMAGE=bloc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  FINAL_MEMPOOL_IMAGE=mempool@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  FINAL_PHASE=latency
  FINAL_SAMPLER=off FINAL_WARMUPS=10 FINAL_REPETITIONS=1000 FINAL_BLOCKS=10
  FINAL_SEED=20260621 FINAL_DEADLINE=12s
  FINAL_FAIL_STAGE=""

  final_topology_prepare() { printf 'prepare\n' >>"$FINAL_EVENT_LOG"; }
  final_topology_apply() {
    printf 'apply\n' >>"$FINAL_EVENT_LOG"
    printf '{"controller":{"public_ip":"192.0.2.1"},"nodes":[{"id":0,"public_ip":"192.0.2.10"}]}\n' >"$1/inventory.json"
  }
  final_topology_key_for_host() { printf '%s/key.pem\n' "$FINAL_TEST_ROOT"; }
  final_topology_destroy() { printf 'destroy\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != cleanup ]]; }
  final_topology_verify_absent() { printf 'verify-absent\n' >>"$FINAL_EVENT_LOG"; printf '{}\n' >"$1/cleanup-topology.json"; }
  final_materialize_public() {
    printf 'materialize\n' >>"$FINAL_EVENT_LOG"
    mkdir -p "$1/generated-public"
    printf 'cluster\n' >"$1/generated-public/cluster.json"
    printf 'crs\n' >"$1/generated-public/cluster.crs"
    printf 'remote\n' >"$1/generated-public/remote-eval.json"
  }
  final_stage_hosts() { printf 'stage\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != checksum ]]; }
  final_pull_verify_images() { printf 'images\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != image ]]; }
  final_start_services() { printf 'start\n' >>"$FINAL_EVENT_LOG"; }
  final_health_gate() { printf 'health\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != health ]]; }
  final_sampler_start() { printf 'sampler-start\n' >>"$FINAL_EVENT_LOG"; }
  final_sampler_stop() { printf 'sampler-stop\n' >>"$FINAL_EVENT_LOG"; }
  final_execute_measurement() { printf 'measure\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != evaluator ]]; }
  final_recover_artifacts() { printf 'recover\n' >>"$FINAL_EVENT_LOG"; }
}

run_case() {
  local name="$1" phase="$2" sampler="$3" fail_stage="$4" expected="$5"
  local root="$fixture/$name"
  make_fixture "$root"
  install_fakes "$root"
  FINAL_PHASE="$phase" FINAL_SAMPLER="$sampler" FINAL_FAIL_STAGE="$fail_stage"
  status=0
  final_run_campaign_lifecycle "$root/artifacts" || status=$?
  [[ "$status" -eq "$expected" ]] || { echo "$name status=$status, want $expected" >&2; exit 1; }
  printf '%s\n' "$root"
}

success_root="$(run_case success latency off '' 0)"
[[ "$(tr '\n' ' ' <"$success_root/events")" == 'prepare apply materialize stage images start health measure recover destroy verify-absent ' ]]
! grep -q sampler "$success_root/events"

resource_root="$(run_case resource resource on '' 0)"
grep -Fq sampler-start "$resource_root/events"
grep -Fq sampler-stop "$resource_root/events"

for stage in checksum image health evaluator cleanup; do
  failed_root="$(run_case "failure-$stage" latency off "$stage" 1)"
  grep -Fq recover "$failed_root/events"
  grep -Fq destroy "$failed_root/events"
  grep -Fq verify-absent "$failed_root/events"
done

checksum_root="$fixture/failure-checksum"
! grep -Fq start "$checksum_root/events"
image_root="$fixture/failure-image"
! grep -Fq start "$image_root/events"

if find "$fixture" -path '*/artifacts/*' -type f \( -name '*secret*' -o -name 'operator-*.json' \) | grep -q .; then
  echo "private secret leaked into a public artifact root" >&2
  exit 1
fi

echo "final campaign lifecycle tests passed"

if [[ "${1:-}" == same-az ]]; then
  [[ -f "$repo_root/deploy/ec2/final-topology-same-az.sh" ]] || { echo "same-AZ adapter is missing" >&2; exit 1; }
  source "$repo_root/deploy/ec2/final-topology-same-az.sh"
  adapter_root="$fixture/same-az-adapter"
  mkdir -p "$adapter_root/bundle"
  FINAL_REPO_ROOT="$repo_root" FINAL_NODE_COUNT=4 FINAL_EXPERIMENT_ID=adapter-test
  FINAL_BUNDLE_ROOT="$adapter_root/bundle"
  FINAL_ADMIN_CIDR=127.0.0.1/32 FINAL_AWS_PROFILE=default
  FINAL_BLOC_IMAGE="123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:$(printf 'a%.0s' {1..64})"
  FINAL_MEMPOOL_IMAGE="123456789012.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:$(printf 'b%.0s' {1..64})"
  final_same_az_prepare_files "$adapter_root" 4 || exit 1
  tfvars="$adapter_root/generated-public/terraform/campaign.auto.tfvars"
  grep -Fq 'availability_zone = "us-east-1a"' "$tfvars"
  grep -Fq 'operator_instance_type = "t3.small"' "$tfvars"
  grep -Fq 'controller_instance_type = "t3.small"' "$tfvars"
  grep -Fq 'cpu_credits = "unlimited"' "$tfvars"
  grep -Fq 'arn:aws:ecr:us-east-1:123456789012:repository/bloc-node' "$tfvars"
  grep -Fq 'arn:aws:ecr:us-east-1:123456789012:repository/mempool-il' "$tfvars"
  echo "same-AZ adapter contract tests passed"
fi

if [[ "${1:-}" == three-region ]]; then
  adapter="$repo_root/deploy/ec2/final-topology-three-region.sh"
  [[ -f "$adapter" ]] || { echo "three-region adapter is missing" >&2; exit 1; }
  source "$adapter"
  adapter_root="$fixture/three-region-adapter"
  mkdir -p "$adapter_root/bundle"
  FINAL_REPO_ROOT="$repo_root" FINAL_NODE_COUNT=7 FINAL_EXPERIMENT_ID=adapter-test
  FINAL_BUNDLE_ROOT="$adapter_root/bundle"
  FINAL_ADMIN_CIDR=127.0.0.1/32 FINAL_AWS_PROFILE=default
  FINAL_BLOC_IMAGE="123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:$(printf 'a%.0s' {1..64})"
  FINAL_MEMPOOL_IMAGE="123456789012.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:$(printf 'b%.0s' {1..64})"
  final_three_region_prepare_files "$adapter_root" 7 || exit 1
  tfvars="$adapter_root/generated-public/terraform/campaign.auto.tfvars"
  grep -Fq 'primary_region = "us-east-1"' "$tfvars"
  grep -Fq 'secondary_region = "eu-west-1"' "$tfvars"
  grep -Fq 'tertiary_region = "eu-central-1"' "$tfvars"
  grep -Fq 'primary_availability_zone = "us-east-1a"' "$tfvars"
  grep -Fq 'secondary_availability_zone = "eu-west-1a"' "$tfvars"
  grep -Fq 'tertiary_availability_zone = "eu-central-1a"' "$tfvars"
  grep -Fq 'operator_instance_type = "t3.small"' "$tfvars"
  grep -Fq 'controller_instance_type = "t3.small"' "$tfvars"
  grep -Fq 'cpu_credits = "unlimited"' "$tfvars"
  grep -Fq 'arn:aws:ecr:us-east-1:123456789012:repository/bloc-node' "$tfvars"
  grep -Fq 'arn:aws:ecr:us-east-1:123456789012:repository/mempool-il' "$tfvars"
  [[ "$(grep -c '^resource "aws_vpc_peering_connection"' "$repo_root/deploy/ec2/terraform-three-region/main.tf")" -eq 3 ]]
  [[ "$(grep -c '^resource "aws_route"' "$repo_root/deploy/ec2/terraform-three-region/main.tf")" -eq 6 ]]

  inventory="$adapter_root/inventory.json"
  jq -n '{controller:{instance_type:"t3.small",region:"us-east-1",zone:"us-east-1a"},nodes:[range(0;7)|{id:.,instance_type:"t3.small",region:(["us-east-1","eu-west-1","eu-central-1"][.%3]),zone:"test-zone"}]}' >"$inventory"
  final_three_region_validate_inventory "$inventory" 7
  jq '.nodes[1].region="us-east-1"' "$inventory" >"$inventory.invalid"
  if final_three_region_validate_inventory "$inventory.invalid" 7; then
    echo "three-region inventory validator accepted invalid id-to-region placement" >&2
    exit 1
  fi
  [[ "$(final_topology_key_for_host '{"region":"us-east-1"}')" == "$FINAL_THREE_REGION_PRIMARY_KEY_PATH" ]]
  [[ "$(final_topology_key_for_host '{"region":"eu-west-1"}')" == "$FINAL_THREE_REGION_SECONDARY_KEY_PATH" ]]
  [[ "$(final_topology_key_for_host '{"region":"eu-central-1"}')" == "$FINAL_THREE_REGION_TERTIARY_KEY_PATH" ]]

  final_three_region_query_array() {
    local query="$1"
    shift
    if [[ "$query" == 'Reservations[].Instances[].InstanceId' && " $* " == *' --region eu-west-1 '* ]]; then
      printf '["i-eu-west-leftover"]\n'
    else
      printf '[]\n'
    fi
  }
  cleanup_record="$(final_three_region_region_cleanup eu-west-1 "$FINAL_THREE_REGION_SECONDARY_KEY_NAME")"
  jq -e '.query_succeeded == true and .instances == ["i-eu-west-leftover"] and .vpcs == [] and .peering_connections == []' <<<"$cleanup_record" >/dev/null
  echo "three-region adapter contract tests passed"
fi
