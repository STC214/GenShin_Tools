[CmdletBinding()]
param(
    [string]$OutputPath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'environment.ps1')
$EnvironmentNames = @('GENSHINTOOLS_S02_READY_FILE', 'GENSHINTOOLS_S02_AUTOCLOSE_MS')
$PreviousEnvironment = Save-ProcessEnvironment -Names $EnvironmentNames
$Executable = (Resolve-Path (Join-Path $ProjectRoot 'dist\GenshinTools.exe')).Path
$ReadyFile = Join-Path $ProjectRoot 'dist\data\capture-ready.tmp'
if (-not $OutputPath) {
    $OutputPath = Join-Path $ProjectRoot 'build\s02-window.png'
}

Add-Type -AssemblyName System.Drawing
if (-not ('S02WindowCapture' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class S02WindowCapture {
    [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindow(string className, string title);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr window, System.Text.StringBuilder className, int capacity);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window, out RECT rectangle);
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
}
'@
}

try {
    Remove-Item -LiteralPath $ReadyFile -Force -ErrorAction SilentlyContinue
    $env:GENSHINTOOLS_S02_READY_FILE = $ReadyFile
    $env:GENSHINTOOLS_S02_AUTOCLOSE_MS = '6000'
    $Process = Start-Process -FilePath $Executable -PassThru -WindowStyle Normal
    $Deadline = (Get-Date).AddSeconds(5)
    while (-not (Test-Path -LiteralPath $ReadyFile) -and (Get-Date) -lt $Deadline) {
        Start-Sleep -Milliseconds 50
    }
    if (-not (Test-Path -LiteralPath $ReadyFile)) {
        [void]$Process.WaitForExit(10000)
        throw 'Capture instance did not become ready'
    }
    $Window = [S02WindowCapture]::FindWindow('GenshinTools.MainWindow.S02', $null)
    if ($Window -eq [IntPtr]::Zero) {
        $HandleDeadline = (Get-Date).AddSeconds(2)
        while ($Window -eq [IntPtr]::Zero -and (Get-Date) -lt $HandleDeadline) {
            $Process.Refresh()
            $Window = [IntPtr]$Process.MainWindowHandle
            if ($Window -eq [IntPtr]::Zero) { Start-Sleep -Milliseconds 50 }
        }
    }
    if ($Window -eq [IntPtr]::Zero) {
        throw 'Could not find S02 main window by class name or process handle'
    }
    $ClassName = [Text.StringBuilder]::new(256)
    [void][S02WindowCapture]::GetClassName($Window, $ClassName, $ClassName.Capacity)
    Write-Host "Captured window class: $ClassName"
    [void][S02WindowCapture]::SetForegroundWindow($Window)
    Start-Sleep -Milliseconds 300
    $Rectangle = New-Object S02WindowCapture+RECT
    if (-not [S02WindowCapture]::GetWindowRect($Window, [ref]$Rectangle)) {
        throw 'GetWindowRect failed'
    }
    $Bitmap = New-Object Drawing.Bitmap ($Rectangle.Right - $Rectangle.Left), ($Rectangle.Bottom - $Rectangle.Top)
    $Graphics = [Drawing.Graphics]::FromImage($Bitmap)
    try {
        $Graphics.CopyFromScreen($Rectangle.Left, $Rectangle.Top, 0, 0, $Bitmap.Size)
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $OutputPath) | Out-Null
        $Bitmap.Save($OutputPath, [Drawing.Imaging.ImageFormat]::Png)
    } finally {
        $Graphics.Dispose()
        $Bitmap.Dispose()
    }
    [void]$Process.WaitForExit(10000)
    Write-Output ([IO.Path]::GetFullPath($OutputPath))
} finally {
    Restore-ProcessEnvironment -Snapshot $PreviousEnvironment -Names $EnvironmentNames
    Remove-Item -LiteralPath $ReadyFile -Force -ErrorAction SilentlyContinue
}
