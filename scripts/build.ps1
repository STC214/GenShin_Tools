[CmdletBinding()]
param(
    [ValidateSet('Debug', 'Release', 'Both')]
    [string]$Configuration = 'Both',
    [string]$Version,
    [string]$BuildTimeUtc,
    [string]$UpdateManifestUrl = $env:GENSHINTOOLS_UPDATE_MANIFEST_URL,
    [string]$UpdatePublicKeysBase64 = $env:GENSHINTOOLS_UPDATE_PUBLIC_KEYS_BASE64
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$VersionFile = Join-Path $ProjectRoot 'VERSION'
$DistDir = Join-Path $ProjectRoot 'dist'
$BuildDir = Join-Path $ProjectRoot 'build'
$GoCache = Join-Path $ProjectRoot '.cache\go-build'
$GoTemp = Join-Path $ProjectRoot '.tmp\go'
$IconPath = Join-Path $ProjectRoot 'assets\app.ico'
$ManifestPath = Join-Path $ProjectRoot 'assets\app.manifest'
$ResourcePath = Join-Path $ProjectRoot 'cmd\genshin-tools\app.syso'
$HelperResourcePath = Join-Path $ProjectRoot 'cmd\injection-helper\app.syso'
$UpdaterResourcePath = Join-Path $ProjectRoot 'cmd\updater\app.syso'
$GeneratedRC = Join-Path $BuildDir 'app.generated.rc'
$HelperGeneratedRC = Join-Path $BuildDir 'injector.generated.rc'
$UpdaterGeneratedRC = Join-Path $BuildDir 'updater.generated.rc'

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

if (-not $Version) {
    $Version = (Get-Content -LiteralPath $VersionFile -Raw -Encoding UTF8).Trim()
}
if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)(?:[-+][0-9A-Za-z.-]+)?$') {
    throw "VERSION must be SemVer compatible (major.minor.patch with optional suffix): $Version"
}
$VersionMajor = [int]$Matches[1]
$VersionMinor = [int]$Matches[2]
$VersionPatch = [int]$Matches[3]
$NumericVersion = "$VersionMajor,$VersionMinor,$VersionPatch,0"
$FileVersion = "$VersionMajor.$VersionMinor.$VersionPatch.0"

if (-not $BuildTimeUtc) {
    if ($env:SOURCE_DATE_EPOCH) {
        $BuildTimeUtc = [DateTimeOffset]::FromUnixTimeSeconds([long]$env:SOURCE_DATE_EPOCH).UtcDateTime.ToString('yyyy-MM-ddTHH:mm:ssZ')
    } else {
        $BuildTimeUtc = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    }
}

if ([bool]$UpdateManifestUrl -ne [bool]$UpdatePublicKeysBase64) {
    throw 'UpdateManifestUrl and UpdatePublicKeysBase64 must be configured together'
}
if ($UpdateManifestUrl) {
    $ParsedUpdateUrl = [Uri]$UpdateManifestUrl
    if (-not $ParsedUpdateUrl.IsAbsoluteUri -or $ParsedUpdateUrl.Scheme -ne 'https' -or $ParsedUpdateUrl.UserInfo -or $ParsedUpdateUrl.Fragment) {
        throw 'UpdateManifestUrl must be an absolute HTTPS URL without credentials or fragment'
    }
    try {
        $DecodedKeys = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($UpdatePublicKeysBase64)) | ConvertFrom-Json
    } catch {
        throw 'UpdatePublicKeysBase64 must encode valid trusted-key JSON'
    }
    if ($null -eq $DecodedKeys -or $DecodedKeys -isnot [PSCustomObject]) {
        throw 'UpdatePublicKeysBase64 must encode a JSON object'
    }
    $KeyProperties = @($DecodedKeys.PSObject.Properties)
    if ($KeyProperties.Count -lt 1 -or $KeyProperties.Count -gt 16) {
        throw 'UpdatePublicKeysBase64 must contain 1..16 trusted keys'
    }
    foreach ($KeyProperty in $KeyProperties) {
        if ($KeyProperty.Name -notmatch '^[a-z0-9][a-z0-9._-]{0,63}$' -or $KeyProperty.Value -isnot [string]) {
            throw 'UpdatePublicKeysBase64 contains an invalid key ID or value'
        }
        try {
            if ([Convert]::FromBase64String($KeyProperty.Value).Length -ne 32) {
                throw 'wrong key length'
            }
        } catch {
            throw 'UpdatePublicKeysBase64 contains a key that is not a 32-byte base64 Ed25519 public key'
        }
    }
}

$Commit = if ($env:BUILD_COMMIT) {
    $env:BUILD_COMMIT
} elseif (Test-Path (Join-Path $ProjectRoot '.git')) {
    $status = @(& git -C $ProjectRoot status --porcelain=v1 --branch)
    if ($LASTEXITCODE -eq 0 -and $status.Count -gt 0 -and $status[0] -notmatch 'No commits yet') {
        $value = (& git -C $ProjectRoot rev-parse --short=12 HEAD)
        if ($LASTEXITCODE -eq 0) {
            $identity = $value.Trim()
            if ($status.Count -gt 1) { $identity += '-dirty' }
            $identity
        } else { 'uncommitted' }
    } else {
        'uncommitted'
    }
} else {
    'unknown'
}

foreach ($directory in @($DistDir, $BuildDir, $GoCache, $GoTemp)) {
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
}
$env:GOCACHE = $GoCache
$env:GOTMPDIR = $GoTemp
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'

Push-Location $ProjectRoot
try {
    Invoke-Checked -Command 'go' -Arguments @('run', './tools/icon', '-output', $IconPath)

    $IconRCPath = (Resolve-Path -LiteralPath $IconPath).Path.Replace('\', '/')
    $ManifestRCPath = (Resolve-Path -LiteralPath $ManifestPath).Path.Replace('\', '/')
    $ResourceText = @"
#include <windows.h>

1 ICON "$IconRCPath"
1 RT_MANIFEST "$ManifestRCPath"

1 VERSIONINFO
FILEVERSION $NumericVersion
PRODUCTVERSION $NumericVersion
FILEFLAGSMASK 0x3fL
FILEFLAGS 0x0L
FILEOS 0x40004L
FILETYPE 0x1L
FILESUBTYPE 0x0L
BEGIN
    BLOCK "StringFileInfo"
    BEGIN
        BLOCK "040904B0"
        BEGIN
            VALUE "CompanyName", "Genshin Tools Project\0"
            VALUE "FileDescription", "Genshin Tools\0"
            VALUE "FileVersion", "$FileVersion\0"
            VALUE "InternalName", "GenshinTools\0"
            VALUE "LegalCopyright", "Copyright (c) 2026 Genshin Tools Project\0"
            VALUE "OriginalFilename", "GenshinTools.exe\0"
            VALUE "ProductName", "Genshin Tools\0"
            VALUE "ProductVersion", "$Version\0"
        END
    END
    BLOCK "VarFileInfo"
    BEGIN
        VALUE "Translation", 0x0409, 1200
    END
END
"@
    [IO.File]::WriteAllText($GeneratedRC, $ResourceText, [Text.UTF8Encoding]::new($false))
    $HelperResourceText = $ResourceText.Replace('VALUE "FileDescription", "Genshin Tools\0"', 'VALUE "FileDescription", "Genshin Tools Injection Helper\0"').Replace('VALUE "InternalName", "GenshinTools\0"', 'VALUE "InternalName", "GenshinTools-injector\0"').Replace('VALUE "OriginalFilename", "GenshinTools.exe\0"', 'VALUE "OriginalFilename", "GenshinTools-injector.exe\0"')
    [IO.File]::WriteAllText($HelperGeneratedRC, $HelperResourceText, [Text.UTF8Encoding]::new($false))
    $UpdaterResourceText = $ResourceText.Replace('VALUE "FileDescription", "Genshin Tools\0"', 'VALUE "FileDescription", "Genshin Tools Update Helper\0"').Replace('VALUE "InternalName", "GenshinTools\0"', 'VALUE "InternalName", "GenshinTools-updater\0"').Replace('VALUE "OriginalFilename", "GenshinTools.exe\0"', 'VALUE "OriginalFilename", "GenshinTools-updater.exe\0"')
    [IO.File]::WriteAllText($UpdaterGeneratedRC, $UpdaterResourceText, [Text.UTF8Encoding]::new($false))

    $Windres = (Get-Command windres -ErrorAction Stop).Source
    Invoke-Checked -Command $Windres -Arguments @('--input', $GeneratedRC, '--output', $ResourcePath, '--output-format', 'coff')
    Invoke-Checked -Command $Windres -Arguments @('--input', $HelperGeneratedRC, '--output', $HelperResourcePath, '--output-format', 'coff')
    Invoke-Checked -Command $Windres -Arguments @('--input', $UpdaterGeneratedRC, '--output', $UpdaterResourcePath, '--output-format', 'coff')

    $Configurations = if ($Configuration -eq 'Both') { @('Debug', 'Release') } else { @($Configuration) }
    $BuiltFiles = @()
    foreach ($CurrentConfiguration in $Configurations) {
        $ConfigValue = $CurrentConfiguration.ToLowerInvariant()
        $LdFlags = @(
            "-X genshintools/internal/buildinfo.Version=$Version"
            "-X genshintools/internal/buildinfo.Commit=$Commit"
            "-X genshintools/internal/buildinfo.BuildTimeUTC=$BuildTimeUtc"
            "-X genshintools/internal/buildinfo.Configuration=$ConfigValue"
        )
        if ($UpdateManifestUrl) {
            $LdFlags += "-X genshintools/internal/selfupdate.updateManifestURL=$UpdateManifestUrl"
            $LdFlags += "-X genshintools/internal/selfupdate.trustedPublicKeysBase64=$UpdatePublicKeysBase64"
        }
        $Output = if ($CurrentConfiguration -eq 'Release') {
            $LdFlags += @('-H=windowsgui', '-s', '-w')
            Join-Path $DistDir 'GenshinTools.exe'
        } else {
            Join-Path $DistDir 'GenshinTools-debug.exe'
        }

        Invoke-Checked -Command 'go' -Arguments @('build', '-trimpath', '-buildvcs=false', '-ldflags', ($LdFlags -join ' '), '-o', $Output, './cmd/genshin-tools')
        $BuiltFiles += $Output
    }

    $HelperLdFlags = @(
        "-X genshintools/internal/buildinfo.Version=$Version"
        "-X genshintools/internal/buildinfo.Commit=$Commit"
        "-X genshintools/internal/buildinfo.BuildTimeUTC=$BuildTimeUtc"
        "-X genshintools/internal/buildinfo.Configuration=helper"
        '-s'
        '-w'
    )
    $HelperOutput = Join-Path $DistDir 'GenshinTools-injector.exe'
    Invoke-Checked -Command 'go' -Arguments @('build', '-trimpath', '-buildvcs=false', '-ldflags', ($HelperLdFlags -join ' '), '-o', $HelperOutput, './cmd/injection-helper')
    $BuiltFiles += $HelperOutput

    $UpdaterLdFlags = @(
        "-X genshintools/internal/buildinfo.Version=$Version"
        "-X genshintools/internal/buildinfo.Commit=$Commit"
        "-X genshintools/internal/buildinfo.BuildTimeUTC=$BuildTimeUtc"
        "-X genshintools/internal/buildinfo.Configuration=updater"
        '-s'
        '-w'
    )
    $UpdaterOutput = Join-Path $DistDir 'GenshinTools-updater.exe'
    Invoke-Checked -Command 'go' -Arguments @('build', '-trimpath', '-buildvcs=false', '-ldflags', ($UpdaterLdFlags -join ' '), '-o', $UpdaterOutput, './cmd/updater')
    $BuiltFiles += $UpdaterOutput

    foreach ($directory in @('logs', 'cache', 'staging', 'injection', 'injection\modules', 'plugins', 'plugins\versions', 'plugins\staging', 'updates', 'updates\versions', 'updates\backups', 'updates\downloads', 'updates\runner')) {
        New-Item -ItemType Directory -Force -Path (Join-Path $DistDir "data\$directory") | Out-Null
    }
    Copy-Item -LiteralPath (Join-Path $ProjectRoot 'THIRD_PARTY_NOTICES.md') -Destination $DistDir -Force
    Copy-Item -LiteralPath (Join-Path $ProjectRoot 'LICENSE_POLICY.md') -Destination $DistDir -Force
    Copy-Item -LiteralPath (Join-Path $ProjectRoot 'LICENSES') -Destination $DistDir -Recurse -Force

    $BuildInfo = [ordered]@{
        product       = 'Genshin Tools'
        version       = $Version
        fileVersion   = $FileVersion
        commit        = $Commit
        buildTimeUtc  = $BuildTimeUtc
        goVersion     = (& go version)
        target        = 'windows/amd64'
        configurations = $Configurations
    }
    $BuildInfo | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $DistDir 'build-info.json') -Encoding UTF8

    Write-Host "Built Genshin Tools $Version ($Commit)"
    $BuiltFiles | ForEach-Object { Write-Host "  $_" }
} finally {
    Pop-Location
}
