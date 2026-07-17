[CmdletBinding()]
param(
    [string]$Assets = 'game,zh-cn'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$env:GOCACHE = Join-Path $ProjectRoot '.cache\go-build'
$env:GOTMPDIR = Join-Path $ProjectRoot '.tmp\go'
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOTMPDIR | Out-Null

Push-Location $ProjectRoot
try {
    & go run ./tools/sophonaudit -assets $Assets -timeout 90s
    if ($LASTEXITCODE -ne 0) {
        throw "Sophon provider audit exited with code $LASTEXITCODE"
    }
} finally {
    Pop-Location
}
