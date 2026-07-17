[CmdletBinding()]
param(
    [ValidateRange(0, 60)]
    [int]$SoakMinutes = 0
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$GoCache = Join-Path $ProjectRoot '.cache\go-build'
$GoTemp = Join-Path $ProjectRoot '.tmp\go'
New-Item -ItemType Directory -Force -Path $GoCache, $GoTemp | Out-Null
$env:GOCACHE = $GoCache
$env:GOTMPDIR = $GoTemp

function Invoke-GoTest {
    param([Parameter(Mandatory)] [string[]]$GoArguments)
    & go test @GoArguments
    if ($LASTEXITCODE -ne 0) { throw "go test exited with code $LASTEXITCODE" }
}

Push-Location $ProjectRoot
try {
    Write-Host '[S03] Unit, race, and 1000-trigger/200-toggle stress tests'
    Invoke-GoTest -GoArguments @('-race', '-count=10', './internal/input')

    Write-Host '[S03] 200 native hook install/uninstall cycles'
    Invoke-GoTest -GoArguments @('-run', 'TestNativeHooksStartAndClose', '-count=200', './internal/input')

    Write-Host '[S03] Direct SendInput keyboard/left/right capture (events swallowed)'
    $env:GENSHINTOOLS_INPUT_CAPTURE = '1'
    Invoke-GoTest -GoArguments @('-run', 'TestCapturedSendInputPairs', '-v', './internal/input')

    Write-Host '[S03] Full engine captured cadence grid: 30/50/100/250 ms x keyboard/left/right'
    Invoke-GoTest -GoArguments @('-run', 'TestCapturedNativeEngine', '-v', './internal/input')

    if ($SoakMinutes -gt 0) {
        $env:GENSHINTOOLS_INPUT_INTERVAL_MS = '50'
        $env:GENSHINTOOLS_INPUT_SOAK_SECONDS = [string]($SoakMinutes * 60)
        Write-Host "[S03] Captured long soak: $SoakMinutes minute(s) each x keyboard/left/right (events swallowed)"
        Invoke-GoTest -GoArguments @('-run', 'TestCapturedNativeEngine', '-v', '-timeout', "$($SoakMinutes * 4 + 2)m", './internal/input')
    }

    Write-Host '[S03] Whole-project regression'
    Remove-Item Env:\GENSHINTOOLS_INPUT_CAPTURE -ErrorAction SilentlyContinue
    Remove-Item Env:\GENSHINTOOLS_INPUT_INTERVAL_MS -ErrorAction SilentlyContinue
    Remove-Item Env:\GENSHINTOOLS_INPUT_SOAK_SECONDS -ErrorAction SilentlyContinue
    Invoke-GoTest -GoArguments @('./...')
    Write-Host '[S03] PASS'
} finally {
    Remove-Item Env:\GENSHINTOOLS_INPUT_CAPTURE -ErrorAction SilentlyContinue
    Remove-Item Env:\GENSHINTOOLS_INPUT_INTERVAL_MS -ErrorAction SilentlyContinue
    Remove-Item Env:\GENSHINTOOLS_INPUT_SOAK_SECONDS -ErrorAction SilentlyContinue
    Pop-Location
}
