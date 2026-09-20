param(
    [string[]] $Pattern = @("./...")
)

$ErrorActionPreference = "Stop"

$sourceRoot = Join-Path (Split-Path -Parent $PSScriptRoot) "src"
$previousGoToolchain = $env:GOTOOLCHAIN
Push-Location $sourceRoot
try {
    $projectGoVersion = (& go env GOVERSION).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $projectGoVersion) {
        throw "Unable to resolve the project Go toolchain version."
    }

    # `go run module@version` otherwise selects from the tool module's go.mod,
    # which can build govulncheck with an older parser than this project needs.
    $env:GOTOOLCHAIN = $projectGoVersion
    & go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 @Pattern
    $govulnExitCode = $LASTEXITCODE
} finally {
    if ($null -eq $previousGoToolchain) {
        Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue
    } else {
        $env:GOTOOLCHAIN = $previousGoToolchain
    }
    Pop-Location
}

if ($govulnExitCode -eq 0) {
    Write-Host "govulncheck passed with no reachable vulnerabilities."
}

exit $govulnExitCode
