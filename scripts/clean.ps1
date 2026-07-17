$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$Targets = @(
    (Join-Path $ProjectRoot 'build'),
    (Join-Path $ProjectRoot 'dist'),
    (Join-Path $ProjectRoot '.cache'),
    (Join-Path $ProjectRoot '.tmp'),
    (Join-Path $ProjectRoot 'assets\app.ico'),
    (Join-Path $ProjectRoot 'cmd\genshin-tools\app.syso')
)

foreach ($Target in $Targets) {
    $FullPath = [IO.Path]::GetFullPath($Target)
    if (-not $FullPath.StartsWith($ProjectRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to clean path outside project: $FullPath"
    }
    if (Test-Path -LiteralPath $FullPath) {
        Remove-Item -LiteralPath $FullPath -Recurse -Force
    }
}

