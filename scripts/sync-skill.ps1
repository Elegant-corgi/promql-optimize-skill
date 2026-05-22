param(
    [string]$Source = (Join-Path (Split-Path -Parent $PSScriptRoot) "promql-optimize"),
    [string]$Target = (Join-Path $HOME ".codex\skills\promql-optimize")
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $Source)) {
    throw "Skill source not found: $Source"
}

$sourcePath = (Resolve-Path -LiteralPath $Source).Path
$targetParent = Split-Path -Parent $Target

if (-not (Test-Path -LiteralPath $targetParent)) {
    New-Item -ItemType Directory -Path $targetParent | Out-Null
}

$backupRoot = $null
if (Test-Path -LiteralPath $Target) {
    $targetConfig = Join-Path $Target "config"
    if (Test-Path -LiteralPath $targetConfig) {
        $backupRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("promql-optimize-config-" + [System.Guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Path $backupRoot | Out-Null
        $backupConfig = Join-Path $backupRoot "config"
        New-Item -ItemType Directory -Path $backupConfig | Out-Null
        foreach ($localStateFile in @("promql-profiles.json", "promql-current-profile")) {
            $sourceFile = Join-Path $targetConfig $localStateFile
            if (Test-Path -LiteralPath $sourceFile) {
                Copy-Item -LiteralPath $sourceFile -Destination (Join-Path $backupConfig $localStateFile)
            }
        }
    }
    Remove-Item -LiteralPath $Target -Recurse -Force
}

Copy-Item -LiteralPath $sourcePath -Destination $Target -Recurse

if ($backupRoot) {
    $restoredConfig = Join-Path $backupRoot "config"
    if (Test-Path -LiteralPath $restoredConfig) {
        $targetConfig = Join-Path $Target "config"
        if (-not (Test-Path -LiteralPath $targetConfig)) {
            New-Item -ItemType Directory -Path $targetConfig | Out-Null
        }
        Get-ChildItem -LiteralPath $restoredConfig -Force |
            Copy-Item -Destination $targetConfig -Force
    }
    Remove-Item -LiteralPath $backupRoot -Recurse -Force
}

Get-ChildItem -LiteralPath $Target -Directory -Recurse -Force |
    Where-Object { $_.Name -eq ".gocache" } |
    ForEach-Object { Remove-Item -LiteralPath $_.FullName -Recurse -Force }

Write-Host "Synced promql-optimize skill to $Target"
