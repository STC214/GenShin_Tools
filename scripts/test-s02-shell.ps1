[CmdletBinding()]
param(
    [int]$StressIterations = 25
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$Executable = (Resolve-Path (Join-Path $ProjectRoot 'dist\GenshinTools.exe')).Path
$DataDirectory = Join-Path $ProjectRoot 'dist\data'
$ReadyFile = Join-Path $DataDirectory 's02-ready.tmp'
$ActivatedFile = Join-Path $DataDirectory 's02-activated.tmp'
$ConfigFile = Join-Path $DataDirectory 'config.json'
$MarkerFile = Join-Path $DataDirectory 'session.marker'
$LogFile = Join-Path $DataDirectory 'logs\genshin-tools.log'

if (-not ('S02ShellTestNative' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class S02ShellTestNative {
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, UIntPtr wParam, IntPtr lParam);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
}
'@
}

function Start-Shell {
    param([int]$AutoCloseMilliseconds, [string]$ReadyPath = '', [switch]$Visible)
    $env:GENSHINTOOLS_S02_AUTOCLOSE_MS = [string]$AutoCloseMilliseconds
    if ($ReadyPath) {
        $env:GENSHINTOOLS_S02_READY_FILE = $ReadyPath
    } else {
        Remove-Item Env:GENSHINTOOLS_S02_READY_FILE -ErrorAction SilentlyContinue
    }
    $WindowStyle = if ($Visible) { 'Normal' } else { 'Hidden' }
    return Start-Process -FilePath $Executable -PassThru -WindowStyle $WindowStyle
}

try {
    Remove-Item -LiteralPath $ReadyFile -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $ActivatedFile -Force -ErrorAction SilentlyContinue
    $env:GENSHINTOOLS_S02_ACTIVATED_FILE = $ActivatedFile
    $First = Start-Shell -AutoCloseMilliseconds 3500 -ReadyPath $ReadyFile -Visible
    $Deadline = (Get-Date).AddSeconds(5)
    while (-not (Test-Path -LiteralPath $ReadyFile) -and (Get-Date) -lt $Deadline) {
        Start-Sleep -Milliseconds 50
    }
    if (-not (Test-Path -LiteralPath $ReadyFile)) {
        [void]$First.WaitForExit(10000)
        throw 'First instance did not publish its ready marker'
    }

    $Window = [IntPtr]::Zero
    $WindowDeadline = (Get-Date).AddSeconds(2)
    while ($Window -eq [IntPtr]::Zero -and (Get-Date) -lt $WindowDeadline) {
        $First.Refresh()
        $Window = [IntPtr]$First.MainWindowHandle
        if ($Window -eq [IntPtr]::Zero) { Start-Sleep -Milliseconds 25 }
    }
    if ($Window -eq [IntPtr]::Zero) {
        throw 'First instance has no main window handle'
    }
    [void][S02ShellTestNative]::PostMessage($Window, 0x0112, [UIntPtr]::new([uint64]0xF020), [IntPtr]::Zero)
    $HiddenDeadline = (Get-Date).AddSeconds(2)
    while ([S02ShellTestNative]::IsWindowVisible($Window) -and (Get-Date) -lt $HiddenDeadline) {
        Start-Sleep -Milliseconds 25
    }
    if ([S02ShellTestNative]::IsWindowVisible($Window)) {
        throw 'Minimize command did not hide the window to the tray'
    }

    $Watch = [Diagnostics.Stopwatch]::StartNew()
    $Second = Start-Process -FilePath $Executable -PassThru -Wait -WindowStyle Hidden
    $Watch.Stop()
    if ($Second.ExitCode -ne 0) {
        throw "Second instance exited with $($Second.ExitCode)"
    }
    if ($Watch.Elapsed.TotalSeconds -gt 3) {
        throw "Second-instance activation took $($Watch.Elapsed.TotalSeconds) seconds"
    }
    $ActivationDeadline = (Get-Date).AddSeconds(2)
    while (-not (Test-Path -LiteralPath $ActivatedFile) -and (Get-Date) -lt $ActivationDeadline) {
        Start-Sleep -Milliseconds 25
    }
    if (-not (Test-Path -LiteralPath $ActivatedFile)) {
        throw 'First instance did not acknowledge the activation message'
    }
    $VisibleDeadline = (Get-Date).AddSeconds(2)
    while (-not [S02ShellTestNative]::IsWindowVisible($Window) -and (Get-Date) -lt $VisibleDeadline) {
        Start-Sleep -Milliseconds 25
    }
    if (-not [S02ShellTestNative]::IsWindowVisible($Window)) {
        throw 'Second instance activation did not restore the tray window'
    }
    if ($First.HasExited) {
        throw 'First instance exited while activating it from the second instance'
    }
    if (-not $First.WaitForExit(10000) -or $First.ExitCode -ne 0) {
        throw 'First instance did not close cleanly'
    }

    if (Test-Path -LiteralPath $MarkerFile) {
        throw 'Clean shutdown left session.marker behind'
    }
    $Configuration = Get-Content -LiteralPath $ConfigFile -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($Configuration.schemaVersion -ne 9 -or $null -eq $Configuration.input -or $null -eq $Configuration.plugins -or $null -eq $Configuration.shell) {
        throw 'Unexpected config schema after clean shutdown'
    }
    $Entries = @(Get-Content -LiteralPath $LogFile -Encoding UTF8 | ForEach-Object { $_ | ConvertFrom-Json })
    if (-not ($Entries | Where-Object message -eq 'tray icon added')) {
        throw 'Successful tray registration was not recorded'
    }

    $CorruptBefore = @(Get-ChildItem -LiteralPath $DataDirectory -Filter 'config.json.corrupt-*' -ErrorAction SilentlyContinue).Count
    [IO.File]::WriteAllText($ConfigFile, '{broken', [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText($MarkerFile, '{}', [Text.UTF8Encoding]::new($false))
    $Recovery = Start-Shell -AutoCloseMilliseconds 300
    if (-not $Recovery.WaitForExit(10000) -or $Recovery.ExitCode -ne 0) {
        throw 'Recovery instance did not close cleanly'
    }
    $CorruptAfter = @(Get-ChildItem -LiteralPath $DataDirectory -Filter 'config.json.corrupt-*').Count
    if ($CorruptAfter -ne $CorruptBefore + 1) {
        throw 'Corrupt config was not quarantined exactly once'
    }
    if (Test-Path -LiteralPath $MarkerFile) {
        throw 'Recovery run left session.marker behind'
    }
    Get-Content -LiteralPath $ConfigFile -Raw -Encoding UTF8 | ConvertFrom-Json | Out-Null

    for ($Index = 1; $Index -le $StressIterations; $Index++) {
        $Process = Start-Shell -AutoCloseMilliseconds 40
        if (-not $Process.WaitForExit(10000) -or $Process.ExitCode -ne 0) {
            throw "Stress iteration $Index failed"
        }
        if (Test-Path -LiteralPath $MarkerFile) {
            throw "Stress iteration $Index left session.marker behind"
        }
    }

    [pscustomobject]@{
        FirstInstanceExit       = $First.ExitCode
        SecondInstanceExit      = $Second.ExitCode
        SecondInstanceLatencyMs = [math]::Round($Watch.Elapsed.TotalMilliseconds)
        ActivationAcknowledged  = 'passed'
        MinimizeToTrayRestore    = 'passed'
        TrayRegistration        = 'passed'
        CorruptConfigRecovery   = 'passed'
        PreviousCrashDetection  = 'passed'
        StressIterations        = $StressIterations
        CleanSessionMarkers     = 'passed'
    } | Format-List
} finally {
    Remove-Item Env:GENSHINTOOLS_S02_AUTOCLOSE_MS -ErrorAction SilentlyContinue
    Remove-Item Env:GENSHINTOOLS_S02_READY_FILE -ErrorAction SilentlyContinue
    Remove-Item Env:GENSHINTOOLS_S02_ACTIVATED_FILE -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $ReadyFile -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $ActivatedFile -Force -ErrorAction SilentlyContinue
}
