[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$env:GOCACHE = Join-Path $ProjectRoot '.cache\go-build'
$env:GOTMPDIR = Join-Path $ProjectRoot '.tmp\go'
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOTMPDIR | Out-Null

Push-Location $ProjectRoot
try {
    & go test -count=1 ./internal/injection ./internal/launch ./internal/config ./internal/paths
    if ($LASTEXITCODE -ne 0) {
        throw "S09 injection tests failed with code $LASTEXITCODE"
    }
    Write-Host '[S09] PASS manifest/PE audit, helper protocol, owned injection fixture, launch integration and migration'
} finally {
    Pop-Location
}
