[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string]$Package,
    [Parameter(Mandatory)] [string]$Manifest,
    [Parameter(Mandatory)] [string]$PublicKey
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
foreach ($PathValue in @($Package, $Manifest, $PublicKey)) {
    if (-not (Test-Path -LiteralPath $PathValue -PathType Leaf)) {
        throw "Required audit input does not exist: $PathValue"
    }
}

Push-Location $ProjectRoot
try {
    & go run ./tools/release-audit --package $Package --manifest $Manifest --public-key $PublicKey
    if ($LASTEXITCODE -ne 0) {
        throw "release-audit exited with code $LASTEXITCODE"
    }
} finally {
    Pop-Location
}
