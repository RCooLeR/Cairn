param(
    [string[]] $Pattern = @("./...")
)

$ErrorActionPreference = "Stop"

$projectGoVersion = (& go env GOVERSION).Trim()
if ($LASTEXITCODE -ne 0 -or -not $projectGoVersion) {
    throw "Unable to resolve the project Go toolchain version."
}

$previousGoToolchain = $env:GOTOOLCHAIN
try {
    # `go run module@version` otherwise selects from the tool module's go.mod,
    # which can build govulncheck with an older parser than this project needs.
    $env:GOTOOLCHAIN = $projectGoVersion
    & go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 @Pattern
    $govulnExitCode = $LASTEXITCODE
} finally {
    if ($null -eq $previousGoToolchain) {
        Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue
    } else {
        $env:GOTOOLCHAIN = $previousGoToolchain
    }
}

if ($govulnExitCode -eq 0) {
    Write-Host "govulncheck passed with no reachable vulnerabilities."
}

exit $govulnExitCode
