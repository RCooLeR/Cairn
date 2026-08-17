param(
  [switch]$SkipRuntime
)

$ErrorActionPreference = "Stop"

$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$root = (Resolve-Path (Join-Path $scriptDir "..")).Path

function Read-SingleCapture(
  [string]$Path,
  [string]$Pattern,
  [string]$Description
) {
  $matches = @(Select-String -LiteralPath $Path -Pattern $Pattern)
  if ($matches.Count -ne 1 -or $matches[0].Matches.Count -ne 1) {
    throw "$Path must declare exactly one $Description."
  }
  return $matches[0].Matches[0].Groups[1].Value
}

function ConvertTo-Version([string]$Value, [string]$Description) {
  $normalized = $Value.Trim().TrimStart("v")
  try {
    return [version]$normalized
  } catch {
    throw "$Description '$Value' is not a semantic numeric version."
  }
}

function Assert-MinimumVersion(
  [string]$Command,
  [version]$Minimum,
  [string]$Description
) {
  $resolved = Get-Command $Command -ErrorAction SilentlyContinue
  if ($null -eq $resolved) {
    throw "$Description was not found on PATH."
  }
  $versionOutput = @(& $resolved.Source --version)
  $commandSucceeded = $?
  $raw = $versionOutput | Select-Object -First 1
  if (-not $commandSucceeded -or [string]::IsNullOrWhiteSpace($raw)) {
    throw "$Description version could not be read."
  }
  $actual = ConvertTo-Version $raw $Description
  if ($actual -lt $Minimum) {
    throw "$Description is $actual, want at least $Minimum."
  }
}

$goMod = Join-Path $root "go.mod"
$expectedGo = Read-SingleCapture $goMod '^\s*toolchain\s+(go\S+)\s*$' "pinned Go toolchain"
$goLanguage = Read-SingleCapture $goMod '^\s*go\s+(\d+\.\d+)\s*$' "Go language version"
if ($expectedGo -notmatch ('^go' + [regex]::Escape($goLanguage) + '\.\d+$')) {
  throw "Pinned toolchain $expectedGo does not match the go $goLanguage language line."
}

$dockerfile = Join-Path $root "build/docker/Dockerfile.cross"
$dockerGo = Read-SingleCapture $dockerfile '^\s*FROM\s+golang:(\d+\.\d+\.\d+)-bookworm@sha256:[0-9a-f]{64}\s*$' "digest-pinned cross-image Go toolchain"
if ("go$dockerGo" -ne $expectedGo) {
  throw "Dockerfile.cross pins go$dockerGo, want $expectedGo from go.mod."
}

$debianSmokeScript = Join-Path $root "scripts/test-debian-container-deb-install.ps1"
$null = Read-SingleCapture `
  $debianSmokeScript `
  '^\s*\[string\]\$Image\s*=\s*"(debian:stable-slim@sha256:[0-9a-f]{64})"\s*$' `
  "digest-pinned Debian package-smoke image"

$package = Get-Content -LiteralPath (Join-Path $root "frontend/package.json") -Raw | ConvertFrom-Json
$nodeEngine = [string]$package.engines.node
$npmEngine = [string]$package.engines.npm
if ($nodeEngine -notmatch '^>=(\d+\.\d+\.\d+)$') {
  throw "frontend/package.json must declare a simple minimum Node version."
}
$minimumNode = ConvertTo-Version $Matches[1] "Node engine"
if ($npmEngine -notmatch '^>=(\d+\.\d+\.\d+)$') {
  throw "frontend/package.json must declare a simple minimum npm version."
}
$minimumNPM = ConvertTo-Version $Matches[1] "npm engine"

$nodeVersionFile = Join-Path $root ".node-version"
if (!(Test-Path -LiteralPath $nodeVersionFile -PathType Leaf)) {
  throw ".node-version must pin the hosted Node.js runtime."
}
$pinnedNodeRaw = (Get-Content -LiteralPath $nodeVersionFile -Raw).Trim()
if ($pinnedNodeRaw -notmatch '^\d+\.\d+\.\d+$') {
  throw ".node-version must contain one exact numeric Node.js version."
}
$pinnedNode = ConvertTo-Version $pinnedNodeRaw "Pinned Node"
if ($pinnedNode -lt $minimumNode) {
  throw "Pinned Node $pinnedNode is below package.json minimum $minimumNode."
}

$workflowFiles = @(Get-ChildItem -LiteralPath (Join-Path $root ".github/workflows") -File -Include "*.yml", "*.yaml")
$nodeVersionFileUses = @($workflowFiles | Select-String -Pattern '^\s*node-version-file:\s*\.node-version\s*$')
$setupNodeUses = @($workflowFiles | Select-String -Pattern '^\s*uses:\s+actions/setup-node@')
if ($setupNodeUses.Count -eq 0 -or $nodeVersionFileUses.Count -ne $setupNodeUses.Count) {
  throw "Every setup-node action must use the checked .node-version pin."
}

foreach ($actionUse in @($workflowFiles | Select-String -Pattern '^\s*uses:\s+')) {
  $line = $actionUse.Line
  if ($line -match '^\s*uses:\s+\./') {
    continue
  }
  if ($line -notmatch '^\s*uses:\s+[^\s@]+@[0-9a-f]{40}\s+#\s+v[0-9][0-9A-Za-z.+-]*\s*$') {
    throw "$($actionUse.Path):$($actionUse.LineNumber) must pin a remote action to a full commit SHA with its version tag comment."
  }
}

$nsisInstalls = @($workflowFiles | Select-String -Pattern '^\s*choco install nsis\b')
if ($nsisInstalls.Count -eq 0) {
  throw "At least one hosted Windows package job must install NSIS."
}
foreach ($nsisInstall in $nsisInstalls) {
  if ($nsisInstall.Line -notmatch '^\s*choco install nsis --version=\d+\.\d+\.\d+\s') {
    throw "$($nsisInstall.Path):$($nsisInstall.LineNumber) must install one exact NSIS version."
  }
}

if (-not $SkipRuntime) {
  $go = Get-Command go -ErrorAction SilentlyContinue
  if ($null -eq $go) {
    throw "Go was not found on PATH."
  }
  $goOutput = @(& $go.Source env GOVERSION)
  $goSucceeded = $?
  $actualGo = ($goOutput | Select-Object -First 1).Trim()
  if (-not $goSucceeded -or $actualGo -ne $expectedGo) {
    throw "Go toolchain is '$actualGo', want $expectedGo."
  }
  Assert-MinimumVersion "node" $minimumNode "Node"
  Assert-MinimumVersion "npm" $minimumNPM "npm"
}

$runtimeStatus = if ($SkipRuntime) { "static pins are consistent" } else { "static pins are consistent and installed runtimes satisfy the declared contract" }
Write-Host "Toolchain policy passed: $runtimeStatus across go.mod, .node-version, and frontend/package.json."
