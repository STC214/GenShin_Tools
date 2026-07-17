[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$SmokeRoot = Join-Path $ProjectRoot 'build\s05-smoke'
$AppRoot = Join-Path $SmokeRoot 'app'
$GameRoot = Join-Path $SmokeRoot 'unicode path with spaces\Genshin Impact Game'
$ResultPath = Join-Path $SmokeRoot 'launch-result.json'
$ReadyPath = Join-Path $SmokeRoot 'ready.tmp'
$AppExe = Join-Path $AppRoot 'GenshinTools.exe'
$GameExe = Join-Path $GameRoot 'YuanShen.exe'
$GoCache = Join-Path $ProjectRoot '.cache\go-build'
$GoTemp = Join-Path $ProjectRoot '.tmp\go'

New-Item -ItemType Directory -Force -Path $AppRoot, $GameRoot, (Join-Path $AppRoot 'data'), $GoCache, $GoTemp | Out-Null
Copy-Item -LiteralPath (Join-Path $ProjectRoot 'dist\GenshinTools.exe') -Destination $AppExe -Force
$env:GOCACHE = $GoCache
$env:GOTMPDIR = $GoTemp
& go build -trimpath -buildvcs=false -o $GameExe ./tools/launchfixture
if ($LASTEXITCODE -ne 0) { throw "build launch fixture failed: $LASTEXITCODE" }
Set-Content -LiteralPath (Join-Path $GameRoot 'config.ini') -Encoding UTF8 -Value "game_version=6.1.2`nchannel=1"

$Settings = [ordered]@{
    schemaVersion = 4
    window = @{ x = 100; y = 100; width = 1100; height = 720 }
    input = @{ enabled = $false; mode = 0; triggerKey = 119; outputKey = 70; stopKey = 123; intervalMs = 50 }
    game = @{ path = $GameRoot; customExecutable = '' }
    launch = @{ width = 2560; height = 1440; monitor = 0; windowMode = 3; customArguments = '--label "A B" --path "C:\path with spaces\x.txt"'; postBehavior = 0 }
}
$Settings | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $AppRoot 'data\config.json') -Encoding UTF8

Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class S05LaunchSmoke {
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindow(string className, string title);
    [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr window, uint message, UIntPtr wParam, IntPtr lParam);
}
'@

try {
    Remove-Item -LiteralPath $ResultPath, $ReadyPath -Force -ErrorAction SilentlyContinue
    $env:GENSHINTOOLS_S02_READY_FILE = $ReadyPath
    $env:GENSHINTOOLS_S02_AUTOCLOSE_MS = '3500'
    $env:GENSHINTOOLS_S05_RESULT = $ResultPath
    $env:GENSHINTOOLS_S05_SLEEP_MS = '300'
    $env:GENSHINTOOLS_S05_EXIT_CODE = '23'
    $Process = Start-Process -FilePath $AppExe -PassThru -WindowStyle Normal
    $Deadline = (Get-Date).AddSeconds(5)
    while (-not (Test-Path -LiteralPath $ReadyPath) -and (Get-Date) -lt $Deadline) { Start-Sleep -Milliseconds 50 }
    if (-not (Test-Path -LiteralPath $ReadyPath)) { throw 'S05 smoke app did not become ready' }
    $Window = [S05LaunchSmoke]::FindWindow('GenshinTools.MainWindow.S02', $null)
    if ($Window -eq [IntPtr]::Zero) {
        $HandleDeadline = (Get-Date).AddSeconds(2)
        while ($Window -eq [IntPtr]::Zero -and (Get-Date) -lt $HandleDeadline) {
            $Process.Refresh()
            $Window = [IntPtr]$Process.MainWindowHandle
            if ($Window -eq [IntPtr]::Zero) { Start-Sleep -Milliseconds 50 }
        }
    }
    if ($Window -eq [IntPtr]::Zero) { throw 'S05 smoke main window not found' }
    $NavX = 100; $NavY = 156
    [void][S05LaunchSmoke]::SendMessage($Window, 0x0201, [UIntPtr]::Zero, [IntPtr](($NavY -shl 16) -bor $NavX))
    Start-Sleep -Milliseconds 500
    $LaunchX = 500; $LaunchY = 190
    [void][S05LaunchSmoke]::SendMessage($Window, 0x0201, [UIntPtr]::Zero, [IntPtr](($LaunchY -shl 16) -bor $LaunchX))
    $ResultDeadline = (Get-Date).AddSeconds(5)
    while (-not (Test-Path -LiteralPath $ResultPath) -and (Get-Date) -lt $ResultDeadline) { Start-Sleep -Milliseconds 50 }
    if (-not (Test-Path -LiteralPath $ResultPath)) { throw 'game fixture did not receive launch' }
    $Result = Get-Content -LiteralPath $ResultPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ([IO.Path]::GetFullPath($Result.workingDirectory) -ne [IO.Path]::GetFullPath($GameRoot)) { throw "working directory mismatch: $($Result.workingDirectory)" }
    $Expected = @('-screen-width','2560','-screen-height','1440','-screen-fullscreen','0','-window-mode','borderless','-popupwindow','--label','A B','--path','C:\path with spaces\x.txt')
    if (($Result.arguments -join "`n") -ne ($Expected -join "`n")) { throw "arguments mismatch: $($Result.arguments -join ' | ')" }
    [void]$Process.WaitForExit(10000)
    if (-not $Process.HasExited -or $Process.ExitCode -ne 0) { throw "launcher smoke failed or hung: exit=$($Process.ExitCode)" }
    Write-Host "[S05] PASS pid=$($Result.pid) cwd=$($Result.workingDirectory) args=$($Result.arguments.Count)"
} finally {
    Remove-Item Env:GENSHINTOOLS_S02_READY_FILE -ErrorAction SilentlyContinue
    Remove-Item Env:GENSHINTOOLS_S02_AUTOCLOSE_MS -ErrorAction SilentlyContinue
    Remove-Item Env:GENSHINTOOLS_S05_RESULT -ErrorAction SilentlyContinue
    Remove-Item Env:GENSHINTOOLS_S05_SLEEP_MS -ErrorAction SilentlyContinue
    Remove-Item Env:GENSHINTOOLS_S05_EXIT_CODE -ErrorAction SilentlyContinue
}
