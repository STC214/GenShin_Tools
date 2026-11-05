function Save-ProcessEnvironment {
    param([Parameter(Mandatory)] [string[]]$Names)
    $Snapshot = @{}
    foreach ($Name in $Names) {
        $Snapshot[$Name] = [Environment]::GetEnvironmentVariable($Name, 'Process')
    }
    return $Snapshot
}

function Restore-ProcessEnvironment {
    param(
        [Parameter(Mandatory)] [hashtable]$Snapshot,
        [Parameter(Mandatory)] [string[]]$Names
    )
    foreach ($Name in $Names) {
        # PowerShell binds $null to an empty string for string parameters.
        # Modern .NET preserves empty environment values, so force actual null
        # for an originally absent variable instead of leaving a new empty one.
        $Value = if ($null -eq $Snapshot[$Name]) { [NullString]::Value } else { $Snapshot[$Name] }
        [Environment]::SetEnvironmentVariable($Name, $Value, 'Process')
    }
}
