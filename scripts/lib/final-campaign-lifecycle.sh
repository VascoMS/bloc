#!/usr/bin/env bash

final_lifecycle_event() {
  local root="$1" event="$2" status="$3"
  printf '{"event":"%s","status":"%s","time":"%s"}\n' "$event" "$status" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$root/lifecycle.jsonl"
}

final_assert_cleanup_empty() {
  jq -e '([.regions[][] | select(type == "array") | length] + [(.iam.roles | length), (.iam.instance_profiles | length), (.terraform_state | length)]) | add == 0' "$1" >/dev/null
}

final_run_campaign_lifecycle() {
  local artifact_root="$1" status=0 sampler_started=0 old_ifs region
  local cleanup_args=()
  [[ ! -e "$artifact_root" ]] || { echo "artifact root already exists: $artifact_root" >&2; return 1; }
  mkdir -p "$artifact_root/generated-public" "$artifact_root/scenarios" "$artifact_root/logs"
  : >"$artifact_root/lifecycle.jsonl"
  jq '{version,source_sha,bloc_image,mempool_image,n,threshold,bmax,public_config_id,encrypted_corpus_id,file_sha256}' \
    "$FINAL_BUNDLE_ROOT/bundle-manifest.json" >"$artifact_root/frozen-inputs.json"

  if final_topology_prepare "$artifact_root" "$FINAL_NODE_COUNT"; then
    final_lifecycle_event "$artifact_root" topology-prepare ok
  else
    final_lifecycle_event "$artifact_root" topology-prepare failed; status=1
  fi
  if [[ "$status" -eq 0 ]] && final_topology_apply "$artifact_root"; then
    final_lifecycle_event "$artifact_root" topology-apply ok
  elif [[ "$status" -eq 0 ]]; then
    final_lifecycle_event "$artifact_root" topology-apply failed; status=1
  fi
  if [[ "$status" -eq 0 ]] && final_materialize_public "$artifact_root"; then
    final_lifecycle_event "$artifact_root" materialize ok
  elif [[ "$status" -eq 0 ]]; then
    final_lifecycle_event "$artifact_root" materialize failed; status=1
  fi
  if [[ "$status" -eq 0 ]] && final_stage_hosts "$artifact_root"; then
    final_lifecycle_event "$artifact_root" stage ok
  elif [[ "$status" -eq 0 ]]; then
    final_lifecycle_event "$artifact_root" stage failed; status=1
  fi
  if [[ "$status" -eq 0 ]] && final_pull_verify_images "$artifact_root"; then
    final_lifecycle_event "$artifact_root" images ok
  elif [[ "$status" -eq 0 ]]; then
    final_lifecycle_event "$artifact_root" images failed; status=1
  fi
  if [[ "$status" -eq 0 ]] && final_start_services "$artifact_root"; then
    final_lifecycle_event "$artifact_root" services ok
  elif [[ "$status" -eq 0 ]]; then
    final_lifecycle_event "$artifact_root" services failed; status=1
  fi
  if [[ "$status" -eq 0 ]] && final_health_gate "$artifact_root"; then
    final_lifecycle_event "$artifact_root" health ok
  elif [[ "$status" -eq 0 ]]; then
    final_lifecycle_event "$artifact_root" health failed; status=1
  fi
  if [[ "$status" -eq 0 && "$FINAL_SAMPLER" == on ]]; then
    if final_sampler_start "$artifact_root"; then
      sampler_started=1
      final_lifecycle_event "$artifact_root" sampler-start ok
    else
      final_lifecycle_event "$artifact_root" sampler-start failed; status=1
    fi
  fi
  if [[ "$status" -eq 0 ]] && final_execute_measurement "$artifact_root"; then
    final_lifecycle_event "$artifact_root" measurement ok
  elif [[ "$status" -eq 0 ]]; then
    final_lifecycle_event "$artifact_root" measurement failed; status=1
  fi
  if [[ "$sampler_started" -eq 1 ]]; then
    if final_sampler_stop "$artifact_root"; then
      final_lifecycle_event "$artifact_root" sampler-stop ok
    else
      final_lifecycle_event "$artifact_root" sampler-stop failed; status=1
    fi
  fi
  if final_recover_artifacts "$artifact_root"; then
    final_lifecycle_event "$artifact_root" recovery ok
  else
    final_lifecycle_event "$artifact_root" recovery failed; status=1
  fi
  if final_topology_destroy "$artifact_root"; then
    final_lifecycle_event "$artifact_root" destroy ok
  else
    final_lifecycle_event "$artifact_root" destroy failed; status=1
  fi
  if final_topology_verify_absent "$artifact_root"; then
    final_lifecycle_event "$artifact_root" cleanup-verification ok
  else
    final_lifecycle_event "$artifact_root" cleanup-verification failed; status=1
  fi
  jq -n --arg status "$([[ "$status" -eq 0 ]] && echo complete || echo invalid)" \
    --arg phase "$FINAL_PHASE" --arg topology "$FINAL_TOPOLOGY" --argjson n "$FINAL_NODE_COUNT" \
    --arg source "$FINAL_SOURCE_SHA" --arg bloc "$FINAL_BLOC_IMAGE" --arg mempool "$FINAL_MEMPOOL_IMAGE" \
    --arg deadline "$FINAL_DEADLINE" --arg sampler "$FINAL_SAMPLER" --argjson warmups "$FINAL_WARMUPS" \
    --argjson repetitions "$FINAL_REPETITIONS" --argjson blocks "$FINAL_BLOCKS" --argjson seed "$FINAL_SEED" \
    --slurpfile bundle "$FINAL_BUNDLE_ROOT/bundle-manifest.json" \
    '{schema_version:"bloc-final-campaign-phase-v1",status:$status,phase:$phase,topology:$topology,node_count:$n,
      source_sha:$source,bloc_image:$bloc,mempool_image:$mempool,bundle_version:$bundle[0].version,
      public_config_id:$bundle[0].public_config_id,encrypted_corpus_id:$bundle[0].encrypted_corpus_id,
      batches:[8,32,128],seed:$seed,deadline:$deadline,warmups:$warmups,repetitions:$repetitions,blocks:$blocks,sampler:$sampler}' \
    >"$artifact_root/manifest.json"
  if [[ "$status" -eq 0 ]]; then
    python3 "$FINAL_REPO_ROOT/scripts/lib/campaign_artifacts.py" assert-final-phase \
      --phase-root "$artifact_root" --expected-topology "$FINAL_TOPOLOGY" --expected-phase "$FINAL_PHASE" || status=1
    cleanup_args=()
    old_ifs="$IFS"; IFS=','; set -- ${FINAL_CLEANUP_REGIONS:?topology adapter must set FINAL_CLEANUP_REGIONS}; IFS="$old_ifs"
    for region in "$@"; do cleanup_args+=(--region "$region"); done
    python3 "$FINAL_REPO_ROOT/scripts/lib/campaign_artifacts.py" assert-final-cleanup \
      --cleanup "$artifact_root/cleanup-topology.json" "${cleanup_args[@]}" || status=1
    if [[ "$status" -ne 0 ]]; then
      jq '.status="invalid"' "$artifact_root/manifest.json" >"$artifact_root/manifest.json.tmp" && mv "$artifact_root/manifest.json.tmp" "$artifact_root/manifest.json"
    fi
  fi
  return "$status"
}

final_ssh() {
  local key="$1" host="$2"; shift 2
  ssh -n -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o ConnectTimeout=10 -o ConnectionAttempts=1 -o ServerAliveInterval=10 -o ServerAliveCountMax=2 \
    "ubuntu@$host" "$@"
}

final_scp() {
  local key="$1" source="$2" host="$3" destination="$4" attempt=1
  while [[ "$attempt" -le 3 ]]; do
    if scp -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
      -o ConnectTimeout=10 -o ConnectionAttempts=1 -o ServerAliveInterval=10 -o ServerAliveCountMax=2 \
      "$source" "ubuntu@$host:$destination"; then
      return 0
    fi
    [[ "$attempt" -eq 3 ]] || sleep 2
    attempt=$((attempt + 1))
  done
  return 1
}

final_rsync() {
  local key="$1" source="$2" host="$3" destination="$4"
  rsync -az --timeout=60 \
    -e "ssh -i $key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o ConnectionAttempts=1 -o ServerAliveInterval=10 -o ServerAliveCountMax=2" \
    "ubuntu@$host:$source" "$destination"
}

final_shell_join() {
  local joined= argument quoted
  for argument in "$@"; do
    printf -v quoted '%q' "$argument"
    joined="${joined:+$joined }$quoted"
  done
  printf '%s\n' "$joined"
}

final_run_remote_job() {
  local key="$1" host="$2" job_id="$3" job_command start_command status_command state exit_status
  local start_attempt=1 poll_attempt=1
  local max_polls="${FINAL_REMOTE_JOB_MAX_POLLS:-180}"
  local poll_interval="${FINAL_REMOTE_JOB_POLL_INTERVAL:-10}"
  shift 3
  job_command="${FINAL_REMOTE_JOB_COMMAND:-/opt/bloc/ec2/run-final-remote-job.sh}"
  [[ "$max_polls" =~ ^[1-9][0-9]*$ ]] || { echo "invalid remote job poll bound: $max_polls" >&2; return 1; }
  start_command="$(final_shell_join "$job_command" start "$job_id" "$@")"
  status_command="$(final_shell_join "$job_command" status "$job_id")"

  while [[ "$start_attempt" -le 3 ]]; do
    if final_ssh "$key" "$host" "$start_command" >/dev/null; then
      break
    fi
    [[ "$start_attempt" -eq 3 ]] || sleep 2
    start_attempt=$((start_attempt + 1))
  done

  while [[ "$poll_attempt" -le "$max_polls" ]]; do
    if state="$(final_ssh "$key" "$host" "$status_command" 2>/dev/null)"; then
      case "$state" in
        EXIT:*)
          exit_status="${state#EXIT:}"
          [[ "$exit_status" =~ ^[0-9]+$ ]] || {
            echo "remote job $job_id returned malformed status: $state" >&2
            return 1
          }
          if [[ "$exit_status" -eq 0 ]]; then
            return 0
          fi
          echo "remote job $job_id failed with exit status $exit_status" >&2
          return 1
          ;;
        RUNNING)
          ;;
        MISSING|AMBIGUOUS|LOST)
          echo "remote job $job_id entered fail-closed state $state" >&2
          return 1
          ;;
        *)
          echo "remote job $job_id returned unknown status: $state" >&2
          return 1
          ;;
      esac
    fi
    [[ "$poll_attempt" -eq "$max_polls" ]] || sleep "$poll_interval"
    poll_attempt=$((poll_attempt + 1))
  done
  echo "remote job $job_id status polling exhausted after $max_polls attempts" >&2
  return 1
}

final_wait_host_ready() {
  local key="$1" host="$2" attempt=1
  while [[ "$attempt" -le 60 ]]; do
    if final_ssh "$key" "$host" 'cloud-init status --wait >/dev/null && docker version >/dev/null && docker compose version >/dev/null'; then
      return 0
    fi
    sleep 10
    attempt=$((attempt + 1))
  done
  echo "host readiness failed: $host" >&2
  return 1
}

final_materialize_public() {
  local artifact_root="$1" topology
  [[ "$FINAL_TOPOLOGY" == same-az ]] && topology=T0-same-az || topology=T2-three-region
  (
    cd "$FINAL_REPO_ROOT/bloc-node"
    env GOCACHE="${GOCACHE:-/tmp/bloc-go-build}" go run ./cmd/bloc-node materialize-campaign-config \
      --bundle-root "$FINAL_BUNDLE_ROOT" --inventory "$artifact_root/inventory.json" --topology "$topology" \
      --cluster-out "$artifact_root/generated-public/cluster.json" \
      --crs-out "$artifact_root/generated-public/cluster.crs" \
      --remote-eval-out "$artifact_root/generated-public/remote-eval.json"
  )
}

final_stage_hosts() {
  local artifact_root="$1" corpus_hash node id host key secret
  corpus_hash="$(jq -er '.file_sha256["encrypted-corpus.json"]' "$FINAL_BUNDLE_ROOT/bundle-manifest.json")" || return 1
  while IFS= read -r node; do
    id="$(jq -r .id <<<"$node")"; host="$(jq -r .public_ip <<<"$node")"; key="$(final_topology_key_for_host "$node")"
    secret="$FINAL_BUNDLE_ROOT/secrets/operator-$id.json"
    final_wait_host_ready "$key" "$host" || return 1
    final_ssh "$key" "$host" 'sudo mkdir -p /etc/bloc /opt/bloc/ec2 && sudo chown ubuntu:ubuntu /etc/bloc /opt/bloc/ec2' || return 1
    final_scp "$key" "$artifact_root/generated-public/cluster.json" "$host" /etc/bloc/cluster.json || return 1
    final_scp "$key" "$artifact_root/generated-public/cluster.crs" "$host" /etc/bloc/cluster.crs || return 1
    final_scp "$key" "$FINAL_BUNDLE_ROOT/encrypted-corpus.json" "$host" /etc/bloc/encrypted-corpus.json || return 1
    final_scp "$key" "$secret" "$host" /etc/bloc/operator.json || return 1
    final_scp "$key" "$FINAL_REPO_ROOT/deploy/ec2/operator-compose.yaml" "$host" /etc/bloc/operator-compose.yaml || return 1
    final_scp "$key" "$FINAL_REPO_ROOT/deploy/ec2/sample-container-resources.sh" "$host" /opt/bloc/ec2/sample-container-resources.sh || return 1
    final_ssh "$key" "$host" "chmod 644 /etc/bloc/cluster.json /etc/bloc/cluster.crs /etc/bloc/encrypted-corpus.json && chmod 600 /etc/bloc/operator.json && sudo chown 10001:10001 /etc/bloc/operator.json && chmod 700 /opt/bloc/ec2/sample-container-resources.sh && test \"\$(sha256sum /etc/bloc/encrypted-corpus.json | awk '{print \$1}')\" = '$corpus_hash'" || return 1
  done < <(jq -c '.nodes | sort_by(.id)[]' "$artifact_root/inventory.json")

  local controller controller_host controller_key
  controller="$(jq -c .controller "$artifact_root/inventory.json")"; controller_host="$(jq -r .public_ip <<<"$controller")"
  controller_key="$(final_topology_key_for_host "$controller")"
  final_wait_host_ready "$controller_key" "$controller_host" || return 1
  final_ssh "$controller_key" "$controller_host" 'sudo mkdir -p /opt/bloc/ec2/results && sudo chown ubuntu:ubuntu /opt/bloc/ec2 && sudo chown 10001:10001 /opt/bloc/ec2/results' || return 1
  final_scp "$controller_key" "$artifact_root/generated-public/remote-eval.json" "$controller_host" /opt/bloc/ec2/remote-eval.json || return 1
  final_scp "$controller_key" "$FINAL_REPO_ROOT/scripts/lib/final-remote-job.sh" "$controller_host" /opt/bloc/ec2/run-final-remote-job.sh || return 1
  final_ssh "$controller_key" "$controller_host" 'chmod 700 /opt/bloc/ec2/run-final-remote-job.sh' || return 1
}

final_pull_one_image() {
  local key="$1" host="$2" image="$3" region registry digest attempt=1
  region="$(sed -E 's#^[0-9]{12}\.dkr\.ecr\.([a-z0-9-]+)\.amazonaws\.com/.*#\1#' <<<"$image")"
  registry="${image%%/*}"; digest="${image##*@}"
  while [[ "$attempt" -le 3 ]]; do
    if final_ssh "$key" "$host" "aws ecr get-login-password --region '$region' | docker login --username AWS --password-stdin '$registry' >/dev/null && docker pull '$image' >/dev/null && test \"\$(docker image inspect '$image' --format '{{.Architecture}}')\" = amd64 && docker image inspect '$image' --format '{{join .RepoDigests \"\\n\"}}' | grep -F '@$digest' >/dev/null"; then
      return 0
    fi
    [[ "$attempt" -eq 3 ]] || sleep 2
    attempt=$((attempt + 1))
  done
  return 1
}

final_pull_verify_images() {
  local artifact_root="$1" host_json host key
  while IFS= read -r host_json; do
    host="$(jq -r .public_ip <<<"$host_json")"; key="$(final_topology_key_for_host "$host_json")"
    final_pull_one_image "$key" "$host" "$FINAL_BLOC_IMAGE" || return 1
    final_pull_one_image "$key" "$host" "$FINAL_MEMPOOL_IMAGE" || return 1
  done < <(jq -c '.nodes[]' "$artifact_root/inventory.json")
  host_json="$(jq -c .controller "$artifact_root/inventory.json")"; host="$(jq -r .public_ip <<<"$host_json")"
  key="$(final_topology_key_for_host "$host_json")"
  final_pull_one_image "$key" "$host" "$FINAL_BLOC_IMAGE" || return 1
}

final_start_services() {
  local artifact_root="$1" node id host key
  while IFS= read -r node; do
    id="$(jq -r .id <<<"$node")"; host="$(jq -r .public_ip <<<"$node")"; key="$(final_topology_key_for_host "$node")"
    final_ssh "$key" "$host" "cd /etc/bloc && NODE_ID='$id' BLOC_IMAGE='$FINAL_BLOC_IMAGE' MEMPOOL_IMAGE='$FINAL_MEMPOOL_IMAGE' docker compose -f operator-compose.yaml up -d"
  done < <(jq -c '.nodes[]' "$artifact_root/inventory.json")
}

final_health_gate() {
  local artifact_root="$1" node host key
  while IFS= read -r node; do
    host="$(jq -r .public_ip <<<"$node")"; key="$(final_topology_key_for_host "$node")"
    final_wait_node_healthy "$key" "$host" || return 1
  done < <(jq -c '.nodes[]' "$artifact_root/inventory.json")
}

final_wait_node_healthy() {
  local key="$1" host="$2" attempt=1
  while [[ "$attempt" -le 60 ]]; do
    if final_ssh "$key" "$host" "curl -fsS http://127.0.0.1:8000/healthz >/dev/null && curl -fsS http://127.0.0.1:8000/metrics >/dev/null && curl -fsS http://127.0.0.1:8080/healthz >/dev/null && test \"\$(curl -fsS 'http://127.0.0.1:8080/inclusion-list?slot=1&limit=8' | jq -r .returned_count)\" = 8"; then
      return 0
    fi
    [[ "$attempt" -eq 60 ]] || sleep 10
    attempt=$((attempt + 1))
  done
  echo "node health readiness failed: $host" >&2
  return 1
}

final_sampler_start() {
  local artifact_root="$1" node id host key
  while IFS= read -r node; do
    id="$(jq -r .id <<<"$node")"; host="$(jq -r .public_ip <<<"$node")"; key="$(final_topology_key_for_host "$node")"
    final_ssh "$key" "$host" "mkdir -p /opt/bloc/ec2/resources; rm -f /tmp/bloc-resource.stop; nohup /opt/bloc/ec2/sample-container-resources.sh run --container bloc-bloc-node-1 --output /opt/bloc/ec2/resources/node-$id.csv --stop-file /tmp/bloc-resource.stop --node '$id' --scenario '$FINAL_EXPERIMENT_ID' --phase resource-measured >/opt/bloc/ec2/resources/node-$id.log 2>&1 &"
  done < <(jq -c '.nodes[]' "$artifact_root/inventory.json")
}

final_sampler_stop() {
  local artifact_root="$1" node host key
  while IFS= read -r node; do
    host="$(jq -r .public_ip <<<"$node")"; key="$(final_topology_key_for_host "$node")"
    final_ssh "$key" "$host" 'touch /tmp/bloc-resource.stop'
  done < <(jq -c '.nodes[]' "$artifact_root/inventory.json")
}

final_execute_measurement() {
  local artifact_root="$1" controller host key block order batch warmups repetitions_per_block next_slot job_id
  local -a evaluator_command
  controller="$(jq -c .controller "$artifact_root/inventory.json")"; host="$(jq -r .public_ip <<<"$controller")"
  key="$(final_topology_key_for_host "$controller")"; repetitions_per_block=$((FINAL_REPETITIONS / FINAL_BLOCKS))
  block=0; next_slot=1
  while [[ "$block" -lt "$FINAL_BLOCKS" ]]; do
    case $((block % 3)) in 0) order=8,32,128;; 1) order=32,128,8;; 2) order=128,8,32;; esac
    IFS=',' read -r -a final_batches <<<"$order"
    for batch in "${final_batches[@]}"; do
      warmups=0; [[ "$block" -eq 0 ]] && warmups="$FINAL_WARMUPS"
      job_id="$FINAL_EXPERIMENT_ID-block-$((block+1))-batch-$batch-slot-$next_slot"
      evaluator_command=(docker run --rm -v /opt/bloc/ec2:/work -w /work "$FINAL_BLOC_IMAGE" eval-remote
        --config remote-eval.json --experiment-id "$FINAL_EXPERIMENT_ID-b$((block+1))-tx$batch"
        --first-slot "$next_slot" --batch-size "$batch" --warmups "$warmups"
        --repetitions "$repetitions_per_block" --repetition-blocks 1 --measurement-block "$((block+1))"
        --planned-scenario-runs "$FINAL_REPETITIONS" --seed "$FINAL_SEED"
        --tx-source mock-encrypted-corpus --mempool-url http://mempool-il:8080 --final-campaign
        --deadline "$FINAL_DEADLINE" --timeout "$FINAL_DEADLINE"
        --out-dir "results/$FINAL_EXPERIMENT_ID/block-$((block+1))/batch-$batch"
        --image-tag "$FINAL_BLOC_IMAGE" --git-commit "$FINAL_SOURCE_SHA")
      final_run_remote_job "$key" "$host" "$job_id" "${evaluator_command[@]}" || return 1
      next_slot=$((next_slot + warmups + repetitions_per_block))
    done
    block=$((block + 1))
  done
}

final_recover_artifacts() {
  local artifact_root="$1" inventory="$artifact_root/inventory.json" host_json host key id
  [[ -f "$inventory" ]] || return 0
  host_json="$(jq -c .controller "$inventory")"; host="$(jq -r .public_ip <<<"$host_json")"; key="$(final_topology_key_for_host "$host_json")"
  mkdir -p "$artifact_root/scenarios/controller"
  final_rsync "$key" /opt/bloc/ec2/results/ "$host" "$artifact_root/scenarios/controller/" || return 1
  if final_ssh "$key" "$host" 'test -d /opt/bloc/ec2/jobs'; then
    mkdir -p "$artifact_root/controller-jobs"
    final_rsync "$key" /opt/bloc/ec2/jobs/ "$host" "$artifact_root/controller-jobs/" || return 1
  fi
  while IFS= read -r host_json; do
    id="$(jq -r .id <<<"$host_json")"; host="$(jq -r .public_ip <<<"$host_json")"; key="$(final_topology_key_for_host "$host_json")"
    mkdir -p "$artifact_root/logs/node-$id"
    final_ssh "$key" "$host" "cd /etc/bloc && NODE_ID='$id' BLOC_IMAGE='$FINAL_BLOC_IMAGE' MEMPOOL_IMAGE='$FINAL_MEMPOOL_IMAGE' docker compose -f operator-compose.yaml logs --no-color" >"$artifact_root/logs/node-$id/compose.log" 2>&1 || true
    if [[ "$FINAL_SAMPLER" == on ]]; then
      final_rsync "$key" /opt/bloc/ec2/resources/ "$host" "$artifact_root/logs/node-$id/resources/" 2>/dev/null || true
    fi
  done < <(jq -c '.nodes[]' "$inventory")
}
