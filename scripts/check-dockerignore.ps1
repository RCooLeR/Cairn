param()

$ErrorActionPreference = "Stop"

$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$root = (Resolve-Path (Join-Path $scriptDir "..")).Path

function ConvertTo-DockerPatternRegex([string]$Pattern, [string]$Source) {
  if ($Pattern.Contains("\")) {
    throw "$Source uses a backslash pattern that this deterministic checker cannot validate: $Pattern"
  }
  if ($Pattern.Contains("[") -or $Pattern.Contains("]")) {
    throw "$Source uses a character-class pattern that this deterministic checker cannot validate: $Pattern"
  }

  $builder = [System.Text.StringBuilder]::new()
  [void]$builder.Append("^")
  for ($index = 0; $index -lt $Pattern.Length; $index++) {
    $character = $Pattern[$index]
    if ($character -eq "*") {
      if ($index + 1 -lt $Pattern.Length -and $Pattern[$index + 1] -eq "*") {
        while ($index + 1 -lt $Pattern.Length -and $Pattern[$index + 1] -eq "*") {
          $index++
        }
        if ($index + 1 -lt $Pattern.Length -and $Pattern[$index + 1] -eq "/") {
          [void]$builder.Append("(?:.*/)?")
          $index++
        } else {
          [void]$builder.Append(".*")
        }
      } else {
        [void]$builder.Append("[^/]*")
      }
      continue
    }
    if ($character -eq "?") {
      [void]$builder.Append("[^/]")
      continue
    }
    [void]$builder.Append([regex]::Escape([string]$character))
  }
  [void]$builder.Append("$")
  return [regex]::new($builder.ToString(), [System.Text.RegularExpressions.RegexOptions]::CultureInvariant)
}

function Read-DockerIgnoreRules([string]$Path) {
  $rules = [System.Collections.Generic.List[object]]::new()
  $lineNumber = 0
  foreach ($rawLine in Get-Content -LiteralPath $Path) {
    $lineNumber++
    if ($rawLine.StartsWith("#")) {
      continue
    }
    $line = $rawLine.Trim()
    if ($line.Length -eq 0) {
      continue
    }

    $negated = $line.StartsWith("!")
    if ($negated) {
      $line = $line.Substring(1).Trim()
      if ($line.Length -eq 0) {
        throw "${Path}:${lineNumber} contains an empty negation rule."
      }
    }

    $line = $line.Trim("/")
    if ($line -eq ".") {
      continue
    }
    if ($line.Length -eq 0 -or $line -match '(^|/)\.\.(/|$)') {
      throw "${Path}:${lineNumber} contains an invalid Docker ignore rule."
    }

    $rules.Add([pscustomobject]@{
        Pattern = $line
        Negated = $negated
        Matcher = ConvertTo-DockerPatternRegex $line "${Path}:${lineNumber}"
      })
  }
  return $rules
}

function Test-DockerPathIgnored([object[]]$Rules, [string]$Path) {
  $normalized = $Path.Replace("\", "/").Trim("/")
  if ($normalized.Length -eq 0 -or $normalized -match '(^|/)\.\.(/|$)') {
    throw "Invalid sentinel path: $Path"
  }

  $candidates = [System.Collections.Generic.List[string]]::new()
  $candidate = $normalized
  while ($candidate.Length -gt 0) {
    $candidates.Add($candidate)
    $separator = $candidate.LastIndexOf("/")
    if ($separator -lt 0) {
      break
    }
    $candidate = $candidate.Substring(0, $separator)
  }

  $ignored = $false
  foreach ($rule in $Rules) {
    $matched = $false
    foreach ($item in $candidates) {
      if ($rule.Matcher.IsMatch($item)) {
        $matched = $true
        break
      }
    }
    if ($matched) {
      $ignored = -not $rule.Negated
    }
  }
  return $ignored
}

function Assert-DockerIgnorePolicy(
  [string]$Name,
  [object[]]$Rules,
  [string[]]$Excluded,
  [string[]]$Included
) {
  $failures = [System.Collections.Generic.List[string]]::new()
  foreach ($path in $Excluded) {
    if (-not (Test-DockerPathIgnored $Rules $path)) {
      $failures.Add("$Name unexpectedly includes sensitive/local path '$path'.")
    }
  }
  foreach ($path in $Included) {
    if (Test-DockerPathIgnored $Rules $path) {
      $failures.Add("$Name unexpectedly excludes required input '$path'.")
    }
  }
  if ($failures.Count -gt 0) {
    throw ($failures -join [Environment]::NewLine)
  }
}

$rootRules = Read-DockerIgnoreRules (Join-Path $root ".dockerignore")
$secretNames = @(
  ".env",
  ".env.local",
  "app.env",
  "app.env.production",
  ".npmrc",
  ".netrc",
  "client.key",
  "certificate.pem",
  "signing.pfx",
  "signing.p12"
)
$secretPaths = foreach ($prefix in @("", "project", "deep/project")) {
  foreach ($name in $secretNames) {
    if ($prefix.Length -eq 0) { $name } else { "$prefix/$name" }
  }
}
$localPaths = @(
  ".git/config",
  "project/.git/config",
  ".cairn/cairn.db",
  "project/.cairn/cairn.db",
  ".release-version.env",
  ".docker/config.json",
  "project/.docker/config.json",
  ".aws/credentials",
  "project/.ssh/id_ed25519",
  ".idea/workspace.xml",
  ".vscode/settings.json",
  ".agents/session.json",
  ".claude/settings.json",
  ".codex/state.json",
  ".cache/go-build/object",
  ".scratch/release-evidence.txt",
  ".task/state.json",
  ".gocache/object",
  ".gomodcache/module.zip",
  ".gopath/bin/tool",
  ".gotmp/work",
  "col-review/00-review.md",
  "project/col-review/00-review.md",
  "claude-review.md",
  "notes/review.md",
  "frontend/node_modules/react/index.js",
  "frontend/dist/assets/app.js",
  "coverage/report.json",
  "frontend/test-results/results.json",
  "state/cairn.db-wal",
  "logs/cairn.log",
  "logs/diagnostics.json",
  "project/logs/diagnostics.json",
  "tmp/staging.tmp",
  "backups/file.bak"
)
$requiredRootInputs = @(
  "go.mod",
  "go.sum",
  "main.go",
  "README.md",
  "internal/services/services.go",
  "frontend/package.json",
  "frontend/package-lock.json",
  "frontend/src/App.tsx",
  "build/config.yml",
  "build/docker/Dockerfile.cross",
  "scripts/check-server-mode-containment.ps1",
  "testdata/projects/build-simple/compose.yaml"
)
Assert-DockerIgnorePolicy "root Docker context" $rootRules ($secretPaths + $localPaths) $requiredRootInputs

$crossSpecificIgnore = Join-Path $root "build/docker/Dockerfile.cross.dockerignore"
if (Test-Path -LiteralPath $crossSpecificIgnore) {
  throw "Dockerfile.cross.dockerignore overrides build/docker/.dockerignore; validate and use one canonical cross-toolchain policy."
}

$crossRules = Read-DockerIgnoreRules (Join-Path $root "build/docker/.dockerignore")
Assert-DockerIgnorePolicy "cross-toolchain Docker context" $crossRules @(
  ".env",
  "local.txt",
  "nested/local.txt",
  "Dockerfile.server"
) @("Dockerfile.cross")

$crossDockerfilePath = Join-Path $root "build/docker/Dockerfile.cross"
$crossDockerfile = Get-Content -LiteralPath $crossDockerfilePath -Raw
if ($crossDockerfile -match '(?m)^\s*[^#\r\n].*\b(?:nodejs|npm)\b') {
  throw "Dockerfile.cross must not install or invoke an unpinned container Node/npm toolchain."
}
if ($crossDockerfile -notmatch '\[\s*!\s+-f\s+"frontend/dist/index\.html"\s*\]') {
  throw "Dockerfile.cross must fail closed unless the host produced frontend/dist/index.html."
}
if ($crossDockerfile -match '\[\s*!\s+-d\s+"frontend/dist"\s*\]') {
  throw "Dockerfile.cross must not treat the tracked frontend/dist directory as a completed build."
}

Write-Host "Docker build-input policy passed: secret/local sentinels are excluded and cross builds require production frontend assets."
