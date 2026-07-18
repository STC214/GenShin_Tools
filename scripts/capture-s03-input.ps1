[CmdletBinding()]
param([string]$OutputPath)

$ErrorActionPreference = 'Stop'
$ProjectRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'environment.ps1')
$EnvironmentNames = @('GENSHINTOOLS_S02_READY_FILE', 'GENSHINTOOLS_S02_AUTOCLOSE_MS')
$PreviousEnvironment = Save-ProcessEnvironment -Names $EnvironmentNames
$Executable = (Resolve-Path (Join-Path $ProjectRoot 'dist\GenshinTools.exe')).Path
$ReadyFile = Join-Path $ProjectRoot 'dist\data\capture-s03-ready.tmp'
if (-not $OutputPath) { $OutputPath = Join-Path $ProjectRoot 'build\s03-input.png' }

Add-Type -AssemblyName System.Drawing
if (-not ('S03WindowCapture' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class S03WindowCapture {
    [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindow(string className, string title);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window, out RECT rectangle);
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr window, uint message, UIntPtr wParam, IntPtr lParam);
    [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr window, IntPtr deviceContext, uint flags);
    [DllImport("user32.dll")] public static extern bool UpdateWindow(IntPtr window);
}
'@
}

try {
    Remove-Item -LiteralPath $ReadyFile -Force -ErrorAction SilentlyContinue
    $env:GENSHINTOOLS_S02_READY_FILE = $ReadyFile
    $env:GENSHINTOOLS_S02_AUTOCLOSE_MS = '6000'
    $Process = Start-Process -FilePath $Executable -PassThru -WindowStyle Normal
    $Deadline = (Get-Date).AddSeconds(5)
    while (-not (Test-Path -LiteralPath $ReadyFile) -and (Get-Date) -lt $Deadline) { Start-Sleep -Milliseconds 50 }
    if (-not (Test-Path -LiteralPath $ReadyFile)) { throw 'Input page capture did not become ready' }
    $Window = [S03WindowCapture]::FindWindow('GenshinTools.MainWindow.S02', $null)
    if ($Window -eq [IntPtr]::Zero) {
        $HandleDeadline = (Get-Date).AddSeconds(2)
        while ($Window -eq [IntPtr]::Zero -and (Get-Date) -lt $HandleDeadline) {
            $Process.Refresh()
            $Window = [IntPtr]$Process.MainWindowHandle
            if ($Window -eq [IntPtr]::Zero) { Start-Sleep -Milliseconds 50 }
        }
    }
    if ($Window -eq [IntPtr]::Zero) { throw 'Could not find the main window' }
    [void][S03WindowCapture]::SetForegroundWindow($Window)
    # Select the third navigation row (Input Enhancement).
    $X = 100; $Y = 204; $LParam = [IntPtr](($Y -shl 16) -bor $X)
    [void][S03WindowCapture]::SendMessage($Window, 0x0201, [UIntPtr]::Zero, $LParam)
    [void][S03WindowCapture]::UpdateWindow($Window)
    Start-Sleep -Milliseconds 400
    $Rectangle = New-Object S03WindowCapture+RECT
    if (-not [S03WindowCapture]::GetWindowRect($Window, [ref]$Rectangle)) { throw 'GetWindowRect failed' }
    $Bitmap = New-Object Drawing.Bitmap ($Rectangle.Right - $Rectangle.Left), ($Rectangle.Bottom - $Rectangle.Top)
    $Graphics = [Drawing.Graphics]::FromImage($Bitmap)
    try {
        $DeviceContext = $Graphics.GetHdc()
        try {
            if (-not [S03WindowCapture]::PrintWindow($Window, $DeviceContext, 2)) { throw 'PrintWindow failed' }
        } finally { $Graphics.ReleaseHdc($DeviceContext) }
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $OutputPath) | Out-Null
        $Bitmap.Save($OutputPath, [Drawing.Imaging.ImageFormat]::Png)
    } finally { $Graphics.Dispose(); $Bitmap.Dispose() }
    [void]$Process.WaitForExit(10000)
    if (-not $Process.HasExited -or $Process.ExitCode -ne 0) { throw "Capture process failed or hung: exit=$($Process.ExitCode)" }
    Write-Output ([IO.Path]::GetFullPath($OutputPath))
} finally {
    Restore-ProcessEnvironment -Snapshot $PreviousEnvironment -Names $EnvironmentNames
    Remove-Item -LiteralPath $ReadyFile -Force -ErrorAction SilentlyContinue
}
