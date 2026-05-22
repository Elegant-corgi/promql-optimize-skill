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

if (Test-Path -LiteralPath $Target) {
    Remove-Item -LiteralPath $Target -Recurse -Force
}

Copy-Item -LiteralPath $sourcePath -Destination $Target -Recurse

Get-ChildItem -LiteralPath $Target -Directory -Recurse -Force |
    Where-Object { $_.Name -eq ".gocache" } |
    ForEach-Object { Remove-Item -LiteralPath $_.FullName -Recurse -Force }

Write-Host "Synced promql-optimize skill to $Target"
