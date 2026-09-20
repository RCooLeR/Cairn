param(
  [string]$Root = "",
  [switch]$SkipLinuxIcons
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($Root)) {
  $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
  $Root = (Resolve-Path (Join-Path $scriptDir "..")).Path
}

$sourceRoot = Join-Path $Root "src"
$icon = Join-Path $sourceRoot "assets/cairn-icon.png"
$logo = Join-Path $sourceRoot "assets/cairn-logo.png"
$buildIcon = Join-Path $Root "build/appicon.png"
$publicDir = Join-Path $sourceRoot "frontend/public"

if (!(Test-Path -LiteralPath $icon)) {
  throw "Missing source icon: $icon"
}
if (!(Test-Path -LiteralPath $logo)) {
  throw "Missing source logo: $logo"
}

New-Item -ItemType Directory -Force -Path $publicDir | Out-Null
Copy-Item -LiteralPath $icon -Destination $buildIcon -Force
Copy-Item -LiteralPath $icon -Destination (Join-Path $publicDir "cairn-icon.png") -Force
Copy-Item -LiteralPath $logo -Destination (Join-Path $publicDir "cairn-logo.png") -Force

if (!$SkipLinuxIcons) {
  Push-Location $sourceRoot
  try {
    go run ./tools/iconset -input $icon -linux-dir (Join-Path $Root "build/linux/icons") -name cairn
    if ($LASTEXITCODE -ne 0) {
      throw "Linux icon generation failed with exit code $LASTEXITCODE"
    }
  } finally {
    Pop-Location
  }
}
