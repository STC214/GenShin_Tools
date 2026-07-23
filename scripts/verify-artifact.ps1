[CmdletBinding()]
param(
    [string]$Version,
    [string]$DistDirectory
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
if (-not $Version) {
    $Version = (Get-Content -LiteralPath (Join-Path $ProjectRoot 'VERSION') -Raw -Encoding UTF8).Trim()
}
if (-not $DistDirectory) {
    $DistDirectory = Join-Path $ProjectRoot 'dist'
}
if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)(?:[-+][0-9A-Za-z.-]+)?$') {
    throw "Version must be SemVer compatible: $Version"
}
$ExpectedFileVersion = "$($Matches[1]).$($Matches[2]).$($Matches[3]).0"

function Get-PESubsystem {
    param([Parameter(Mandatory)] [string]$Path)
    $Bytes = [IO.File]::ReadAllBytes($Path)
    if ($Bytes.Length -lt 256 -or $Bytes[0] -ne 0x4D -or $Bytes[1] -ne 0x5A) {
        throw "$Path is not a valid PE file"
    }
    $PEOffset = [BitConverter]::ToInt32($Bytes, 0x3c)
    return [BitConverter]::ToUInt16($Bytes, $PEOffset + 24 + 68)
}

$Expected = @(
    @{ Path = (Join-Path $DistDirectory 'GenshinTools-debug.exe'); Subsystem = 3; Name = 'console' }
    @{ Path = (Join-Path $DistDirectory 'GenshinTools.exe'); Subsystem = 2; Name = 'windows-gui' }
    @{ Path = (Join-Path $DistDirectory 'GenshinTools-injector.exe'); Subsystem = 2; Name = 'windows-gui injection-helper' }
    @{ Path = (Join-Path $DistDirectory 'GenshinTools-updater.exe'); Subsystem = 2; Name = 'windows-gui update-helper' }
)

Add-Type -AssemblyName System.Drawing
foreach ($Item in $Expected) {
    if (-not (Test-Path -LiteralPath $Item.Path)) {
        throw "Missing artifact: $($Item.Path)"
    }
    $Info = [Diagnostics.FileVersionInfo]::GetVersionInfo($Item.Path)
    if ($Info.ProductVersion -ne $Version) {
        throw "$($Item.Path) ProductVersion is '$($Info.ProductVersion)', expected '$Version'"
    }
    if ($Info.FileVersion -ne $ExpectedFileVersion) {
        throw "$($Item.Path) FileVersion is '$($Info.FileVersion)', expected '$ExpectedFileVersion'"
    }
    $Subsystem = Get-PESubsystem -Path $Item.Path
    if ($Subsystem -ne $Item.Subsystem) {
        throw "$($Item.Path) subsystem is $Subsystem, expected $($Item.Subsystem) ($($Item.Name))"
    }
    $Icon = [Drawing.Icon]::ExtractAssociatedIcon($Item.Path)
    if ($null -eq $Icon -or $Icon.Width -le 0 -or $Icon.Height -le 0) {
        throw "$($Item.Path) has no extractable application icon"
    }
    $Icon.Dispose()
    Write-Host "Verified $([IO.Path]::GetFileName($Item.Path)): FileVersion=$($Info.FileVersion), ProductVersion=$($Info.ProductVersion), Subsystem=$Subsystem, Icon=ok"
}

$HelperPath = Join-Path $DistDirectory 'GenshinTools-injector.exe'
$ProbeID = "verify-helper-$PID"
$ProbeDirectory = Join-Path $DistDirectory "data\staging\injection\$ProbeID"
$ProbeRequest = Join-Path $ProbeDirectory 'request.json'
$ProbeResult = Join-Path $ProbeDirectory 'result.json'
$ProbeStdout = Join-Path $ProbeDirectory 'stdout.txt'
$ProbeStderr = Join-Path $ProbeDirectory 'stderr.txt'
try {
    New-Item -ItemType Directory -Force -Path $ProbeDirectory | Out-Null
    [IO.File]::WriteAllText($ProbeRequest, '{', [Text.UTF8Encoding]::new($false))
    $QuotedProbeRequest = '"' + $ProbeRequest + '"'
    $HelperProcess = Start-Process -FilePath $HelperPath -ArgumentList @('--request', $QuotedProbeRequest) -WorkingDirectory $DistDirectory -WindowStyle Hidden -RedirectStandardOutput $ProbeStdout -RedirectStandardError $ProbeStderr -Wait -PassThru
    if ($HelperProcess.ExitCode -ne 2) {
        throw "Injection helper invalid-request probe exited with $($HelperProcess.ExitCode), expected 2"
    }
    if (-not (Test-Path -LiteralPath $ProbeResult -PathType Leaf)) {
        throw 'Injection helper did not write result.json'
    }
    $Result = Get-Content -LiteralPath $ProbeResult -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($Result.protocolVersion -ne 2 -or $Result.requestId -ne 'invalid' -or $Result.success -ne $false -or $Result.code -ne 'invalid_request' -or -not $Result.error) {
        throw 'Injection helper returned an unexpected invalid-request result'
    }
    Write-Host 'Verified actual injection helper process request parsing and result write-back'
} finally {
    Remove-Item -LiteralPath $ProbeDirectory -Recurse -Force -ErrorAction SilentlyContinue
}

$RequiredDirectories = @('data', 'data\logs', 'data\cache', 'data\staging', 'data\injection', 'data\injection\modules', 'data\plugins', 'data\plugins\versions', 'data\plugins\staging', 'data\updates', 'data\updates\versions', 'data\updates\backups', 'data\updates\downloads', 'data\updates\runner')
foreach ($Relative in $RequiredDirectories) {
    $Path = Join-Path $DistDirectory $Relative
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) {
        throw "Missing portable directory: $Path"
    }
}

$BuildInfoPath = Join-Path $DistDirectory 'build-info.json'
$BuildInfo = Get-Content -LiteralPath $BuildInfoPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ($BuildInfo.version -ne $Version -or $BuildInfo.target -ne 'windows/amd64') {
    throw "Unexpected build-info.json contents"
}
Write-Host "Verified portable data layout and build-info.json"
