param(
  [Parameter(Mandatory = $true)]
  [string[]]$AdminCidrs,
  [string]$AwsProfile = "bloc",
  [string]$AwsRegion = "us-east-1",
  [string]$AvailabilityZone = "us-east-1a",
  [int[]]$NodeCounts = @(4, 7, 10),
  [string]$NodeCountsCsv = "",
  [string]$OperatorInstanceType = "t3.small",
  [string]$ControllerInstanceType = "t3.small",
  [int[]]$BatchSizes = @(8, 32, 128),
  [string]$BatchSizesCsv = "",
  [int]$Warmups = 5,
  [int]$Repetitions = 30,
  [string]$CampaignId = "",
  [string]$BaselineCampaignRoot = "",
  [switch]$AutoApprovePlan,
  [switch]$AutoApprovePhases,
  [switch]$Unattended,
  [switch]$KeepResourcesOnFailure,
  [switch]$SkipChartGeneration
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function ConvertFrom-IntegerCsv {
  param([string]$Raw, [string]$Name)
  $values = @()
  foreach ($part in ($Raw -split ",")) {
    $trimmed = $part.Trim()
    $parsed = 0
    if ([string]::IsNullOrWhiteSpace($trimmed) -or -not [int]::TryParse($trimmed, [ref]$parsed)) {
      throw "$Name must be a comma-separated list of integers; received '$Raw'."
    }
    $values += $parsed
  }
  return [int[]]$values
}

if (-not [string]::IsNullOrWhiteSpace($NodeCountsCsv)) {
  $NodeCounts = ConvertFrom-IntegerCsv $NodeCountsCsv "NodeCountsCsv"
}
if (-not [string]::IsNullOrWhiteSpace($BatchSizesCsv)) {
  $BatchSizes = ConvertFrom-IntegerCsv $BatchSizesCsv "BatchSizesCsv"
}
if ($NodeCounts.Count -eq 0 -or @($NodeCounts | Where-Object { $_ -notin @(4, 7, 10) }).Count -gt 0) {
  throw "M3 node counts must be selected from 4, 7, and 10; received $($NodeCounts -join ',')."
}
if ($BatchSizes.Count -eq 0 -or @($BatchSizes | Where-Object { $_ -notin @(8, 32, 128) }).Count -gt 0) {
  throw "M3 batch sizes must be selected from 8, 32, and 128; received $($BatchSizes -join ',')."
}
if ($Warmups -lt 0 -or $Repetitions -lt 1) {
  throw "Warmups must be non-negative and Repetitions must be positive."
}
if ($Unattended) {
  $AutoApprovePlan = $true
  $AutoApprovePhases = $true
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$phaseScript = Join-Path $PSScriptRoot "run-a1-pilot.ps1"
if (-not (Test-Path $phaseScript)) { throw "missing phase runner: $phaseScript" }

if ([string]::IsNullOrWhiteSpace($CampaignId)) {
  $stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd't'HHmmss'z'")
  $CampaignId = "m3-same-az-synthetic-$stamp"
}
if ($CampaignId -notmatch "^[a-z0-9][a-z0-9._-]*$") {
  throw "CampaignId must contain only lowercase letters, numbers, '.', '_', and '-'."
}

$campaignRoot = Join-Path $repoRoot (Join-Path "results\ec2" $CampaignId)
$commands = [System.Collections.Generic.List[string]]::new()
$phaseResults = [System.Collections.Generic.List[object]]::new()
$campaignStartedAt = (Get-Date).ToUniversalTime().ToString("o")
$campaignStatus = "invalid"
$invalidReason = ""

function Write-Step {
  param([string]$Message)
  Write-Host ""
  Write-Host "==> $Message"
}

function Record-Command {
  param([string]$Command)
  $commands.Add($Command)
  if (Test-Path $campaignRoot) {
    $commands | Set-Content -Encoding utf8 (Join-Path $campaignRoot "commands.txt")
  }
}

function Quote-PSArgument {
  param([string]$Value)
  return "'" + ($Value -replace "'", "''") + "'"
}

function Add-CsvFromPhases {
  param([string]$CsvName, [object[]]$Phases, [string]$Destination)
  if (Test-Path $Destination) { Remove-Item -LiteralPath $Destination -Force }
  $first = $true
  foreach ($phase in $Phases) {
    $source = Join-Path $phase.artifact_root $CsvName
    if (-not (Test-Path $source)) { throw "missing phase CSV: $source" }
    $lines = Get-Content $source
    if ($first) {
      $lines | Add-Content -Encoding ascii $Destination
      $first = $false
    } else {
      $lines | Select-Object -Skip 1 | Add-Content -Encoding ascii $Destination
    }
  }
}

function Assert-PhaseMeasurements {
  param([string]$PhaseRoot, [int[]]$ExpectedBatches, [int]$ExpectedRepetitions)
  $manifestPath = Join-Path $PhaseRoot "manifest.json"
  if (-not (Test-Path $manifestPath)) { throw "missing phase manifest: $manifestPath" }
  $manifest = Get-Content -Raw $manifestPath | ConvertFrom-Json
  if ($manifest.status -ne "complete") {
    throw "phase $($manifest.experiment_id) status is $($manifest.status): $($manifest.invalid_reason)"
  }

  $runs = Import-Csv (Join-Path $PhaseRoot "run_measurements.csv")
  $measured = @($runs | Where-Object { $_.phase -eq "measured" })
  $bad = @($measured | Where-Object { $_.success -ne "true" -or $_.consistent -ne "true" })
  if ($bad.Count -gt 0) {
    throw "phase $($manifest.experiment_id) has $($bad.Count) failed or inconsistent measured run(s)"
  }
  foreach ($batch in $ExpectedBatches) {
    $count = @($measured | Where-Object { [int]$_.batch_size -eq $batch }).Count
    if ($count -ne $ExpectedRepetitions) {
      throw "phase $($manifest.experiment_id) batch $batch has $count measured runs, expected $ExpectedRepetitions"
    }
  }
}

function Write-CampaignManifest {
  param([string]$Status, [string]$Reason = "")
  $cleanup = [ordered]@{}
  foreach ($phase in $phaseResults) {
    if ($phase.PSObject.Properties.Name -contains "cleanup_checks") {
      $cleanup["n$($phase.node_count)"] = $phase.cleanup_checks
    }
  }
  $cleanup | ConvertTo-Json -Depth 50 | Set-Content -Encoding utf8 (Join-Path $campaignRoot "cleanup-verification.json")

  $manifest = [ordered]@{
    schema_version = "bloc-ec2-m3-campaign/v1"
    experiment_id = $CampaignId
    campaign = "M3-same-az-synthetic"
    status = $Status
    invalid_reason = if ([string]::IsNullOrWhiteSpace($Reason)) { $null } else { $Reason }
    started_at = $campaignStartedAt
    finished_at = (Get-Date).ToUniversalTime().ToString("o")
    aws_region = $AwsRegion
    availability_zone = $AvailabilityZone
    topology = "T0-same-az"
    tx_source = "synthetic"
    node_counts = $NodeCounts
    operator_instance_type = $OperatorInstanceType
    controller_instance_type = $ControllerInstanceType
    batch_sizes = $BatchSizes
    warmups = $Warmups
    repetitions = $Repetitions
    baseline_campaign_root = if ([string]::IsNullOrWhiteSpace($BaselineCampaignRoot)) { $null } else { $BaselineCampaignRoot }
    unattended = [bool]$Unattended
    resource_policy = "destroy-after-success; keep only on failure when requested"
    failure_rule = "any failed or inconsistent measured run invalidates the phase"
    phases = $phaseResults.ToArray()
    commands = $commands.ToArray()
  }
  $manifest | ConvertTo-Json -Depth 50 | Set-Content -Encoding utf8 (Join-Path $campaignRoot "manifest.json")
}

function Merge-CampaignOutputs {
  param([object[]]$Phases)
  foreach ($csvName in @("run_measurements.csv", "node_measurements.csv", "scenario_summary.csv", "resource-samples.csv")) {
    Add-CsvFromPhases $csvName $Phases (Join-Path $campaignRoot $csvName)
  }

  $summaries = @()
  foreach ($phase in $Phases) {
    $summaryPath = Join-Path $phase.artifact_root "scenario_summary.json"
    if (-not (Test-Path $summaryPath)) { throw "missing scenario summary: $summaryPath" }
    $summaries += Get-Content -Raw $summaryPath | ConvertFrom-Json
  }
  $summaries | ConvertTo-Json -Depth 50 | Set-Content -Encoding utf8 (Join-Path $campaignRoot "scenario_summary.json")
}

New-Item -ItemType Directory -Force $campaignRoot | Out-Null

try {
  foreach ($nodeCount in $NodeCounts) {
    $phaseExperimentId = "bloc-ec2-m3-same-az-n$nodeCount-$($CampaignId -replace '^m3-same-az-synthetic-', '')"
    $phaseSourceRoot = Join-Path $repoRoot (Join-Path "results\ec2" $phaseExperimentId)
    $phaseDestRoot = Join-Path $campaignRoot "n$nodeCount"
    if (Test-Path $phaseSourceRoot) { throw "phase artifact root already exists: $phaseSourceRoot" }
    if (Test-Path $phaseDestRoot) { throw "phase destination already exists: $phaseDestRoot" }

    Write-Step "run M3 same-AZ phase n=$nodeCount"
    $adminCidrsArg = ($AdminCidrs | ForEach-Object { Quote-PSArgument $_ }) -join ","
    $batchSizesArg = ($BatchSizes | ForEach-Object { $_.ToString() }) -join ","
    $phaseCommand = @(
      "&",
      (Quote-PSArgument $phaseScript),
      "-AdminCidrs", "@($adminCidrsArg)",
      "-AwsProfile", (Quote-PSArgument $AwsProfile),
      "-AwsRegion", (Quote-PSArgument $AwsRegion),
      "-AvailabilityZone", (Quote-PSArgument $AvailabilityZone),
      "-NodeCount", $nodeCount,
      "-OperatorInstanceType", (Quote-PSArgument $OperatorInstanceType),
      "-ControllerInstanceType", (Quote-PSArgument $ControllerInstanceType),
      "-BatchSizes", "@($batchSizesArg)",
      "-Warmups", $Warmups,
      "-Repetitions", $Repetitions,
      "-CampaignLabel", (Quote-PSArgument "M3-same-az-synthetic"),
      "-ExperimentId", (Quote-PSArgument $phaseExperimentId)
    )
    if ($AutoApprovePlan) { $phaseCommand += "-AutoApprovePlan" }
    if ($KeepResourcesOnFailure) { $phaseCommand += "-KeepResourcesOnFailure" }
    if ($SkipChartGeneration) { $phaseCommand += "-SkipChartGeneration" }
    $phaseCommandText = $phaseCommand -join " "

    Record-Command "powershell -NoProfile -ExecutionPolicy Bypass -Command $phaseCommandText"
    & powershell -NoProfile -ExecutionPolicy Bypass -Command $phaseCommandText
    $phaseExit = $LASTEXITCODE

    if (Test-Path $phaseSourceRoot) {
      Move-Item -LiteralPath $phaseSourceRoot -Destination $phaseDestRoot
    }

    $phaseRecord = [ordered]@{
      node_count = $nodeCount
      experiment_id = $phaseExperimentId
      artifact_root = $phaseDestRoot
      exit_code = $phaseExit
      status = if ($phaseExit -eq 0) { "complete" } else { "invalid" }
    }
    if (Test-Path (Join-Path $phaseDestRoot "cleanup-verification.json")) {
      $phaseRecord.cleanup_checks = Get-Content -Raw (Join-Path $phaseDestRoot "cleanup-verification.json") | ConvertFrom-Json
    }
    $phaseResults.Add([pscustomobject]$phaseRecord)
    Write-CampaignManifest "invalid" "campaign still running"

    if ($phaseExit -ne 0) {
      throw "M3 phase n=$nodeCount failed with exit code $phaseExit"
    }

    Assert-PhaseMeasurements $phaseDestRoot $BatchSizes $Repetitions

    if (-not $AutoApprovePhases -and $nodeCount -ne $NodeCounts[-1]) {
      $answer = Read-Host "Phase n=$nodeCount passed. Type NEXT to launch the next node count"
      if ($answer -ne "NEXT") { throw "operator stopped M3 campaign after n=$nodeCount" }
    }
  }

  Merge-CampaignOutputs $phaseResults.ToArray()
  $campaignStatus = "complete"
  Write-CampaignManifest "complete"

  if (-not $SkipChartGeneration) {
    $chartPython = Join-Path $repoRoot "latency-charts\.venv\Scripts\python.exe"
    if (Test-Path $chartPython) {
      Push-Location (Join-Path $repoRoot "latency-charts")
      & $chartPython -m bloc_latency_charts $campaignRoot
      if ($LASTEXITCODE -ne 0) { throw "campaign chart generation failed" }
      Pop-Location
    } else {
      Write-Warning "Skipping campaign chart generation because latency-charts .venv is missing."
    }
  }

  if (-not [string]::IsNullOrWhiteSpace($BaselineCampaignRoot)) {
    $baseline = (Resolve-Path $BaselineCampaignRoot).Path
    $comparisonPython = Join-Path $repoRoot "latency-charts\.venv\Scripts\python.exe"
    if (-not (Test-Path $comparisonPython)) {
      throw "baseline comparison requires latency-charts .venv: $comparisonPython"
    }
    $comparisonOutput = Join-Path $campaignRoot "comparison"
    Record-Command "python -m bloc_latency_charts.campaign_comparison $baseline $campaignRoot --output $comparisonOutput"
    Push-Location (Join-Path $repoRoot "latency-charts")
    try {
      & $comparisonPython -m bloc_latency_charts.campaign_comparison $baseline $campaignRoot --output $comparisonOutput
      if ($LASTEXITCODE -ne 0) { throw "campaign baseline comparison failed" }
    } finally {
      Pop-Location
    }
  }
}
catch {
  $invalidReason = $_.Exception.Message
  Write-Error $_
  Write-CampaignManifest $campaignStatus $invalidReason
  exit 1
}
