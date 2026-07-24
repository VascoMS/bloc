#!/usr/bin/env bash
# Host-local container resource sampler. It intentionally records only runtime
# counters and fixed campaign metadata; it never reads process arguments or
# shell variables from the sampled container.
set -Eeuo pipefail

RESOURCE_TIMESERIES_HEADER='timestamp,sample_index,node,region,scenario,phase,cpu_usage_us,memory_current_bytes,memory_peak_bytes,network_receive_bytes,network_transmit_bytes,restart_count,oom_killed'
interval_ms=250
max_samples=14400
container=''
output=''
stop_file=''
node=''
region=''
scenario=''
phase=''

usage() {
  printf '%s\n' 'Usage: sample-container-resources.sh run --container NAME --output PATH --stop-file PATH --node ID --region REGION --scenario NAME --phase resource-measured [--max-samples N]'
}

die() { printf 'error: %s\n' "$*" >&2; exit 2; }
trim() { sed 's/^[[:space:]]*//;s/[[:space:]]*$//'; }

bytes_from_human() {
  awk '
    function scale(unit) {
      if (unit == "B") return 1
      if (unit == "kB" || unit == "KB" || unit == "KiB") return 1024
      if (unit == "MB" || unit == "MiB") return 1024 * 1024
      if (unit == "GB" || unit == "GiB") return 1024 * 1024 * 1024
      if (unit == "TB" || unit == "TiB") return 1024 * 1024 * 1024 * 1024
      return -1
    }
    {
      number = $0; sub(/[A-Za-z]+$/, "", number)
      unit = $0; sub(/^[0-9.]+/, "", unit)
      if (number !~ /^[0-9.]+$/ || unit !~ /^[A-Za-z]+$/) exit 1
      factor = scale(unit); if (factor < 0) exit 1
      printf "%.0f\n", number * factor
    }'
}

container_pid() { docker inspect --format '{{.State.Pid}}' "$container"; }
container_state() { docker inspect --format '{{.State.Status}},{{.RestartCount}},{{.State.OOMKilled}}' "$container"; }

read_cgroup_sample() {
  local pid rel path cpu current peak usage
  pid="$(container_pid)" || return 1
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1
  rel="$(awk -F: '$1 == "0" && $2 == "" {print $3; exit}' "/proc/$pid/cgroup")"
  path="/sys/fs/cgroup${rel}"
  [[ -n "$rel" && -r "$path/cpu.stat" && -r "$path/memory.current" && -r "$path/memory.peak" ]] || return 1
  usage="$(awk '$1 == "usage_usec" {print $2; exit}' "$path/cpu.stat")"
  current="$(cat "$path/memory.current")"; peak="$(cat "$path/memory.peak")"
  [[ "$usage" =~ ^[0-9]+$ && "$current" =~ ^[0-9]+$ && "$peak" =~ ^[0-9]+$ ]] || return 1
  printf '%s,%s,%s\n' "$usage" "$current" "$peak"
}

read_docker_fallback_sample() {
  local pid ticks cpu stats memory net receive transmit current
  pid="$(container_pid)" || return 1
  [[ "$pid" =~ ^[1-9][0-9]*$ && -r "/proc/$pid/stat" ]] || return 1
  ticks="$(getconf CLK_TCK)"
  cpu="$(awk -v ticks="$ticks" '{printf "%.0f", (($14 + $15) * 1000000) / ticks}' "/proc/$pid/stat")"
  stats="$(docker stats --no-stream --format '{{.MemUsage}}|{{.NetIO}}' "$container")" || return 1
  memory="${stats%%|*}"; net="${stats#*|}"
  current="$(printf '%s' "${memory%%/*}" | trim | bytes_from_human)" || return 1
  receive="$(printf '%s' "${net%%/*}" | trim | bytes_from_human)" || return 1
  transmit="$(printf '%s' "${net#*/}" | trim | bytes_from_human)" || return 1
  [[ "$cpu" =~ ^[0-9]+$ && "$current" =~ ^[0-9]+$ && "$receive" =~ ^[0-9]+$ && "$transmit" =~ ^[0-9]+$ ]] || return 1
  printf '%s,%s,%s,%s,%s\n' "$cpu" "$current" "$current" "$receive" "$transmit"
}

read_network_counters() {
  local net receive transmit
  net="$(docker stats --no-stream --format '{{.NetIO}}' "$container")" || return 1
  receive="$(printf '%s' "${net%%/*}" | trim | bytes_from_human)" || return 1
  transmit="$(printf '%s' "${net#*/}" | trim | bytes_from_human)" || return 1
  printf '%s,%s\n' "$receive" "$transmit"
}

sample_once() {
  local index="$1" state cpu current peak counters receive transmit timestamp
  state="$(container_state)" || return 1
  IFS=, read -r status restart oom <<<"$state"
  [[ "$status" == running && "$restart" =~ ^[0-9]+$ && ( "$oom" == true || "$oom" == false ) ]] || return 1
  if values="$(read_cgroup_sample)"; then
    IFS=, read -r cpu current peak <<<"$values"
    counters="$(read_network_counters)" || return 1
    IFS=, read -r receive transmit <<<"$counters"
  else
    values="$(read_docker_fallback_sample)" || return 1
    IFS=, read -r cpu current peak receive transmit <<<"$values"
  fi
  timestamp="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "$timestamp" "$index" "$node" "$region" "$scenario" "$phase" "$cpu" "$current" "$peak" \
    "$receive" "$transmit" "$restart" "$oom" >>"$output"
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  run) ;;
  *) usage; exit 2 ;;
esac
shift
while [[ $# -gt 0 ]]; do
  case "$1" in
    --container) container="${2:-}"; shift 2 ;;
    --output) output="${2:-}"; shift 2 ;;
    --stop-file) stop_file="${2:-}"; shift 2 ;;
    --node) node="${2:-}"; shift 2 ;;
    --region) region="${2:-}"; shift 2 ;;
    --scenario) scenario="${2:-}"; shift 2 ;;
    --phase) phase="${2:-}"; shift 2 ;;
    --max-samples) max_samples="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done
[[ -n "$container" && -n "$output" && -n "$stop_file" && -n "$node" && -n "$region" && -n "$scenario" ]] || die 'container, output, stop-file, node, region, and scenario are required'
[[ "$phase" == resource-measured ]] || die 'phase must be resource-measured'
[[ "$max_samples" =~ ^[0-9]+$ && "$max_samples" -ge 2 ]] || die 'max-samples must be at least 2'
case "$node$region$scenario$phase" in *$'\n'*|*,*) die 'metadata must not contain commas or newlines' ;; esac
mkdir -p "$(dirname "$output")" "$(dirname "$stop_file")"
rm -f "$stop_file" "$output"
umask 077
printf '%s\n' "$RESOURCE_TIMESERIES_HEADER" >"$output"

for ((index = 0; index < max_samples; index++)); do
  [[ -e "$stop_file" ]] && exit 0
  sample_once "$index"
  [[ $((index + 1)) -lt "$max_samples" ]] && sleep 0.25
done
