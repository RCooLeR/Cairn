param()

$ErrorActionPreference = "Stop"

$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$root = (Resolve-Path (Join-Path $scriptDir "..")).Path

Push-Location $root
try {
  & go test ./internal/store -run TestReleaseDBFixtureUpgrade$ -count=1 -timeout=30s
  if ($LASTEXITCODE -ne 0) {
    throw "release DB upgrade fixture test failed with exit code $LASTEXITCODE"
  }
} finally {
  Pop-Location
}
