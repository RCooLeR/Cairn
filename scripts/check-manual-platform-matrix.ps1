param(
  [string]$SpecPath = "",
  [string]$ManualPath = ""
)

$ErrorActionPreference = "Stop"

$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$root = (Resolve-Path (Join-Path $scriptDir "..")).Path
if ([string]::IsNullOrWhiteSpace($SpecPath)) {
  $candidateSpec = Join-Path $root "dev-docs/06-testing.md"
  if (Test-Path -LiteralPath $candidateSpec -PathType Leaf) {
    $SpecPath = $candidateSpec
  }
}
if ([string]::IsNullOrWhiteSpace($ManualPath)) {
  $ManualPath = Join-Path $root "docs/manual-platform-validation.md"
}

function Normalize-Text([string]$Value) {
  return (($Value -replace "[`*_]", "") -replace "\s+", " ").ToLowerInvariant().Trim()
}

function Read-PlatformSpec([string]$Path) {
  if (!(Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "Spec file not found: $Path"
  }
  $lines = Get-Content -LiteralPath $Path -Encoding UTF8
  $platform = @{}
  foreach ($line in $lines) {
    if ($line -match "^\*\*(Windows|Linux|macOS):\*\*\s*(.+)$") {
      $platform[$matches[1]] = Normalize-Text $matches[2]
    }
  }
  foreach ($key in @("Windows", "Linux", "macOS")) {
    if (!$platform.ContainsKey($key)) {
      throw "Missing platform matrix line for $key in $Path"
    }
  }
  return $platform
}

function Read-FullMatrixTodo([string]$Path) {
  if (!(Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "Manual platform TODO file not found: $Path"
  }

  $lines = Get-Content -LiteralPath $Path -Encoding UTF8
  $sectionIndex = -1
  for ($i = 0; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match "^## Full Platform Matrix TODO\s*$") {
      $sectionIndex = $i
      break
    }
  }
  if ($sectionIndex -lt 0) {
    throw "Could not find '## Full Platform Matrix TODO' in $Path"
  }

  $sectionLines = [System.Collections.Generic.List[string]]::new()
  for ($i = $sectionIndex + 1; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match "^##\s+") {
      break
    }
    if (![string]::IsNullOrWhiteSpace($lines[$i])) {
      $sectionLines.Add($lines[$i])
    }
  }
  if ($sectionLines.Count -eq 0) {
    throw "Full Platform Matrix TODO section is empty in $Path"
  }
  return Normalize-Text ($sectionLines -join " ")
}

function Assert-Phrases([string]$Platform, [string]$Text, [string[]]$Phrases, [System.Collections.Generic.List[string]]$Errors) {
  foreach ($phrase in $Phrases) {
    $normalizedPhrase = Normalize-Text $phrase
    if (!$Text.Contains($normalizedPhrase)) {
      $Errors.Add("$Platform matrix TODO is missing phrase: $phrase")
    }
  }
}

if (![string]::IsNullOrWhiteSpace($SpecPath)) {
  $null = Read-PlatformSpec $SpecPath
} else {
  Write-Host "dev-docs/06-testing.md not present; validating committed manual platform matrix phrases only."
}
$todoText = Read-FullMatrixTodo $ManualPath
$errors = [System.Collections.Generic.List[string]]::new()

Assert-Phrases "Windows" $todoText @(
  "Windows 11 x64",
  "WSL present/absent",
  "Ubuntu present/absent/multiple",
  "Docker in Ubuntu present/absent",
  "systemd on/off",
  "Docker Desktop installed",
  "docker-desktop",
  "desktop-linux"
) $errors

Assert-Phrases "Linux" $todoText @(
  "Ubuntu LTS",
  "Debian stable",
  "Docker present/absent",
  "user in/not in docker group",
  "service stopped",
  "rootless"
) $errors

Assert-Phrases "macOS" $todoText @(
  "Apple Silicon",
  "Intel best-effort",
  "Homebrew present/absent",
  "Colima present/absent",
  "existing Docker Desktop context",
  "remote context"
) $errors

if ($errors.Count -gt 0) {
  foreach ($errorItem in $errors) {
    Write-Error $errorItem
  }
  throw "manual platform matrix validation failed with $($errors.Count) issue(s)."
}

Write-Host "manual platform matrix validated."
