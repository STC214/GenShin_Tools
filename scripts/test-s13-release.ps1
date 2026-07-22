[CmdletBinding()]
param(
    [ValidateRange(1, 25)]
    [int]$ShellIterations = 10,
    [switch]$SkipOnlineProvider,
    [string]$BuildTimeUtc
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$ProjectRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'environment.ps1')
$EnvironmentNames = @(
    'GOCACHE', 'GOTMPDIR', 'GOOS', 'GOARCH', 'CGO_ENABLED',
    'GENSHINTOOLS_S02_AUTOCLOSE_MS', 'GENSHINTOOLS_S02_READY_FILE', 'GENSHINTOOLS_S02_ACTIVATED_FILE',
    'GENSHINTOOLS_INPUT_CAPTURE', 'GENSHINTOOLS_INPUT_INTERVAL_MS', 'GENSHINTOOLS_INPUT_SOAK_SECONDS',
    'GENSHINTOOLS_S05_RESULT', 'GENSHINTOOLS_S05_SLEEP_MS', 'GENSHINTOOLS_S05_EXIT_CODE'
)
$PreviousEnvironment = Save-ProcessEnvironment -Names $EnvironmentNames
$VersionFile = Join-Path $ProjectRoot 'VERSION'
if (-not $BuildTimeUtc) {
    if ($env:SOURCE_DATE_EPOCH) {
        $BuildTimeUtc = [DateTimeOffset]::FromUnixTimeSeconds([long]$env:SOURCE_DATE_EPOCH).UtcDateTime.ToString('yyyy-MM-ddTHH:mm:ssZ')
    } elseif (Test-Path (Join-Path $ProjectRoot '.git')) {
        $CommitEpoch = (& git -C $ProjectRoot show -s --format=%ct HEAD).Trim()
        if ($LASTEXITCODE -ne 0 -or $CommitEpoch -notmatch '^\d+$') { throw 'Could not derive deterministic build time from Git HEAD' }
        $BuildTimeUtc = [DateTimeOffset]::FromUnixTimeSeconds([long]$CommitEpoch).UtcDateTime.ToString('yyyy-MM-ddTHH:mm:ssZ')
    } else {
        $BuildTimeUtc = (Get-Item -LiteralPath $VersionFile).LastWriteTimeUtc.ToString('yyyy-MM-ddTHH:mm:ssZ')
    }
}
try {
    [void][DateTimeOffset]::ParseExact($BuildTimeUtc, "yyyy-MM-dd'T'HH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AssumeUniversal)
} catch { throw 'BuildTimeUtc must use yyyy-MM-ddTHH:mm:ssZ' }
$ReportDirectory = Join-Path $ProjectRoot 'artifacts\s13'
$ReportPath = Join-Path $ReportDirectory 'automated-verification.json'
$Results = [Collections.Generic.List[object]]::new()
$StartedUtc = (Get-Date).ToUniversalTime()

function Invoke-S13Step {
    param(
        [Parameter(Mandatory)] [string]$Name,
        [Parameter(Mandatory)] [scriptblock]$Action
    )
    Write-Host "[S13] $Name"
    $Watch = [Diagnostics.Stopwatch]::StartNew()
    try {
        & $Action
        $Watch.Stop()
        $Results.Add([ordered]@{ name = $Name; status = 'passed'; durationMs = $Watch.ElapsedMilliseconds; error = '' })
    } catch {
        $Watch.Stop()
        $Results.Add([ordered]@{ name = $Name; status = 'failed'; durationMs = $Watch.ElapsedMilliseconds; error = $_.Exception.Message })
        throw
    }
}

function Write-S13Report {
    param([string]$Status)
    New-Item -ItemType Directory -Force -Path $ReportDirectory | Out-Null
    $Report = [ordered]@{
        schemaVersion = 1
        stage = 'S13'
        status = $Status
        startedUtc = $StartedUtc.ToString('yyyy-MM-ddTHH:mm:ssZ')
        completedUtc = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
        shellIterations = $ShellIterations
        buildTimeUtc = $BuildTimeUtc
        onlineProviderSkipped = [bool]$SkipOnlineProvider
        results = $Results
        manualGates = @(
            'real game input and anti-cheat compatibility',
            'lock/unlock, sleep/resume and RDP transitions',
            'mixed integrity/UAC cancellation and antivirus quarantine',
            'multi-monitor 125/150/200 percent DPI and display removal',
            'clean Windows 10/11 first-run, update and rollback',
            'project license selection'
        )
    }
    $Temporary = "$ReportPath.tmp"
    $Report | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $Temporary -Encoding UTF8
    Move-Item -LiteralPath $Temporary -Destination $ReportPath -Force
}

function Assert-S13EnvironmentRestored {
    foreach ($Name in $EnvironmentNames) {
        $Current = [Environment]::GetEnvironmentVariable($Name, 'Process')
        if ($Current -ne $PreviousEnvironment[$Name]) {
            throw "child script changed process environment variable $Name"
        }
    }
}

Push-Location $ProjectRoot
try {
    Invoke-S13Step 'full source, race, formatting and vet gate' { & .\scripts\test.ps1 -Race }
    Invoke-S13Step 'clean deterministic Debug/Release/helper/updater build' { & .\scripts\build.ps1 -Configuration Both -BuildTimeUtc $BuildTimeUtc }
    Invoke-S13Step 'PE version, icon, subsystem and portable layout audit' { & .\scripts\verify-artifact.ps1 }
    Invoke-S13Step 'Win32 shell lifecycle and short close stress' { & .\scripts\test-s02-shell.ps1 -StressIterations $ShellIterations }
    Invoke-S13Step 'captured keyboard/left/right SendInput and hook lifecycle' { & .\scripts\test-s03-input.ps1 -SoakMinutes 0 }
    Invoke-S13Step 'real-process pure launch fixture' { & .\scripts\test-s05-launch.ps1 }
    Invoke-S13Step 'bounded injection/helper regression' { & .\scripts\test-s09-injection.ps1 }
    if (-not $SkipOnlineProvider) {
        Invoke-S13Step 'read-only Sophon provider schema audit' { & .\scripts\test-s06-provider.ps1 }
    }
    Invoke-S13Step 'deterministic candidate ZIP, reopen and SHA-256' { & .\scripts\package-candidate.ps1 }
    Invoke-S13Step 'child script process-environment isolation' { Assert-S13EnvironmentRestored }
    Write-S13Report -Status 'automated-passed'
    Write-Host "[S13] AUTOMATED PASS report=$ReportPath"
} catch {
    Write-S13Report -Status 'failed'
    throw
} finally {
    Pop-Location
    Restore-ProcessEnvironment -Snapshot $PreviousEnvironment -Names $EnvironmentNames
}
