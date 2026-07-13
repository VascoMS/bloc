[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string[]]$AdminCidrs,
  [string]$AwsProfile = "bloc",
  [string]$AwsRegion = "us-east-1",
  [string[]]$AvailabilityZones = @("us-east-1a", "us-east-1b"),
  [string]$ExperimentId = ("bloc-ec2-bteattr-" + (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")),
  [int[]]$BatchSizes = @(8, 32, 128),
  [int]$Warmups = 5,
  [int]$Repetitions = 30,
  [int]$TxSize = 256,
  [int]$PaperBenchmarkCount = 10,
  [string]$SameAZData = "",
  [string]$CrossAZData = "",
  [switch]$AutoApprovePlan,
  [switch]$KeepResourcesOnFailure,
  [switch]$AllowDirtyTree
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$terraformSource = Join-Path $PSScriptRoot "bte-attribution\terraform"
$artifactRoot = Join-Path $repoRoot "results\ec2\$ExperimentId"
$terraformWork = Join-Path $artifactRoot "generated\terraform-work"
$tfvarsPath = Join-Path $terraformWork "campaign.tfvars.json"
$planPath = Join-Path $terraformWork "campaign.tfplan"
$keyName = "$ExperimentId-key"
$keyPath = Join-Path $artifactRoot "$keyName.pem"
$ecrName = $ExperimentId.ToLowerInvariant() -replace "[^a-z0-9._/-]", "-"
$commandLog = Join-Path $artifactRoot "commands.log"
$terraformStarted = $false
$terraformApplied = $false
$keyCreated = $false
$campaignComplete = $false
$previousProfile = $env:AWS_PROFILE
if ([string]::IsNullOrWhiteSpace($SameAZData)) {
  $SameAZData = Join-Path $repoRoot "results\ec2\m3-same-az-synthetic-20260706t105535z"
}
if ([string]::IsNullOrWhiteSpace($CrossAZData)) {
  $CrossAZData = Join-Path $repoRoot "results\ec2\m3-cross-az-synthetic-20260706t122922z\node_measurements.csv"
}

function Require-Command([string]$Name) {
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "required command not found: $Name"
  }
}

function Record-Command([string]$Command) {
  $timestamp = (Get-Date).ToUniversalTime().ToString("o")
  Add-Content -Encoding utf8 $commandLog "$timestamp $Command"
}

function Invoke-Checked([scriptblock]$Command, [string]$Description) {
  & $Command
  if ($LASTEXITCODE -ne 0) {
    throw "$Description failed with exit code $LASTEXITCODE"
  }
}

function Invoke-SSH([string]$HostName, [string]$Command) {
  Record-Command "ssh ubuntu@$HostName -- $Command"
  & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$HostName" $Command
  if ($LASTEXITCODE -ne 0) {
    throw "ssh failed on $HostName with exit code $LASTEXITCODE"
  }
}

function Wait-SSH([string]$HostName) {
  for ($attempt = 1; $attempt -le 36; $attempt++) {
    & ssh -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=10 "ubuntu@$HostName" "test -f /opt/bloc/ready && docker version >/dev/null 2>&1"
    if ($LASTEXITCODE -eq 0) { return }
    Start-Sleep -Seconds 5
  }
  throw "host $HostName did not become ready"
}

function Save-AwsJson([string[]]$Arguments, [string]$Path) {
  Record-Command "aws $($Arguments -join ' ')"
  $output = & aws @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "aws $($Arguments[0]) failed with exit code $LASTEXITCODE"
  }
  $output | Set-Content -Encoding utf8 $Path
}

function Save-CreditMetrics($Host, [datetime]$StartTime, [datetime]$EndTime, [string]$Destination) {
  New-Item -ItemType Directory -Force $Destination | Out-Null
  Save-AwsJson @("ec2", "describe-credit-specifications", "--profile", $AwsProfile, "--region", $AwsRegion, "--instance-ids", $Host.instance_id, "--output", "json") (Join-Path $Destination "credit-specification.json")
  foreach ($metric in @("CPUCreditBalance", "CPUCreditUsage", "CPUSurplusCreditBalance", "CPUSurplusCreditsCharged")) {
    Save-AwsJson @("cloudwatch", "get-metric-statistics", "--profile", $AwsProfile, "--region", $AwsRegion, "--namespace", "AWS/EC2", "--metric-name", $metric, "--dimensions", "Name=InstanceId,Value=$($Host.instance_id)", "--statistics", "Average", "Maximum", "--period", "300", "--start-time", $StartTime.ToUniversalTime().ToString("o"), "--end-time", $EndTime.ToUniversalTime().ToString("o"), "--output", "json") (Join-Path $Destination "$metric.json")
  }
}

function Assert-HostArtifacts([string]$HostDir) {
  $manifestPath = Join-Path $HostDir "timed\manifest.json"
  $measurementsPath = Join-Path $HostDir "timed\measurements.csv"
  $paperPath = Join-Path $HostDir "paper-benchmark.txt"
  foreach ($path in @($manifestPath, $measurementsPath, $paperPath, (Join-Path $HostDir "profiles\paper\cpu.pprof"), (Join-Path $HostDir "profiles\bloc\cpu.pprof"))) {
    if (-not (Test-Path $path) -or (Get-Item $path).Length -eq 0) {
      throw "missing or empty host artifact: $path"
    }
  }
  $manifest = Get-Content -Raw $manifestPath | ConvertFrom-Json
  if ($manifest.status -ne "complete" -or $manifest.warmups -ne $Warmups -or $manifest.repetitions -ne $Repetitions) {
    throw "invalid timed manifest: $manifestPath"
  }
  $rows = Import-Csv $measurementsPath | Where-Object { $_.phase -eq "measured" -and $_.success -eq "true" }
  $expectedVariantsPerBatch = 11
  $expectedRows = $BatchSizes.Count * $expectedVariantsPerBatch * $Repetitions
  if ($rows.Count -ne $expectedRows) {
    throw "host $HostDir has $($rows.Count) successful measured rows; expected $expectedRows"
  }
  $groups = $rows | Group-Object variant, batch_size
  if ($groups.Count -ne ($BatchSizes.Count * $expectedVariantsPerBatch) -or ($groups | Where-Object Count -ne $Repetitions)) {
    throw "host $HostDir does not have exactly $Repetitions runs for every variant/batch pair"
  }
	$paperOutput = Get-Content -Raw $paperPath
	foreach ($batch in $BatchSizes) {
		if ($paperOutput -notmatch "BenchmarkBatchCombine$batch/") {
			throw "inherited paper benchmark output is missing batch $batch in $paperPath"
		}
	}
	if ($paperOutput -notmatch "PASS") { throw "inherited paper benchmark did not pass: $paperPath" }
	foreach ($profileManifest in @((Join-Path $HostDir "profiles\paper\manifest.json"), (Join-Path $HostDir "profiles\bloc\manifest.json"))) {
		if ((Get-Content -Raw $profileManifest | ConvertFrom-Json).status -ne "complete") {
			throw "invalid profile manifest: $profileManifest"
		}
	}
}

foreach ($command in @("aws", "docker", "go", "git", "scp", "ssh", "terraform")) {
  Require-Command $command
}
if ($AvailabilityZones.Count -ne 2) { throw "exactly two availability zones are required" }
if ($Warmups -lt 0 -or $Repetitions -lt 1 -or $PaperBenchmarkCount -lt 1) { throw "invalid repetition counts" }
if ((($BatchSizes | Sort-Object) -join ",") -ne "8,32,128") { throw "this evidence campaign requires batch sizes 8,32,128" }
if (-not $AllowDirtyTree -and ($Warmups -ne 5 -or $Repetitions -ne 30 -or $TxSize -ne 256)) {
  throw "an evidence campaign requires 5 warmups, 30 repetitions, and 256-byte transactions"
}
if ($ExperimentId -notmatch '^bloc-ec2-') { throw "ExperimentId must start with bloc-ec2-" }
foreach ($cidr in $AdminCidrs) {
  if ($cidr -eq "0.0.0.0/0" -or $cidr -eq "::/0") { throw "world-open SSH is not allowed" }
}

New-Item -ItemType Directory -Force $artifactRoot | Out-Null
if ((Get-ChildItem -Force $artifactRoot | Measure-Object).Count -gt 0) {
  throw "artifact directory is not empty: $artifactRoot"
}
New-Item -ItemType Directory -Force $terraformWork | Out-Null
New-Item -ItemType Directory -Force (Join-Path $artifactRoot "hosts") | Out-Null

Push-Location $repoRoot
try {
  $gitCommit = (& git rev-parse HEAD).Trim()
  if ($LASTEXITCODE -ne 0) { throw "unable to resolve git commit" }
  $relevantStatus = & git status --porcelain -- bte/btd-impl-main deploy/ec2/bte-attribution.Dockerfile deploy/ec2/bte-attribution deploy/ec2/run-bte-attribution.ps1
  if ($relevantStatus -and -not $AllowDirtyTree) {
    throw "campaign-relevant paths are dirty; commit them or pass -AllowDirtyTree for a non-evidence smoke run"
  }
  if ($AllowDirtyTree) { $gitCommit = "$gitCommit-dirty" }
	$imageTag = $gitCommit -replace "[^a-zA-Z0-9_.-]", "-"
	$localImage = "bloc-bte-attribution:$imageTag"
	Record-Command "docker version"
	Invoke-Checked { docker version } "Docker preflight"
	Record-Command "docker build -f deploy/ec2/bte-attribution.Dockerfile -t $localImage ."
	Invoke-Checked { docker build -f deploy/ec2/bte-attribution.Dockerfile -t $localImage . } "benchmark image build"
	Record-Command "aws sts get-caller-identity --profile $AwsProfile"
	Invoke-Checked { aws sts get-caller-identity --profile $AwsProfile --output json } "AWS identity preflight"

  Copy-Item -Path @("$terraformSource\main.tf", "$terraformSource\outputs.tf", "$terraformSource\variables.tf", "$terraformSource\user-data.sh") -Destination $terraformWork
  if (Test-Path "$terraformSource\.terraform.lock.hcl") { Copy-Item "$terraformSource\.terraform.lock.hcl" $terraformWork }

  $hosts = @(
    [ordered]@{ label = "t3-small-a"; instance_type = "t3.small"; zone_index = 0 },
    [ordered]@{ label = "t3-small-b"; instance_type = "t3.small"; zone_index = 1 },
    [ordered]@{ label = "c7a-large-a"; instance_type = "c7a.large"; zone_index = 0 },
    [ordered]@{ label = "c7a-large-b"; instance_type = "c7a.large"; zone_index = 1 }
  )
  [ordered]@{
    aws_region = $AwsRegion
    availability_zones = $AvailabilityZones
    name_prefix = $ExperimentId
    ecr_repository_name = $ecrName
    key_name = $keyName
    admin_cidrs = $AdminCidrs
    hosts = $hosts
  } | ConvertTo-Json -Depth 10 | Set-Content -Encoding ascii $tfvarsPath

  $env:AWS_PROFILE = $AwsProfile
  Record-Command "aws ec2 create-key-pair --key-name $keyName"
  $keyMaterial = & aws ec2 create-key-pair --profile $AwsProfile --region $AwsRegion --key-name $keyName --key-type rsa --query KeyMaterial --output text
  if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($keyMaterial)) { throw "failed to create EC2 key pair" }
	$keyCreated = $true
  $keyMaterial | Set-Content -Encoding ascii -NoNewline $keyPath
  & icacls $keyPath /inheritance:r /grant:r "$($env:USERNAME):(R)" | Out-Null

  Push-Location $terraformWork
  try {
    Record-Command "terraform init -input=false"
    Invoke-Checked { terraform init -input=false } "terraform init"
    Record-Command "terraform fmt -check -diff"
    Invoke-Checked { terraform fmt -check -diff } "terraform fmt check"
    Record-Command "terraform validate"
    Invoke-Checked { terraform validate } "terraform validate"
    Record-Command "terraform plan -var-file=$tfvarsPath -out=$planPath -input=false"
    Invoke-Checked { terraform plan "-var-file=$tfvarsPath" "-out=$planPath" -input=false } "terraform plan"
    terraform show -no-color $planPath | Set-Content -Encoding utf8 (Join-Path $artifactRoot "terraform-plan.txt")
    if (-not $AutoApprovePlan) {
      Write-Host "Terraform plan saved to $artifactRoot\terraform-plan.txt"
      if ((Read-Host "Type APPLY to create four benchmark instances") -ne "APPLY") { throw "operator declined terraform apply" }
    }
    Record-Command "terraform apply -input=false $planPath"
	$terraformStarted = $true
    Invoke-Checked { terraform apply -input=false $planPath } "terraform apply"
    $terraformApplied = $true
    terraform output -json inventory | Set-Content -Encoding ascii (Join-Path $artifactRoot "inventory.json")
    $ecrUrl = (& terraform output -raw ecr_repository_url).Trim()
    terraform state pull | Set-Content -Encoding utf8 (Join-Path $artifactRoot "terraform-state-after-apply.json")
  } finally {
    Pop-Location
  }

  $imageUri = "$ecrUrl`:$imageTag"
  $registry = ($ecrUrl -split "/")[0]
	Record-Command "docker tag $localImage $imageUri"
	Invoke-Checked { docker tag $localImage $imageUri } "benchmark image tag"
  Record-Command "aws ecr get-login-password | docker login $registry"
  $password = & aws ecr get-login-password --profile $AwsProfile --region $AwsRegion
  $password | docker login --username AWS --password-stdin $registry
  if ($LASTEXITCODE -ne 0) { throw "ECR login failed" }
  Record-Command "docker push $imageUri"
  Invoke-Checked { docker push $imageUri } "benchmark image push"

  $inventory = Get-Content -Raw (Join-Path $artifactRoot "inventory.json") | ConvertFrom-Json
  $campaignStart = (Get-Date).ToUniversalTime()
  foreach ($host in ($inventory.hosts | Sort-Object label)) {
    $hostDir = Join-Path $artifactRoot "hosts\$($host.label)"
    New-Item -ItemType Directory -Force $hostDir | Out-Null
    Wait-SSH $host.public_ip
    Invoke-SSH $host.public_ip "set -e; lscpu; echo; uname -a; echo; docker version" | Set-Content -Encoding utf8 (Join-Path $hostDir "host-facts.txt")
    $remoteRoot = "/opt/bloc/results/$ExperimentId/$($host.label)"
    $batchCsv = $BatchSizes -join ","
    $paperRegex = '^BenchmarkBatchCombine(8|32|128)/B=(8|32|128),_alpha=2[.]000000[*]sqrt[(]B[)]$'
    $remote = "set -euo pipefail; aws ecr get-login-password --region '$AwsRegion' | docker login --username AWS --password-stdin '$registry'; docker pull '$imageUri'; sudo mkdir -p '$remoteRoot/timed' '$remoteRoot/profiles/paper' '$remoteRoot/profiles/bloc'; sudo chown -R 10001:10001 '$remoteRoot'; docker run --rm --entrypoint /usr/local/bin/paper-bench '$imageUri' '-test.run=^`$' '-test.bench=$paperRegex' '-test.benchtime=${PaperBenchmarkCount}x' '-test.count=3' | sudo tee '$remoteRoot/paper-benchmark.txt' >/dev/null; docker run --rm -v '$remoteRoot/timed:/work' '$imageUri' run --batch-sizes '$batchCsv' --warmups '$Warmups' --repetitions '$Repetitions' --tx-size '$TxSize' --out-dir /work --host-label '$($host.label)' --instance-type '$($host.instance_type)' --zone '$($host.zone)' --git-commit '$gitCommit' --image-tag '$imageUri'; docker run --rm -v '$remoteRoot/profiles/paper:/work' '$imageUri' run --batch-sizes 128 --warmups 2 --repetitions 10 --variants paper-opt2-sequential-t2 --cpu-profile /work/cpu.pprof --out-dir /work --host-label '$($host.label)-profile-paper' --instance-type '$($host.instance_type)' --zone '$($host.zone)' --git-commit '$gitCommit' --image-tag '$imageUri'; docker run --rm -v '$remoteRoot/profiles/bloc:/work' '$imageUri' run --batch-sizes 128 --warmups 2 --repetitions 10 --variants bloc-hybrid-n7-t5-verified --cpu-profile /work/cpu.pprof --out-dir /work --host-label '$($host.label)-profile-bloc' --instance-type '$($host.instance_type)' --zone '$($host.zone)' --git-commit '$gitCommit' --image-tag '$imageUri'"
    Invoke-SSH $host.public_ip $remote | Set-Content -Encoding utf8 (Join-Path $hostDir "remote-run.log")
    Record-Command "scp -r ubuntu@$($host.public_ip):$remoteRoot/. $hostDir"
    & scp -i $keyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -r "ubuntu@$($host.public_ip):$remoteRoot/." $hostDir
    if ($LASTEXITCODE -ne 0) { throw "artifact collection failed for $($host.label)" }
    Assert-HostArtifacts $hostDir
  }
  $campaignEnd = (Get-Date).ToUniversalTime()

  foreach ($host in $inventory.hosts | Where-Object instance_type -like "t3.*") {
    Save-CreditMetrics $host $campaignStart $campaignEnd (Join-Path $artifactRoot "hosts\$($host.label)\cloudwatch")
  }

  $reportArgs = @("run", "./cmd/bte-attribution", "report", "--campaign-dir", (Join-Path $artifactRoot "hosts"), "--out", (Join-Path $artifactRoot "RUN_REPORT.md"))
  if (Test-Path $SameAZData) { $reportArgs += @("--same-az-data", (Resolve-Path $SameAZData).Path) }
  if (Test-Path $CrossAZData) { $reportArgs += @("--cross-az-data", (Resolve-Path $CrossAZData).Path) }
  Push-Location (Join-Path $repoRoot "bte\btd-impl-main")
  try {
    $env:GOCACHE = Join-Path (Get-Location) ".gocache"
    Record-Command "go $($reportArgs -join ' ')"
    & go @reportArgs
    if ($LASTEXITCODE -ne 0) { throw "report generation failed" }
  } finally {
    Pop-Location
  }

  [ordered]@{
    schema_version = "bloc-bte-attribution-campaign/v1"
    status = "complete"
    experiment_id = $ExperimentId
    git_commit = $gitCommit
    image = $imageUri
    started_at = $campaignStart.ToString("o")
    finished_at = $campaignEnd.ToString("o")
    batch_sizes = $BatchSizes
    warmups = $Warmups
    repetitions = $Repetitions
    hosts = $inventory.hosts
  } | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $artifactRoot "campaign-manifest.json")
  $campaignComplete = $true
  Write-Host "Attribution campaign complete: $artifactRoot"
} catch {
  [ordered]@{
    schema_version = "bloc-bte-attribution-campaign/v1"
    status = "invalid"
    experiment_id = $ExperimentId
    finished_at = (Get-Date).ToUniversalTime().ToString("o")
    invalid_reason = $_.Exception.Message
  } | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $artifactRoot "campaign-manifest.json")
  throw
} finally {
  Pop-Location
	$cleanupError = $null
	$shouldDestroy = ($terraformApplied -or $terraformStarted) -and ($campaignComplete -or -not $KeepResourcesOnFailure)
	try {
		if ($shouldDestroy) {
			Push-Location $terraformWork
			try {
				Record-Command "terraform destroy -var-file=$tfvarsPath -auto-approve"
				Invoke-Checked { terraform destroy "-var-file=$tfvarsPath" -auto-approve } "terraform destroy"
				$remainingState = @(terraform state list)
				$remainingState | Set-Content -Encoding utf8 (Join-Path $artifactRoot "terraform-state-after-destroy.txt")
				if ($remainingState.Count -gt 0) { throw "Terraform state is not empty after destroy" }
			} finally {
				Pop-Location
			}
		} elseif ($terraformApplied -or $terraformStarted) {
			Write-Warning "Resources retained after failure because -KeepResourcesOnFailure was set."
		}
		if ($keyCreated -and (-not ($terraformApplied -or $terraformStarted) -or $shouldDestroy)) {
			Record-Command "aws ec2 delete-key-pair --key-name $keyName"
			& aws ec2 delete-key-pair --profile $AwsProfile --region $AwsRegion --key-name $keyName
			if ($LASTEXITCODE -ne 0) { throw "failed to delete temporary EC2 key pair $keyName" }
		}
		if ($shouldDestroy) {
			[ordered]@{ status = "complete"; terraform_state_entries = @(); key_pair_deleted = $keyCreated } |
				ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 (Join-Path $artifactRoot "cleanup-verification.json")
		}
	} catch {
		$cleanupError = $_.Exception.Message
		[ordered]@{ status = "invalid"; error = $cleanupError } |
			ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 (Join-Path $artifactRoot "cleanup-verification.json")
		if (Test-Path (Join-Path $artifactRoot "campaign-manifest.json")) {
			$failedManifest = Get-Content -Raw (Join-Path $artifactRoot "campaign-manifest.json") | ConvertFrom-Json
			$failedManifest.status = "invalid"
			$failedManifest | Add-Member -NotePropertyName cleanup_error -NotePropertyValue $cleanupError -Force
			$failedManifest | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $artifactRoot "campaign-manifest.json")
		}
	} finally {
		$env:AWS_PROFILE = $previousProfile
	}
	if ($cleanupError) { throw $cleanupError }
}
