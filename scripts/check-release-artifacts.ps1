param(
  [Parameter(Mandatory = $true)]
  [ValidateSet("windows", "linux", "darwin")]
  [string]$Platform,
  [string]$Root = ""
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

switch ($Platform) {
  "windows" {
    Require-Any "cairn-*-installer*.exe" "NSIS installer"
  }
  "linux" {
    Require-Any "*.AppImage" "AppImage"
    Require-Any "*.deb" "Debian package"
  }
  "darwin" {
    Require-Any "*.dmg" "macOS dmg"
    $app = Join-Path $Root "bin/cairn.app/Contents/MacOS/cairn"
    if (!(Test-Path -LiteralPath $app)) {
      throw "Missing app bundle executable: $app"
    }
    Write-Host "Found app bundle executable: $app"
  }
}
