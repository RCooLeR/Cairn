param(
  [Parameter(Mandatory = $true)]
  [ValidateSet("windows", "linux", "darwin")]
  [string]$Platform,
  [string]$Root = "",
  [string]$ExpectedVersion = $env:CAIRN_VERSION,
  [string]$ExpectedCommit = $env:CAIRN_COMMIT,
  [string]$ExpectedBuildDate = $env:CAIRN_BUILD_DATE,
  [ValidateSet("amd64", "arm64")]
  [string]$ExpectedArchitecture = "amd64"
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($Root)) {
  $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
  $Root = (Resolve-Path (Join-Path $scriptDir "..")).Path
}

function Require-Any([string]$Pattern, [string]$Description) {
  $artifacts = Get-ChildItem -Path (Join-Path $Root "bin") -Filter $Pattern -File -ErrorAction SilentlyContinue
  if (!$artifacts) {
    throw "Missing $Description matching bin/$Pattern"
  }
  foreach ($artifact in $artifacts) {
    if ($artifact.Length -le 0) {
      throw "Artifact is empty: $($artifact.FullName)"
    }
  }
  $artifacts | ForEach-Object { Write-Host "Found ${Description}: $($_.Name)" }
}

function Resolve-ExpectedVersion {
  if (![string]::IsNullOrWhiteSpace($ExpectedVersion)) {
    return $ExpectedVersion.Trim()
  }

  $versionFile = Join-Path $Root ".release-version.env"
  if (Test-Path -LiteralPath $versionFile -PathType Leaf) {
    $line = Get-Content -LiteralPath $versionFile |
      Where-Object { $_ -match "^CAIRN_VERSION=(.+)$" } |
      Select-Object -First 1
    if ($line -match "^CAIRN_VERSION=(.+)$") {
      return $Matches[1].Trim()
    }
  }

  throw "ExpectedVersion is required for Windows artifact verification (pass -ExpectedVersion or set CAIRN_VERSION)."
}

switch ($Platform) {
  "windows" {
    $application = Join-Path $Root "bin/cairn.exe"
    if (!(Test-Path -LiteralPath $application -PathType Leaf)) {
      throw "Missing Windows application executable: $application"
    }
    & (Join-Path $Root "scripts/check-windows-binary.ps1") `
      -Path $application `
      -ExpectedVersion (Resolve-ExpectedVersion) `
      -ExpectedArchitecture $ExpectedArchitecture `
      -ExpectedCommit $ExpectedCommit `
      -ExpectedBuildDate $ExpectedBuildDate
    Require-Any "cairn-*-installer*.exe" "NSIS installer"
  }
  "linux" {
    & (Join-Path $Root "scripts/check-binary-build-metadata.ps1") `
      -Path (Join-Path $Root "bin/cairn") `
      -ExpectedCommit $ExpectedCommit `
      -ExpectedBuildDate $ExpectedBuildDate
    Require-Any "*.AppImage" "AppImage"
    Require-Any "*.deb" "Debian package"
  }
  "darwin" {
    Require-Any "*.dmg" "macOS dmg"
    $app = Join-Path $Root "bin/cairn.app/Contents/MacOS/cairn"
    if (!(Test-Path -LiteralPath $app)) {
      throw "Missing app bundle executable: $app"
    }
    & (Join-Path $Root "scripts/check-binary-build-metadata.ps1") `
      -Path $app `
      -ExpectedCommit $ExpectedCommit `
      -ExpectedBuildDate $ExpectedBuildDate
    Write-Host "Found app bundle executable: $app"
  }
}
