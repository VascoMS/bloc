#!/usr/bin/env bash

final_campaign_usage() {
  cat <<'EOF'
Usage: run-final-campaign.sh --topology same-az|three-region
  --phase readiness-pilot|latency|resource|extension-pilot|extension-full|extension-boundary
  --bundle-root DIR --node-count 4|7|10 --source-sha SHA
  --bloc-image ECR@DIGEST --mempool-image ECR@DIGEST
  --experiment-id ID --admin-cidr CIDR --aws-profile PROFILE
  [--batch-size 8|32|128|512]
  [--stream-mode fresh|persistent|persistent-lanes]
  [--acs-trace-schema bloc-acs-trace/v1|bloc-acs-trace/v2|bloc-acs-trace/v3]
  [--validate-only | --execute-live]
EOF
}

final_die() {
  printf '%s\n' "$*" >&2
  return 2
}

final_take_value() {
  [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || final_die "$1 requires a value"
}

final_parse_campaign_args() {
  FINAL_TOPOLOGY="" FINAL_PHASE="" FINAL_BUNDLE_ROOT="" FINAL_NODE_COUNT=""
  FINAL_SOURCE_SHA="" FINAL_BLOC_IMAGE="" FINAL_MEMPOOL_IMAGE=""
  FINAL_EXPERIMENT_ID="" FINAL_ADMIN_CIDR="" FINAL_AWS_PROFILE=""
  FINAL_ACS_TRACE_SCHEMA="" FINAL_STREAM_MODE=fresh FINAL_BATCH_SIZE=""
  FINAL_VALIDATE_ONLY=0 FINAL_EXECUTE_LIVE=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --topology) final_take_value "$1" "${2-}" || return; FINAL_TOPOLOGY="$2"; shift 2 ;;
      --phase) final_take_value "$1" "${2-}" || return; FINAL_PHASE="$2"; shift 2 ;;
      --bundle-root) final_take_value "$1" "${2-}" || return; FINAL_BUNDLE_ROOT="$2"; shift 2 ;;
      --node-count) final_take_value "$1" "${2-}" || return; FINAL_NODE_COUNT="$2"; shift 2 ;;
      --source-sha) final_take_value "$1" "${2-}" || return; FINAL_SOURCE_SHA="$2"; shift 2 ;;
      --bloc-image) final_take_value "$1" "${2-}" || return; FINAL_BLOC_IMAGE="$2"; shift 2 ;;
      --mempool-image) final_take_value "$1" "${2-}" || return; FINAL_MEMPOOL_IMAGE="$2"; shift 2 ;;
      --experiment-id) final_take_value "$1" "${2-}" || return; FINAL_EXPERIMENT_ID="$2"; shift 2 ;;
      --admin-cidr) final_take_value "$1" "${2-}" || return; FINAL_ADMIN_CIDR="$2"; shift 2 ;;
      --aws-profile) final_take_value "$1" "${2-}" || return; FINAL_AWS_PROFILE="$2"; shift 2 ;;
      --batch-size) final_take_value "$1" "${2-}" || return; FINAL_BATCH_SIZE="$2"; shift 2 ;;
      --stream-mode) final_take_value "$1" "${2-}" || return; FINAL_STREAM_MODE="$2"; shift 2 ;;
      --acs-trace-schema) final_take_value "$1" "${2-}" || return; FINAL_ACS_TRACE_SCHEMA="$2"; shift 2 ;;
      --validate-only) FINAL_VALIDATE_ONLY=1; shift ;;
      --execute-live) FINAL_EXECUTE_LIVE=1; shift ;;
      -h|--help) final_campaign_usage; return 64 ;;
      *) final_die "unknown argument: $1"; return ;;
    esac
  done
}

final_validate_ecr_image() {
  [[ "$1" =~ ^[0-9]{12}\.dkr\.ecr\.us-east-1\.amazonaws\.com/[a-z0-9][a-z0-9._/-]*@sha256:[0-9a-f]{64}$ ]]
}

final_validate_campaign_contract() {
  local repo_root="$1" manifest="$FINAL_BUNDLE_ROOT/bundle-manifest.json" expected_bmax=128 expected_threshold=""
  [[ "$FINAL_TOPOLOGY" == same-az || "$FINAL_TOPOLOGY" == three-region ]] || final_die "topology must be same-az or three-region" || return
  [[ "$FINAL_NODE_COUNT" == 4 || "$FINAL_NODE_COUNT" == 7 || "$FINAL_NODE_COUNT" == 10 ]] || final_die "node count must be 4, 7, or 10" || return
  [[ "$FINAL_SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]] || final_die "source SHA must be 40 lowercase hexadecimal characters" || return
  final_validate_ecr_image "$FINAL_BLOC_IMAGE" || final_die "bloc image must use a private ECR digest" || return
  final_validate_ecr_image "$FINAL_MEMPOOL_IMAGE" || final_die "mempool image must use a private ECR digest" || return
  [[ "$FINAL_EXPERIMENT_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || final_die "invalid experiment id" || return
  [[ ${#FINAL_EXPERIMENT_ID} -le 47 ]] || final_die "experiment id must be at most 47 characters" || return
  [[ "$FINAL_ADMIN_CIDR" == */* ]] || final_die "admin CIDR is required" || return
  [[ -n "$FINAL_AWS_PROFILE" ]] || final_die "AWS profile is required" || return
  [[ "$FINAL_STREAM_MODE" == fresh || "$FINAL_STREAM_MODE" == persistent || "$FINAL_STREAM_MODE" == persistent-lanes ]] || final_die "stream mode must be fresh, persistent, or persistent-lanes" || return
  [[ -z "$FINAL_ACS_TRACE_SCHEMA" || "$FINAL_ACS_TRACE_SCHEMA" == bloc-acs-trace/v1 || "$FINAL_ACS_TRACE_SCHEMA" == bloc-acs-trace/v2 || "$FINAL_ACS_TRACE_SCHEMA" == bloc-acs-trace/v3 ]] || final_die "unsupported ACS trace schema" || return
  [[ "$FINAL_STREAM_MODE" != persistent || "$FINAL_ACS_TRACE_SCHEMA" == bloc-acs-trace/v2 || "$FINAL_ACS_TRACE_SCHEMA" == bloc-acs-trace/v3 ]] || final_die "persistent stream mode requires ACS trace schema bloc-acs-trace/v2 or bloc-acs-trace/v3" || return
  [[ "$FINAL_STREAM_MODE" != persistent-lanes || -z "$FINAL_ACS_TRACE_SCHEMA" || "$FINAL_ACS_TRACE_SCHEMA" == bloc-acs-trace/v3 ]] || final_die "persistent-lanes stream mode requires trace-off operation or ACS trace schema bloc-acs-trace/v3" || return
  if [[ "$FINAL_ACS_TRACE_SCHEMA" == bloc-acs-trace/v3 ]]; then
    [[ "$FINAL_TOPOLOGY" == three-region && "$FINAL_PHASE" == latency && "$FINAL_NODE_COUNT" == 4 && ( "$FINAL_STREAM_MODE" == persistent || "$FINAL_STREAM_MODE" == persistent-lanes ) ]] ||
      final_die "bloc-acs-trace/v3 is restricted to the n=4 three-region persistent/persistent-lanes latency diagnostic" || return
  fi
  [[ $((FINAL_VALIDATE_ONLY + FINAL_EXECUTE_LIVE)) -eq 1 ]] || final_die "choose exactly one of --validate-only and --execute-live" || return
  [[ "$(git -C "$repo_root" rev-parse HEAD)" == "$FINAL_SOURCE_SHA" ]] || final_die "source SHA does not match local HEAD" || return

  FINAL_BATCHES=8,32,128 FINAL_SEED=20260621 FINAL_DEADLINE=12s
  case "$FINAL_PHASE" in
    readiness-pilot)
      [[ -z "$FINAL_BATCH_SIZE" ]] || final_die "batch size is supported only for extension phases" || return
      [[ "$FINAL_NODE_COUNT" == 4 ]] || final_die "readiness pilot is restricted to n=4" || return
      FINAL_WARMUPS=1 FINAL_REPETITIONS=3 FINAL_BLOCKS=1 FINAL_SAMPLER=off
      ;;
    latency)
      [[ -z "$FINAL_BATCH_SIZE" ]] || final_die "batch size is supported only for extension phases" || return
      [[ "$FINAL_NODE_COUNT" == 4 || "$FINAL_NODE_COUNT" == 7 ]] || final_die "latency is restricted to the n=4/n=7 primary matrix" || return
      FINAL_WARMUPS=10 FINAL_REPETITIONS=1000 FINAL_BLOCKS=10 FINAL_SAMPLER=off
      ;;
    resource)
      [[ -z "$FINAL_BATCH_SIZE" ]] || final_die "batch size is supported only for extension phases" || return
      [[ "$FINAL_NODE_COUNT" == 4 || "$FINAL_NODE_COUNT" == 7 ]] || final_die "resource is restricted to the n=4/n=7 primary matrix" || return
      FINAL_WARMUPS=0 FINAL_REPETITIONS=1000 FINAL_BLOCKS=10 FINAL_SAMPLER=on
      ;;
    extension-pilot|extension-full|extension-boundary)
      [[ "$FINAL_BATCH_SIZE" == 8 || "$FINAL_BATCH_SIZE" == 32 || "$FINAL_BATCH_SIZE" == 128 || "$FINAL_BATCH_SIZE" == 512 ]] ||
        final_die "extension phase requires batch size 8, 32, 128, or 512" || return
      [[ "$FINAL_NODE_COUNT" == 10 || "$FINAL_BATCH_SIZE" == 512 ]] ||
        final_die "extension cells require n=10 or batch size 512" || return
      [[ "$FINAL_STREAM_MODE" == persistent-lanes && -z "$FINAL_ACS_TRACE_SCHEMA" ]] ||
        final_die "extension phases require trace-off persistent-lanes" || return
      FINAL_BATCHES="$FINAL_BATCH_SIZE" FINAL_SAMPLER=off
      [[ "$FINAL_BATCH_SIZE" != 512 ]] || expected_bmax=512
      if [[ "$FINAL_PHASE" == extension-pilot ]]; then
        FINAL_WARMUPS=5 FINAL_REPETITIONS=30 FINAL_BLOCKS=3
      elif [[ "$FINAL_PHASE" == extension-full ]]; then
        FINAL_WARMUPS=10 FINAL_REPETITIONS=1000 FINAL_BLOCKS=10
      else
        FINAL_WARMUPS=10 FINAL_REPETITIONS=100 FINAL_BLOCKS=10
      fi
      ;;
    *) final_die "unsupported campaign phase"; return ;;
  esac
  case "$FINAL_NODE_COUNT" in
    4) expected_threshold=3 ;;
    7) expected_threshold=5 ;;
    10) expected_threshold=7 ;;
  esac
  [[ -f "$manifest" ]] || final_die "bundle manifest is missing" || return
  jq -e --arg source "$FINAL_SOURCE_SHA" --arg bloc "$FINAL_BLOC_IMAGE" --arg mempool "$FINAL_MEMPOOL_IMAGE" \
    --argjson n "$FINAL_NODE_COUNT" --argjson threshold "$expected_threshold" --argjson bmax "$expected_bmax" '
    .version == "bloc-campaign-bundle-v1" and .source_sha == $source and
    .bloc_image == $bloc and .mempool_image == $mempool and .n == $n and
    .threshold == $threshold and .bmax == $bmax
  ' "$manifest" >/dev/null || final_die "bundle identities do not match invocation" || return
  if [[ -n "$FINAL_ACS_TRACE_SCHEMA" ]]; then
    if [[ "$FINAL_PHASE" == latency ]]; then
      FINAL_WARMUPS=5 FINAL_REPETITIONS=30 FINAL_BLOCKS=3 FINAL_SAMPLER=off
    elif [[ "$FINAL_PHASE" != readiness-pilot ]]; then
      final_die "ACS diagnostics are supported only for readiness-pilot or latency"; return
    fi
  fi
}

final_print_campaign_contract() {
  printf 'final campaign contract valid: topology=%s phase=%s n=%s\n' "$FINAL_TOPOLOGY" "$FINAL_PHASE" "$FINAL_NODE_COUNT"
  printf 'warmups=%s repetitions=%s blocks=%s sampler=%s batches=%s seed=%s deadline=%s\n' \
    "$FINAL_WARMUPS" "$FINAL_REPETITIONS" "$FINAL_BLOCKS" "$FINAL_SAMPLER" "$FINAL_BATCHES" "$FINAL_SEED" "$FINAL_DEADLINE"
  printf 'acs_trace_schema=%s\n' "${FINAL_ACS_TRACE_SCHEMA:-disabled}"
  printf 'stream_mode=%s\n' "$FINAL_STREAM_MODE"
}
