#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"

usage() {
  cat <<'EOF'
Usage: run-same-az-campaign.sh --source-sha SHA --bloc-image NAME@sha256:DIGEST
  --mempool-image NAME@sha256:DIGEST --n4-config PATH --n4-corpus PATH
  --n7-config PATH --n7-corpus PATH --validate-only

This runner validates the issue-15 primary contract without contacting AWS.
Live execution remains disabled until image distribution and authenticated
cleanup are implemented and separately authorized.
EOF
}

source_sha="" bloc_image="" mempool_image=""
n4_config="" n4_corpus="" n7_config="" n7_corpus=""
node_counts="4,7" batch_sizes="8,32,128" warmups="10" repetitions="1000"
repetition_blocks="10" seed="20260621" deadline="12s" validate_only="0"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source-sha) source_sha="$2"; shift 2 ;;
    --bloc-image) bloc_image="$2"; shift 2 ;;
    --mempool-image) mempool_image="$2"; shift 2 ;;
    --n4-config) n4_config="$2"; shift 2 ;;
    --n4-corpus) n4_corpus="$2"; shift 2 ;;
    --n7-config) n7_config="$2"; shift 2 ;;
    --n7-corpus) n7_corpus="$2"; shift 2 ;;
    --node-counts) node_counts="$2"; shift 2 ;;
    --batch-sizes) batch_sizes="$2"; shift 2 ;;
    --warmups) warmups="$2"; shift 2 ;;
    --repetitions) repetitions="$2"; shift 2 ;;
    --repetition-blocks) repetition_blocks="$2"; shift 2 ;;
    --seed) seed="$2"; shift 2 ;;
    --deadline) deadline="$2"; shift 2 ;;
    --validate-only) validate_only="1"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || { echo "--source-sha must be a 40-character commit" >&2; exit 2; }
for image in "$bloc_image" "$mempool_image"; do
  [[ "$image" =~ @sha256:[0-9a-f]{64}$ ]] || { echo "images must use immutable sha256 digests" >&2; exit 2; }
done
[[ "$node_counts" == "4,7" ]] || { echo "primary node counts must be 4,7" >&2; exit 2; }
[[ "$batch_sizes" == "8,32,128" ]] || { echo "primary batch sizes must be 8,32,128" >&2; exit 2; }
[[ "$warmups/$repetitions/$repetition_blocks/$seed/$deadline" == "10/1000/10/20260621/12s" ]] ||
  { echo "primary schedule must be 10/1000/10/20260621/12s" >&2; exit 2; }
[[ "$(git -C "$repo_root" rev-parse HEAD)" == "$source_sha" ]] ||
  { echo "source SHA does not match local HEAD" >&2; exit 2; }

validate_pair() {
  local expected_n="$1" expected_t="$2" config="$3" corpus="$4"
  [[ -f "$config" && -f "$corpus" ]] || { echo "missing n=$expected_n config or corpus" >&2; exit 2; }
  jq -e --argjson n "$expected_n" --argjson t "$expected_t" '
    .version=="bloc-cluster-v3" and .n==$n and .threshold==$t and .bmax==128 and
    .provider.mode=="mempool-http" and .provider.require_exact_count==true
  ' "$config" >/dev/null
  jq -e '
    .schema_version=="bloc-encrypted-corpus-v1" and
    .ciphertext_wire_version=="bte-tx-v2" and .bmax==128 and
    .available_count>=128 and
    (.encrypted_prefix_set_ids["8"]|length)>0 and
    (.encrypted_prefix_set_ids["32"]|length)>0 and
    (.encrypted_prefix_set_ids["128"]|length)>0
  ' "$corpus" >/dev/null
  local config_public corpus_public config_plain corpus_plain config_encrypted corpus_encrypted
  config_public="$(jq -r .provider.expected_public_config_id "$config")"
  corpus_public="$(jq -r .public_config_id "$corpus")"
  config_plain="$(jq -r .provider.expected_plaintext_master_corpus_id "$config")"
  corpus_plain="$(jq -r .plaintext_master_corpus_id "$corpus")"
  config_encrypted="$(jq -r .provider.expected_encrypted_corpus_id "$config")"
  corpus_encrypted="$(jq -r .encrypted_corpus_id "$corpus")"
  [[ "$config_public/$config_plain/$config_encrypted" == "$corpus_public/$corpus_plain/$corpus_encrypted" ]] ||
    { echo "n=$expected_n config/corpus identities differ" >&2; exit 2; }
  for size in 8 32 128; do
    local config_prefix corpus_prefix
    config_prefix="$(jq -r --arg size "$size" '.provider.expected_encrypted_prefix_set_ids[$size]' "$config")"
    corpus_prefix="$(jq -r --arg size "$size" '.encrypted_prefix_set_ids[$size]' "$corpus")"
    [[ "$config_prefix" == "$corpus_prefix" ]] ||
      { echo "n=$expected_n prefix $size identity differs" >&2; exit 2; }
  done
}

validate_pair 4 3 "$n4_config" "$n4_corpus"
validate_pair 7 5 "$n7_config" "$n7_corpus"

if [[ "$validate_only" == "1" ]]; then
  echo "same-AZ campaign contract valid: us-east-1a, t3.small, n=4/7, batches=8/32/128"
  echo "latency and resource phases must be launched separately; n=10/batch-512 remain extension-only"
  exit 0
fi

echo "live AWS execution is intentionally unavailable in this refactor checkpoint" >&2
echo "implement and validate immutable two-image/corpus distribution plus authenticated cleanup before authorization" >&2
exit 3
