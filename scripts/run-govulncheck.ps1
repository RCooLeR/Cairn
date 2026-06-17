param(
    [string[]] $Pattern = @("./...")
)

$ErrorActionPreference = "Stop"

$allowedDockerAdvisories = @(
    "GO-2026-4883",
    "GO-2026-4887"
)

function ConvertFrom-JsonStream {
    param(
        [Parameter(Mandatory = $true)]
        [string] $InputText
    )

    $objects = New-Object System.Collections.Generic.List[object]
    $buffer = New-Object System.Text.StringBuilder
    $depth = 0
    $inString = $false
    $escaped = $false

    foreach ($char in $InputText.ToCharArray()) {
        [void] $buffer.Append($char)

        if ($escaped) {
            $escaped = $false
            continue
        }
        if ($char -eq "\") {
            $escaped = $true
            continue
        }
        if ($char -eq '"') {
            $inString = -not $inString
            continue
        }
        if ($inString) {
            continue
        }

        if ($char -eq "{") {
            $depth++
        } elseif ($char -eq "}") {
            $depth--
            if ($depth -eq 0) {
                $json = $buffer.ToString().Trim()
                if ($json) {
                    $objects.Add(($json | ConvertFrom-Json))
                }
                [void] $buffer.Clear()
            }
        }
    }

    return $objects
}

$output = & go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 -format json @Pattern 2>&1
$govulnExitCode = $LASTEXITCODE
$outputText = ($output | Out-String)

if ($govulnExitCode -eq 0) {
    Write-Host "govulncheck passed with no reachable vulnerabilities."
    exit 0
}

$items = ConvertFrom-JsonStream -InputText $outputText
$findings = @($items | Where-Object { $_.finding } | ForEach-Object { $_.finding })

if ($findings.Count -eq 0) {
    Write-Error $outputText
    exit $govulnExitCode
}

$blocking = New-Object System.Collections.Generic.List[object]
$allowed = New-Object System.Collections.Generic.List[object]

foreach ($finding in $findings) {
    $traceModules = @($finding.trace | ForEach-Object { $_.module } | Where-Object { $_ })
    $isAllowedDockerFinding =
        $allowedDockerAdvisories -contains $finding.osv -and
        $traceModules -contains "github.com/docker/docker"

    if ($isAllowedDockerFinding) {
        $allowed.Add($finding)
    } else {
        $blocking.Add($finding)
    }
}

if ($blocking.Count -gt 0) {
    Write-Error "govulncheck found $($blocking.Count) blocking finding(s):"
    $blocking |
        Select-Object osv, fixed_version, @{Name = "module"; Expression = { ($_.trace | Select-Object -First 1).module } } |
        Format-Table -AutoSize | Out-String | Write-Error
    exit 1
}

$allowedIDs = @($allowed | ForEach-Object { $_.osv } | Sort-Object -Unique)
Write-Warning "Allowed govulncheck finding(s): $($allowedIDs -join ', ') from github.com/docker/docker. These advisories currently have no fixed SDK version; keep Docker Engine updated and remove this allowlist once upstream publishes a fix."
exit 0
