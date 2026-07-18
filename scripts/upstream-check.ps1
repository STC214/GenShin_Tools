[CmdletBinding()]
param(
    [string]$ApiBase = 'https://api.github.com',
    [switch]$UpdateBaseline,
    [string]$Disposition = ''
)

$ErrorActionPreference = 'Stop'
$ProjectRoot = Split-Path -Parent $PSScriptRoot

Push-Location $ProjectRoot
try {
    $arguments = @('run', './tools/upstream-check', '--root', $ProjectRoot, '--api-base', $ApiBase)
    if ($UpdateBaseline) {
        if ([string]::IsNullOrWhiteSpace($Disposition)) {
            throw '-Disposition is required with -UpdateBaseline'
        }
        $resolvedDisposition = (Resolve-Path -LiteralPath $Disposition).Path
        $arguments += @('--update-baseline', '--disposition', $resolvedDisposition)
    }
    & go @arguments
    if ($LASTEXITCODE -ne 0) {
        throw "upstream check failed with exit code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}
