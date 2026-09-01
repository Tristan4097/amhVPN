param(
    [switch]$DryRun,
    [int]$Limit = 500,
    [int]$Concurrency = 24,
    [int]$MinimumHealthy = 5
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$desktop = Join-Path $root 'desktop'
$sources = Join-Path $PSScriptRoot 'sources.txt'
$healthy = Join-Path $root 'worker\data\healthy.txt'
$status = Join-Path $root 'worker\data\status.json'

Push-Location $desktop
try {
    $env:GOSUMDB = 'sum.golang.org'
    $args = @('run', './cmd/sub-harvester', '-sources', $sources, '-output', $healthy, '-status', $status, '-limit', $Limit, '-concurrency', $Concurrency, '-minimum-healthy', $MinimumHealthy)
    if ($DryRun) { $args += '-dry-run' }
    & go @args
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
} finally {
    Pop-Location
}

if ($DryRun) { exit 0 }
$result = Get-Content -Raw -LiteralPath $status | ConvertFrom-Json
if ($result.healthy -lt $MinimumHealthy) {
    Write-Host "Healthy nodes below publish threshold; healthy.txt and Git were left untouched."
    exit 0
}

Push-Location $root
try {
    git diff --quiet -- worker/data/healthy.txt worker/data/status.json
    if ($LASTEXITCODE -eq 0) {
        Write-Host 'Healthy list has not changed; no commit created.'
        exit 0
    }
    git add worker/data/healthy.txt worker/data/status.json
    git commit -m 'Refresh healthy amhVPN subscription'
    git push
} finally {
    Pop-Location
}
