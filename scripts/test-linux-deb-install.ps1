param(
  [string]$Root = ""
)

$ErrorActionPreference = "Stop"

if (![System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Linux)) {
  throw "Linux deb install smoke must run on Linux."
}

if ([string]::IsNullOrWhiteSpace($Root)) {
  $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
  $Root = (Resolve-Path (Join-Path $scriptDir "..")).Path
}

function Invoke-Native([string]$File, [string[]]$Arguments) {
  & $File @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "$File failed with exit code $LASTEXITCODE"
  }
}

function Read-Native([string]$File, [string[]]$Arguments) {
  $output = & $File @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "$File failed with exit code $LASTEXITCODE"
  }
  return ($output -join "`n").Trim()
}

function Test-Executable([string]$Path) {
  if (!(Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "Missing expected file: $Path"
  }
  Invoke-Native "test" @("-x", $Path)
}

function Test-Absent([string]$Path) {
  if (Test-Path -LiteralPath $Path) {
    throw "Expected package-owned path to be removed: $Path"
  }
}

$deb = Get-ChildItem -Path (Join-Path $Root "bin") -Filter "*.deb" -File | Select-Object -First 1
if (!$deb) {
  throw "No Debian package found under bin/."
}

$packageName = Read-Native "dpkg-deb" @("--field", $deb.FullName, "Package")
$depends = Read-Native "dpkg-deb" @("--field", $deb.FullName, "Depends")
if ($depends -match "(^|[, ])docker($|[, ])|docker-ce|docker\.io|containerd") {
  throw "Deb package must not depend on Docker packages: $depends"
}

$dockerGroupBefore = (& bash -lc "getent group docker || true") -join "`n"
$env:DEBIAN_FRONTEND = "noninteractive"
$installed = $false

try {
  Write-Host "Installing $($deb.Name) as package $packageName"
  Invoke-Native "sudo" @("apt-get", "install", "-y", "--no-install-recommends", $deb.FullName)
  $installed = $true

  $status = Read-Native "dpkg-query" @("-W", "-f", '${Status} ${Version}', $packageName)
  if ($status -notmatch "^install ok installed ") {
    throw "Package status after install was unexpected: $status"
  }

  Test-Executable "/usr/bin/cairn"
  if (!(Test-Path -LiteralPath "/usr/share/applications/cairn.desktop" -PathType Leaf)) {
    throw "Missing installed desktop file."
  }
  if (!(Test-Path -LiteralPath "/usr/share/icons/hicolor/512x512/apps/cairn.png" -PathType Leaf)) {
    throw "Missing installed 512px hicolor icon."
  }

  $dockerGroupAfterInstall = (& bash -lc "getent group docker || true") -join "`n"
  if ($dockerGroupAfterInstall -ne $dockerGroupBefore) {
    throw "Deb install changed the docker group."
  }
} finally {
  if ($installed) {
    Write-Host "Removing package $packageName"
    Invoke-Native "sudo" @("apt-get", "remove", "-y", $packageName)
  }
}

$queryOutput = (& dpkg-query -W -f='${db:Status-Abbrev} ${Version}' $packageName 2>$null) -join "`n"
if ($LASTEXITCODE -eq 0) {
  $queryOutput = $queryOutput.Trim()
  $packageState = if ($queryOutput.Length -ge 2) { [string]$queryOutput[1] } else { "" }
  if ($packageState -notin @("n", "c")) {
    throw "Package still appears active after remove: $queryOutput"
  }
  Write-Host "Package dpkg state after remove: $queryOutput"
}
Test-Absent "/usr/bin/cairn"
Test-Absent "/usr/share/applications/cairn.desktop"
Test-Absent "/usr/share/icons/hicolor/512x512/apps/cairn.png"

$dockerGroupAfterRemove = (& bash -lc "getent group docker || true") -join "`n"
if ($dockerGroupAfterRemove -ne $dockerGroupBefore) {
  throw "Deb remove changed the docker group."
}

Write-Host "Linux deb install/uninstall smoke passed for $($deb.Name)"
