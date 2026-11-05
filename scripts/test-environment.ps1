$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'environment.ps1')
$Name = 'GENSHINTOOLS_ENVIRONMENT_REGRESSION'
$Original = Save-ProcessEnvironment -Names @($Name)
try {
    foreach ($Value in @([NullString]::Value, '', 'preserved-value')) {
        [Environment]::SetEnvironmentVariable($Name, $Value, 'Process')
        $Expected = [Environment]::GetEnvironmentVariable($Name, 'Process')
        $Snapshot = Save-ProcessEnvironment -Names @($Name)
        [Environment]::SetEnvironmentVariable($Name, 'changed-by-child', 'Process')
        Restore-ProcessEnvironment -Snapshot $Snapshot -Names @($Name)
        $Actual = [Environment]::GetEnvironmentVariable($Name, 'Process')
        if ($Actual -cne $Expected -or ($null -eq $Actual) -ne ($null -eq $Expected)) {
            throw 'Process environment absent/empty/value round-trip changed its exact state'
        }
    }
    Write-Output 'PASS process environment absent/empty/value round-trip'
} finally {
    if ($null -eq $Original[$Name]) {
        [Environment]::SetEnvironmentVariable($Name, [NullString]::Value, 'Process')
    } else {
        [Environment]::SetEnvironmentVariable($Name, $Original[$Name], 'Process')
    }
}
