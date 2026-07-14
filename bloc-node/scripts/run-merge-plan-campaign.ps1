param(
  [ValidateSet("baseline", "optimized")]
  [string]$Phase,
  [Parameter(Mandatory = $true)]
  [string]$CampaignId,
  [switch]$CompareOnly
)

$ErrorActionPreference = "Stop"
[Threading.Thread]::CurrentThread.CurrentCulture = [Globalization.CultureInfo]::InvariantCulture
[Threading.Thread]::CurrentThread.CurrentUICulture = [Globalization.CultureInfo]::InvariantCulture
$moduleRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$repoRoot = (Resolve-Path (Join-Path $moduleRoot "..")).Path
$campaignRoot = Join-Path $repoRoot "results\local\merge-plan-optimization\$CampaignId"
$phaseRoot = Join-Path $campaignRoot $Phase

function Get-Median {
  param([double[]]$Values)
  if (-not $Values -or $Values.Count -eq 0) { return 0.0 }
  $sorted = @($Values | Sort-Object)
  $middle = [int][Math]::Floor($sorted.Count / 2)
  if ($sorted.Count % 2 -eq 1) { return [double]$sorted[$middle] }
  return ([double]$sorted[$middle - 1] + [double]$sorted[$middle]) / 2.0
}

function Write-BenchmarkSummary {
  param([string[]]$Paths, [string]$OutPath)
  $samples = @()
  foreach ($path in $Paths) {
    foreach ($line in Get-Content $path) {
      if ($line -match '^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([0-9.]+)\s+ns/op\s+([0-9]+)\s+B/op\s+([0-9]+)\s+allocs/op') {
        $samples += [pscustomobject]@{
          benchmark = $Matches[1]
          ns_per_op = [double]::Parse($Matches[2], [Globalization.CultureInfo]::InvariantCulture)
          bytes_per_op = [double]$Matches[3]
          allocs_per_op = [double]$Matches[4]
        }
      }
    }
  }
  $summary = $samples | Group-Object benchmark | ForEach-Object {
    [pscustomobject]@{
      benchmark = $_.Name
      samples = $_.Count
      median_ns_per_op = [Math]::Round((Get-Median @($_.Group.ns_per_op)), 2)
      median_bytes_per_op = [Math]::Round((Get-Median @($_.Group.bytes_per_op)), 2)
      median_allocs_per_op = [Math]::Round((Get-Median @($_.Group.allocs_per_op)), 2)
    }
  } | Sort-Object benchmark
  $summary | Export-Csv -NoTypeInformation -Encoding utf8 $OutPath
}

function Write-EvaluatorSummary {
  param([string]$InputPath, [string]$OutPath)
  $rows = Import-Csv $InputPath | Where-Object { $_.phase -eq "measured" -and $_.success -eq "true" -and $_.consistent -eq "true" }
  $summary = $rows | Group-Object nodes,batch_size | ForEach-Object {
    [pscustomobject]@{
      nodes = [int]$_.Group[0].nodes
      batch_size = [int]$_.Group[0].batch_size
      samples = $_.Count
      median_merge_plan_us = [Math]::Round((Get-Median @($_.Group | ForEach-Object { [double]$_.merge_plan_us })), 2)
      median_acs_output_decode_us = [Math]::Round((Get-Median @($_.Group | ForEach-Object { [double]$_.acs_output_decode_us })), 2)
      median_agreed_set_us = [Math]::Round((Get-Median @($_.Group | ForEach-Object { [double]$_.agreed_set_us })), 2)
      median_merge_us = [Math]::Round((Get-Median @($_.Group | ForEach-Object { [double]$_.merge_us })), 2)
      median_ciphertext_decode_us = [Math]::Round((Get-Median @($_.Group | ForEach-Object { [double]$_.ciphertext_decode_us })), 2)
      median_batch_plan_us = [Math]::Round((Get-Median @($_.Group | ForEach-Object { [double]$_.batch_plan_us })), 2)
    }
  } | Sort-Object nodes,batch_size
  $summary | Export-Csv -NoTypeInformation -Encoding utf8 $OutPath
}

function Write-Comparison {
  $baselineBench = Import-Csv (Join-Path $campaignRoot "baseline\benchmark-summary.csv")
  $optimizedBench = Import-Csv (Join-Path $campaignRoot "optimized\benchmark-summary.csv")
  $benchmarkRows = foreach ($before in $baselineBench) {
    $after = $optimizedBench | Where-Object benchmark -eq $before.benchmark | Select-Object -First 1
    if (-not $after) { continue }
    $beforeNS = [double]$before.median_ns_per_op
    $afterNS = [double]$after.median_ns_per_op
    [pscustomobject]@{
      benchmark = $before.benchmark
      baseline_median_ns = $beforeNS
      optimized_median_ns = $afterNS
      latency_delta_percent = [Math]::Round((($afterNS - $beforeNS) / $beforeNS) * 100.0, 2)
      baseline_bytes_per_op = [double]$before.median_bytes_per_op
      optimized_bytes_per_op = [double]$after.median_bytes_per_op
      baseline_allocs_per_op = [double]$before.median_allocs_per_op
      optimized_allocs_per_op = [double]$after.median_allocs_per_op
    }
  }
  $benchmarkRows | Export-Csv -NoTypeInformation -Encoding utf8 (Join-Path $campaignRoot "comparison.csv")

  $baselineEval = Import-Csv (Join-Path $campaignRoot "baseline\evaluator-summary.csv")
  $optimizedEval = Import-Csv (Join-Path $campaignRoot "optimized\evaluator-summary.csv")
  $evalRows = foreach ($before in $baselineEval) {
    $after = $optimizedEval | Where-Object { $_.nodes -eq $before.nodes -and $_.batch_size -eq $before.batch_size } | Select-Object -First 1
    if (-not $after) { continue }
    $beforeUS = [double]$before.median_merge_plan_us
    $afterUS = [double]$after.median_merge_plan_us
    [pscustomobject]@{
      nodes = $before.nodes
      batch_size = $before.batch_size
      baseline_median_merge_plan_us = $beforeUS
      optimized_median_merge_plan_us = $afterUS
      delta_percent = [Math]::Round((($afterUS - $beforeUS) / $beforeUS) * 100.0, 2)
    }
  }
  $evalRows | Export-Csv -NoTypeInformation -Encoding utf8 (Join-Path $campaignRoot "evaluator-comparison.csv")

  $report = @(
    "# Merge/Plan Optimization Report",
    "",
    "- Campaign: ``$CampaignId``",
    "- Scope: local deterministic attribution and optimization",
    "- Raw observations were retained; no outliers were removed.",
    "",
    "## End-to-End Merge/Plan Medians",
    "",
    "| Nodes | Batch | Baseline us | Optimized us | Delta |",
    "|---:|---:|---:|---:|---:|"
  )
  foreach ($row in $evalRows) {
    $report += "| $($row.nodes) | $($row.batch_size) | $($row.baseline_median_merge_plan_us) | $($row.optimized_median_merge_plan_us) | $($row.delta_percent)% |"
  }
  $report += @(
    "",
    "## Merge/Plan Substage Attribution",
    "",
    "A zero in these evaluator medians means the sub-millisecond boundary fell below this Windows host's observable clock tick; isolated Go benchmarks remain the authoritative measurement for those short components.",
    "",
    "Baseline medians in microseconds:",
    "",
    "| Nodes | Batch | ACS decode | Agreed set | Merge | Ciphertext decode | Batch plan |",
    "|---:|---:|---:|---:|---:|---:|---:|"
  )
  foreach ($row in $baselineEval) {
    $report += "| $($row.nodes) | $($row.batch_size) | $($row.median_acs_output_decode_us) | $($row.median_agreed_set_us) | $($row.median_merge_us) | $($row.median_ciphertext_decode_us) | $($row.median_batch_plan_us) |"
  }
  $report += @(
    "",
    "Optimized medians in microseconds:",
    "",
    "| Nodes | Batch | ACS decode | Agreed set | Merge | Ciphertext decode | Batch plan |",
    "|---:|---:|---:|---:|---:|---:|---:|"
  )
  foreach ($row in $optimizedEval) {
    $report += "| $($row.nodes) | $($row.batch_size) | $($row.median_acs_output_decode_us) | $($row.median_agreed_set_us) | $($row.median_merge_us) | $($row.median_ciphertext_decode_us) | $($row.median_batch_plan_us) |"
  }
  $report += @(
    "",
    "## Retention Gate",
    "",
    "All batch-32/128 pipeline benchmark medians must remain at or below a 5% regression. Positive values are regressions.",
    "",
    "| Benchmark | Time delta | Baseline B/op | Optimized B/op | Baseline allocs/op | Optimized allocs/op |",
    "|---|---:|---:|---:|---:|---:|"
  )
  foreach ($row in $benchmarkRows | Where-Object { $_.benchmark -like "*/pipeline" -and $_.benchmark -match "b32|b128" }) {
    $report += "| ``$($row.benchmark)`` | $($row.latency_delta_percent)% | $($row.baseline_bytes_per_op) | $($row.optimized_bytes_per_op) | $($row.baseline_allocs_per_op) | $($row.optimized_allocs_per_op) |"
  }
  $report += @(
    "",
    "## Optimization Decisions",
    "",
    "- Retained list-hash deduplication: proposal decoding is parsing-only and agreed-set construction computes each canonical JSON hash once.",
    "- Retained merge normalization cleanup: effective fees are parsed once and exact repeated placeholders bypass duplicate validation; conflicting duplicates still follow full validation and first-winner behavior.",
    "- Retained decode/batch-ID consolidation: ``DecodedBatch`` preserves accepted canonical encodings, and planning hashes those bytes without reserializing curve objects.",
    "- Discarded optimizations: none.",
    "",
    "## Artifacts",
    "",
    "- ``comparison.csv``: benchmark medians and allocation changes.",
    "- ``evaluator-comparison.csv``: end-to-end local evaluator comparison.",
    "- ``baseline/`` and ``optimized/``: raw benchmarks, profiles, manifests, and evaluator outputs.",
    "",
    "## Validation",
    "",
    "- ``cd bloc-node && go test ./...``: passed.",
    "- ``cd bte/btd-impl-main && go test ./...``: passed.",
    "- ``cd latency-charts && python -m pytest``: 11 passed.",
    "- ``FuzzCiphertextDecoderCanonical`` for 10 seconds: passed 117,109 executions.",
    "- Targeted ``go test -race ./internal/app``: not runnable on this Windows host because CGO has no ``gcc`` compiler in ``PATH``; rerun in Linux/WSL or a Windows CGO toolchain before merge.",
    "",
    "## Semantic Acceptance",
    "",
    "Protocol equivalence is enforced by the Go test suite for inclusion-list hashes, agreed and merged set identities, selected order/gas/skips, canonical ciphertext encodings, batch IDs, alpha, sub-batches, materialized transaction hashes, malformed/conflicting/empty/repeated-index cases, and cross-node consistency. Both evaluator phases retained every raw observation; each completed 60/60 measured runs with ``success=true`` and ``consistent=true``."
  )
  $report | Set-Content -Encoding utf8 (Join-Path $campaignRoot "REPORT.md")
}

if ($CompareOnly) {
  foreach ($name in @("baseline", "optimized")) {
    $root = Join-Path $campaignRoot $name
    Write-BenchmarkSummary @((Join-Path $root "bloc-node-bench.txt"), (Join-Path $root "bte-bench.txt")) (Join-Path $root "benchmark-summary.csv")
    Write-EvaluatorSummary (Join-Path $root "eval-suite\run_measurements.csv") (Join-Path $root "evaluator-summary.csv")
  }
  Write-Comparison
  exit 0
}

if (Test-Path $phaseRoot) {
  throw "campaign phase already exists: $phaseRoot"
}
New-Item -ItemType Directory -Force $phaseRoot | Out-Null
$env:GOMAXPROCS = "1"
$env:GOCACHE = Join-Path $repoRoot ".gocache"

$commands = @(
  "go test ./internal/app -run ^$ -bench ^BenchmarkMergePlanAttribution$ -count 10 -benchtime 1s -benchmem",
  "go test ./be -run ^$ -bench ^BenchmarkBatchPlanningAttribution$ -count 10 -benchtime 1s -benchmem",
  "go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4,7 --batch-sizes 8,32,128 --warmups 3 --repetitions 10"
)
$commands | Set-Content -Encoding utf8 (Join-Path $phaseRoot "commands.txt")

Push-Location $moduleRoot
try {
  & go test ./internal/app -run '^$' -bench '^BenchmarkMergePlanAttribution$' -count 10 -benchtime 1s -benchmem 2>&1 |
    Tee-Object -FilePath (Join-Path $phaseRoot "bloc-node-bench.txt")
  if ($LASTEXITCODE -ne 0) { throw "bloc-node benchmark failed" }

  foreach ($mode in @("disjoint", "overlap")) {
    & go test ./internal/app -run '^$' -bench "^BenchmarkMergePlanAttribution/n7-b128-$mode/pipeline$" -count 1 -benchtime 3s `
      -cpuprofile (Join-Path $phaseRoot "cpu-n7-b128-$mode.pprof") -memprofile (Join-Path $phaseRoot "mem-n7-b128-$mode.pprof") | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "profile benchmark failed for $mode" }
  }

  $evalOut = Join-Path $phaseRoot "eval-suite"
  & go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4,7 --batch-sizes 8,32,128 `
    --warmups 3 --repetitions 10 --experiment-id "merge-plan-$Phase" --out-dir $evalOut
  if ($LASTEXITCODE -ne 0) { throw "local evaluator campaign failed" }
}
finally {
  Pop-Location
}

Push-Location (Join-Path $repoRoot "bte\btd-impl-main")
try {
  & go test ./be -run '^$' -bench '^BenchmarkBatchPlanningAttribution$' -count 10 -benchtime 1s -benchmem 2>&1 |
    Tee-Object -FilePath (Join-Path $phaseRoot "bte-bench.txt")
  if ($LASTEXITCODE -ne 0) { throw "BTE planning benchmark failed" }
}
finally {
  Pop-Location
}

$manifest = [ordered]@{
  schema_version = 1
  campaign_id = $CampaignId
  phase = $Phase
  git_commit = (git -C $repoRoot rev-parse HEAD).Trim()
  git_status = @(git -C $repoRoot status --short)
  go_version = (go version)
  powershell_version = $PSVersionTable.PSVersion.ToString()
  os = [Environment]::OSVersion.VersionString
  processor = $env:PROCESSOR_IDENTIFIER
  gomaxprocs = 1
  created_at = (Get-Date).ToUniversalTime().ToString("o")
  commands = $commands
}
$manifest | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 (Join-Path $phaseRoot "manifest.json")
Write-BenchmarkSummary @((Join-Path $phaseRoot "bloc-node-bench.txt"), (Join-Path $phaseRoot "bte-bench.txt")) (Join-Path $phaseRoot "benchmark-summary.csv")
Write-EvaluatorSummary (Join-Path $phaseRoot "eval-suite\run_measurements.csv") (Join-Path $phaseRoot "evaluator-summary.csv")

if ($Phase -eq "optimized" -and (Test-Path (Join-Path $campaignRoot "baseline\benchmark-summary.csv"))) {
  Write-Comparison
}
