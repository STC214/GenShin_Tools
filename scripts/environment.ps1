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
        [Environment]::SetEnvironmentVariable($Name, $Snapshot[$Name], 'Process')
    }
}
