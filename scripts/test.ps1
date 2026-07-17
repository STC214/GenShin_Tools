[CmdletBinding()]
param(
    [switch]$Race
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$env:GOCACHE = Join-Path $ProjectRoot '.cache\go-build'
$env:GOTMPDIR = Join-Path $ProjectRoot '.tmp\go'
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOTMPDIR | Out-Null

function Invoke-Checked {
    param(
        [Parameter(Mandatory)] [string]$Command,
        [Parameter(ValueFromRemainingArguments)] [string[]]$Arguments
    )
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command exited with code $LASTEXITCODE"
    }
}

Push-Location $ProjectRoot
try {
    $Unformatted = @(gofmt -l .)
    if ($Unformatted.Count -ne 0) {
        throw "gofmt is required for: $($Unformatted -join ', ')"
    }
    Invoke-Checked go mod verify
    Invoke-Checked go test -count=1 ./...
    if ($Race) {
        Invoke-Checked go test -race -count=1 ./...
    }
    # Win32 callbacks legitimately reconstruct SDK-owned structures from LPARAM.
    # Keep every other vet analyzer enabled and isolate unsafe use in the shell boundary.
    Invoke-Checked go vet -unsafeptr=false ./...
    if (Test-Path (Join-Path $ProjectRoot '.git')) {
        Invoke-Checked git diff --check
    }
} finally {
    Pop-Location
}
