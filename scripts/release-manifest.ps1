[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string]$Package,
    [Parameter(Mandatory)] [string]$Output,
    [Parameter(Mandatory)] [string]$Version,
    [Parameter(Mandatory)] [string]$MinimumVersion,
    [Parameter(Mandatory)] [string]$Url,
    [Parameter(Mandatory)] [string]$KeyId,
    [Parameter(Mandatory)] [string]$PrivateKey,
    [string]$PublishedUtc
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
foreach ($PathValue in @($Package, $PrivateKey)) {
    if (-not (Test-Path -LiteralPath $PathValue -PathType Leaf)) {
        throw "Required input file does not exist: $PathValue"
    }
}

$ToolArguments = @(
    '--package', $Package,
    '--output', $Output,
    '--version', $Version,
    '--minimum-version', $MinimumVersion,
    '--url', $Url,
    '--key-id', $KeyId,
    '--private-key', $PrivateKey
)
if ($PublishedUtc) {
    $ToolArguments += @('--published-utc', $PublishedUtc)
}

Push-Location $ProjectRoot
try {
    & go run ./tools/release-manifest @ToolArguments
    if ($LASTEXITCODE -ne 0) {
        throw "release-manifest exited with code $LASTEXITCODE"
    }
} finally {
    Pop-Location
}
