param(
  [Parameter(Mandatory = $true)]
  [string[]]$AdminCidrs,
  [string]$AwsProfile = "bloc",
  [string]$AwsRegion = "us-east-1",
  [string]$AvailabilityZone = "us-east-1a",
  [string]$ComputeFlexInstanceType = "c7i-flex.large",
  [string]$ComputeFlexFallbackInstanceType = "m7i-flex.large",
  [string]$BurstableInstanceType = "t3.small",
  [string]$ControllerInstanceType = "t3.small",
  [string]$CampaignId = "",
  [string]$PrebuiltCampaignImageTag = "",
  [decimal]$CostCeilingUSD = 5.00,
  [switch]$AutoApprovePlan,
  [switch]$ResumeCompletedPhases,
  [switch]$KeepResourcesOnFailure
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$aws = "C:\Program Files\Amazon\AWSCLIV2\aws.exe"
if (-not (Test-Path $aws)) { $aws = "aws" }
$python = Join-Path $repoRoot "latency-charts\.venv\Scripts\python.exe"
if (-not (Test-Path $python)) { $python = "python" }
if ([string]::IsNullOrWhiteSpace($CampaignId)) {
  $CampaignId = "merge-plan-attribution-" + (Get-Date).ToUniversalTime().ToString("yyyyMMdd't'HHmmss'z'")
}
if ($CampaignId -notmatch '^[a-z0-9-]+$') {
  throw "CampaignId must contain only lowercase letters, digits, and hyphens."
}

$campaignRoot = Join-Path $repoRoot (Join-Path "results\ec2" $CampaignId)
$runner = Join-Path $PSScriptRoot "run-a1-pilot.ps1"
$startedAt = Get-Date
$awsRunToken = $startedAt.ToUniversalTime().ToString("yyyyMMddHHmmss")
$phasesCompleted = @()
$expectedDigest = ""
$estimatedCostUSD = [decimal]0
$campaignCommands = [System.Collections.Generic.List[string]]::new()
$gitCommit = ""
$imageTag = ""
$localImageID = ""
$goVersion = ""

function Invoke-Required {
  param([scriptblock]$Command, [string]$Description)
  Write-Host ""
  Write-Host "==> $Description"
  & $Command
  if ($LASTEXITCODE -ne 0) {
    throw "$Description failed with exit code $LASTEXITCODE"
  }
}

function Assert-CommittedCampaignSources {
  $allowedDirty = @(
    "deploy/k8s/",
    "bloc-node/remote-eval.k8s-local.json"
  )
  $relevantPrefixes = @(
    "bloc-node/",
    "bte/",
    "sbc/",
    "latency-charts/",
    "deploy/ec2/",
    "docs/"
  )
  $blocked = @()
  foreach ($line in (git status --porcelain=v1 --untracked-files=all)) {
    if ([string]::IsNullOrWhiteSpace($line)) { continue }
    $path = $line.Substring(3).Replace("\", "/")
    if ($path.Contains(" -> ")) { $path = ($path -split " -> ")[-1] }
    if ($allowedDirty | Where-Object { $path.StartsWith($_) }) { continue }
    if ($relevantPrefixes | Where-Object { $path.StartsWith($_) }) { $blocked += $path }
  }
  if ($blocked.Count -gt 0) {
    throw "Campaign sources must be committed before image creation. Relevant dirty paths: $($blocked -join ', ')"
  }
}

function Get-AwsText {
  param([string[]]$Arguments)
  $output = & $aws @Arguments
  if ($LASTEXITCODE -ne 0) { throw "AWS command failed: $($Arguments -join ' ')" }
  return (($output | Out-String).Trim())
}

function Test-InstanceOffering {
  param([string]$InstanceType)
  $value = Get-AwsText @(
    "ec2", "describe-instance-type-offerings",
    "--profile", $AwsProfile,
    "--region", $AwsRegion,
    "--location-type", "availability-zone",
    "--filters", "Name=location,Values=$AvailabilityZone", "Name=instance-type,Values=$InstanceType",
    "--query", "InstanceTypeOfferings[0].InstanceType",
    "--output", "text"
  )
  return -not [string]::IsNullOrWhiteSpace($value) -and $value -ne "None"
}

function Assert-FreeTierEligible {
  param([string]$InstanceType)
  $eligible = Get-AwsText @(
    "ec2", "describe-instance-types",
    "--profile", $AwsProfile,
    "--region", $AwsRegion,
    "--instance-types", $InstanceType,
    "--query", "InstanceTypes[0].FreeTierEligible",
    "--output", "text"
  )
  if ($eligible -ne "True") {
    throw "$InstanceType is not Free Tier eligible; refusing to use it with this campaign."
  }
}

function Test-PrometheusTargets {
  param([string]$Path, [int]$Expected)
  $payload = Get-Content -Raw $Path | ConvertFrom-Json
  $targets = @($payload.data.activeTargets | Where-Object { $_.labels.job -eq "bloc-sidecars" })
  if ($targets.Count -ne $Expected -or @($targets | Where-Object { $_.health -ne "up" }).Count -gt 0) {
    throw "Prometheus targets are not $Expected/$Expected up in $Path"
  }
}

function Assert-PhaseArtifacts {
  param([string]$Path, [object]$Phase)
  $manifest = Get-Content -Raw (Join-Path $Path "manifest.json") | ConvertFrom-Json
  if ($manifest.status -ne "complete") { throw "$($Phase.id) did not complete" }
  $rows = @(Import-Csv (Join-Path $Path "node_measurements.csv") | Where-Object { $_.phase -eq "measured" })
  if ($rows.Count -eq 0) { throw "$($Phase.id) has no measured node rows" }
  $required = @("acs_output_decode_us", "agreed_set_us", "merge_us", "ciphertext_decode_us", "batch_plan_us", "merge_plan_us", "selected_ciphertexts", "metrics_finalized", "measurement_block")
  foreach ($column in $required) {
    if (-not ($rows[0].PSObject.Properties.Name -contains $column)) { throw "$($Phase.id) is missing $column" }
  }
  foreach ($batch in @(8, 32, 128)) {
    $batchRows = @($rows | Where-Object { [int]$_.selected_ciphertexts -eq $batch })
    $runs = @($batchRows.run_id | Sort-Object -Unique)
    if ($runs.Count -ne 30) { throw "$($Phase.id) batch $batch has $($runs.Count) runs instead of 30" }
    foreach ($row in $batchRows) {
      if ($row.success -ne "true" -or $row.consistent -ne "true" -or $row.metrics_finalized -ne "true") {
        throw "$($Phase.id) contains an invalid measured row"
      }
      $sum = [long]$row.acs_output_decode_us + [long]$row.agreed_set_us + [long]$row.merge_us + [long]$row.ciphertext_decode_us + [long]$row.batch_plan_us
      if ([Math]::Abs($sum - [long]$row.merge_plan_us) -gt 20) {
        throw "$($Phase.id) violates Merge + Plan additivity"
      }
    }
  }
  Test-PrometheusTargets (Join-Path $Path "prometheus-targets.json") $Phase.nodes
  $cleanup = Get-Content -Raw (Join-Path $Path "cleanup-verification.json") | ConvertFrom-Json
  foreach ($property in $cleanup.PSObject.Properties) {
    if (-not [string]::IsNullOrWhiteSpace([string]$property.Value)) {
      throw "$($Phase.id) cleanup left $($property.Name): $($property.Value)"
    }
  }
  return $manifest
}

function Write-CampaignManifest {
  param(
    [string]$Status,
    [string]$GitCommit,
    [string]$ImageTag,
    [string]$GoVersion,
    [string]$InvalidReason = ""
  )
  [ordered]@{
    schema_version = "bloc-merge-plan-attribution/v1"
    campaign_id = $CampaignId
    status = $Status
    invalid_reason = if ([string]::IsNullOrWhiteSpace($InvalidReason)) { $null } else { $InvalidReason }
    started_at = $startedAt.ToUniversalTime().ToString("o")
    updated_at = (Get-Date).ToUniversalTime().ToString("o")
    git_commit = $GitCommit
    local_image_tag = $ImageTag
    image_digest = $expectedDigest
    build_go_version = $GoVersion
    aws_region = $AwsRegion
    availability_zone = $AvailabilityZone
    transaction_source = "synthetic"
    transaction_size_bytes = 256
    bmax = 128
    warmups = 5
    repetitions = 30
    batch_order_blocks = @("32,8,128", "128,32,8", "8,128,32")
    cost_ceiling_usd = $CostCeilingUSD
    conservative_estimated_cost_usd = [Math]::Round($estimatedCostUSD, 4)
    commands = $campaignCommands.ToArray()
    phases = $phasesCompleted
  } | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 (Join-Path $campaignRoot "manifest.json")
}

Push-Location $repoRoot
try {
  New-Item -ItemType Directory -Force $campaignRoot | Out-Null
  Assert-CommittedCampaignSources
  $gitCommit = (git rev-parse --short=12 HEAD).Trim()

  $campaignCommands.Add("aws sts get-caller-identity --profile $AwsProfile --output json")
  Invoke-Required { & $aws sts get-caller-identity --profile $AwsProfile --output json | Out-Null } "AWS identity preflight"
  $campaignCommands.Add("docker version")
  Invoke-Required { docker version | Out-Null } "Docker preflight"
  $campaignCommands.Add("terraform -chdir=deploy/ec2/terraform fmt -check -diff")
  Invoke-Required { terraform -chdir=deploy/ec2/terraform fmt -check -diff } "Terraform format check"
  $campaignCommands.Add("terraform -chdir=deploy/ec2/terraform init -backend=false -input=false")
  Invoke-Required { terraform -chdir=deploy/ec2/terraform init -backend=false -input=false | Out-Null } "Terraform init"
  $campaignCommands.Add("terraform -chdir=deploy/ec2/terraform validate")
  Invoke-Required { terraform -chdir=deploy/ec2/terraform validate } "Terraform validation"
  $campaignCommands.Add("cd bloc-node; go test ./...")
  Invoke-Required { Push-Location bloc-node; try { $env:GOCACHE = Join-Path (Get-Location) ".gocache"; go test ./... } finally { Pop-Location } } "bloc-node tests"
  $campaignCommands.Add("cd bte/btd-impl-main; go test ./...")
  Invoke-Required { Push-Location bte/btd-impl-main; try { $env:GOCACHE = Join-Path (Get-Location) ".gocache"; go test ./... } finally { Pop-Location } } "BTE tests"
  $campaignCommands.Add("cd latency-charts; python -m pytest")
  Invoke-Required { Push-Location latency-charts; try { & $python -m pytest } finally { Pop-Location } } "latency chart tests"

  $quotaText = Get-AwsText @(
    "service-quotas", "get-service-quota",
    "--profile", $AwsProfile,
    "--region", $AwsRegion,
    "--service-code", "ec2",
    "--quota-code", "L-1216C47A",
    "--query", "Quota.Value",
    "--output", "text"
  )
  if ([double]$quotaText -lt 16) { throw "Standard On-Demand vCPU quota is $quotaText; compute-flex-n7 requires 16." }

  Assert-FreeTierEligible $ComputeFlexInstanceType
  Assert-FreeTierEligible $ComputeFlexFallbackInstanceType
  Assert-FreeTierEligible $BurstableInstanceType
  Assert-FreeTierEligible $ControllerInstanceType

  if (-not (Test-InstanceOffering $ComputeFlexInstanceType)) {
    if (Test-InstanceOffering $ComputeFlexFallbackInstanceType) {
      Write-Warning "$ComputeFlexInstanceType is not offered in $AvailabilityZone; using $ComputeFlexFallbackInstanceType."
      $ComputeFlexInstanceType = $ComputeFlexFallbackInstanceType
    } else {
      throw "Neither $ComputeFlexInstanceType nor $ComputeFlexFallbackInstanceType is offered in $AvailabilityZone."
    }
  }
  if (-not (Test-InstanceOffering $BurstableInstanceType)) {
    throw "$BurstableInstanceType is not offered in $AvailabilityZone."
  }

  $conservativeProjectedCost = [decimal]4.71
  if ($CostCeilingUSD -lt $conservativeProjectedCost) {
    throw "The conservative three-phase estimate is $conservativeProjectedCost USD, above the configured ceiling of $CostCeilingUSD USD."
  }

  if ([string]::IsNullOrWhiteSpace($PrebuiltCampaignImageTag)) {
    $imageTag = "bloc-node:$CampaignId-$gitCommit"
    $campaignCommands.Add("docker build -f bloc-node/Dockerfile -t $imageTag .")
    Invoke-Required { docker build -f bloc-node/Dockerfile -t $imageTag . } "build one campaign image"
  } else {
    $imageTag = $PrebuiltCampaignImageTag
    $campaignCommands.Add("docker image inspect $imageTag")
    Invoke-Required { docker image inspect $imageTag | Out-Null } "verify prebuilt campaign image"
  }
  $localImageID = (docker image inspect $imageTag --format '{{.Id}}').Trim()
  if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($localImageID)) {
    throw "could not capture the local campaign image ID"
  }
  $goVersion = ((docker run --rm --entrypoint go golang:1.25-bookworm version | Out-String).Trim())
  if ($LASTEXITCODE -ne 0) { throw "could not capture the image builder Go version" }

  $phases = @(
    [pscustomobject]@{ id = "compute-flex-n4"; nodes = 4; operator = $ComputeFlexInstanceType },
    [pscustomobject]@{ id = "compute-flex-n7"; nodes = 7; operator = $ComputeFlexInstanceType },
    [pscustomobject]@{ id = "burstable-n7"; nodes = 7; operator = $BurstableInstanceType }
  )
  $ecrTag = ($gitCommit + "-" + $CampaignId)

  foreach ($phase in $phases) {
    $operatorHourlyCap = if ($phase.operator -eq $BurstableInstanceType) { [decimal]0.05 } else { [decimal]0.24 }
    $phaseHourlyCap = ([decimal]$phase.nodes * $operatorHourlyCap) + [decimal]0.05
    $phasePath = Join-Path $campaignRoot $phase.id
    if ($ResumeCompletedPhases -and (Test-Path $phasePath)) {
      Write-Host "Resuming accepted phase artifacts: $($phase.id)"
      $phaseManifest = Assert-PhaseArtifacts $phasePath $phase
      if ([int]$phaseManifest.node_count -ne $phase.nodes -or [string]$phaseManifest.operator_instance_type -ne $phase.operator) {
        throw "$($phase.id) resume metadata does not match the requested topology"
      }
      $digest = [string]$phaseManifest.terraform.docker_image_digest
      if ([string]::IsNullOrWhiteSpace($expectedDigest)) { $expectedDigest = $digest }
      if ($digest -ne $expectedDigest) { throw "$($phase.id) image digest differs from accepted phases" }
      if ($localImageID -ne $expectedDigest) {
        throw "local prebuilt image ID $localImageID differs from resumed digest $expectedDigest"
      }
      $phaseStartedAt = [datetime]$phaseManifest.started_at
      $phaseFinishedAt = [datetime]$phaseManifest.finished_at
      $phaseHours = [decimal]($phaseFinishedAt - $phaseStartedAt).TotalHours
      $phaseEstimatedCost = $phaseHours * $phaseHourlyCap
      $estimatedCostUSD += $phaseEstimatedCost
      $phasesCompleted += [ordered]@{
        id = $phase.id
        path = $phase.id
        nodes = $phase.nodes
        operator_instance_type = $phase.operator
        controller_instance_type = $ControllerInstanceType
        image_digest = $digest
        started_at = $phaseStartedAt.ToUniversalTime().ToString("o")
        finished_at = $phaseFinishedAt.ToUniversalTime().ToString("o")
        duration_minutes = [Math]::Round(($phaseFinishedAt - $phaseStartedAt).TotalMinutes, 2)
        conservative_estimated_cost_usd = [Math]::Round($phaseEstimatedCost, 4)
        resumed_from_completed_artifacts = $true
      }
      Write-CampaignManifest "in-progress" $gitCommit $imageTag $goVersion
      continue
    }
    $phaseWorstCaseCost = [decimal]1.5 * $phaseHourlyCap
    if (($estimatedCostUSD + $phaseWorstCaseCost) -gt $CostCeilingUSD) {
      throw "Conservative cost projection would exceed $CostCeilingUSD USD; refusing to launch $($phase.id)."
    }
    $phaseStartedAt = Get-Date
    $experimentId = "bloc-ec2-mpa-$awsRunToken-$($phase.id)"
    $sourcePath = Join-Path $repoRoot (Join-Path "results\ec2" $experimentId)
    $phaseArgs = @{
      AdminCidrs = $AdminCidrs
      AwsProfile = $AwsProfile
      AwsRegion = $AwsRegion
      AvailabilityZone = $AvailabilityZone
      NodeCount = $phase.nodes
      OperatorInstanceType = $phase.operator
      ControllerInstanceType = $ControllerInstanceType
      BatchSizes = @(8, 32, 128)
      Warmups = 5
      Repetitions = 30
      RepetitionBlocks = 3
      BatchOrderBlocks = @("32,8,128", "128,32,8", "8,128,32")
      PrebuiltImageTag = $imageTag
      EcrImageTag = $ecrTag
      CampaignLabel = "Merge-Plan-attribution-$($phase.id)"
      Topology = "T0-same-az"
      ExperimentId = $experimentId
      MaxRuntimeMinutes = 90
      SkipChartGeneration = $true
    }
    if ($AutoApprovePlan) { $phaseArgs.AutoApprovePlan = $true }
    if ($KeepResourcesOnFailure) { $phaseArgs.KeepResourcesOnFailure = $true }
    $campaignCommands.Add("run-a1-pilot.ps1 -ExperimentId $experimentId -NodeCount $($phase.nodes) -OperatorInstanceType $($phase.operator) -RepetitionBlocks 3 -BatchOrderBlocks 32,8,128 128,32,8 8,128,32")
    & $runner @phaseArgs
    if ($LASTEXITCODE -ne 0) { throw "$($phase.id) runner failed" }

    if (Test-Path $phasePath) { throw "phase artifact path already exists: $phasePath" }
    Move-Item -LiteralPath $sourcePath -Destination $phasePath
    $phaseManifest = Assert-PhaseArtifacts $phasePath $phase
    $digest = [string]$phaseManifest.terraform.docker_image_digest
    if ([string]::IsNullOrWhiteSpace($digest)) { throw "$($phase.id) has no image digest" }
    if ([string]::IsNullOrWhiteSpace($expectedDigest)) { $expectedDigest = $digest }
    if ($digest -ne $expectedDigest) { throw "$($phase.id) image digest differs from accepted phases" }
    $phaseHours = [decimal]((Get-Date) - $phaseStartedAt).TotalHours
    $phaseEstimatedCost = $phaseHours * $phaseHourlyCap
    $estimatedCostUSD += $phaseEstimatedCost
    $phasesCompleted += [ordered]@{
      id = $phase.id
      path = $phase.id
      nodes = $phase.nodes
      operator_instance_type = $phase.operator
      controller_instance_type = $ControllerInstanceType
      image_digest = $digest
      started_at = $phaseStartedAt.ToUniversalTime().ToString("o")
      finished_at = (Get-Date).ToUniversalTime().ToString("o")
      duration_minutes = [Math]::Round(((Get-Date) - $phaseStartedAt).TotalMinutes, 2)
      conservative_estimated_cost_usd = [Math]::Round($phaseEstimatedCost, 4)
    }
    Write-CampaignManifest "in-progress" $gitCommit $imageTag $goVersion
  }

  $campaignCommands.Add("python -m bloc_latency_charts.merge_plan_campaign $campaignRoot")
  Write-CampaignManifest "analysis-pending" $gitCommit $imageTag $goVersion
  Invoke-Required {
    Push-Location latency-charts
    try { & $python -m bloc_latency_charts.merge_plan_campaign $campaignRoot }
    finally { Pop-Location }
  } "campaign analysis and chart generation"
  Write-CampaignManifest "complete" $gitCommit $imageTag $goVersion
  Write-Host "Campaign complete: $campaignRoot"
}
catch {
  Write-CampaignManifest "invalid" $gitCommit $imageTag $goVersion $_.Exception.Message
  throw
}
finally {
  Pop-Location
}
