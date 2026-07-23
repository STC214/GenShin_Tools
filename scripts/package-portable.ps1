[CmdletBinding()]
param(
    [string]$Version,
    [string]$DistDirectory,
    [string]$Output
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$ProjectRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'environment.ps1')
if (-not $Version) { $Version = (Get-Content -LiteralPath (Join-Path $ProjectRoot 'VERSION') -Raw -Encoding UTF8).Trim() }
if (-not $DistDirectory) { $DistDirectory = Join-Path $ProjectRoot 'dist' }
if (-not $Output) { $Output = Join-Path $ProjectRoot "artifacts\release\GenshinTools-$Version-windows-amd64-portable.zip" }

$EnvironmentNames = @('GOCACHE', 'GOTMPDIR')
$PreviousEnvironment = Save-ProcessEnvironment -Names $EnvironmentNames
$env:GOCACHE = Join-Path $ProjectRoot '.cache\go-build'
$env:GOTMPDIR = Join-Path $ProjectRoot '.tmp\go'
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOTMPDIR | Out-Null

Push-Location $ProjectRoot
try {
    & go run ./tools/release-package --dist $DistDirectory --output $Output --version $Version
    if ($LASTEXITCODE -ne 0) { throw "portable release packaging failed with code $LASTEXITCODE" }
} finally {
    Pop-Location
    Restore-ProcessEnvironment -Snapshot $PreviousEnvironment -Names $EnvironmentNames
}
