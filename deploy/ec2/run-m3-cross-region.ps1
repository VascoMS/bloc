param(
  [Parameter(Mandatory = $true)]
  [string[]]$AdminCidrs,
  [string]$AwsProfile = "bloc",
  [string]$PrimaryRegion = "us-east-1",
  [string]$SecondaryRegion = "eu-west-1",
  [string]$PrimaryAvailabilityZone = "us-east-1a",
  [string]$SecondaryAvailabilityZone = "eu-west-1a",
  [int[]]$NodeCounts = @(4, 7),
  [string]$NodeCountsCsv = "",
  [int[]]$BatchSizes = @(8, 32, 128),
  [string]$BatchSizesCsv = "",
  [string]$OperatorInstanceType = "t3.medium",
  [string]$ControllerInstanceType = "t3.medium",
  [int]$Warmups = 5,
  [int]$Repetitions = 30,
  [string]$EvalTimeout = "60s",
  [string]$CampaignId = "",
  [switch]$AutoApprovePlan,
  [switch]$AutoApprovePhases,
  [switch]$Unattended,
  [switch]$KeepResourcesOnFailure,
  [switch]$SkipChartGeneration,
  [switch]$PlanOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function ConvertFrom-IntegerCsv {
  param([string]$Raw, [string]$Name)
  $values = @()
  foreach ($part in ($Raw -split ",")) {
    $parsed = 0
    if (-not [int]::TryParse($part.Trim(), [ref]$parsed)) {
      throw "$Name must be a comma-separated list of integers"
    }
    $values += $parsed
  }
  return [int[]]$values
}

if ($NodeCountsCsv) { $NodeCounts = ConvertFrom-IntegerCsv $NodeCountsCsv "NodeCountsCsv" }
if ($BatchSizesCsv) { $BatchSizes = ConvertFrom-IntegerCsv $BatchSizesCsv "BatchSizesCsv" }
if ($NodeCounts.Count -eq 0 -or @($NodeCounts | Where-Object { $_ -notin @(4, 7, 10) }).Count -gt 0) {
  throw "NodeCounts must be selected from 4, 7, and 10"
}
if ($BatchSizes.Count -eq 0 -or @($BatchSizes | Where-Object { $_ -notin @(8, 32, 128) }).Count -gt 0) {
  throw "BatchSizes must be selected from 8, 32, and 128"
}
if ($OperatorInstanceType -ne "t3.medium" -or $ControllerInstanceType -ne "t3.medium") {
  throw "The canonical cross-region campaign requires t3.medium operators and controller"
}
if ($Warmups -lt 0 -or $Repetitions -lt 1) { throw "Warmups and Repetitions are invalid" }
if ($EvalTimeout -notmatch '^\d+(ms|s|m)$') { throw "EvalTimeout must be a Go duration such as 60s" }
if ($PrimaryRegion -eq $SecondaryRegion) { throw "PrimaryRegion and SecondaryRegion must differ" }
if ($Unattended) { $AutoApprovePlan = $true; $AutoApprovePhases = $true }

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$terraformSource = Join-Path $PSScriptRoot "terraform-cross-region"
$aws = "C:\Program Files\Amazon\AWSCLIV2\aws.exe"
if (-not (Test-Path $aws)) { $aws = "aws" }
if (-not $CampaignId) {
  $stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd't'HHmmss'z'")
  $CampaignId = "m3-cross-region-synthetic-$stamp"
}
if ($CampaignId -notmatch '^[a-z0-9][a-z0-9._-]*$') { throw "CampaignId has invalid characters" }

$campaignRoot = Join-Path $repoRoot "results\ec2\$CampaignId"
$commands = [System.Collections.Generic.List[string]]::new()
$phaseRecords = [System.Collections.Generic.List[object]]::new()
$preflightRecords = [System.Collections.Generic.List[object]]::new()
$gitCommit = (& git -C $repoRoot rev-parse --short=12 HEAD).Trim()
$imageTag = "bloc-node:cross-region-$gitCommit"
$imageDigest = ""
$campaignStart = (Get-Date).ToUniversalTime().ToString("o")
New-Item -ItemType Directory -Force $campaignRoot | Out-Null

function Record-Command {
  param([string]$Command)
  $commands.Add($Command)
  $commands | Set-Content -Encoding utf8 (Join-Path $campaignRoot "commands.txt")
}

function Invoke-Checked {
  param([scriptblock]$Action, [string]$Description)
  & $Action
  if ($LASTEXITCODE -ne 0) { throw "$Description failed with exit code $LASTEXITCODE" }
}

function ConvertTo-TerraformStringList {
  param([string[]]$Values)
  return (($Values | ForEach-Object { '"' + $_ + '"' }) -join ", ")
}

function Get-KeyPath {
  param([object]$HostValue, [string]$PrimaryKeyPath, [string]$SecondaryKeyPath)
  if ($HostValue.region -eq $PrimaryRegion) { return $PrimaryKeyPath }
  if ($HostValue.region -eq $SecondaryRegion) { return $SecondaryKeyPath }
  throw "unknown host region $($HostValue.region)"
}

function Invoke-HostSSH {
  param([object]$HostValue, [string]$Command, [string]$PrimaryKeyPath, [string]$SecondaryKeyPath, [switch]$AllowFailure)
  $key = Get-KeyPath $HostValue $PrimaryKeyPath $SecondaryKeyPath
  Record-Command "ssh ubuntu@$($HostValue.public_ip) -- $Command"
  $output = & ssh -i $key -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$($HostValue.public_ip)" $Command
  $code = $LASTEXITCODE
  if ($null -ne $output) { Write-Host (($output | Out-String).TrimEnd()) }
  if ($code -ne 0 -and -not $AllowFailure) { throw "ssh failed on $($HostValue.public_ip)" }
  return $code
}

function Get-HostSSHText {
  param([object]$HostValue, [string]$Command, [string]$PrimaryKeyPath, [string]$SecondaryKeyPath)
  $key = Get-KeyPath $HostValue $PrimaryKeyPath $SecondaryKeyPath
  $output = & ssh -i $key -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$($HostValue.public_ip)" $Command
  if ($LASTEXITCODE -ne 0) { throw "ssh metadata command failed on $($HostValue.public_ip)" }
  return (($output | Out-String).Trim())
}

function Invoke-HostSCP {
  param([object]$HostValue, [string[]]$Arguments, [string]$PrimaryKeyPath, [string]$SecondaryKeyPath)
  $key = Get-KeyPath $HostValue $PrimaryKeyPath $SecondaryKeyPath
  Record-Command "scp $($Arguments -join ' ')"
  & scp -i $key -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL @Arguments
  if ($LASTEXITCODE -ne 0) { throw "scp failed for $($HostValue.public_ip)" }
}

function Wait-HostReady {
  param([object]$HostValue, [string]$PrimaryKeyPath, [string]$SecondaryKeyPath)
  for ($attempt = 1; $attempt -le 60; $attempt++) {
    $code = Invoke-HostSSH $HostValue "cloud-init status --wait >/dev/null 2>&1 && docker version >/dev/null && docker compose version >/dev/null" $PrimaryKeyPath $SecondaryKeyPath -AllowFailure
    if ($code -eq 0) { return }
    Start-Sleep -Seconds 10
  }
  throw "timed out waiting for $($HostValue.public_ip)"
}

function New-PrometheusConfig {
  param([object]$Inventory, [string]$Path)
  $targets = $Inventory.nodes | Sort-Object id | ForEach-Object { "          - $($_.private_ip):8000" }
  @("global:", "  scrape_interval: 2s", "scrape_configs:", "  - job_name: 'bloc-sidecars'", "    metrics_path: /metrics", "    static_configs:", "      - targets:") + $targets |
    Set-Content -Encoding ascii $Path
}

function Collect-PairwiseNetwork {
  param([object]$Inventory, [string]$Phase, [string]$Path, [string]$PrimaryKeyPath, [string]$SecondaryKeyPath)
  $rows = [System.Collections.Generic.List[string]]::new()
  $rows.Add("phase,source_node_id,source_region,target_node_id,target_region,target_private_ip,attempts,successes,avg_connect_ms,avg_total_ms")
  foreach ($source in ($Inventory.nodes | Sort-Object id)) {
    foreach ($target in ($Inventory.nodes | Sort-Object id)) {
      $command = "for i in 1 2 3 4 5; do curl --max-time 5 -sS -o /dev/null -w '%{http_code},%{time_connect},%{time_total}\n' http://$($target.private_ip):8000/healthz || echo '000,0,0'; done"
      $key = Get-KeyPath $source $PrimaryKeyPath $SecondaryKeyPath
      $samples = & ssh -i $key -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($source.public_ip)" $command
      $attempts = 0; $successes = 0; $connect = 0.0; $total = 0.0
      foreach ($sample in $samples) {
        if ($sample -match '^(\d{3}),([0-9.]+),([0-9.]+)$') {
          $attempts++
          if ($Matches[1] -eq "200") {
            $successes++; $connect += [double]::Parse($Matches[2], [Globalization.CultureInfo]::InvariantCulture); $total += [double]::Parse($Matches[3], [Globalization.CultureInfo]::InvariantCulture)
          }
        }
      }
      $connectMs = if ($successes) { [Math]::Round(1000 * $connect / $successes, 3) } else { "" }
      $totalMs = if ($successes) { [Math]::Round(1000 * $total / $successes, 3) } else { "" }
      $rows.Add("$Phase,$($source.id),$($source.region),$($target.id),$($target.region),$($target.private_ip),$attempts,$successes,$connectMs,$totalMs")
    }
  }
  $rows | Set-Content -Encoding ascii $Path
}

function Collect-CPUCreditSnapshot {
  param([object]$Inventory, [string]$Phase, [string]$Path)
  $end = (Get-Date).ToUniversalTime(); $start = $end.AddMinutes(-15)
  $rows = @()
  foreach ($hostValue in @($Inventory.controller) + @($Inventory.nodes)) {
    $nodeID = if ($hostValue.PSObject.Properties.Name -contains "id") { $hostValue.id } else { "controller" }
    foreach ($metric in @("CPUUtilization", "CPUCreditBalance", "CPUCreditUsage")) {
      $value = & $aws cloudwatch get-metric-statistics --profile $AwsProfile --region $hostValue.region --namespace AWS/EC2 --metric-name $metric --dimensions "Name=InstanceId,Value=$($hostValue.instance_id)" --start-time $start.ToString("o") --end-time $end.ToString("o") --period 300 --statistics Average --query "Datapoints | sort_by(@,&Timestamp)[-1].Average" --output text
      $rows += [pscustomobject]@{ timestamp = $end.ToString("o"); phase = $Phase; instance_id = $hostValue.instance_id; node_id = $nodeID; region = $hostValue.region; metric = $metric; average = $value }
    }
  }
  $rows | Export-Csv -NoTypeInformation -Encoding utf8 $Path
}

function Merge-ScenarioCSVs {
  param([string]$PhaseRoot, [object[]]$Scenarios)
  foreach ($name in @("run_measurements.csv", "node_measurements.csv", "scenario_summary.csv")) {
    $destination = Join-Path $PhaseRoot $name
    $first = $true
    foreach ($scenario in $Scenarios) {
      $source = Join-Path $scenario $name
      if (-not (Test-Path $source)) { throw "missing scenario artifact $source" }
      $lines = Get-Content $source
      if ($first) { $lines | Set-Content -Encoding ascii $destination; $first = $false }
      else { $lines | Select-Object -Skip 1 | Add-Content -Encoding ascii $destination }
    }
  }
  $summaries = @($Scenarios | ForEach-Object { Get-Content -Raw (Join-Path $_ "scenario_summary.json") | ConvertFrom-Json })
  $summaries | ConvertTo-Json -Depth 50 | Set-Content -Encoding utf8 (Join-Path $PhaseRoot "scenario_summary.json")
}

function Assert-PrometheusTargets {
  param([string]$Path, [int]$Expected)
  $targets = Get-Content -Raw $Path | ConvertFrom-Json
  $active = @($targets.data.activeTargets)
  $up = @($active | Where-Object { $_.health -eq "up" })
  if ($active.Count -ne $Expected -or $up.Count -ne $Expected) { throw "Prometheus has $($up.Count)/$Expected targets up" }
}

function Assert-PhaseResults {
  param([string]$PhaseRoot, [int]$NodeCount)
  $runs = @(Import-Csv (Join-Path $PhaseRoot "run_measurements.csv") | Where-Object phase -eq "measured")
  $nodes = @(Import-Csv (Join-Path $PhaseRoot "node_measurements.csv") | Where-Object phase -eq "measured")
  foreach ($batch in $BatchSizes) {
    $batchRuns = @($runs | Where-Object { [int]$_.batch_size -eq $batch })
    if ($batchRuns.Count -ne $Repetitions) { throw "batch $batch has $($batchRuns.Count) measured runs" }
    foreach ($row in $batchRuns) {
      if ($row.success -ne "true" -or $row.consistent -ne "true") { throw "batch $batch contains a failed or inconsistent run" }
      if ([int]$row.selected_ciphertexts -ne $batch) { throw "batch $batch selected ciphertext count mismatch" }
      $fourStage = [long]$row.proposal_preparation_us + [long]$row.acs_us + [long]$row.merge_plan_us + [long]$row.threshold_wait_us + [long]$row.combine_us + [long]$row.materialization_us
      if ([Math]::Abs($fourStage - [long]$row.total_slot_us) -gt 20) { throw "four-stage additivity failed for $($row.run_id)" }
    }
    $batchNodes = @($nodes | Where-Object { [int]$_.batch_size -eq $batch })
    if ($batchNodes.Count -ne ($Repetitions * $NodeCount) -or @($batchNodes | Where-Object metrics_finalized -ne "true").Count -gt 0) {
      throw "batch $batch node metrics are incomplete"
    }
  }
}

function Write-CampaignManifest {
  param([string]$Status, [string]$Reason = "")
  [ordered]@{
    schema_version = "bloc-ec2-m3-cross-region/v1"; experiment_id = $CampaignId; campaign = "M3-cross-region-synthetic";
    status = $Status; invalid_reason = $(if ($Reason) { $Reason } else { $null }); started_at = $campaignStart; finished_at = (Get-Date).ToUniversalTime().ToString("o");
    topology = "T2-cross-region"; primary_region = $PrimaryRegion; secondary_region = $SecondaryRegion;
    primary_availability_zone = $PrimaryAvailabilityZone; secondary_availability_zone = $SecondaryAvailabilityZone;
    controller_region = $PrimaryRegion; node_counts = $NodeCounts; batch_sizes = $BatchSizes; warmups = $Warmups; repetitions = $Repetitions;
    eval_timeout = $EvalTimeout; operator_instance_type = $OperatorInstanceType; controller_instance_type = $ControllerInstanceType;
    git_commit = $gitCommit; docker_image_digest = $imageDigest; reporting_stages = @("proposal", "acs", "merge_plan", "decryption_materialization");
    comparison_policy = "standalone-current-build; prior topology data is historical context only";
    preflight = $preflightRecords.ToArray(); phases = $phaseRecords.ToArray(); commands = $commands.ToArray()
  } | ConvertTo-Json -Depth 50 | Set-Content -Encoding utf8 (Join-Path $campaignRoot "manifest.json")
}

function Merge-PhaseOutputs {
  foreach ($name in @("run_measurements.csv", "node_measurements.csv", "scenario_summary.csv", "network-pre.csv", "network-post.csv", "cpu-credits-pre.csv", "cpu-credits-post.csv")) {
    $destination = Join-Path $campaignRoot $name
    $first = $true
    foreach ($phase in $phaseRecords) {
      $source = Join-Path $phase.artifact_root $name
      if (-not (Test-Path $source)) { continue }
      $lines = Get-Content $source
      if ($first) { $lines | Set-Content -Encoding ascii $destination; $first = $false }
      else { $lines | Select-Object -Skip 1 | Add-Content -Encoding ascii $destination }
    }
  }
  $scenarioSummaries = @($phaseRecords | ForEach-Object { Get-Content -Raw (Join-Path $_.artifact_root "scenario_summary.json") | ConvertFrom-Json })
  $scenarioSummaries | ConvertTo-Json -Depth 50 | Set-Content -Encoding utf8 (Join-Path $campaignRoot "scenario_summary.json")
}

Record-Command "git rev-parse --short=12 HEAD"
$dirty = @(& git -C $repoRoot status --porcelain -- `
  ':(glob)bloc-node/**/*.go' bloc-node/go.mod bloc-node/go.sum bloc-node/Dockerfile `
  bte sbc deploy/ec2/run-m3-cross-region.ps1 deploy/ec2/terraform-cross-region `
  deploy/ec2/operator-compose.yaml deploy/ec2/controller-compose.yaml `
  latency-charts/src latency-charts/tests latency-charts/README.md)
if (-not $PlanOnly -and $dirty.Count -gt 0) { throw "relevant campaign sources are uncommitted: $($dirty -join '; ')" }
Record-Command "aws sts get-caller-identity --profile $AwsProfile"
Invoke-Checked { & $aws sts get-caller-identity --profile $AwsProfile --output json | Set-Content -Encoding utf8 (Join-Path $campaignRoot "aws-caller-identity.json") } "AWS identity preflight"
$env:AWS_PROFILE = $AwsProfile

foreach ($region in @($PrimaryRegion, $SecondaryRegion)) {
  Record-Command "aws ec2 describe-instance-type-offerings --region $region --instance-types t3.medium"
  $offerings = & $aws ec2 describe-instance-type-offerings --profile $AwsProfile --region $region --location-type availability-zone --filters "Name=instance-type,Values=t3.medium" --query "InstanceTypeOfferings[].Location" --output text
  $expectedZone = if ($region -eq $PrimaryRegion) { $PrimaryAvailabilityZone } else { $SecondaryAvailabilityZone }
  if (($offerings -split '\s+') -notcontains $expectedZone) { throw "t3.medium is not offered in $expectedZone" }
  $previousErrorAction = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try {
    $quota = & $aws service-quotas get-service-quota --profile $AwsProfile --region $region --service-code ec2 --quota-code L-1216C47A --query "Quota.Value" --output text 2>&1
    $quotaExit = $LASTEXITCODE
  }
  finally { $ErrorActionPreference = $previousErrorAction }
  if ($quotaExit -ne 0) {
    $preflightRecords.Add([pscustomobject]@{ region = $region; availability_zone = $expectedZone; instance_type = "t3.medium"; instance_offered = $true; standard_vcpu_quota = $null; quota_status = "unavailable"; quota_error = (($quota | Out-String).Trim()) })
    if (-not $PlanOnly) { throw "unable to verify standard-instance vCPU quota in $region; servicequotas:GetServiceQuota is required" }
    Write-Warning "Quota verification is unavailable in $region; plan-only validation will continue, but apply remains blocked."
    continue
  }
  $maximumNodes = [int](($NodeCounts | Measure-Object -Maximum).Maximum)
  $requiredVCPUs = if ($region -eq $PrimaryRegion) {
    2 * ([Math]::Ceiling($maximumNodes / 2.0) + 1)
  } else {
    2 * [Math]::Floor($maximumNodes / 2.0)
  }
  if ([double]$quota -lt $requiredVCPUs) { throw "standard-instance vCPU quota in $region is below the required $requiredVCPUs" }
  $preflightRecords.Add([pscustomobject]@{ region = $region; availability_zone = $expectedZone; instance_type = "t3.medium"; instance_offered = $true; standard_vcpu_quota = [double]$quota; quota_status = "verified"; quota_error = $null })
}

if (-not $PlanOnly) {
  Record-Command "docker build -f bloc-node/Dockerfile -t $imageTag ."
  Push-Location $repoRoot
  try { Invoke-Checked { docker build -f bloc-node/Dockerfile -t $imageTag . } "Docker build" }
  finally { Pop-Location }
}

try {
  foreach ($nodeCount in $NodeCounts) {
    $phaseSuffix = $CampaignId -replace '^m3-cross-region-synthetic-', ''
    $phaseId = "bloc-ec2-xr-n$nodeCount-$phaseSuffix"
    if ($phaseId.Length -gt 44) { throw "CampaignId is too long for scoped IAM role names" }
    $phaseRoot = Join-Path $campaignRoot "n$nodeCount"
    $workDir = Join-Path $phaseRoot "generated\terraform-work"
    New-Item -ItemType Directory -Force $workDir, (Join-Path $phaseRoot "generated"), (Join-Path $phaseRoot "logs"), (Join-Path $phaseRoot "scenarios") | Out-Null
    foreach ($sourceFile in (Get-ChildItem -LiteralPath $terraformSource -File | Where-Object { $_.Extension -eq ".tf" -or $_.Name -in @(".terraform.lock.hcl", "user-data.sh") })) {
      Copy-Item -LiteralPath $sourceFile.FullName -Destination $workDir -Force
    }
    $primaryKeyName = "$phaseId-primary-key"; $secondaryKeyName = "$phaseId-secondary-key"
    $primaryKeyPath = Join-Path $env:TEMP "$primaryKeyName.pem"; $secondaryKeyPath = Join-Path $env:TEMP "$secondaryKeyName.pem"
    $tfvars = Join-Path $workDir "campaign.tfvars"
    @(
      "primary_region = `"$PrimaryRegion`"", "secondary_region = `"$SecondaryRegion`"",
      "primary_availability_zone = `"$PrimaryAvailabilityZone`"", "secondary_availability_zone = `"$SecondaryAvailabilityZone`"",
      "name_prefix = `"$phaseId`"", "node_count = $nodeCount", "operator_instance_type = `"$OperatorInstanceType`"", "controller_instance_type = `"$ControllerInstanceType`"",
      "primary_key_name = `"$primaryKeyName`"", "secondary_key_name = `"$secondaryKeyName`"", "admin_cidrs = [$(ConvertTo-TerraformStringList $AdminCidrs)]",
      "ecr_repository_name = `"bloc-node-$phaseId`""
    ) | Set-Content -Encoding ascii $tfvars

    $primaryKeyCreated = $false; $secondaryKeyCreated = $false; $applyAttempted = $false
    Push-Location $workDir
    try {
      Record-Command "terraform init -input=false"
      Invoke-Checked { terraform init -input=false } "Terraform init"
      Record-Command "terraform plan -var-file=campaign.tfvars"
      Invoke-Checked { terraform plan -input=false "-var-file=campaign.tfvars" "-out=campaign.tfplan" } "Terraform plan"
      terraform show -no-color campaign.tfplan | Set-Content -Encoding utf8 (Join-Path $phaseRoot "terraform-plan.txt")
      $planText = Get-Content -Raw (Join-Path $phaseRoot "terraform-plan.txt")
      foreach ($forbidden in @("aws_nat_gateway", "aws_lb", "aws_eks_cluster", "aws_db_instance", "aws_autoscaling_group")) {
        if ($planText.Contains($forbidden)) { throw "Terraform plan contains forbidden resource $forbidden" }
      }
      $allowedTypes = @(
        "aws_vpc", "aws_subnet", "aws_internet_gateway", "aws_route_table", "aws_route_table_association",
        "aws_vpc_peering_connection", "aws_vpc_peering_connection_accepter", "aws_route", "aws_ecr_repository",
        "aws_iam_role", "aws_iam_role_policy_attachment", "aws_iam_instance_profile", "aws_security_group", "aws_instance"
      )
      $plannedTypes = @([regex]::Matches($planText, '(?m)^\s*#\s+(aws_[a-z0-9_]+)\.[^ ]+\s+will be created') | ForEach-Object { $_.Groups[1].Value } | Sort-Object -Unique)
      $unexpectedTypes = @($plannedTypes | Where-Object { $_ -notin $allowedTypes })
      if ($unexpectedTypes.Count -gt 0) { throw "Terraform plan contains unexpected resource types: $($unexpectedTypes -join ', ')" }
      $expectedAdds = 22 + $nodeCount
      if ($planText -notmatch "Plan:\s+$expectedAdds to add, 0 to change, 0 to destroy") { throw "Terraform plan does not contain the expected $expectedAdds resources" }
      $plannedInstances = [regex]::Matches($planText, '(?m)^\s*#\s+aws_instance\.[^ ]+\s+will be created').Count
      if ($plannedInstances -ne ($nodeCount + 1) -or $planText -match 'instance_type\s+=\s+"(?!t3\.medium)[^"]+"') { throw "Terraform plan does not contain only the expected t3.medium instances" }
      if ($PlanOnly) {
        $phaseRecords.Add([pscustomobject]@{ node_count = $nodeCount; experiment_id = $phaseId; artifact_root = $phaseRoot; status = "planned" })
        continue
      }
      if (-not $AutoApprovePlan) {
        $answer = Read-Host "Type APPLY to launch cross-region n=$nodeCount"
        if ($answer -ne "APPLY") { throw "operator declined Terraform apply" }
      }
      $primaryKeyMaterial = & $aws ec2 create-key-pair --profile $AwsProfile --region $PrimaryRegion --key-name $primaryKeyName --key-type rsa --key-format pem --query KeyMaterial --output text
      if ($LASTEXITCODE -ne 0) { throw "primary key-pair creation failed" }
      $primaryKeyCreated = $true
      [IO.File]::WriteAllText($primaryKeyPath, $primaryKeyMaterial, [Text.Encoding]::ASCII)
      $secondaryKeyMaterial = & $aws ec2 create-key-pair --profile $AwsProfile --region $SecondaryRegion --key-name $secondaryKeyName --key-type rsa --key-format pem --query KeyMaterial --output text
      if ($LASTEXITCODE -ne 0) { throw "secondary key-pair creation failed" }
      $secondaryKeyCreated = $true
      [IO.File]::WriteAllText($secondaryKeyPath, $secondaryKeyMaterial, [Text.Encoding]::ASCII)
      icacls $primaryKeyPath /inheritance:r /grant:r "$env:USERNAME`:R" | Out-Null
      icacls $secondaryKeyPath /inheritance:r /grant:r "$env:USERNAME`:R" | Out-Null
      $applyAttempted = $true
      Invoke-Checked { terraform apply -input=false -auto-approve campaign.tfplan } "Terraform apply"
      terraform output -json inventory | Set-Content -Encoding utf8 (Join-Path $phaseRoot "inventory.json")
      $inventory = Get-Content -Raw (Join-Path $phaseRoot "inventory.json") | ConvertFrom-Json
      $ecrURL = (terraform output -raw ecr_repository_url).Trim(); $peeringID = (terraform output -raw peering_connection_id).Trim()
    }
    catch {
      if ($KeepResourcesOnFailure -and $applyAttempted) {
        Write-Warning "Terraform apply failed and resources were kept; SSH keys remain at $primaryKeyPath and $secondaryKeyPath"
      } else {
        if ($applyAttempted) { terraform destroy -auto-approve "-var-file=campaign.tfvars" 2>&1 | Set-Content -Encoding utf8 (Join-Path $phaseRoot "terraform-destroy-after-apply-failure.log") }
        if ($primaryKeyCreated) { & $aws ec2 delete-key-pair --profile $AwsProfile --region $PrimaryRegion --key-name $primaryKeyName 2>$null | Out-Null }
        if ($secondaryKeyCreated) { & $aws ec2 delete-key-pair --profile $AwsProfile --region $SecondaryRegion --key-name $secondaryKeyName 2>$null | Out-Null }
        foreach ($keyPath in @($primaryKeyPath, $secondaryKeyPath)) {
          if (Test-Path $keyPath) { icacls $keyPath /grant:r "$env:USERNAME`:F" | Out-Null; Remove-Item -LiteralPath $keyPath -Force }
        }
      }
      throw
    }
    finally { Pop-Location }

    $phaseFailed = $true
    try {
      if ($inventory.controller.region -ne $PrimaryRegion -or $inventory.controller.instance_type -ne "t3.medium") { throw "controller placement or instance type is invalid" }
      if (@($inventory.nodes).Count -ne $nodeCount) { throw "inventory node count is invalid" }
      foreach ($node in $inventory.nodes) {
        $expectedRegion = if (([int]$node.id % 2) -eq 0) { $PrimaryRegion } else { $SecondaryRegion }
        if ($node.region -ne $expectedRegion -or $node.instance_type -ne "t3.medium") { throw "operator $($node.id) placement or instance type is invalid" }
      }
      foreach ($hostValue in @($inventory.controller) + @($inventory.nodes)) { Wait-HostReady $hostValue $primaryKeyPath $secondaryKeyPath }
      $registry = ($ecrURL -split '/')[0]; $imageURI = "$ecrURL`:$gitCommit"
      $password = & $aws ecr get-login-password --profile $AwsProfile --region $PrimaryRegion
      $password | docker login --username AWS --password-stdin $registry
      if ($LASTEXITCODE -ne 0) { throw "ECR login failed" }
      docker tag $imageTag $imageURI; if ($LASTEXITCODE -ne 0) { throw "Docker tag failed" }
      docker push $imageURI; if ($LASTEXITCODE -ne 0) { throw "Docker push failed" }
      $phaseDigest = (& $aws ecr describe-images --profile $AwsProfile --region $PrimaryRegion --repository-name "bloc-node-$phaseId" --image-ids "imageTag=$gitCommit" --query "imageDetails[0].imageDigest" --output text).Trim()
      if ($imageDigest -and $imageDigest -ne $phaseDigest) { throw "image digest changed between phases" }
      $imageDigest = $phaseDigest

      $clusterPath = Join-Path $phaseRoot "generated\cluster.ec2.json"; $crsPath = Join-Path $phaseRoot "generated\cluster.ec2.crs"
      $remotePath = Join-Path $phaseRoot "generated\remote-eval.ec2.json"; $secretsPath = Join-Path $phaseRoot "generated\secrets.ec2"
      Push-Location (Join-Path $repoRoot "bloc-node")
      try {
        $env:GOCACHE = Join-Path (Get-Location) ".gocache"
        go run ./cmd/bloc-node gen-ec2-config --inventory (Join-Path $phaseRoot "inventory.json") --cluster-out $clusterPath --crs-out $crsPath --secrets-dir $secretsPath --remote-eval-out $remotePath --cluster-id $phaseId --nodes $nodeCount --bmax 128 --prometheus-url "http://$($inventory.controller.private_ip):9090" --grafana-url "http://$($inventory.controller.private_ip):3000" --controller-url $inventory.controller.private_ip
        if ($LASTEXITCODE -ne 0) { throw "EC2 config generation failed" }
      } finally { Pop-Location }
      $prometheusPath = Join-Path $phaseRoot "generated\prometheus.ec2.yml"; New-PrometheusConfig $inventory $prometheusPath

      $hostMetadata = @()
      foreach ($hostValue in @($inventory.controller) + @($inventory.nodes)) {
        $metadataNodeID = if ($hostValue.PSObject.Properties.Name -contains "id") { $hostValue.id } else { "controller" }
        $hostMetadata += [ordered]@{ instance_id = $hostValue.instance_id; node_id = $metadataNodeID; region = $hostValue.region; zone = $hostValue.zone; instance_type = $hostValue.instance_type; ami_id = $hostValue.ami_id; cpu_model = Get-HostSSHText $hostValue "lscpu | grep -m1 'Model name' | cut -d: -f2- | xargs" $primaryKeyPath $secondaryKeyPath; logical_cpus = Get-HostSSHText $hostValue "nproc" $primaryKeyPath $secondaryKeyPath }
      }
      $hostMetadata | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $phaseRoot "host-metadata.json")

      foreach ($node in ($inventory.nodes | Sort-Object id)) {
        Invoke-HostSSH $node "sudo mkdir -p /etc/bloc /opt/bloc/ec2 && sudo chown -R ubuntu:ubuntu /etc/bloc /opt/bloc" $primaryKeyPath $secondaryKeyPath | Out-Null
        Invoke-HostSCP $node @($clusterPath, "ubuntu@$($node.public_ip):/etc/bloc/cluster.json") $primaryKeyPath $secondaryKeyPath
        Invoke-HostSCP $node @($crsPath, "ubuntu@$($node.public_ip):/etc/bloc/cluster.crs") $primaryKeyPath $secondaryKeyPath
        Invoke-HostSCP $node @((Join-Path $secretsPath "operator-$($node.id).json"), "ubuntu@$($node.public_ip):/etc/bloc/operator.json") $primaryKeyPath $secondaryKeyPath
        Invoke-HostSCP $node @((Join-Path $PSScriptRoot "operator-compose.yaml"), "ubuntu@$($node.public_ip):/opt/bloc/ec2/operator-compose.yaml") $primaryKeyPath $secondaryKeyPath
        Invoke-HostSSH $node "sudo chown 10001:10001 /etc/bloc/operator.json; sudo chmod 600 /etc/bloc/operator.json; aws ecr get-login-password --region '$PrimaryRegion' | docker login --username AWS --password-stdin '$registry'; cd /opt/bloc/ec2; NODE_ID='$($node.id)' BLOC_IMAGE='$imageURI' docker compose -f operator-compose.yaml up -d" $primaryKeyPath $secondaryKeyPath | Out-Null
      }
      Remove-Item -LiteralPath $secretsPath -Recurse -Force
      $controller = $inventory.controller
      Invoke-HostSSH $controller "sudo mkdir -p /opt/bloc/ec2 /opt/bloc/docker-compose/grafana && sudo chown -R ubuntu:ubuntu /opt/bloc" $primaryKeyPath $secondaryKeyPath | Out-Null
      Invoke-HostSCP $controller @((Join-Path $PSScriptRoot "controller-compose.yaml"), "ubuntu@$($controller.public_ip):/opt/bloc/ec2/controller-compose.yaml") $primaryKeyPath $secondaryKeyPath
      Invoke-HostSCP $controller @($prometheusPath, "ubuntu@$($controller.public_ip):/opt/bloc/ec2/prometheus.ec2.yml") $primaryKeyPath $secondaryKeyPath
      Invoke-HostSCP $controller @($remotePath, "ubuntu@$($controller.public_ip):/opt/bloc/ec2/remote-eval.ec2.json") $primaryKeyPath $secondaryKeyPath
      $primaryKey = Get-KeyPath $controller $primaryKeyPath $secondaryKeyPath
      & scp -i $primaryKey -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -r (Join-Path $repoRoot "deploy\docker-compose\grafana\*") "ubuntu@$($controller.public_ip):/opt/bloc/docker-compose/grafana/"
      if ($LASTEXITCODE -ne 0) { throw "Grafana provisioning copy failed" }
      Invoke-HostSSH $controller "cd /opt/bloc/ec2; docker compose -f controller-compose.yaml up -d" $primaryKeyPath $secondaryKeyPath | Out-Null
      foreach ($node in ($inventory.nodes | Sort-Object id)) {
        for ($attempt = 1; $attempt -le 30; $attempt++) {
          $code = Invoke-HostSSH $controller "curl --max-time 5 -fsS http://$($node.private_ip):8000/healthz" $primaryKeyPath $secondaryKeyPath -AllowFailure
          if ($code -eq 0) { break }; if ($attempt -eq 30) { throw "operator $($node.id) did not become healthy" }; Start-Sleep -Seconds 5
        }
      }
      Invoke-HostSSH $controller "curl -fsS http://127.0.0.1:9090/api/v1/targets > /opt/bloc/ec2/prometheus-targets-before.json" $primaryKeyPath $secondaryKeyPath | Out-Null
      Invoke-HostSCP $controller @("ubuntu@$($controller.public_ip):/opt/bloc/ec2/prometheus-targets-before.json", (Join-Path $phaseRoot "prometheus-targets-before.json")) $primaryKeyPath $secondaryKeyPath
      Assert-PrometheusTargets (Join-Path $phaseRoot "prometheus-targets-before.json") $nodeCount
      Collect-PairwiseNetwork $inventory "pre" (Join-Path $phaseRoot "network-pre.csv") $primaryKeyPath $secondaryKeyPath
      Collect-CPUCreditSnapshot $inventory "pre" (Join-Path $phaseRoot "cpu-credits-pre.csv")

      $scenarioRoots = @(); $nextSlot = 1
      foreach ($batch in $BatchSizes) {
        $remoteDir = "/opt/bloc/ec2/results/$phaseId/batch-$batch"
        $command = "sudo mkdir -p '$remoteDir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$imageURI' eval-remote --config remote-eval.ec2.json --experiment-id '$phaseId-b$batch' --first-slot '$nextSlot' --batch-size '$batch' --warmups '$Warmups' --repetitions '$Repetitions' --out-dir 'results/$phaseId/batch-$batch' --image-tag '$imageURI' --git-commit '$gitCommit' --timeout '$EvalTimeout'"
        $code = Invoke-HostSSH $controller $command $primaryKeyPath $secondaryKeyPath -AllowFailure
        $localParent = Join-Path $phaseRoot "scenarios\batch-$batch"; New-Item -ItemType Directory -Force $localParent | Out-Null
        & scp -i $primaryKey -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -r "ubuntu@$($controller.public_ip):$remoteDir" $localParent
        if ($LASTEXITCODE -ne 0) { throw "scenario artifact collection failed for batch $batch" }
        $scenarioRoots += Join-Path $localParent "batch-$batch"
        if ($code -ne 0) { throw "eval-remote failed for batch $batch" }
        $nextSlot += $Warmups + $Repetitions
      }
      Merge-ScenarioCSVs $phaseRoot $scenarioRoots
      Collect-PairwiseNetwork $inventory "post" (Join-Path $phaseRoot "network-post.csv") $primaryKeyPath $secondaryKeyPath
      Collect-CPUCreditSnapshot $inventory "post" (Join-Path $phaseRoot "cpu-credits-post.csv")
      Invoke-HostSSH $controller "curl -fsS http://127.0.0.1:9090/api/v1/targets > /opt/bloc/ec2/prometheus-targets-after.json" $primaryKeyPath $secondaryKeyPath | Out-Null
      Invoke-HostSCP $controller @("ubuntu@$($controller.public_ip):/opt/bloc/ec2/prometheus-targets-after.json", (Join-Path $phaseRoot "prometheus-targets.json")) $primaryKeyPath $secondaryKeyPath
      Assert-PrometheusTargets (Join-Path $phaseRoot "prometheus-targets.json") $nodeCount
      Assert-PhaseResults $phaseRoot $nodeCount
      foreach ($node in ($inventory.nodes | Sort-Object id)) {
        $key = Get-KeyPath $node $primaryKeyPath $secondaryKeyPath
        & ssh -i $key -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($node.public_ip)" "docker logs --timestamps ec2-bloc-node-1 2>&1" | Set-Content -Encoding utf8 (Join-Path $phaseRoot "logs\operator-$($node.id).log")
      }
      & ssh -i $primaryKey -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($controller.public_ip)" "docker logs --timestamps ec2-prometheus-1 2>&1" | Set-Content -Encoding utf8 (Join-Path $phaseRoot "logs\prometheus.log")
      & ssh -i $primaryKey -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($controller.public_ip)" "docker logs --timestamps ec2-grafana-1 2>&1" | Set-Content -Encoding utf8 (Join-Path $phaseRoot "logs\grafana.log")
      [ordered]@{ schema_version = "bloc-ec2-cross-region-phase/v1"; experiment_id = $phaseId; status = "complete"; topology = "T2-cross-region"; node_count = $nodeCount; placement = @($inventory.nodes | Sort-Object id | Select-Object id,region,zone); primary_region = $PrimaryRegion; secondary_region = $SecondaryRegion; peering_connection_id = $peeringID; operator_instance_type = $OperatorInstanceType; controller_instance_type = $ControllerInstanceType; git_commit = $gitCommit; docker_image = $imageURI; docker_image_digest = $phaseDigest; batch_sizes = $BatchSizes; warmups = $Warmups; repetitions = $Repetitions; eval_timeout = $EvalTimeout } | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 (Join-Path $phaseRoot "manifest.json")
      $phaseFailed = $false
    }
    finally {
      if (Test-Path (Join-Path $phaseRoot "generated\secrets.ec2")) { Remove-Item -LiteralPath (Join-Path $phaseRoot "generated\secrets.ec2") -Recurse -Force }
      if ($phaseFailed -and $KeepResourcesOnFailure) {
        Write-Warning "Keeping failed n=$nodeCount resources. SSH keys remain at $primaryKeyPath and $secondaryKeyPath"
      } else {
        Push-Location $workDir
        try {
          terraform destroy -auto-approve "-var-file=campaign.tfvars" 2>&1 | Set-Content -Encoding utf8 (Join-Path $phaseRoot "terraform-destroy.log")
          if ($LASTEXITCODE -ne 0) { throw "Terraform destroy failed for n=$nodeCount" }
        }
        finally { Pop-Location }
        & $aws ec2 delete-key-pair --profile $AwsProfile --region $PrimaryRegion --key-name $primaryKeyName | Out-Null
        & $aws ec2 delete-key-pair --profile $AwsProfile --region $SecondaryRegion --key-name $secondaryKeyName | Out-Null
        foreach ($keyPath in @($primaryKeyPath, $secondaryKeyPath)) { if (Test-Path $keyPath) { icacls $keyPath /grant:r "$env:USERNAME`:F" | Out-Null; Remove-Item -LiteralPath $keyPath -Force } }
      }
    }
    if ($phaseFailed -and $KeepResourcesOnFailure) {
      $phaseRecords.Add([pscustomobject]@{ node_count = $nodeCount; experiment_id = $phaseId; artifact_root = $phaseRoot; status = "invalid-resources-kept" })
      throw "cross-region phase n=$nodeCount failed; resources were kept for debugging"
    }
    $cleanup = [ordered]@{ regions = [ordered]@{} }
    foreach ($region in @($PrimaryRegion, $SecondaryRegion)) {
      $regionCleanup = [ordered]@{
        instances = (& $aws ec2 describe-instances --profile $AwsProfile --region $region --filters "Name=tag:Name,Values=$phaseId-*" "Name=instance-state-name,Values=pending,running,stopping,stopped" --query "Reservations[].Instances[].InstanceId" --output text).Trim()
        volumes = (& $aws ec2 describe-volumes --profile $AwsProfile --region $region --filters "Name=tag:Name,Values=$phaseId-*" --query "Volumes[].VolumeId" --output text).Trim()
        vpcs = (& $aws ec2 describe-vpcs --profile $AwsProfile --region $region --filters "Name=tag:Name,Values=$phaseId-*" --query "Vpcs[].VpcId" --output text).Trim()
        peering_connections = (& $aws ec2 describe-vpc-peering-connections --profile $AwsProfile --region $region --filters "Name=tag:Name,Values=$phaseId-peering" --query "VpcPeeringConnections[?Status.Code!='deleted'].VpcPeeringConnectionId" --output text).Trim()
        key_pairs = (& $aws ec2 describe-key-pairs --profile $AwsProfile --region $region --filters "Name=key-name,Values=$phaseId-*" --query "KeyPairs[].KeyName" --output text).Trim()
      }
      $cleanup.regions[$region] = $regionCleanup
    }
    $cleanup.ecr_repository = ((& $aws ecr describe-repositories --profile $AwsProfile --region $PrimaryRegion --repository-names "bloc-node-$phaseId" --query "repositories[].repositoryName" --output text 2>$null) | Out-String).Trim()
    $cleanup.iam_role = ((& $aws iam get-role --profile $AwsProfile --role-name "$phaseId-ec2-ecr-readonly" --query "Role.RoleName" --output text 2>$null) | Out-String).Trim()
    $cleanup.instance_profile = ((& $aws iam get-instance-profile --profile $AwsProfile --instance-profile-name "$phaseId-ec2-ecr-readonly" --query "InstanceProfile.InstanceProfileName" --output text 2>$null) | Out-String).Trim()
    $regionalLeftovers = @($cleanup.regions.Values | ForEach-Object { $_.Values } | Where-Object { $_ })
    $globalLeftovers = @($cleanup.ecr_repository, $cleanup.iam_role, $cleanup.instance_profile) | Where-Object { $_ }
    $leftovers = $regionalLeftovers + $globalLeftovers
    if ($leftovers.Count -gt 0) { throw "cross-region cleanup is incomplete: $($leftovers -join ', ')" }
    $cleanup | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $phaseRoot "cleanup-verification.json")
    if ($phaseFailed) { throw "cross-region phase n=$nodeCount failed" }
    $phaseRecords.Add([pscustomobject]@{ node_count = $nodeCount; experiment_id = $phaseId; artifact_root = $phaseRoot; status = "complete"; image_digest = $imageDigest; cleanup_checks = $cleanup })
    Write-CampaignManifest "invalid" "campaign still running"
    if (-not $AutoApprovePhases -and $nodeCount -ne $NodeCounts[-1]) { if ((Read-Host "Type NEXT to launch the next node count") -ne "NEXT") { throw "campaign stopped after n=$nodeCount" } }
  }

  if ($PlanOnly) { Write-CampaignManifest "planned"; exit 0 }
  Merge-PhaseOutputs
  Write-CampaignManifest "analyzing"
  if (-not $SkipChartGeneration) {
    $python = Join-Path $repoRoot "latency-charts\.venv\Scripts\python.exe"
    if (-not (Test-Path $python)) { throw "cross-region chart generation requires $python" }
    Push-Location (Join-Path $repoRoot "latency-charts")
    try { & $python -m bloc_latency_charts.cross_region $campaignRoot; if ($LASTEXITCODE -ne 0) { throw "cross-region analysis failed" } }
    finally { Pop-Location }
  }
  Write-CampaignManifest "complete"
}
catch {
  Write-CampaignManifest "invalid" $_.Exception.Message
  throw
}
