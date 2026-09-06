#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runner="$repo_root/deploy/ec2/run-final-campaign.sh"
same_wrapper="$repo_root/deploy/ec2/run-same-az-campaign.sh"
three_wrapper="$repo_root/deploy/ec2/run-three-region-campaign.sh"
fixture="$(mktemp -d "${TMPDIR:-/tmp}/bloc-final-contract.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT

source_sha="$(git -C "$repo_root" rev-parse HEAD)"
bloc_image="123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
mempool_image="123456789012.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
source "$repo_root/scripts/lib/final-campaign-contract.sh"
if final_validate_ecr_image "${bloc_image/us-east-1/eu-west-1}"; then
  echo "final image validator accepted a non-us-east-1 registry" >&2
  exit 1
fi
mkdir -p "$fixture/n4" "$fixture/n7" "$fixture/fake-bin"
printf '{"version":"bloc-campaign-bundle-v1","source_sha":"%s","bloc_image":"%s","mempool_image":"%s","n":4,"threshold":3,"bmax":128}\n' \
  "$source_sha" "$bloc_image" "$mempool_image" >"$fixture/n4/bundle-manifest.json"
printf '{"version":"bloc-campaign-bundle-v1","source_sha":"%s","bloc_image":"%s","mempool_image":"%s","n":7,"threshold":5,"bmax":128}\n' \
  "$source_sha" "$bloc_image" "$mempool_image" >"$fixture/n7/bundle-manifest.json"

call_log="$fixture/calls.log"
: >"$call_log"
for command in aws terraform docker ssh scp rsync; do
  printf '#!/bin/sh\nprintf "%%s %%s\\n" "%s" "$*" >>"%s"\nexit 99\n' "$command" "$call_log" >"$fixture/fake-bin/$command"
  chmod +x "$fixture/fake-bin/$command"
done

common_args=(
  --source-sha "$source_sha"
  --bloc-image "$bloc_image"
  --mempool-image "$mempool_image"
  --experiment-id contract-test
  --admin-cidr 127.0.0.1/32
  --aws-profile default
)

expect_success() {
  PATH="$fixture/fake-bin:$PATH" "$@" >"$fixture/stdout" 2>"$fixture/stderr" || {
    cat "$fixture/stderr" >&2
    return 1
  }
}

expect_failure() {
  if PATH="$fixture/fake-bin:$PATH" "$@" >"$fixture/stdout" 2>"$fixture/stderr"; then
    echo "expected failure: $*" >&2
    return 1
  fi
}

expect_success "$runner" --topology same-az --phase readiness-pilot --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --validate-only
grep -Fq 'warmups=1 repetitions=3 blocks=1 sampler=off batches=8,32,128 seed=20260621 deadline=12s' "$fixture/stdout"
max_experiment_id="$(printf 'x%.0s' {1..47})"
too_long_experiment_id="${max_experiment_id}x"
expect_success "$runner" --topology same-az --phase readiness-pilot --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]/contract-test/$max_experiment_id}" --validate-only
expect_failure "$runner" --topology same-az --phase readiness-pilot --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]/contract-test/$too_long_experiment_id}" --validate-only
grep -Fq 'experiment id must be at most 47 characters' "$fixture/stderr"
expect_success "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --validate-only
grep -Fq 'warmups=10 repetitions=1000 blocks=10 sampler=off batches=8,32,128 seed=20260621 deadline=12s' "$fixture/stdout"
expect_success "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" \
  --acs-trace-schema bloc-acs-trace/v1 --validate-only
grep -Fq 'warmups=5 repetitions=30 blocks=3 sampler=off batches=8,32,128 seed=20260621 deadline=12s' "$fixture/stdout"
grep -Fq 'acs_trace_schema=bloc-acs-trace/v1' "$fixture/stdout"
grep -Fq 'stream_mode=fresh' "$fixture/stdout"
expect_success "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" \
  --stream-mode persistent --acs-trace-schema bloc-acs-trace/v2 --validate-only
grep -Fq 'stream_mode=persistent' "$fixture/stdout"
grep -Fq 'acs_trace_schema=bloc-acs-trace/v2' "$fixture/stdout"
expect_success "$runner" --topology three-region --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" \
  --stream-mode persistent --acs-trace-schema bloc-acs-trace/v3 --validate-only
grep -Fq 'stream_mode=persistent' "$fixture/stdout"
grep -Fq 'acs_trace_schema=bloc-acs-trace/v3' "$fixture/stdout"
expect_success "$runner" --topology three-region --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" \
  --stream-mode persistent-lanes --acs-trace-schema bloc-acs-trace/v3 --validate-only
grep -Fq 'warmups=5 repetitions=30 blocks=3 sampler=off batches=8,32,128 seed=20260621 deadline=12s' "$fixture/stdout"
grep -Fq 'stream_mode=persistent-lanes' "$fixture/stdout"
grep -Fq 'acs_trace_schema=bloc-acs-trace/v3' "$fixture/stdout"
expect_success "$runner" --topology three-region --phase resource --bundle-root "$fixture/n7" --node-count 7 "${common_args[@]}" --validate-only
grep -Fq 'warmups=0 repetitions=1000 blocks=10 sampler=on batches=8,32,128 seed=20260621 deadline=12s' "$fixture/stdout"

expect_success "$same_wrapper" --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --validate-only
grep -Fq 'topology=same-az' "$fixture/stdout"
expect_success "$three_wrapper" --phase latency --bundle-root "$fixture/n7" --node-count 7 "${common_args[@]}" --validate-only
grep -Fq 'topology=three-region' "$fixture/stdout"
expect_success "$same_wrapper" --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" \
  --acs-trace-schema bloc-acs-trace/v1 --validate-only
expect_success "$three_wrapper" --phase latency --bundle-root "$fixture/n7" --node-count 7 "${common_args[@]}" \
  --acs-trace-schema bloc-acs-trace/v1 --validate-only

expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]/$source_sha/0000000000000000000000000000000000000000}" --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]/$bloc_image/$mempool_image}" --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 --source-sha "$source_sha" --bloc-image bloc-node:latest --mempool-image "$mempool_image" --experiment-id invalid --admin-cidr 127.0.0.1/32 --aws-profile default --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]/us-east-1/eu-west-1}" --validate-only
expect_failure "$runner" --topology same-az --phase readiness-pilot --bundle-root "$fixture/n7" --node-count 7 "${common_args[@]}" --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 10 "${common_args[@]}" --validate-only
expect_failure "$runner" --topology same-az --phase extension-pilot --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --warmups 1 --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}"
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --validate-only --execute-live
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --unknown value --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --acs-trace-schema bloc-acs-trace/v999 --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --stream-mode reuse --acs-trace-schema bloc-acs-trace/v2 --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --stream-mode persistent --acs-trace-schema bloc-acs-trace/v1 --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --stream-mode persistent-lanes --acs-trace-schema bloc-acs-trace/v2 --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --stream-mode persistent-lanes --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --stream-mode persistent --acs-trace-schema bloc-acs-trace/v3 --validate-only
expect_failure "$runner" --topology three-region --phase latency --bundle-root "$fixture/n7" --node-count 7 "${common_args[@]}" --stream-mode persistent-lanes --acs-trace-schema bloc-acs-trace/v3 --validate-only
expect_failure "$runner" --topology three-region --phase readiness-pilot --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --stream-mode persistent-lanes --acs-trace-schema bloc-acs-trace/v3 --validate-only
expect_failure "$runner" --topology three-region --phase latency --bundle-root "$fixture/n4" --node-count 4 "${common_args[@]}" --stream-mode fresh --acs-trace-schema bloc-acs-trace/v3 --validate-only
expect_failure "$runner" --topology same-az --phase latency --bundle-root

[[ ! -s "$call_log" ]] || { echo "validate-only invoked lifecycle tools" >&2; cat "$call_log" >&2; exit 1; }
echo "final campaign contract tests passed"
