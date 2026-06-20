param(
  [string]$Root = "",
  [switch]$SkipLinuxIcons
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($Root)) {
  $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
  $Root = (Resolve-Path (Join-Path $scriptDir "..")).Path
}

$icon = Join-Path $Root "assets/cairn-icon.png"
$logo = Join-Path $Root "assets/cairn-logo.png"
$buildIcon = Join-Path $Root "build/appicon.png"
$publicDir = Join-Path $Root "frontend/public"

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
  Push-Location $Root
  try {
    go run ./tools/iconset -input $icon -linux-dir (Join-Path $Root "build/linux/icons") -name cairn
    if ($LASTEXITCODE -ne 0) {
      throw "Linux icon generation failed with exit code $LASTEXITCODE"
    }
  } finally {
    Pop-Location
  }
}
