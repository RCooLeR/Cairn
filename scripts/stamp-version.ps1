param(
  [Parameter(Mandatory = $true)]
  [string]$Version,
  [string]$Commit = $env:GITHUB_SHA,
  [string]$BuildDate = "",
  [string]$Root = ""
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($Root)) {
  $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
  $Root = (Resolve-Path (Join-Path $scriptDir "..")).Path
}

function Normalize-Version([string]$Raw) {
  $value = $Raw.Trim()
  if ($value.StartsWith("refs/tags/")) {
    $value = $value.Substring("refs/tags/".Length)
  }
  if ($value.StartsWith("v")) {
    $value = $value.Substring(1)
  }
  if ($value -notmatch "^\d+\.\d+\.\d+([+-][0-9A-Za-z.-]+)?$") {
    throw "Version '$Raw' must be a semver tag like v1.0.0"
  }
  return $value
}

function File-Version([string]$Semver) {
  return ($Semver -replace "[+-].*$", "") + ".0"
}

function Set-Text([string]$RelativePath, [string]$Text) {
  $path = Join-Path $Root $RelativePath
  $encoding = [System.Text.UTF8Encoding]::new($false)
  [System.IO.File]::WriteAllText($path, $Text, $encoding)
}

function Update-Text([string]$RelativePath, [scriptblock]$Mutate) {
  $path = Join-Path $Root $RelativePath
  $text = [System.IO.File]::ReadAllText($path)
  $next = & $Mutate $text
  if ($next -eq $null) {
    throw "Mutation for $RelativePath returned null"
  }
  Set-Text $RelativePath $next
}

$semver = Normalize-Version $Version
$fileVersion = File-Version $semver
if ([string]::IsNullOrWhiteSpace($BuildDate)) {
  $BuildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
}

Update-Text "build/config.yml" {
  param($text)
  $text -replace 'version: "\d+\.\d+\.\d+(?:[+-][0-9A-Za-z.-]+)?" # The application version', "version: `"$semver`" # The application version"
}

Update-Text "build/windows/info.json" {
  param($text)
  $text = $text -replace '"file_version": "\d+\.\d+\.\d+"', "`"file_version`": `"$semver`""
  $text = $text -replace '"ProductVersion": "\d+\.\d+\.\d+"', "`"ProductVersion`": `"$semver`""
  $text = $text -replace '"FileVersion": "\d+\.\d+\.\d+"', "`"FileVersion`": `"$semver`""
  $text
}

Update-Text "build/windows/wails.exe.manifest" {
  param($text)
  $text -replace '(<assemblyIdentity type="win32" name="app\.cairn\.desktop" version=")[^"]+(")', "`${1}$fileVersion`$2"
}

Update-Text "build/windows/nsis/wails_tools.nsh" {
  param($text)
  $text = $text -replace '!define INFO_PRODUCTVERSION "\d+\.\d+\.\d+(?:[+-][0-9A-Za-z.-]+)?"', "!define INFO_PRODUCTVERSION `"$semver`""
  $text
}

foreach ($plist in @("build/darwin/Info.plist", "build/darwin/Info.dev.plist")) {
  Update-Text $plist {
    param($text)
    $text = $text -replace '<key>CFBundleVersion</key>\s*<string>[^<]+</string>', "<key>CFBundleVersion</key>`n            <string>$semver</string>"
    $text = $text -replace '<key>CFBundleShortVersionString</key>\s*<string>[^<]+</string>', "<key>CFBundleShortVersionString</key>`n            <string>$semver</string>"
    $text
  }
}

Update-Text "build/linux/nfpm/nfpm.yaml" {
  param($text)
  $text -replace 'version: "\d+\.\d+\.\d+(?:[+-][0-9A-Za-z.-]+)?"', "version: `"$semver`""
}

$envFile = Join-Path $Root ".release-version.env"
Set-Text ".release-version.env" "CAIRN_VERSION=$semver`nCAIRN_COMMIT=$Commit`nCAIRN_BUILD_DATE=$BuildDate`n"
Write-Host "Stamped Cairn $semver ($Commit $BuildDate)"
