#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT_ROOT="${BLOC_DEMO_OUT:-$ROOT/results/mvp-demo/$STAMP}"
BASE_PORT="${BLOC_DEMO_BASE_PORT:-30000}"
DEMO_GOCACHE="${GOCACHE:-$ROOT/.cache/go-build}"

cd "$ROOT"
mkdir -p "$OUT_ROOT"
mkdir -p "$DEMO_GOCACHE"

if (( BASE_PORT < 1024 || BASE_PORT > 45000 )); then
  echo "BLOC_DEMO_BASE_PORT must be between 1024 and 45000; got $BASE_PORT" >&2
  exit 2
fi

run_scenario() {
  local name="$1"
  local base="$2"
  shift 2
  local out="$OUT_ROOT/$name"
  echo
  echo "== $name =="
  GOCACHE="$DEMO_GOCACHE" \
    go run ./cmd/bloc-node eval-local \
      --nodes 4 \
      --batch-sizes 8 \
      --tx-size 256 \
      --bmax 16 \
      --timeout 30s \
      --base-port "$base" \
      --out-dir "$out" \
      --print summary \
      "$@"
}

run_scenario normal "$BASE_PORT"
run_scenario blockspace-cap "$((BASE_PORT + 2000))" --max-decrypted-txs 4
run_scenario withhold-share "$((BASE_PORT + 4000))" --fault 3:withhold-share

GOCACHE="$DEMO_GOCACHE" \
  go run ./cmd/bloc-node report \
    --dir "$OUT_ROOT" \
    --out "$OUT_ROOT/DEMO_REPORT.md"

echo
echo "Demo artifacts: $OUT_ROOT"
echo "Demo report: $OUT_ROOT/DEMO_REPORT.md"
