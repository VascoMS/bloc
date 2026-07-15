param(
  [Parameter(Mandatory = $true)]
  [string[]]$AdminCidrs,
  [string]$AwsProfile = "bloc",
  [string]$AwsRegion = "us-east-1",
  [string]$AvailabilityZone = "us-east-1a",
  [string[]]$AvailabilityZones = @(),
  [string[]]$SubnetCidrs = @(),
  [int]$NodeCount = 4,
  [string]$OperatorInstanceType = "t3.small",
  [string]$ControllerInstanceType = "t3.small",
  [int[]]$BatchSizes = @(8, 32, 128),
  [int]$Warmups = 1,
  [int]$Repetitions = 3,
  [int]$RepetitionBlocks = 1,
  [string[]]$BatchOrderBlocks = @(),
  [string]$PrebuiltImageTag = "",
  [string]$EcrImageTag = "",
  [int]$MaxRuntimeMinutes = 0,
  [string]$CampaignLabel = "A1-pilot-same-az",
  [string]$Topology = "T0-same-az",
  [string]$ExperimentId = "",
  [switch]$AutoApprovePlan,
  [switch]$KeepResourcesOnFailure,
  [switch]$KeepResourcesAfterRun,
  [switch]$SkipChartGeneration
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$terraformSourceDir = Join-Path $PSScriptRoot "terraform"
$aws = "C:\Program Files\Amazon\AWSCLIV2\aws.exe"
if (-not (Test-Path $aws)) { $aws = "aws" }

if ([string]::IsNullOrWhiteSpace($ExperimentId)) {
  $stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd't'HHmmss'z'")
  $ExperimentId = "bloc-ec2-a1-pilot-same-az-n$NodeCount-$stamp"
}
if (-not $ExperimentId.StartsWith("bloc-ec2-")) {
  throw "ExperimentId must start with 'bloc-ec2-' so generated IAM names match the scoped deploy policy."
}
if ($NodeCount -lt 1 -or $NodeCount -gt 10) {
  throw "NodeCount must be between 1 and 10 for the current EC2 test bed; received $NodeCount."
}
if ($BatchSizes.Count -eq 0 -or @($BatchSizes | Where-Object { $_ -lt 1 -or $_ -gt 128 }).Count -gt 0) {
  throw "BatchSizes must contain values between 1 and BMax=128; received $($BatchSizes -join ',')."
}
if ($AvailabilityZones.Count -eq 0) {
  $AvailabilityZones = @($AvailabilityZone)
} else {
  $AvailabilityZone = $AvailabilityZones[0]
}
if ($SubnetCidrs.Count -eq 0) {
  $SubnetCidrs = for ($i = 0; $i -lt $AvailabilityZones.Count; $i++) { "10.40.$($i + 1).0/24" }
}
if ($SubnetCidrs.Count -eq 0) {
  throw "At least one generated subnet CIDR is required."
}
if ($RepetitionBlocks -lt 1 -or $Repetitions -lt 1 -or ($Repetitions % $RepetitionBlocks) -ne 0) {
  throw "Repetitions must be positive and evenly divisible by RepetitionBlocks."
}
if ($BatchOrderBlocks.Count -eq 0) {
  $BatchOrderBlocks = for ($i = 0; $i -lt $RepetitionBlocks; $i++) { $BatchSizes -join "," }
}
if ($BatchOrderBlocks.Count -ne $RepetitionBlocks) {
  throw "BatchOrderBlocks must contain exactly RepetitionBlocks entries."
}
$resolvedBatchOrders = @()
foreach ($orderText in $BatchOrderBlocks) {
  $order = @($orderText.Split(",") | ForEach-Object { [int]$_.Trim() })
  if ($order.Count -ne $BatchSizes.Count -or (Compare-Object ($order | Sort-Object) ($BatchSizes | Sort-Object))) {
    throw "Each batch-order block must contain every configured batch exactly once: $($BatchSizes -join ',')."
  }
  $resolvedBatchOrders += ,$order
}

$ecrRepositoryName = ("bloc-node-$ExperimentId").ToLowerInvariant() -replace "[^a-z0-9._/-]", "-"
$artifactRoot = Join-Path $repoRoot (Join-Path "results\ec2" $ExperimentId)
$terraformWorkDir = Join-Path $artifactRoot "generated\terraform-work"
$keyName = "$ExperimentId-key"
$keyPath = Join-Path $env:TEMP "$keyName.pem"
$artifactKeyPath = Join-Path $artifactRoot (Join-Path "generated" "$keyName.pem")
$tfvarsPath = Join-Path $terraformWorkDir "a1-pilot.tfvars"
$planPath = Join-Path $terraformWorkDir "a1-pilot.tfplan"
$campaignStartedAt = (Get-Date).ToUniversalTime().ToString("o")
$campaignStartedClock = Get-Date

$terraformStarted = $false
$terraformApplied = $false
$keyCreated = $false
$gitCommit = ""
$imageUri = ""
$sourceImageTag = ""
$ecrRepositoryUrl = ""
$registry = ""
$campaignStatus = "invalid"
$invalidReason = ""
$commandLog = [System.Collections.Generic.List[string]]::new()
$campaignFailed = $false

function Record-Command {
  param([string]$Command)
  $commandLog.Add($Command)
  if (Test-Path $artifactRoot) {
    $commandLog | Set-Content -Encoding utf8 (Join-Path $artifactRoot "commands.txt")
  }
}

function Write-Step {
  param([string]$Message)
  Write-Host ""
  Write-Host "==> $Message"
}

function Invoke-Checked {
  param(
    [Parameter(Mandatory = $true)]
    [scriptblock]$Command,
    [string]$Label = "command"
  )
  Write-Step $Label
  & $Command
  if ($LASTEXITCODE -ne 0) {
    throw "$Label failed with exit code $LASTEXITCODE"
  }
}

function Get-AwsText {
  param([string[]]$Arguments)
  $psi = [System.Diagnostics.ProcessStartInfo]::new()
  $psi.FileName = $aws
  $psi.Arguments = ($Arguments | ForEach-Object {
    if ($_ -match '[\s"]') {
      '"' + ($_ -replace '"', '\"') + '"'
    } else {
      $_
    }
  }) -join " "
  $psi.RedirectStandardOutput = $true
  $psi.RedirectStandardError = $true
  $psi.UseShellExecute = $false
  $psi.CreateNoWindow = $true
  $process = [System.Diagnostics.Process]::Start($psi)
  $stdout = $process.StandardOutput.ReadToEnd()
  [void]$process.StandardError.ReadToEnd()
  $process.WaitForExit()
  if ($process.ExitCode -ne 0) { return "" }
  return $stdout.Trim()
}

function Assert-NoExistingCampaignResources {
  $instances = Get-AwsText @("ec2", "describe-instances", "--profile", $AwsProfile, "--region", $AwsRegion, "--filters", "Name=tag:Name,Values=$ExperimentId-controller,$ExperimentId-operator-*", "Name=instance-state-name,Values=pending,running,stopping,stopped", "--query", "Reservations[].Instances[].InstanceId", "--output", "text")
  $volumes = Get-AwsText @("ec2", "describe-volumes", "--profile", $AwsProfile, "--region", $AwsRegion, "--filters", "Name=tag:Name,Values=$ExperimentId-controller-volume,$ExperimentId-operator-*-volume", "--query", "Volumes[].VolumeId", "--output", "text")
  $vpcs = Get-AwsText @("ec2", "describe-vpcs", "--profile", $AwsProfile, "--region", $AwsRegion, "--filters", "Name=tag:Name,Values=$ExperimentId-vpc", "--query", "Vpcs[].VpcId", "--output", "text")
  $repository = Get-AwsText @("ecr", "describe-repositories", "--profile", $AwsProfile, "--region", $AwsRegion, "--repository-names", $ecrRepositoryName, "--query", "repositories[].repositoryName", "--output", "text")
  $role = Get-AwsText @("iam", "get-role", "--profile", $AwsProfile, "--role-name", "$ExperimentId-ec2-ecr-readonly", "--query", "Role.RoleName", "--output", "text")
  $profile = Get-AwsText @("iam", "get-instance-profile", "--profile", $AwsProfile, "--instance-profile-name", "$ExperimentId-ec2-ecr-readonly", "--query", "InstanceProfile.InstanceProfileName", "--output", "text")
  $key = Get-AwsText @("ec2", "describe-key-pairs", "--profile", $AwsProfile, "--region", $AwsRegion, "--key-names", $keyName, "--query", "KeyPairs[].KeyName", "--output", "text")
  $leftovers = @()
  if (-not [string]::IsNullOrWhiteSpace($instances)) { $leftovers += "instances=$instances" }
  if (-not [string]::IsNullOrWhiteSpace($volumes)) { $leftovers += "volumes=$volumes" }
  if (-not [string]::IsNullOrWhiteSpace($vpcs)) { $leftovers += "vpcs=$vpcs" }
  if (-not [string]::IsNullOrWhiteSpace($repository)) { $leftovers += "ecr_repository=$repository" }
  if (-not [string]::IsNullOrWhiteSpace($role)) { $leftovers += "iam_role=$role" }
  if (-not [string]::IsNullOrWhiteSpace($profile)) { $leftovers += "instance_profile=$profile" }
  if (-not [string]::IsNullOrWhiteSpace($key)) { $leftovers += "key_pair=$key" }
  if ($leftovers.Count -gt 0) {
    throw "Existing AWS resources found for experiment id '$ExperimentId': $($leftovers -join '; '). Use a fresh -ExperimentId or clean these resources before retrying."
  }
}

function ConvertTo-Pem {
  param([string]$KeyMaterial)
  $content = $KeyMaterial.Trim()
  if ($content.Contains("`n")) {
    return "$content`n"
  }
  $begin = "-----BEGIN RSA PRIVATE KEY-----"
  $end = "-----END RSA PRIVATE KEY-----"
  if (-not $content.StartsWith($begin) -or -not $content.EndsWith($end)) {
    throw "Unexpected EC2 key material envelope."
  }
  $body = $content.Substring($begin.Length, $content.Length - $begin.Length - $end.Length)
  $chunks = for ($i = 0; $i -lt $body.Length; $i += 64) {
    $body.Substring($i, [Math]::Min(64, $body.Length - $i))
  }
  return "$begin`n$($chunks -join "`n")`n$end`n"
}

function Write-AsciiFile {
  param([string]$Path, [string]$Content)
  [System.IO.File]::WriteAllText($Path, $Content, [System.Text.Encoding]::ASCII)
}

function ConvertTo-TerraformStringList {
  param([string[]]$Values)
  return (($Values | ForEach-Object { "`"$_`"" }) -join ", ")
}

function Invoke-SSH {
  param([string]$HostName, [string]$Command)
  Record-Command "ssh ubuntu@$HostName -- $Command"
  & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$HostName" $Command
  if ($LASTEXITCODE -ne 0) {
    throw "ssh failed on $HostName"
  }
}

function Get-SSHText {
  param([string]$HostName, [string]$Command)
  Record-Command "ssh ubuntu@$HostName -- $Command"
  $output = & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$HostName" $Command
  if ($LASTEXITCODE -ne 0) {
    throw "ssh metadata command failed on $HostName"
  }
  return (($output | Out-String).Trim())
}

function Invoke-SCP {
  param([string[]]$ScpArgs)
  Record-Command "scp $($ScpArgs -join ' ')"
  & scp -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL @ScpArgs
  if ($LASTEXITCODE -ne 0) {
    throw "scp failed: $($ScpArgs -join ' ')"
  }
}

function Invoke-RetrySSH {
  param(
    [string]$HostName,
    [string]$Command,
    [string]$Description,
    [int]$Attempts = 30,
    [int]$DelaySeconds = 5
  )
  for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
    if ($attempt -eq 1) {
      Record-Command "ssh ubuntu@$HostName -- $Command"
    }
    & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$HostName" $Command
    if ($LASTEXITCODE -eq 0) { return }
    if ($attempt -lt $Attempts) {
      Write-Host "waiting for $Description ($attempt/$Attempts)"
      Start-Sleep -Seconds $DelaySeconds
    }
  }
  throw "timed out waiting for $Description"
}

function New-PrometheusConfig {
  param([object]$Inventory, [string]$Path)
  $targets = $Inventory.nodes | Sort-Object id | ForEach-Object { "          - $($_.private_ip):8000" }
  @(
    "global:",
    "  scrape_interval: 2s",
    "scrape_configs:",
    "  - job_name: 'bloc-sidecars'",
    "    metrics_path: /metrics",
    "    static_configs:",
    "      - targets:"
  ) + $targets | Set-Content -Encoding ascii $Path
}

function Collect-NetworkMatrix {
  param([object]$Inventory, [string]$Phase, [string]$OutPath)
  $lines = New-Object System.Collections.Generic.List[string]
  $lines.Add("phase,source,target_node_id,target_private_ip,endpoint,attempts,successes,avg_http_ms")
  foreach ($node in ($Inventory.nodes | Sort-Object id)) {
    $endpoint = "http://$($node.private_ip):8000/healthz"
    $curl = & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($Inventory.controller.public_ip)" "for i in 1 2 3 4 5; do curl -fsS -o /dev/null -w '%{http_code},%{time_total}\n' '$endpoint' || echo '000,0'; done"
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace(($curl -join "`n"))) {
      $lines.Add("$Phase,controller,$($node.id),$($node.private_ip),$endpoint,error,error,error")
      continue
    }
    $attempts = 0
    $successes = 0
    $totalSeconds = 0.0
    foreach ($line in $curl) {
      if ($line -match "^(\d{3}),([0-9.]+)$") {
        $attempts++
        if ($Matches[1] -eq "200") {
          $successes++
          $totalSeconds += [double]::Parse($Matches[2], [System.Globalization.CultureInfo]::InvariantCulture)
        }
      }
    }
    $avgMs = ""
    if ($successes -gt 0) {
      $avgMs = [Math]::Round(($totalSeconds / $successes) * 1000.0, 3).ToString([System.Globalization.CultureInfo]::InvariantCulture)
    }
    $lines.Add("$Phase,controller,$($node.id),$($node.private_ip),$endpoint,$attempts,$successes,$avgMs")
  }
  $lines | Set-Content -Encoding ascii $OutPath
}

function Convert-DockerPercent {
  param([string]$Value)
  if ([string]::IsNullOrWhiteSpace($Value)) { return "" }
  return ($Value.Trim() -replace "%", "")
}

function Collect-ResourceSample {
  param([object]$Inventory, [string]$Phase, [string]$BatchSize, [string]$OutPath)
  $timestamp = (Get-Date).ToUniversalTime().ToString("o")
  $lines = New-Object System.Collections.Generic.List[string]
  if (-not (Test-Path $OutPath)) {
    $lines.Add("timestamp,phase,batch_size,node_id,private_ip,container,cpu_percent,mem_usage,mem_percent,net_io,block_io,pids")
  }
  foreach ($node in ($Inventory.nodes | Sort-Object id)) {
    $format = "{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.MemPerc}},{{.NetIO}},{{.BlockIO}},{{.PIDs}}"
    $sample = & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($node.public_ip)" "docker stats --no-stream --format '$format' ec2-bloc-node-1"
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace(($sample -join "`n"))) {
      $lines.Add("$timestamp,$Phase,$BatchSize,$($node.id),$($node.private_ip),error,error,error,error,error,error,error")
      continue
    }
    foreach ($line in $sample) {
      $parts = $line.Split(",", 7)
      if ($parts.Count -lt 7) {
        $lines.Add("$timestamp,$Phase,$BatchSize,$($node.id),$($node.private_ip),parse-error,error,error,error,error,error,error")
        continue
      }
      $container = $parts[0]
      $cpu = Convert-DockerPercent $parts[1]
      $mem = $parts[2]
      $memPercent = Convert-DockerPercent $parts[3]
      $net = $parts[4]
      $block = $parts[5]
      $pids = $parts[6]
      $lines.Add("$timestamp,$Phase,$BatchSize,$($node.id),$($node.private_ip),$container,$cpu,""$mem"",$memPercent,""$net"",""$block"",$pids")
    }
  }
  $lines | Add-Content -Encoding ascii $OutPath
}

function Start-ResourceSamplers {
  param([object]$Inventory, [int]$Batch)
  $samplers = @()
  foreach ($node in ($Inventory.nodes | Sort-Object id)) {
    $remoteFile = "/tmp/bloc-resource-$ExperimentId-b$Batch.csv"
    $format = '$(date -u +%Y-%m-%dT%H:%M:%SZ),during-batch,' + $Batch + ',' + $node.id + ',' + $node.private_ip + ',{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.MemPerc}},{{.NetIO}},{{.BlockIO}},{{.PIDs}}'
    $command = "rm -f '$remoteFile'; (while true; do docker stats --no-stream --format '$format' ec2-bloc-node-1; sleep 10; done) > '$remoteFile' 2>'$remoteFile.err' & echo `$!"
    Record-Command "ssh ubuntu@$($node.public_ip) -- $command"
    $samplerPid = (& ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($node.public_ip)" $command).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($samplerPid)) {
      throw "failed to start resource sampler for operator $($node.id)"
    }
    $samplers += [pscustomobject]@{
      node_id = $node.id
      public_ip = $node.public_ip
      remote_file = $remoteFile
      pid = $samplerPid
    }
  }
  return $samplers
}

function Stop-ResourceSamplers {
  param([object[]]$Samplers, [string]$OutPath)
  foreach ($sampler in $Samplers) {
    $stopCommand = "kill '$($sampler.pid)' >/dev/null 2>&1 || true"
    Record-Command "ssh ubuntu@$($sampler.public_ip) -- $stopCommand"
    & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($sampler.public_ip)" $stopCommand | Out-Null
    $rows = & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($sampler.public_ip)" "cat '$($sampler.remote_file)' 2>/dev/null || true"
    if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace(($rows -join "`n"))) {
      $rows | Add-Content -Encoding ascii $OutPath
    }
  }
}

function Merge-CsvOutputs {
  param([string]$Root, [object[]]$Scenarios)
  foreach ($csvName in @("run_measurements.csv", "node_measurements.csv", "scenario_summary.csv")) {
    $dest = Join-Path $Root $csvName
    if (Test-Path $dest) { Remove-Item -LiteralPath $dest -Force }
    $mergedRows = @()
    foreach ($scenario in $Scenarios) {
      $source = Join-Path $scenario.local_results $csvName
      if (-not (Test-Path $source)) { throw "missing scenario output: $source" }
      foreach ($row in (Import-Csv $source)) {
        $row | Add-Member -NotePropertyName measurement_block -NotePropertyValue $scenario.block -Force
        if ($RepetitionBlocks -gt 1 -and $row.PSObject.Properties.Name -contains "run_id") {
          $row.run_id = "block-$($scenario.block)-$($row.run_id)"
        }
        $mergedRows += $row
      }
    }
    $mergedRows | Export-Csv -NoTypeInformation -Encoding utf8 $dest
  }
  $summaries = @()
  foreach ($scenario in $Scenarios) {
    $summaryPath = Join-Path $scenario.local_results "scenario_summary.json"
    $summary = Get-Content -Raw $summaryPath | ConvertFrom-Json
    $summary | Add-Member -NotePropertyName measurement_block -NotePropertyValue $scenario.block -Force
    $summaries += $summary
  }
  $summaries | ConvertTo-Json -Depth 50 | Set-Content -Encoding utf8 (Join-Path $Root "scenario_summary.json")
}

function Write-CampaignManifest {
  param(
    [string]$Status,
    [object]$Inventory,
    [object]$TerraformMetadata,
    [object]$CleanupChecks,
    [string]$InvalidReason = ""
  )
  $manifest = [ordered]@{
    schema_version = "bloc-ec2-campaign/v1"
    experiment_id = $ExperimentId
    campaign = $CampaignLabel
    status = $Status
    invalid_reason = if ([string]::IsNullOrWhiteSpace($InvalidReason)) { $null } else { $InvalidReason }
    started_at = $campaignStartedAt
    finished_at = (Get-Date).ToUniversalTime().ToString("o")
    git_commit = $gitCommit
    docker_image = $imageUri
    ecr_repository_name = $ecrRepositoryName
    aws_region = $AwsRegion
    availability_zone = $AvailabilityZone
    availability_zones = $AvailabilityZones
    node_count = $NodeCount
    operator_instance_type = $OperatorInstanceType
    controller_instance_type = $ControllerInstanceType
    topology = $Topology
    tx_source = "synthetic"
    batch_sizes = $BatchSizes
    warmups = $Warmups
    repetitions = $Repetitions
    repetition_blocks = $RepetitionBlocks
    batch_order_blocks = $resolvedBatchOrders
    commands = $commandLog.ToArray()
    terraform = $TerraformMetadata
    inventory = $Inventory
    cleanup_checks = $CleanupChecks
  }
  $manifest | ConvertTo-Json -Depth 50 | Set-Content -Encoding utf8 (Join-Path $artifactRoot "manifest.json")
}

New-Item -ItemType Directory -Force $artifactRoot, "$artifactRoot\generated", "$artifactRoot\logs", "$artifactRoot\scenarios" | Out-Null
$cleanupChecks = [ordered]@{}
$terraformMetadata = [ordered]@{}

try {
  Push-Location $repoRoot

  Record-Command "git rev-parse --short=12 HEAD"
  Invoke-Checked { git rev-parse --short=12 HEAD | Out-Null } "resolve git commit"
  $gitCommit = (& git rev-parse --short=12 HEAD).Trim()
  if ([string]::IsNullOrWhiteSpace($EcrImageTag)) {
    $EcrImageTag = $gitCommit
  }
  if ($EcrImageTag -notmatch '^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$') {
    throw "EcrImageTag is not a valid Docker tag: $EcrImageTag"
  }

  Record-Command "aws sts get-caller-identity --profile $AwsProfile --output json"
  Invoke-Checked { & $aws sts get-caller-identity --profile $AwsProfile --output json | Set-Content -Encoding utf8 (Join-Path $artifactRoot "aws-caller-identity.json") } "aws identity preflight"

  Write-Step "campaign id collision preflight"
  Assert-NoExistingCampaignResources

  if ([string]::IsNullOrWhiteSpace($PrebuiltImageTag)) {
    Write-Step "build Docker image"
    $sourceImageTag = "bloc-node:$gitCommit"
    Record-Command "docker build -f bloc-node/Dockerfile -t $sourceImageTag ."
    docker build -f bloc-node/Dockerfile -t $sourceImageTag .
    if ($LASTEXITCODE -ne 0) { throw "docker build failed" }
  } else {
    Write-Step "verify prebuilt Docker image"
    $sourceImageTag = $PrebuiltImageTag
    Record-Command "docker image inspect $sourceImageTag"
    docker image inspect $sourceImageTag | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "prebuilt Docker image not found: $sourceImageTag" }
  }

  Write-Step "create temporary EC2 key pair"
  Record-Command "aws ec2 create-key-pair --profile $AwsProfile --region $AwsRegion --key-name $keyName --key-type rsa --key-format pem --output json"
  $keyJson = & $aws ec2 create-key-pair --profile $AwsProfile --region $AwsRegion --key-name $keyName --key-type rsa --key-format pem --output json
  if ($LASTEXITCODE -ne 0) { throw "create-key-pair failed" }
  $keyCreated = $true
  $keyObj = $keyJson | ConvertFrom-Json
  Write-AsciiFile $keyPath (ConvertTo-Pem $keyObj.KeyMaterial)
  icacls $keyPath /inheritance:r | Out-Null
  icacls $keyPath /grant:r "$env:USERNAME`:R" | Out-Null
  if ($KeepResourcesOnFailure -or $KeepResourcesAfterRun) {
    Copy-Item -LiteralPath $keyPath -Destination $artifactKeyPath -Force
    icacls $artifactKeyPath /inheritance:r | Out-Null
    icacls $artifactKeyPath /grant:r "$env:USERNAME`:R" | Out-Null
  }

  Write-Step "prepare isolated Terraform workdir"
  if (Test-Path $terraformWorkDir) { Remove-Item -LiteralPath $terraformWorkDir -Recurse -Force }
  New-Item -ItemType Directory -Force $terraformWorkDir | Out-Null
  Copy-Item -Path @("$terraformSourceDir\main.tf", "$terraformSourceDir\outputs.tf", "$terraformSourceDir\variables.tf", "$terraformSourceDir\user-data.sh") -Destination $terraformWorkDir
  if (Test-Path "$terraformSourceDir\.terraform.lock.hcl") {
    Copy-Item "$terraformSourceDir\.terraform.lock.hcl" $terraformWorkDir
  }

  $adminCidrsTf = ConvertTo-TerraformStringList $AdminCidrs
  $availabilityZonesTf = ConvertTo-TerraformStringList $AvailabilityZones
  $subnetCidrsTf = ConvertTo-TerraformStringList $SubnetCidrs
  @(
    "aws_region               = `"$AwsRegion`"",
    "availability_zone        = `"$AvailabilityZone`"",
    "availability_zones       = [$availabilityZonesTf]",
    "subnet_cidrs             = [$subnetCidrsTf]",
    "name_prefix              = `"$ExperimentId`"",
    "node_count               = $NodeCount",
    "operator_instance_type   = `"$OperatorInstanceType`"",
    "controller_instance_type = `"$ControllerInstanceType`"",
    "create_ecr_repository    = true",
    "ecr_repository_name      = `"$ecrRepositoryName`"",
    "key_name                 = `"$keyName`"",
    "admin_cidrs              = [$adminCidrsTf]"
  ) | Set-Content -Encoding ascii $tfvarsPath

  Push-Location $terraformWorkDir
  $env:AWS_PROFILE = $AwsProfile
  Record-Command "terraform init -input=false"
  Invoke-Checked { terraform init -input=false } "terraform init"
  Record-Command "terraform fmt -check -diff"
  Invoke-Checked { terraform fmt -check -diff } "terraform fmt check"
  Record-Command "terraform validate"
  Invoke-Checked { terraform validate } "terraform validate"
  Record-Command "terraform plan -var-file=$tfvarsPath -out=$planPath -input=false"
  Invoke-Checked { terraform plan "-var-file=$tfvarsPath" "-out=$planPath" -input=false } "terraform plan"
  Record-Command "terraform show -no-color $planPath"
  terraform show -no-color $planPath | Set-Content -Encoding utf8 (Join-Path $artifactRoot "terraform-plan.txt")
  $planText = Get-Content -Raw (Join-Path $artifactRoot "terraform-plan.txt")
  foreach ($forbidden in @("aws_nat_gateway", "aws_lb", "aws_eks_cluster", "aws_db_instance", "aws_eip", "aws_autoscaling_group")) {
    if ($planText.Contains($forbidden)) { throw "Terraform plan contains forbidden expensive resource: $forbidden" }
  }
  $forbiddenSettings = [ordered]@{
    "Spot market type" = '(?m)^\s*\+\s*market_type\s*=\s*"spot"\s*$'
    "detailed monitoring" = '(?m)^\s*\+\s*monitoring\s*=\s*true\s*$'
  }
  foreach ($forbiddenSetting in $forbiddenSettings.GetEnumerator()) {
    if ($planText -match $forbiddenSetting.Value) {
      throw "Terraform plan contains forbidden setting: $($forbiddenSetting.Key)"
    }
  }
  $allowedResourceTypes = @(
    "aws_vpc",
    "aws_subnet",
    "aws_internet_gateway",
    "aws_route_table",
    "aws_route_table_association",
    "aws_ecr_repository",
    "aws_iam_role",
    "aws_iam_role_policy",
    "aws_iam_role_policy_attachment",
    "aws_iam_instance_profile",
    "aws_security_group",
    "aws_instance"
  )
  $plannedResourceTypes = @(
    [regex]::Matches($planText, '(?m)^\s*#\s+(aws_[a-z0-9_]+)\.[^ ]+\s+will be created') |
      ForEach-Object { $_.Groups[1].Value } |
      Sort-Object -Unique
  )
  $unexpectedResourceTypes = @($plannedResourceTypes | Where-Object { $_ -notin $allowedResourceTypes })
  if ($unexpectedResourceTypes.Count -gt 0) {
    throw "Terraform plan contains resource types outside the EC2 campaign allowlist: $($unexpectedResourceTypes -join ', ')"
  }
  if (-not $AutoApprovePlan) {
    Write-Host "Terraform plan saved to $artifactRoot\terraform-plan.txt"
    $answer = Read-Host "Type APPLY to create AWS resources for this $CampaignLabel phase"
    if ($answer -ne "APPLY") { throw "operator declined terraform apply" }
  }

  $terraformStarted = $true
  Record-Command "terraform apply -input=false $planPath"
  Invoke-Checked { terraform apply -input=false $planPath } "terraform apply"
  $terraformApplied = $true
  Record-Command "terraform output -json inventory"
  terraform output -json inventory | Set-Content -Encoding ascii (Join-Path $PSScriptRoot "inventory.json")
  terraform output -json inventory | Set-Content -Encoding ascii (Join-Path $artifactRoot "inventory.json")
  Record-Command "terraform output -raw ecr_repository_url"
  $ecrRepositoryUrl = (& terraform output -raw ecr_repository_url).Trim()
  Record-Command "terraform state pull"
  terraform state pull | Set-Content -Encoding utf8 (Join-Path $artifactRoot "terraform-state-after-apply.json")
  Pop-Location

  $imageUri = $ecrRepositoryUrl + ":" + $EcrImageTag
  $registry = ($ecrRepositoryUrl -split "/")[0]
  Record-Command "docker tag $sourceImageTag $imageUri"
  Invoke-Checked { docker tag $sourceImageTag $imageUri } "tag Docker image"
  Record-Command "aws ecr get-login-password --profile $AwsProfile --region $AwsRegion | docker login --username AWS --password-stdin $registry"
  $password = & $aws ecr get-login-password --profile $AwsProfile --region $AwsRegion
  if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($password)) { throw "ECR login password retrieval failed" }
  $password | docker login --username AWS --password-stdin $registry
  if ($LASTEXITCODE -ne 0) { throw "docker login failed" }
  Record-Command "docker push $imageUri"
  Invoke-Checked { docker push $imageUri } "push Docker image"
  Record-Command "aws ecr describe-images --profile $AwsProfile --region $AwsRegion --repository-name $ecrRepositoryName --image-ids imageTag=$EcrImageTag"
  $imageDigest = (& $aws ecr describe-images --profile $AwsProfile --region $AwsRegion --repository-name $ecrRepositoryName --image-ids "imageTag=$EcrImageTag" --query "imageDetails[0].imageDigest" --output text).Trim()
  $terraformMetadata["ecr_repository_url"] = $ecrRepositoryUrl
  $terraformMetadata["docker_image_digest"] = $imageDigest
  $terraformMetadata | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $artifactRoot "terraform-metadata.json")

  $inventory = Get-Content -Raw (Join-Path $PSScriptRoot "inventory.json") | ConvertFrom-Json
  New-PrometheusConfig $inventory (Join-Path $PSScriptRoot "prometheus.ec2.yml")
  Copy-Item (Join-Path $PSScriptRoot "prometheus.ec2.yml") (Join-Path $artifactRoot "generated\prometheus.ec2.yml") -Force

  Push-Location (Join-Path $repoRoot "bloc-node")
  $env:GOCACHE = Join-Path (Get-Location) ".gocache"
  Record-Command "go run ./cmd/bloc-node gen-ec2-config --inventory ..\deploy\ec2\inventory.json --cluster-out ..\deploy\ec2\cluster.ec2.json --remote-eval-out ..\deploy\ec2\remote-eval.ec2.json --cluster-id $ExperimentId --nodes $NodeCount --bmax 128 --prometheus-url http://$($inventory.controller.private_ip):9090 --grafana-url http://$($inventory.controller.private_ip):3000 --controller-url $($inventory.controller.private_ip)"
  Invoke-Checked {
    go run ./cmd/bloc-node gen-ec2-config `
      --inventory ..\deploy\ec2\inventory.json `
      --cluster-out ..\deploy\ec2\cluster.ec2.json `
      --remote-eval-out ..\deploy\ec2\remote-eval.ec2.json `
      --cluster-id $ExperimentId `
      --nodes $NodeCount `
      --bmax 128 `
      --prometheus-url "http://$($inventory.controller.private_ip):9090" `
      --grafana-url "http://$($inventory.controller.private_ip):3000" `
      --controller-url $inventory.controller.private_ip
  } "generate EC2 configs"
  Pop-Location

  Copy-Item (Join-Path $PSScriptRoot "cluster.ec2.json") (Join-Path $artifactRoot "generated\cluster.ec2.json") -Force
  Copy-Item (Join-Path $PSScriptRoot "cluster.ec2.crs") (Join-Path $artifactRoot "generated\cluster.ec2.crs") -Force
  Copy-Item (Join-Path $PSScriptRoot "remote-eval.ec2.json") (Join-Path $artifactRoot "generated\remote-eval.ec2.json") -Force

  $allHosts = @($inventory.controller.public_ip) + @($inventory.nodes | Sort-Object id | ForEach-Object { $_.public_ip })
  foreach ($hostIp in $allHosts) {
    Invoke-RetrySSH $hostIp "echo ssh-ready" "ssh on $hostIp" 30 5
    Invoke-RetrySSH $hostIp "cloud-init status --wait >/tmp/bloc-cloud-init-wait.log 2>&1 && docker version >/tmp/bloc-docker-version.log 2>&1 && docker compose version >/tmp/bloc-docker-compose-version.log 2>&1" "cloud-init and Docker on $hostIp" 60 10
    Invoke-SSH $hostIp "sudo mkdir -p /etc/bloc /opt/bloc/ec2 /opt/bloc/docker-compose/grafana && sudo chown -R ubuntu:ubuntu /opt/bloc /etc/bloc"
  }

  Write-Step "collect EC2 host metadata"
  $hostMetadata = @()
  $hosts = @(
    [pscustomobject]@{
      role = "controller"
      node_id = $null
      value = $inventory.controller
      gomaxprocs = $null
    }
  )
  foreach ($node in ($inventory.nodes | Sort-Object id)) {
    $hosts += [pscustomobject]@{
      role = "operator"
      node_id = $node.id
      value = $node
      gomaxprocs = "runtime-default"
    }
  }
  foreach ($hostEntry in $hosts) {
    $hostValue = $hostEntry.value
    $hostMetadata += [ordered]@{
      role = $hostEntry.role
      node_id = $hostEntry.node_id
      instance_id = $hostValue.instance_id
      ami_id = $hostValue.ami_id
      instance_type = $hostValue.instance_type
      availability_zone = $hostValue.zone
      private_ip = $hostValue.private_ip
      cpu_model = Get-SSHText $hostValue.public_ip "lscpu | grep -m1 'Model name' | cut -d: -f2- | xargs"
      logical_cpus = Get-SSHText $hostValue.public_ip "nproc"
      kernel = Get-SSHText $hostValue.public_ip "uname -srvmo"
      docker_version = Get-SSHText $hostValue.public_ip "docker version --format '{{.Server.Version}}'"
      gomaxprocs = $hostEntry.gomaxprocs
    }
  }
  $hostMetadata | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $artifactRoot "host-metadata.json")

  foreach ($node in ($inventory.nodes | Sort-Object id)) {
    Invoke-SCP @((Join-Path $PSScriptRoot "cluster.ec2.json"), "ubuntu@$($node.public_ip):/etc/bloc/cluster.json")
    Invoke-SCP @((Join-Path $PSScriptRoot "cluster.ec2.crs"), "ubuntu@$($node.public_ip):/etc/bloc/cluster.crs")
    Invoke-SCP @((Join-Path $PSScriptRoot "secrets.ec2\operator-$($node.id).json"), "ubuntu@$($node.public_ip):/etc/bloc/operator.json")
    Invoke-SSH $node.public_ip "sudo chown 10001:10001 /etc/bloc/operator.json && sudo chmod 600 /etc/bloc/operator.json"
    Invoke-SCP @((Join-Path $PSScriptRoot "operator-compose.yaml"), "ubuntu@$($node.public_ip):/opt/bloc/ec2/operator-compose.yaml")
    Invoke-SSH $node.public_ip "set -e; aws ecr get-login-password --region '$AwsRegion' | docker login --username AWS --password-stdin '$registry'; cd /opt/bloc/ec2; NODE_ID='$($node.id)' BLOC_IMAGE='$imageUri' docker compose -f operator-compose.yaml up -d"
  }

  Invoke-SCP @((Join-Path $PSScriptRoot "controller-compose.yaml"), "ubuntu@$($inventory.controller.public_ip):/opt/bloc/ec2/controller-compose.yaml")
  Invoke-SCP @((Join-Path $PSScriptRoot "prometheus.ec2.yml"), "ubuntu@$($inventory.controller.public_ip):/opt/bloc/ec2/prometheus.ec2.yml")
  Invoke-SCP @((Join-Path $PSScriptRoot "remote-eval.ec2.json"), "ubuntu@$($inventory.controller.public_ip):/opt/bloc/ec2/remote-eval.ec2.json")
  Invoke-SCP @("-r", (Join-Path $repoRoot "deploy\docker-compose\grafana\*"), "ubuntu@$($inventory.controller.public_ip):/opt/bloc/docker-compose/grafana/")
  Invoke-SSH $inventory.controller.public_ip "set -e; cd /opt/bloc/ec2; docker compose -f controller-compose.yaml up -d"

  foreach ($node in ($inventory.nodes | Sort-Object id)) {
    Invoke-RetrySSH $inventory.controller.public_ip "curl -fsS http://$($node.private_ip):8000/healthz" "operator $($node.id) healthz" 30 5
    Invoke-RetrySSH $inventory.controller.public_ip "curl -fsS http://$($node.private_ip):8000/metrics | head -n 5" "operator $($node.id) metrics" 12 5
  }
  Invoke-RetrySSH $inventory.controller.public_ip "curl -fsS http://127.0.0.1:9090/api/v1/targets > /opt/bloc/ec2/prometheus-targets-before.json" "Prometheus targets" 12 5
  Invoke-SCP @("ubuntu@$($inventory.controller.public_ip):/opt/bloc/ec2/prometheus-targets-before.json", (Join-Path $artifactRoot "prometheus-targets-before.json"))

  Collect-NetworkMatrix $inventory "pre" (Join-Path $artifactRoot "network-pre.csv")
  Collect-ResourceSample $inventory "pre-campaign" "" (Join-Path $artifactRoot "resource-samples.csv")

  $nextSlot = 1
  $blockRepetitions = [int]($Repetitions / $RepetitionBlocks)
  $warmedBatches = [System.Collections.Generic.HashSet[int]]::new()
  $scenarioRecords = @()
  for ($blockIndex = 0; $blockIndex -lt $resolvedBatchOrders.Count; $blockIndex++) {
    $blockNumber = $blockIndex + 1
    foreach ($batch in $resolvedBatchOrders[$blockIndex]) {
      if ($MaxRuntimeMinutes -gt 0 -and ((Get-Date) - $campaignStartedClock).TotalMinutes -ge $MaxRuntimeMinutes) {
        throw "phase runtime reached the configured $MaxRuntimeMinutes minute ceiling"
      }
      $scenarioWarmups = if ($warmedBatches.Add($batch)) { $Warmups } else { 0 }
      $relativeScenarioPath = if ($RepetitionBlocks -eq 1) {
        "batch-$batch"
      } else {
        "block-$blockNumber/batch-$batch"
      }
      $scenarioDir = "/opt/bloc/ec2/results/$ExperimentId/$relativeScenarioPath"
      $firstSlot = $nextSlot
      Collect-ResourceSample $inventory "before-block-$blockNumber-batch" $batch (Join-Path $artifactRoot "resource-samples.csv")
      $resourceSamplers = Start-ResourceSamplers $inventory $batch
      $remoteExperimentId = "$ExperimentId-block$blockNumber-b$batch"
      $remoteCmd = "set -e; aws ecr get-login-password --region '$AwsRegion' | docker login --username AWS --password-stdin '$registry'; sudo mkdir -p '$scenarioDir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$imageUri' eval-remote --config remote-eval.ec2.json --experiment-id '$remoteExperimentId' --first-slot '$firstSlot' --batch-size '$batch' --warmups '$scenarioWarmups' --repetitions '$blockRepetitions' --out-dir 'results/$ExperimentId/$relativeScenarioPath' --image-tag '$imageUri' --git-commit '$gitCommit' --timeout 30s"
      Record-Command "ssh ubuntu@$($inventory.controller.public_ip) -- $remoteCmd"
      $scenarioExit = -1
      try {
        & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$($inventory.controller.public_ip)" $remoteCmd
        $scenarioExit = $LASTEXITCODE
      }
      finally {
        Stop-ResourceSamplers $resourceSamplers (Join-Path $artifactRoot "resource-samples.csv")
      }
      Collect-ResourceSample $inventory "after-block-$blockNumber-batch" $batch (Join-Path $artifactRoot "resource-samples.csv")
      $localScenarioDir = Join-Path $artifactRoot (Join-Path "scenarios" $relativeScenarioPath)
      New-Item -ItemType Directory -Force $localScenarioDir | Out-Null
      Invoke-SCP @("-r", "ubuntu@$($inventory.controller.public_ip):$scenarioDir", (Join-Path $localScenarioDir "results"))
      if ($scenarioExit -ne 0) {
        throw "eval-remote failed for block $blockNumber batch $batch with exit code $scenarioExit"
      }
      if ($MaxRuntimeMinutes -gt 0 -and ((Get-Date) - $campaignStartedClock).TotalMinutes -gt $MaxRuntimeMinutes) {
        throw "phase exceeded the configured $MaxRuntimeMinutes minute ceiling"
      }
      $scenarioRecords += [pscustomobject]@{
        block = $blockNumber
        batch = $batch
        local_results = Join-Path $localScenarioDir "results"
      }
      $nextSlot += $scenarioWarmups + $blockRepetitions
    }
  }

  Collect-NetworkMatrix $inventory "post" (Join-Path $artifactRoot "network-post.csv")
  Invoke-SSH $inventory.controller.public_ip "curl -fsS http://127.0.0.1:9090/api/v1/targets > /opt/bloc/ec2/prometheus-targets-after.json"
  Invoke-SCP @("ubuntu@$($inventory.controller.public_ip):/opt/bloc/ec2/prometheus-targets-after.json", (Join-Path $artifactRoot "prometheus-targets.json"))

  foreach ($node in ($inventory.nodes | Sort-Object id)) {
    Record-Command "ssh ubuntu@$($node.public_ip) -- docker logs --tail=500 ec2-bloc-node-1"
    & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($node.public_ip)" "docker logs --tail=500 ec2-bloc-node-1 2>&1" | Set-Content -Encoding utf8 (Join-Path $artifactRoot "logs\operator-$($node.id).log")
  }
  Record-Command "ssh ubuntu@$($inventory.controller.public_ip) -- docker logs --tail=500 ec2-prometheus-1"
  & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($inventory.controller.public_ip)" "docker logs --tail=500 ec2-prometheus-1 2>&1" | Set-Content -Encoding utf8 (Join-Path $artifactRoot "logs\prometheus.log")
  Record-Command "ssh ubuntu@$($inventory.controller.public_ip) -- docker logs --tail=500 ec2-grafana-1"
  & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "ubuntu@$($inventory.controller.public_ip)" "docker logs --tail=500 ec2-grafana-1 2>&1" | Set-Content -Encoding utf8 (Join-Path $artifactRoot "logs\grafana.log")

  Merge-CsvOutputs $artifactRoot $scenarioRecords

  if (-not $SkipChartGeneration) {
    $chartPython = Join-Path $repoRoot "latency-charts\.venv\Scripts\python.exe"
    if (Test-Path $chartPython) {
      Push-Location (Join-Path $repoRoot "latency-charts")
      & $chartPython -m bloc_latency_charts $artifactRoot
      if ($LASTEXITCODE -ne 0) { throw "chart generation failed" }
      Pop-Location
    } else {
      Write-Warning "Skipping chart generation because latency-charts .venv is missing."
    }
  }

  $campaignStatus = "complete"
  Write-CampaignManifest "complete" $inventory $terraformMetadata $cleanupChecks
}
catch {
  $invalidReason = $_.Exception.Message
  $campaignFailed = $true
  Write-Error $_
  if ($KeepResourcesOnFailure) {
    Write-Warning "Keeping AWS resources because -KeepResourcesOnFailure was supplied."
  }
}
finally {
  try {
    $shouldKeepResources = $KeepResourcesAfterRun -or ($KeepResourcesOnFailure -and $campaignFailed)
    if (($terraformApplied -or $terraformStarted) -and -not $shouldKeepResources) {
      Push-Location $terraformWorkDir
      $env:AWS_PROFILE = $AwsProfile
      Record-Command "terraform destroy -var-file=$tfvarsPath -auto-approve"
      terraform destroy "-var-file=$tfvarsPath" -auto-approve
      Record-Command "terraform state list"
      terraform state list | Set-Content -Encoding utf8 (Join-Path $artifactRoot "terraform-state-after-destroy.txt")
      Pop-Location
    }
    if ($keyCreated) {
      Record-Command "aws ec2 delete-key-pair --profile $AwsProfile --region $AwsRegion --key-name $keyName"
      & $aws ec2 delete-key-pair --profile $AwsProfile --region $AwsRegion --key-name $keyName | Out-Null
    }
    if ((Test-Path $keyPath) -and -not $shouldKeepResources) {
      icacls $keyPath /grant:r "$env:USERNAME`:F" | Out-Null
      Remove-Item -LiteralPath $keyPath -Force
    }
    Record-Command "aws ec2 describe-instances --profile $AwsProfile --region $AwsRegion --filters Name=tag:Name,Values=$ExperimentId-controller,$ExperimentId-operator-* Name=instance-state-name,Values=pending,running,stopping,stopped"
    $instanceCheck = Get-AwsText @("ec2", "describe-instances", "--profile", $AwsProfile, "--region", $AwsRegion, "--filters", "Name=tag:Name,Values=$ExperimentId-controller,$ExperimentId-operator-*", "Name=instance-state-name,Values=pending,running,stopping,stopped", "--query", "Reservations[].Instances[].InstanceId", "--output", "text")
    Record-Command "aws ec2 describe-volumes --profile $AwsProfile --region $AwsRegion --filters Name=tag:Name,Values=$ExperimentId-controller-volume,$ExperimentId-operator-*-volume"
    $volumeCheck = Get-AwsText @("ec2", "describe-volumes", "--profile", $AwsProfile, "--region", $AwsRegion, "--filters", "Name=tag:Name,Values=$ExperimentId-controller-volume,$ExperimentId-operator-*-volume", "--query", "Volumes[].VolumeId", "--output", "text")
    Record-Command "aws ec2 describe-vpcs --profile $AwsProfile --region $AwsRegion --filters Name=tag:Name,Values=$ExperimentId-vpc"
    $vpcCheck = Get-AwsText @("ec2", "describe-vpcs", "--profile", $AwsProfile, "--region", $AwsRegion, "--filters", "Name=tag:Name,Values=$ExperimentId-vpc", "--query", "Vpcs[].VpcId", "--output", "text")
    Record-Command "aws ec2 describe-key-pairs --profile $AwsProfile --region $AwsRegion --key-names $keyName"
    $keyCheck = Get-AwsText @("ec2", "describe-key-pairs", "--profile", $AwsProfile, "--region", $AwsRegion, "--key-names", $keyName, "--query", "KeyPairs[].KeyName", "--output", "text")
    Record-Command "aws ecr describe-repositories --profile $AwsProfile --region $AwsRegion --repository-names $ecrRepositoryName"
    $ecrCheck = Get-AwsText @("ecr", "describe-repositories", "--profile", $AwsProfile, "--region", $AwsRegion, "--repository-names", $ecrRepositoryName, "--query", "repositories[].repositoryUri", "--output", "text")
    Record-Command "aws iam get-role --profile $AwsProfile --role-name $ExperimentId-ec2-ecr-readonly"
    $iamRoleCheck = Get-AwsText @("iam", "get-role", "--profile", $AwsProfile, "--role-name", "$ExperimentId-ec2-ecr-readonly", "--query", "Role.RoleName", "--output", "text")
    Record-Command "aws iam get-instance-profile --profile $AwsProfile --instance-profile-name $ExperimentId-ec2-ecr-readonly"
    $instanceProfileCheck = Get-AwsText @("iam", "get-instance-profile", "--profile", $AwsProfile, "--instance-profile-name", "$ExperimentId-ec2-ecr-readonly", "--query", "InstanceProfile.InstanceProfileName", "--output", "text")
    $cleanupChecks["instances"] = $instanceCheck
    $cleanupChecks["volumes"] = $volumeCheck
    $cleanupChecks["vpc"] = $vpcCheck
    $cleanupChecks["key_pair"] = $keyCheck
    $cleanupChecks["ecr_repository"] = $ecrCheck
    $cleanupChecks["iam_role"] = $iamRoleCheck
    $cleanupChecks["instance_profile"] = $instanceProfileCheck
    $cleanupChecks | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $artifactRoot "cleanup-verification.json")

    $inventoryForManifest = $null
    if (Test-Path (Join-Path $artifactRoot "inventory.json")) {
      $inventoryForManifest = Get-Content -Raw (Join-Path $artifactRoot "inventory.json") | ConvertFrom-Json
    }
    Write-CampaignManifest $campaignStatus $inventoryForManifest $terraformMetadata $cleanupChecks $invalidReason
  }
  finally {
    while ((Get-Location).Path -ne $repoRoot -and (Get-Location).Path.StartsWith($repoRoot)) {
      Pop-Location -ErrorAction SilentlyContinue
    }
  }
}

if ($campaignFailed) {
  exit 1
}
