param(
  [Parameter(Mandatory = $true)]
  [string]$ArtifactRoot,
  [string]$AwsProfile = "bloc",
  [int[]]$BatchSizes = @(8, 32, 128),
  [string]$BatchSizesCsv = "",
  [int]$Warmups = 1,
  [int]$Repetitions = 3,
  [uint64]$FirstSlot = 1000,
  [string]$EvalTimeout = "30s",
  [string]$KeyPath = "",
  [switch]$SkipImageBuild,
  [switch]$RegenerateConfig
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function ConvertFrom-IntegerCsv {
  param([string]$Value, [string]$Name)
  $parts = @($Value.Split(",", [System.StringSplitOptions]::RemoveEmptyEntries))
  if ($parts.Count -eq 0) { throw "$Name must contain at least one integer" }
  $values = foreach ($part in $parts) {
    $parsed = 0
    if (-not [int]::TryParse($part.Trim(), [ref]$parsed) -or $parsed -le 0) {
      throw "$Name contains an invalid positive integer: '$part'"
    }
    $parsed
  }
  return @($values)
}

if (-not [string]::IsNullOrWhiteSpace($BatchSizesCsv)) {
  $BatchSizes = @(ConvertFrom-IntegerCsv $BatchSizesCsv "BatchSizesCsv")
}
if ($BatchSizes.Count -eq 0) { throw "at least one batch size is required" }
if (@($BatchSizes | Where-Object { $_ -gt 128 }).Count -gt 0) {
  throw "batch sizes above the configured BMax=128 are not supported"
}
if ($Warmups -lt 0) { throw "Warmups cannot be negative" }
if ($Repetitions -le 0) { throw "Repetitions must be positive" }
if ($EvalTimeout -notmatch '^\d+(ms|s|m)$') { throw "EvalTimeout must be a Go duration such as 30s or 1m" }

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$artifactRootPath = (Resolve-Path $ArtifactRoot).Path
$manifestPath = Join-Path $artifactRootPath "manifest.json"
$inventoryPath = Join-Path $artifactRootPath "inventory.json"
if (-not (Test-Path $manifestPath)) { throw "missing manifest: $manifestPath" }
if (-not (Test-Path $inventoryPath)) { throw "missing inventory: $inventoryPath" }

$manifest = Get-Content -Raw $manifestPath | ConvertFrom-Json
$inventory = Get-Content -Raw $inventoryPath | ConvertFrom-Json
$awsRegion = $manifest.aws_region
$experimentId = $manifest.experiment_id
$ecrRepositoryUrl = $manifest.terraform.ecr_repository_url
if ([string]::IsNullOrWhiteSpace($ecrRepositoryUrl)) {
  throw "manifest does not contain terraform.ecr_repository_url; was the original environment kept alive?"
}
$registry = ($ecrRepositoryUrl -split "/")[0]

if ([string]::IsNullOrWhiteSpace($KeyPath)) {
  $keys = @(Get-ChildItem -Path (Join-Path $artifactRootPath "generated") -Filter "*.pem" -File)
  if ($keys.Count -ne 1) {
    throw "could not infer SSH key from generated/*.pem; pass -KeyPath explicitly"
  }
  $KeyPath = $keys[0].FullName
}
$keyPathResolved = (Resolve-Path $KeyPath).Path

$stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd't'HHmmss'z'")
$rerunId = "$experimentId-rerun-$stamp"
$rerunRoot = Join-Path $artifactRootPath (Join-Path "reruns" $rerunId)
New-Item -ItemType Directory -Force $rerunRoot, "$rerunRoot\generated", "$rerunRoot\scenarios", "$rerunRoot\logs" | Out-Null

$commands = [System.Collections.Generic.List[string]]::new()
function Record-Command {
  param([string]$Command)
  $commands.Add($Command)
  $commands | Set-Content -Encoding utf8 (Join-Path $rerunRoot "commands.txt")
}

function Invoke-SSH {
  param([string]$HostName, [string]$Command, [switch]$AllowFailure)
  Record-Command "ssh ubuntu@$HostName -- $Command"
  & ssh -i $keyPathResolved -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$HostName" $Command
  $exitCode = $LASTEXITCODE
  if ($exitCode -ne 0 -and -not $AllowFailure) {
    throw "ssh failed on $HostName with exit code $exitCode"
  }
  return $exitCode
}

function Invoke-SCP {
  param([string[]]$ScpArgs, [switch]$AllowFailure)
  Record-Command "scp $($ScpArgs -join ' ')"
  & scp -i $keyPathResolved -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL @ScpArgs
  $exitCode = $LASTEXITCODE
  if ($exitCode -ne 0 -and -not $AllowFailure) {
    throw "scp failed with exit code $exitCode`: $($ScpArgs -join ' ')"
  }
  return $exitCode
}

function Collect-OperatorLogs {
  foreach ($node in ($inventory.nodes | Sort-Object id)) {
    $logPath = Join-Path $rerunRoot "logs\operator-$($node.id).log"
    Record-Command "ssh ubuntu@$($node.public_ip) -- docker logs --timestamps ec2-bloc-node-1"
    $output = & ssh -i $keyPathResolved -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$($node.public_ip)" "docker logs --timestamps ec2-bloc-node-1 2>&1"
    $output | Set-Content -Encoding utf8 $logPath
  }
}

Push-Location $repoRoot
try {
  $gitCommit = (& git rev-parse --short=12 HEAD).Trim()
  $imageTag = "$gitCommit-$stamp"
  $imageUri = "$ecrRepositoryUrl`:$imageTag"

  if (-not $SkipImageBuild) {
    Record-Command "docker build -f bloc-node/Dockerfile -t bloc-node:$imageTag ."
    docker build -f bloc-node/Dockerfile -t "bloc-node:$imageTag" .
    if ($LASTEXITCODE -ne 0) { throw "docker build failed" }
    Record-Command "docker tag bloc-node:$imageTag $imageUri"
    docker tag "bloc-node:$imageTag" $imageUri
    if ($LASTEXITCODE -ne 0) { throw "docker tag failed" }
    Record-Command "aws ecr get-login-password --profile $AwsProfile --region $awsRegion | docker login --username AWS --password-stdin $registry"
    $password = & aws ecr get-login-password --profile $AwsProfile --region $awsRegion
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($password)) { throw "ECR login password retrieval failed" }
    $password | docker login --username AWS --password-stdin $registry
    if ($LASTEXITCODE -ne 0) { throw "docker login failed" }
    Record-Command "docker push $imageUri"
    docker push $imageUri
    if ($LASTEXITCODE -ne 0) { throw "docker push failed" }
  } else {
    $imageUri = $manifest.docker_image
    if ([string]::IsNullOrWhiteSpace($imageUri)) { throw "-SkipImageBuild requires manifest.docker_image" }
  }

  $clusterConfig = Join-Path $artifactRootPath "generated\cluster.ec2.json"
  $remoteEvalConfig = Join-Path $artifactRootPath "generated\remote-eval.ec2.json"
  if ($RegenerateConfig) {
    $clusterConfig = Join-Path $rerunRoot "generated\cluster.ec2.json"
    $remoteEvalConfig = Join-Path $rerunRoot "generated\remote-eval.ec2.json"
    Push-Location (Join-Path $repoRoot "bloc-node")
    $env:GOCACHE = Join-Path (Get-Location) ".gocache"
    Record-Command "go run ./cmd/bloc-node gen-ec2-config --inventory $inventoryPath --cluster-out $clusterConfig --remote-eval-out $remoteEvalConfig --cluster-id $experimentId --nodes $($manifest.node_count) --bmax 128"
    go run ./cmd/bloc-node gen-ec2-config `
      --inventory $inventoryPath `
      --cluster-out $clusterConfig `
      --remote-eval-out $remoteEvalConfig `
      --cluster-id $experimentId `
      --nodes $manifest.node_count `
      --bmax 128 `
      --slot $FirstSlot `
      --prometheus-url "http://$($inventory.controller.private_ip):9090" `
      --grafana-url "http://$($inventory.controller.private_ip):3000" `
      --controller-url $inventory.controller.private_ip
    if ($LASTEXITCODE -ne 0) { throw "config regeneration failed" }
    Pop-Location
  } else {
    # The recovery flow restarts every sidecar, so its first requested slot
    # must also be the initial slot in both configs. Preserve all generated
    # keys and identities while changing only that slot boundary.
    $sourceClusterConfig = $clusterConfig
    $sourceRemoteEvalConfig = $remoteEvalConfig
    $clusterConfig = Join-Path $rerunRoot "generated\cluster.ec2.json"
    $remoteEvalConfig = Join-Path $rerunRoot "generated\remote-eval.ec2.json"
    $cluster = Get-Content -Raw $sourceClusterConfig | ConvertFrom-Json
    $cluster.slot = $FirstSlot
    [System.IO.File]::WriteAllText($clusterConfig, ($cluster | ConvertTo-Json -Depth 100), [System.Text.UTF8Encoding]::new($false))
    $remote = Get-Content -Raw $sourceRemoteEvalConfig | ConvertFrom-Json
    $remote.initial_slot = $FirstSlot
    [System.IO.File]::WriteAllText($remoteEvalConfig, ($remote | ConvertTo-Json -Depth 100), [System.Text.UTF8Encoding]::new($false))
  }

  foreach ($node in ($inventory.nodes | Sort-Object id)) {
    Invoke-SCP @($clusterConfig, "ubuntu@$($node.public_ip):/etc/bloc/cluster.json") | Out-Null
    Invoke-SCP @((Join-Path $PSScriptRoot "operator-compose.yaml"), "ubuntu@$($node.public_ip):/opt/bloc/ec2/operator-compose.yaml") | Out-Null
    Invoke-SSH $node.public_ip "set -e; aws ecr get-login-password --region '$awsRegion' | docker login --username AWS --password-stdin '$registry'; cd /opt/bloc/ec2; NODE_ID='$($node.id)' BLOC_IMAGE='$imageUri' docker compose -f operator-compose.yaml pull" | Out-Null
  }

  # A cluster-wide stop avoids early nodes retaining connections to peer
  # containers that are replaced later in a sequential deployment pass.
  foreach ($node in ($inventory.nodes | Sort-Object id)) {
    Invoke-SSH $node.public_ip "cd /opt/bloc/ec2; NODE_ID='$($node.id)' BLOC_IMAGE='$imageUri' docker compose -f operator-compose.yaml down" | Out-Null
  }
  foreach ($node in ($inventory.nodes | Sort-Object id)) {
    Invoke-SSH $node.public_ip "set -e; cd /opt/bloc/ec2; NODE_ID='$($node.id)' BLOC_IMAGE='$imageUri' docker compose -f operator-compose.yaml up -d" | Out-Null
  }

  Invoke-SCP @($remoteEvalConfig, "ubuntu@$($inventory.controller.public_ip):/opt/bloc/ec2/remote-eval.ec2.json") | Out-Null
  foreach ($node in ($inventory.nodes | Sort-Object id)) {
    Invoke-SSH $inventory.controller.public_ip "for attempt in `$(seq 1 24); do curl --max-time 3 -fsS http://$($node.private_ip):8000/healthz && exit 0; sleep 5; done; exit 1" | Out-Null
    Invoke-SSH $inventory.controller.public_ip "curl --max-time 5 -fsS http://$($node.private_ip):8000/metrics | head -n 5" | Out-Null
  }
  Invoke-SSH $inventory.controller.public_ip "curl -fsS http://127.0.0.1:9090/api/v1/targets > /opt/bloc/ec2/prometheus-targets-$rerunId.json" | Out-Null
  Invoke-SCP @("ubuntu@$($inventory.controller.public_ip):/opt/bloc/ec2/prometheus-targets-$rerunId.json", (Join-Path $rerunRoot "prometheus-targets.json")) | Out-Null

  $nextSlot = $FirstSlot
  $allPassed = $true
  foreach ($batch in $BatchSizes) {
    $scenarioDir = "/opt/bloc/ec2/results/$rerunId/batch-$batch"
    $remoteCmd = "set -e; aws ecr get-login-password --region '$awsRegion' | docker login --username AWS --password-stdin '$registry'; sudo mkdir -p '$scenarioDir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$imageUri' eval-remote --config remote-eval.ec2.json --experiment-id '$rerunId-b$batch' --first-slot '$nextSlot' --batch-size '$batch' --warmups '$Warmups' --repetitions '$Repetitions' --out-dir 'results/$rerunId/batch-$batch' --image-tag '$imageUri' --git-commit '$gitCommit' --timeout '$EvalTimeout'"
    $exitCode = Invoke-SSH $inventory.controller.public_ip $remoteCmd -AllowFailure
    $localScenarioDir = Join-Path $rerunRoot "scenarios\batch-$batch"
    New-Item -ItemType Directory -Force $localScenarioDir | Out-Null
    Invoke-SCP @("-r", "ubuntu@$($inventory.controller.public_ip):$scenarioDir", (Join-Path $localScenarioDir "results")) -AllowFailure | Out-Null
    if ($exitCode -ne 0) {
      $allPassed = $false
    } else {
      $runCsv = Join-Path $localScenarioDir "results\batch-$batch\run_measurements.csv"
      if (-not (Test-Path $runCsv)) {
        $allPassed = $false
      } else {
        $measured = @(Import-Csv $runCsv | Where-Object { $_.phase -eq "measured" })
        $bad = @($measured | Where-Object { $_.success -ne "true" -or $_.consistent -ne "true" })
        if ($measured.Count -ne $Repetitions -or $bad.Count -gt 0) { $allPassed = $false }
      }
    }
    $nextSlot += $Warmups + $Repetitions
  }

  Collect-OperatorLogs

  $summary = [ordered]@{
    rerun_id = $rerunId
    parent_experiment_id = $experimentId
    status = if ($allPassed) { "complete" } else { "invalid" }
    git_commit = $gitCommit
    docker_image = $imageUri
    first_slot = $FirstSlot
    batch_sizes = $BatchSizes
    warmups = $Warmups
    repetitions = $Repetitions
    eval_timeout = $EvalTimeout
    commands = $commands.ToArray()
    finished_at = (Get-Date).ToUniversalTime().ToString("o")
  }
  $summary | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 (Join-Path $rerunRoot "manifest.json")
  if (-not $allPassed) { exit 1 }
}
finally {
  Pop-Location
}
