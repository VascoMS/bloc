#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$repo_root/scripts/lib/final-campaign-lifecycle.sh"

fixture="$(mktemp -d "${TMPDIR:-/tmp}/bloc-final-lifecycle.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT

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
  FINAL_BLOC_IMAGE=bloc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  FINAL_MEMPOOL_IMAGE=mempool@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  FINAL_PHASE=latency
  FINAL_SAMPLER=off
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
