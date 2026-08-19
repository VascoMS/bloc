#!/usr/bin/env bash

final_three_region_repository_arn() {
  local image="$1" account repository
  account="${image%%.*}"
  repository="${image#*.amazonaws.com/}"
  repository="${repository%@*}"
  printf 'arn:aws:ecr:us-east-1:%s:repository/%s\n' "$account" "$repository"
}

final_three_region_prepare_files() {
  local artifact_root="$1" node_count="$2" work bloc_arn mempool_arn key_root
  work="$artifact_root/generated-public/terraform"
  mkdir -p "$work"
  cp "$FINAL_REPO_ROOT/deploy/ec2/terraform-three-region/"*.tf "$work/"
  cp "$FINAL_REPO_ROOT/deploy/ec2/terraform-three-region/user-data.sh" "$work/"
  [[ ! -f "$FINAL_REPO_ROOT/deploy/ec2/terraform-three-region/.terraform.lock.hcl" ]] || cp "$FINAL_REPO_ROOT/deploy/ec2/terraform-three-region/.terraform.lock.hcl" "$work/"

  key_root="$FINAL_BUNDLE_ROOT/.campaign-keys"
  FINAL_THREE_REGION_PRIMARY_KEY_NAME="$FINAL_EXPERIMENT_ID-us-key"
  FINAL_THREE_REGION_SECONDARY_KEY_NAME="$FINAL_EXPERIMENT_ID-eu-west-key"
  FINAL_THREE_REGION_TERTIARY_KEY_NAME="$FINAL_EXPERIMENT_ID-eu-central-key"
  FINAL_THREE_REGION_PRIMARY_KEY_PATH="$key_root/$FINAL_EXPERIMENT_ID-us.pem"
  FINAL_THREE_REGION_SECONDARY_KEY_PATH="$key_root/$FINAL_EXPERIMENT_ID-eu-west.pem"
  FINAL_THREE_REGION_TERTIARY_KEY_PATH="$key_root/$FINAL_EXPERIMENT_ID-eu-central.pem"
  export FINAL_THREE_REGION_PRIMARY_KEY_NAME FINAL_THREE_REGION_SECONDARY_KEY_NAME FINAL_THREE_REGION_TERTIARY_KEY_NAME
  export FINAL_THREE_REGION_PRIMARY_KEY_PATH FINAL_THREE_REGION_SECONDARY_KEY_PATH FINAL_THREE_REGION_TERTIARY_KEY_PATH

  bloc_arn="$(final_three_region_repository_arn "$FINAL_BLOC_IMAGE")"
  mempool_arn="$(final_three_region_repository_arn "$FINAL_MEMPOOL_IMAGE")"
  {
    printf 'primary_region = "us-east-1"\nsecondary_region = "eu-west-1"\ntertiary_region = "eu-central-1"\n'
    printf 'primary_availability_zone = "us-east-1a"\nsecondary_availability_zone = "eu-west-1a"\ntertiary_availability_zone = "eu-central-1a"\n'
    printf 'name_prefix = "%s"\nnode_count = %s\n' "$FINAL_EXPERIMENT_ID" "$node_count"
    printf 'operator_instance_type = "t3.small"\ncontroller_instance_type = "t3.small"\n'
    printf 'controller_root_volume_size = 16\n'
    printf 'cpu_credits = "unlimited"\n'
    printf 'primary_key_name = "%s"\n' "$FINAL_THREE_REGION_PRIMARY_KEY_NAME"
    printf 'secondary_key_name = "%s"\n' "$FINAL_THREE_REGION_SECONDARY_KEY_NAME"
    printf 'tertiary_key_name = "%s"\n' "$FINAL_THREE_REGION_TERTIARY_KEY_NAME"
    printf 'admin_cidrs = ["%s"]\n' "$FINAL_ADMIN_CIDR"
    printf 'ecr_repository_arns = ["%s", "%s"]\n' "$bloc_arn" "$mempool_arn"
  } >"$work/campaign.auto.tfvars"
}

final_three_region_validate_inventory() {
  local inventory="$1" node_count="$2"
  jq -e --argjson count "$node_count" '
    .controller.region == "us-east-1" and
    .controller.instance_type == "t3.small" and
    (.controller.zone | type == "string" and length > 0) and
    (.nodes | length) == $count and
    (all(.nodes[]; .instance_type == "t3.small" and (.zone | type == "string" and length > 0))) and
    (all(.nodes[]; .region == (["us-east-1", "eu-west-1", "eu-central-1"][.id % 3])))
  ' "$inventory" >/dev/null
}

final_three_region_create_key() {
  local region="$1" name="$2" path="$3"
  aws ec2 create-key-pair --profile "$FINAL_AWS_PROFILE" --region "$region" --key-name "$name" --query KeyMaterial --output text >"$path"
  chmod 600 "$path"
}

final_topology_prepare() {
  local artifact_root="$1" node_count="$2" key_root
  final_three_region_prepare_files "$artifact_root" "$node_count"
  key_root="$(dirname "$FINAL_THREE_REGION_PRIMARY_KEY_PATH")"
  mkdir -p "$key_root"
  chmod 700 "$key_root"
  final_three_region_create_key us-east-1 "$FINAL_THREE_REGION_PRIMARY_KEY_NAME" "$FINAL_THREE_REGION_PRIMARY_KEY_PATH"
  final_three_region_create_key eu-west-1 "$FINAL_THREE_REGION_SECONDARY_KEY_NAME" "$FINAL_THREE_REGION_SECONDARY_KEY_PATH"
  final_three_region_create_key eu-central-1 "$FINAL_THREE_REGION_TERTIARY_KEY_NAME" "$FINAL_THREE_REGION_TERTIARY_KEY_PATH"
}

final_topology_apply() {
  local artifact_root="$1" work
  work="$artifact_root/generated-public/terraform"
  terraform -chdir="$work" init -input=false
  AWS_PROFILE="$FINAL_AWS_PROFILE" terraform -chdir="$work" apply -input=false -auto-approve
  AWS_PROFILE="$FINAL_AWS_PROFILE" terraform -chdir="$work" output -json inventory >"$artifact_root/inventory.json"
  AWS_PROFILE="$FINAL_AWS_PROFILE" terraform -chdir="$work" output -json peering_connection_ids >"$artifact_root/peering-connection-ids.json"
  final_three_region_validate_inventory "$artifact_root/inventory.json" "$FINAL_NODE_COUNT"
  jq -e 'length == 3 and ([.[]] | unique | length == 3) and all(.[]; type == "string" and length > 0)' \
    "$artifact_root/peering-connection-ids.json" >/dev/null
}

final_topology_key_for_host() {
  local host_json="$1" region
  region="$(jq -er .region <<<"$host_json")"
  case "$region" in
    us-east-1) printf '%s\n' "$FINAL_THREE_REGION_PRIMARY_KEY_PATH" ;;
    eu-west-1) printf '%s\n' "$FINAL_THREE_REGION_SECONDARY_KEY_PATH" ;;
    eu-central-1) printf '%s\n' "$FINAL_THREE_REGION_TERTIARY_KEY_PATH" ;;
    *) echo "unsupported host region: $region" >&2; return 1 ;;
  esac
}

final_topology_destroy() {
  local artifact_root="$1" work status=0
  work="$artifact_root/generated-public/terraform"
  AWS_PROFILE="$FINAL_AWS_PROFILE" terraform -chdir="$work" destroy -input=false -auto-approve || status=1
  aws ec2 delete-key-pair --profile "$FINAL_AWS_PROFILE" --region us-east-1 --key-name "$FINAL_THREE_REGION_PRIMARY_KEY_NAME" || status=1
  aws ec2 delete-key-pair --profile "$FINAL_AWS_PROFILE" --region eu-west-1 --key-name "$FINAL_THREE_REGION_SECONDARY_KEY_NAME" || status=1
  aws ec2 delete-key-pair --profile "$FINAL_AWS_PROFILE" --region eu-central-1 --key-name "$FINAL_THREE_REGION_TERTIARY_KEY_NAME" || status=1
  rm -f "$FINAL_THREE_REGION_PRIMARY_KEY_PATH" "$FINAL_THREE_REGION_SECONDARY_KEY_PATH" "$FINAL_THREE_REGION_TERTIARY_KEY_PATH" || status=1
  return "$status"
}

final_three_region_query_array() {
  local query="$1"
  shift
  local output
  output="$(aws "$@" --query "$query" --output text)" || return 1
  jq -n --arg value "$output" '$value|split(" ")|map(select(length>0 and .!="None"))'
}

final_three_region_region_cleanup() {
  local region="$1" key_name="$2" ok=true
  local instances volumes vpcs subnets groups routes keys peerings
  instances="$(final_three_region_query_array 'Reservations[].Instances[].InstanceId' ec2 describe-instances --profile "$FINAL_AWS_PROFILE" --region "$region" --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*" "Name=instance-state-name,Values=pending,running,stopping,stopped")" || ok=false
  volumes="$(final_three_region_query_array 'Volumes[].VolumeId' ec2 describe-volumes --profile "$FINAL_AWS_PROFILE" --region "$region" --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  vpcs="$(final_three_region_query_array 'Vpcs[].VpcId' ec2 describe-vpcs --profile "$FINAL_AWS_PROFILE" --region "$region" --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  subnets="$(final_three_region_query_array 'Subnets[].SubnetId' ec2 describe-subnets --profile "$FINAL_AWS_PROFILE" --region "$region" --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  groups="$(final_three_region_query_array 'SecurityGroups[].GroupId' ec2 describe-security-groups --profile "$FINAL_AWS_PROFILE" --region "$region" --filters "Name=group-name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  routes="$(final_three_region_query_array 'RouteTables[].RouteTableId' ec2 describe-route-tables --profile "$FINAL_AWS_PROFILE" --region "$region" --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  keys="$(final_three_region_query_array 'KeyPairs[].KeyName' ec2 describe-key-pairs --profile "$FINAL_AWS_PROFILE" --region "$region" --filters "Name=key-name,Values=$key_name")" || ok=false
  peerings="$(final_three_region_query_array 'VpcPeeringConnections[?Status.Code!=`deleted`].VpcPeeringConnectionId' ec2 describe-vpc-peering-connections --profile "$FINAL_AWS_PROFILE" --region "$region" --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  jq -n --argjson query_succeeded "$ok" --argjson instances "${instances:-[]}" --argjson volumes "${volumes:-[]}" \
    --argjson vpcs "${vpcs:-[]}" --argjson subnets "${subnets:-[]}" --argjson security_groups "${groups:-[]}" \
    --argjson route_tables "${routes:-[]}" --argjson key_pairs "${keys:-[]}" --argjson peering_connections "${peerings:-[]}" \
    '{query_succeeded:$query_succeeded,instances:$instances,volumes:$volumes,vpcs:$vpcs,subnets:$subnets,security_groups:$security_groups,route_tables:$route_tables,key_pairs:$key_pairs,peering_connections:$peering_connections}'
  [[ "$ok" == true ]]
}

final_topology_verify_absent() {
  local artifact_root="$1" work ok=true
  local primary secondary tertiary roles profiles state
  work="$artifact_root/generated-public/terraform"
  primary="$(final_three_region_region_cleanup us-east-1 "$FINAL_THREE_REGION_PRIMARY_KEY_NAME")" || ok=false
  secondary="$(final_three_region_region_cleanup eu-west-1 "$FINAL_THREE_REGION_SECONDARY_KEY_NAME")" || ok=false
  tertiary="$(final_three_region_region_cleanup eu-central-1 "$FINAL_THREE_REGION_TERTIARY_KEY_NAME")" || ok=false
  roles="$(final_three_region_query_array "Roles[?RoleName=='$FINAL_EXPERIMENT_ID-ec2-ecr-readonly'].RoleName" iam list-roles --profile "$FINAL_AWS_PROFILE")" || ok=false
  profiles="$(final_three_region_query_array "InstanceProfiles[?InstanceProfileName=='$FINAL_EXPERIMENT_ID-ec2-ecr-readonly'].InstanceProfileName" iam list-instance-profiles --profile "$FINAL_AWS_PROFILE")" || ok=false
  state="$(terraform -chdir="$work" state list 2>/dev/null | jq -R -s 'split("\n")|map(select(length>0))')" || ok=false
  [[ -n "$primary" ]] || primary='{}'
  [[ -n "$secondary" ]] || secondary='{}'
  [[ -n "$tertiary" ]] || tertiary='{}'
  jq -n --argjson primary "$primary" --argjson secondary "$secondary" --argjson tertiary "$tertiary" \
    --argjson query_succeeded "$ok" --argjson roles "${roles:-[]}" --argjson instance_profiles "${profiles:-[]}" \
    --argjson terraform_state "${state:-[]}" \
    '{regions:{"us-east-1":$primary,"eu-west-1":$secondary,"eu-central-1":$tertiary},iam:{query_succeeded:$query_succeeded,roles:$roles,instance_profiles:$instance_profiles},terraform_state:$terraform_state}' \
    >"$artifact_root/cleanup-topology.json"
  FINAL_CLEANUP_REGIONS=us-east-1,eu-west-1,eu-central-1
  export FINAL_CLEANUP_REGIONS
  [[ "$ok" == true ]] && final_assert_cleanup_empty "$artifact_root/cleanup-topology.json"
}
