param(
  [string]$CampaignId = ("acs-safety-" + (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")),
  [int]$ScheduleSeeds = 1000,
  [int]$GateRepetitions = 100,
  [int]$MatrixRepetitions = 30,
  [ValidateSet("safety", "race", "gate", "matrix", "identity")]
  [string]$StartAt = "safety",
  [switch]$Resume,
  [switch]$ReportOnly,
  [switch]$SkipRace
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
[Threading.Thread]::CurrentThread.CurrentCulture = [Globalization.CultureInfo]::InvariantCulture
[Threading.Thread]::CurrentThread.CurrentUICulture = [Globalization.CultureInfo]::InvariantCulture

if ($CampaignId -notmatch '^[A-Za-z0-9._-]+$') { throw "CampaignId contains unsupported characters" }
if ($ScheduleSeeds -ne 1000) { throw "the thesis safety gate requires exactly 1000 scheduler seeds" }
if ($GateRepetitions -ne 100) { throw "the sustained gate requires exactly 100 measured repetitions" }
if ($MatrixRepetitions -ne 30) { throw "the compatibility matrix requires exactly 30 measured repetitions per scenario" }

$moduleRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$repoRoot = (Resolve-Path (Join-Path $moduleRoot "..")).Path
$hbbftRoot = Join-Path $repoRoot "sbc\hbbft"
$bteRoot = Join-Path $repoRoot "bte\btd-impl-main"
$campaignRoot = Join-Path $repoRoot "results\local\acs-common-subset-safety\$CampaignId"
$logsRoot = Join-Path $campaignRoot "logs"
$gateRoot = Join-Path $campaignRoot "gate-n4-b128"
$matrixRoot = Join-Path $campaignRoot "matrix-n4-n7"

if ((Test-Path $campaignRoot) -and -not $Resume) { throw "campaign already exists; pass -Resume with an explicit -StartAt stage: $campaignRoot" }
if (-not (Test-Path $campaignRoot) -and $Resume) { throw "cannot resume a campaign that does not exist: $campaignRoot" }
New-Item -ItemType Directory -Force $logsRoot | Out-Null
$env:GOCACHE = Join-Path $repoRoot ".gocache"

$commands = [Collections.Generic.List[object]]::new()
$campaignStarted = (Get-Date).ToUniversalTime()
$resumedStatus = ""
if ($Resume) {
  $previousManifestPath = Join-Path $campaignRoot "manifest.json"
  if (-not (Test-Path $previousManifestPath)) { throw "resume manifest is missing: $previousManifestPath" }
  $previousManifest = Get-Content -Raw $previousManifestPath | ConvertFrom-Json
  $campaignStarted = [DateTime]::Parse($previousManifest.started_at).ToUniversalTime()
  $resumedStatus = [string]$previousManifest.status
  foreach ($command in @($previousManifest.commands)) { $commands.Add($command) }
}
if ($ReportOnly -and -not $Resume) { throw "ReportOnly requires Resume and an existing campaign" }
$campaignStatus = "running"
$failureStage = ""
$failureMessage = ""
$stageOrder = @{ safety = 0; race = 1; gate = 2; matrix = 3; identity = 4 }

function Test-RunStage {
  param([string]$Stage)
  return $stageOrder[$Stage] -ge $stageOrder[$StartAt]
}

function Invoke-RecordedCommand {
  param(
    [string]$Stage,
    [string]$WorkingDirectory,
    [string]$Executable,
    [string[]]$Arguments,
    [string]$LogName
  )
  $started = (Get-Date).ToUniversalTime()
  $display = $Executable + " " + ($Arguments -join " ")
  $logPath = Join-Path $logsRoot $LogName
  $exitCode = 1
  Push-Location $WorkingDirectory
  try {
    $previousErrorAction = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
      & $Executable @Arguments 2>&1 | Tee-Object -FilePath $logPath
      $exitCode = $LASTEXITCODE
    }
    finally {
      $ErrorActionPreference = $previousErrorAction
    }
  }
  finally {
    Pop-Location
    $commands.Add([ordered]@{
      stage = $Stage
      working_directory = $WorkingDirectory
      command = $display
      started_at = $started.ToString("o")
      finished_at = (Get-Date).ToUniversalTime().ToString("o")
      exit_code = $exitCode
      log = "logs/$LogName"
    })
  }
  if ($exitCode -ne 0) { throw "$Stage failed with exit code $exitCode" }
}

function Assert-EvaluatorResults {
  param(
    [string]$Stage,
    [string]$Root,
    [hashtable]$ExpectedCounts
  )
  $csvPath = Join-Path $Root "run_measurements.csv"
  if (-not (Test-Path $csvPath)) { throw "$Stage did not produce run_measurements.csv" }
  $measured = @(Import-Csv $csvPath | Where-Object { $_.phase -eq "measured" })
  $bad = @($measured | Where-Object { $_.success -ne "true" -or $_.consistent -ne "true" })
  if ($bad.Count -gt 0) { throw "$Stage has $($bad.Count) failed or inconsistent measured runs" }
  foreach ($key in $ExpectedCounts.Keys) {
    $parts = $key.Split("/")
    $count = @($measured | Where-Object { $_.nodes -eq $parts[0] -and $_.batch_size -eq $parts[1] }).Count
    if ($count -ne $ExpectedCounts[$key]) {
      throw "$Stage $key measured count is $count, expected $($ExpectedCounts[$key])"
    }
  }
}

function Get-EvaluatorRows {
  param([string]$Root)
  $csvPath = Join-Path $Root "run_measurements.csv"
  if (-not (Test-Path $csvPath)) { return @() }
  return @(Import-Csv $csvPath | Where-Object { $_.phase -eq "measured" } | Group-Object nodes,batch_size | ForEach-Object {
    $successful = @($_.Group | Where-Object { $_.success -eq "true" -and $_.consistent -eq "true" }).Count
    [pscustomobject]@{
      nodes = $_.Group[0].nodes
      batch = $_.Group[0].batch_size
      measured = $_.Count
      successful = $successful
      failed = $_.Count - $successful
    }
  } | Sort-Object {[int]$_.nodes}, {[int]$_.batch})
}

function Write-CampaignArtifacts {
  $finished = (Get-Date).ToUniversalTime()
  $protocolSourceFiles = [ordered]@{}
  foreach ($relativePath in @("sbc/hbbft/acs.go", "sbc/hbbft/acs_test.go", "sbc/hbbft/bba.go", "sbc/hbbft/bba_test.go")) {
    $nativePath = Join-Path $repoRoot ($relativePath.Replace("/", "\"))
    $protocolSourceFiles[$relativePath] = (Get-FileHash -Algorithm SHA256 $nativePath).Hash.ToLowerInvariant()
  }
  $scheduler = [ordered]@{
    test = "TestACSCommonSubsetAcrossReorderedDeliverySchedules"
    seed_start = 0
    seed_end_inclusive = $ScheduleSeeds - 1
    schedules = $ScheduleSeeds
    nodes = 4
    delivery = "select one pending RBC/BBA message using math/rand seeded by the schedule id"
    assertions = @("identical proposer set", "identical payload bytes", "subset size >= N-F")
    ec2_regression = "all four RBC outputs with only three completed true BBA results must remain incomplete"
  }
  $scheduler | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 (Join-Path $campaignRoot "scheduler.json")

  $manifest = [ordered]@{
    schema_version = 1
    campaign_id = $CampaignId
    status = $campaignStatus
    failure_stage = $failureStage
    failure_message = $failureMessage
    started_at = $campaignStarted.ToString("o")
    finished_at = $finished.ToString("o")
    git_commit = (& git -C $repoRoot rev-parse HEAD).Trim()
    git_status = @(& git -C $repoRoot status --short)
    protocol_source_sha256 = $protocolSourceFiles
    go_version = (& go version)
    go_env = [ordered]@{ goos = (& go env GOOS); goarch = (& go env GOARCH) }
    powershell_version = $PSVersionTable.PSVersion.ToString()
    os = [Environment]::OSVersion.VersionString
    processor = $env:PROCESSOR_IDENTIFIER
    aws_allocated = $false
    scheduler = $scheduler
    commands = $commands
  }
  $manifest | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $campaignRoot "manifest.json")

  $rows = @((Get-EvaluatorRows $gateRoot)) + @((Get-EvaluatorRows $matrixRoot))
  $failedCommandCount = @($commands | Where-Object { [int]$_.exit_code -ne 0 }).Count
  $report = @(
    "# Local ACS Common-Subset Safety Report",
    "",
    "- Campaign: ``$CampaignId``",
    "- Status: **$campaignStatus**",
    "- Base commit: ``$($manifest.git_commit)``",
    "- Exact protocol source hashes: ``manifest.json``",
    "- AWS resources allocated: no",
    "- Raw observations were retained; no outliers were removed.",
    "",
    "## ACS Safety",
    "",
    "The campaign tests 1,000 fixed reordered RBC/BBA delivery schedules. Every correct operator must select the same proposer IDs and identical proposal bytes. The EC2 3-versus-4-list state is a direct regression: all RBC outputs alone cannot complete ACS, and the output follows only completed true BBA decisions.",
    "",
    "## ACS Liveness",
    "",
    "| Nodes | Batch | Measured | Successful and consistent | Failed |",
    "|---:|---:|---:|---:|---:|"
  )
  foreach ($row in $rows) {
    $report += "| $($row.nodes) | $($row.batch) | $($row.measured) | $($row.successful) | $($row.failed) |"
  }
  $report += @(
    "",
    "The n4/batch-128 campaign is the sustained 100-slot gate. The n4/n7 matrix covers batches 8, 32, and 128 with 30 measured slots per scenario. Failed evaluator runs retain per-node results, process logs, and ``slot-status.json`` under their run directory.",
    "",
    "## Post-ACS Optimization Compatibility",
    "",
    "The bloc-node and BTE suites verify inclusion-list, agreed-set, merged-set, batch-plan, plaintext, and Ethereum transaction identities. The focused Merge + Plan benchmark is diagnostic only and does not alter acceptance.",
    "",
    "## Failure",
    "",
    $(if ($campaignStatus -eq "passed") { "No campaign acceptance stage failed." } else { "Stage ``$failureStage`` failed: $failureMessage" }),
    "The command history preserves $failedCommandCount failed tooling or argument setup attempt(s) from resumable execution; none produced accepted measurements or allocated AWS resources.",
    "",
    "## Artifacts",
    "",
    "- ``manifest.json`` records commands and environment metadata.",
    "- ``scheduler.json`` records deterministic delivery coverage.",
    "- ``logs/`` contains unit, race, compatibility, and benchmark output.",
    "- ``gate-n4-b128/`` and ``matrix-n4-n7/`` contain evaluator CSV/JSON, node logs, and failed slot status snapshots."
  )
  $report | Set-Content -Encoding utf8 (Join-Path $campaignRoot "REPORT.md")
}

if ($ReportOnly) {
  $campaignStatus = $resumedStatus
  Write-CampaignArtifacts
  Write-Host "ACS safety campaign report refreshed: $campaignRoot"
  exit 0
}

try {
  if (Test-RunStage "safety") {
    Invoke-RecordedCommand "hbbft repeated safety tests" $hbbftRoot "go" @("test", "./...", "-count=20", "-timeout=10m") "hbbft-count20.log"
  }

  if ((Test-RunStage "race") -and -not $SkipRace) {
    $goos = (& go env GOOS).Trim()
    if ($goos -eq "windows") {
      $goModCache = (& go env GOMODCACHE).Trim()
      Invoke-RecordedCommand "hbbft race tests" $repoRoot "docker" @(
        "run", "--rm", "-v", "${repoRoot}:/workspace", "-v", "${goModCache}:/go/pkg/mod", "-w", "/workspace/sbc/hbbft",
        "golang:1.25-bookworm", "go", "test", "-race", "./...", "-run", "Test(ACS|BBA|SlotACS)", "-count=1", "-timeout=10m"
      ) "hbbft-race.log"
    } else {
      Invoke-RecordedCommand "hbbft race tests" $hbbftRoot "go" @("test", "-race", "./...", "-run", "Test(ACS|BBA|SlotACS)", "-count=1", "-timeout=10m") "hbbft-race.log"
    }
  }

  if (Test-RunStage "gate") {
    Invoke-RecordedCommand "n4 batch-128 sustained gate" $moduleRoot "go" @(
      "run", "./cmd/bloc-node", "eval-suite", "--execution-mode", "persistent",
      "--node-counts", "4", "--batch-sizes", "128", "--warmups", "5",
      "--repetitions", "$GateRepetitions", "--max-restarts", "1", "--timeout", "30s",
      "--seed", "640", "--experiment-id", "$CampaignId-gate", "--out-dir", $gateRoot
    ) "gate-n4-b128.log"
    Assert-EvaluatorResults "n4 batch-128 sustained gate" $gateRoot @{ "4/128" = $GateRepetitions }
  }

  if (Test-RunStage "matrix") {
    Invoke-RecordedCommand "n4 n7 compatibility matrix" $moduleRoot "go" @(
      "run", "./cmd/bloc-node", "eval-suite", "--execution-mode", "persistent",
      "--node-counts", "4,7", "--batch-sizes", "8,32,128", "--warmups", "3",
      "--repetitions", "$MatrixRepetitions", "--max-restarts", "1", "--timeout", "30s",
      "--seed", "640", "--experiment-id", "$CampaignId-matrix", "--out-dir", $matrixRoot
    ) "matrix-n4-n7.log"
    Assert-EvaluatorResults "n4 n7 compatibility matrix" $matrixRoot @{
      "4/8" = $MatrixRepetitions; "4/32" = $MatrixRepetitions; "4/128" = $MatrixRepetitions
      "7/8" = $MatrixRepetitions; "7/32" = $MatrixRepetitions; "7/128" = $MatrixRepetitions
    }
  }

  if (Test-RunStage "identity") {
    Invoke-RecordedCommand "bloc-node protocol identity tests" $moduleRoot "go" @("test", "./...", "-count=1", "-timeout=10m") "bloc-node-tests.log"
    Invoke-RecordedCommand "BTE protocol identity tests" $bteRoot "go" @("test", "./...", "-count=1", "-timeout=10m") "bte-tests.log"
    Invoke-RecordedCommand "Merge Plan focused benchmark" $moduleRoot "go" @(
      "test", "./internal/app", "-run", "^$", "-bench", "^BenchmarkMergePlanAttribution$",
      "-count", "3", "-benchtime", "1s", "-benchmem"
    ) "merge-plan-benchmark.log"
  }
  $campaignStatus = "passed"
}
catch {
  $campaignStatus = "failed"
  $failureMessage = $_.Exception.Message
  if ($commands.Count -gt 0) { $failureStage = [string]$commands[$commands.Count - 1].stage }
}
finally {
  Write-CampaignArtifacts
}

Write-Host "ACS safety campaign ${campaignStatus}: $campaignRoot"
if ($campaignStatus -ne "passed") { throw "$failureStage failed: $failureMessage" }
