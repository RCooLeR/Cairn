param(
  [string]$SpecPath = "",
  [string]$ChecklistPath = ""
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
if ([string]::IsNullOrWhiteSpace($ChecklistPath)) {
  $ChecklistPath = Join-Path $root "docs/v1-release-checklist.md"
}

function Read-NormativeChecklistItems([string]$Path) {
  if (!(Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "Spec file not found: $Path"
  }

  $lines = Get-Content -LiteralPath $Path -Encoding UTF8
  $sectionIndex = -1
  for ($i = 0; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match "^## 9\. v1 release checklist\s*$") {
      $sectionIndex = $i
      break
    }
  }
  if ($sectionIndex -lt 0) {
    throw "Could not find '## 9. v1 release checklist' in $Path"
  }

  $fenceStart = -1
  for ($i = $sectionIndex + 1; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match '^```') {
      $fenceStart = $i
      break
    }
  }
  if ($fenceStart -lt 0) {
    throw "Could not find opening checklist code fence in $Path"
  }

  $items = [System.Collections.Generic.List[string]]::new()
  for ($i = $fenceStart + 1; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match '^```') {
      break
    }
    $line = $lines[$i].Trim()
    if ([string]::IsNullOrWhiteSpace($line)) {
      continue
    }
    if ($line.Length -lt 2) {
      throw "Malformed checklist line in $Path`: '$line'"
    }
    $item = ConvertTo-ChecklistIdentity $line.Substring(1).Trim()
    if ([string]::IsNullOrWhiteSpace($item)) {
      throw "Malformed checklist item in $Path`: '$line'"
    }
    $items.Add($item)
  }

  if ($items.Count -eq 0) {
    throw "No checklist items found in $Path"
  }
  return $items.ToArray()
}

function Read-EvidenceRows([string]$Path) {
  if (!(Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "Checklist evidence file not found: $Path"
  }

  $rows = [System.Collections.Generic.List[object]]::new()
  foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
    if ($line -notmatch "^\|") {
      continue
    }
    if ($line -match "^\|\s*-+\s*\|") {
      continue
    }
    $cells = $line.Trim().Trim("|").Split("|") | ForEach-Object { $_.Trim() }
    if ($cells.Count -lt 4) {
      continue
    }
    if ($cells[0] -eq "Checklist item") {
      continue
    }
    $rows.Add([pscustomobject]@{
      Item      = ConvertTo-ChecklistIdentity $cells[0]
      Status    = $cells[1]
      Evidence  = $cells[2]
      Remaining = $cells[3]
    })
  }

  if ($rows.Count -eq 0) {
    throw "No evidence rows found in $Path"
  }
  return $rows.ToArray()
}

function ConvertTo-ChecklistIdentity([string]$Value) {
  $normalized = $Value -replace "\s+", " "
  $normalized = $normalized -replace "\u00a7\s*", "section "
  return $normalized.Trim()
}

function Default-ChecklistItems {
  return @(
    "All P0 features pass on Linux native",
    "All P0 features pass on Windows WSL",
    "All P0 features pass on macOS (Colima + existing context)",
    "Update + lineage + registry-account test suite green (section 5 all 14 cases)",
    "Visual-regression and axe accessibility suites green",
    "No destructive action without confirmation (section 6 suite green)",
    "No plaintext secrets anywhere (redaction suite green)",
    "No Docker TCP exposure configured by Cairn",
    "Performance targets met at seeded scale (section 7)",
    "Installers install/uninstall cleanly on all 3 OS",
    "App handles Docker-stopped state gracefully on all pages",
    "Crash-free soak: 24 h run with active streams, zero goroutine leaks (pprof check)"
  ) | ForEach-Object { ConvertTo-ChecklistIdentity $_ }
}

if (![string]::IsNullOrWhiteSpace($SpecPath)) {
  $expectedItems = Read-NormativeChecklistItems $SpecPath
} else {
  Write-Host "dev-docs/06-testing.md not present; validating against the embedded v1 checklist item set."
  $expectedItems = Default-ChecklistItems
}
$rows = Read-EvidenceRows $ChecklistPath
$allowedStatuses = @("green", "in_progress", "blocked_by_platform")
$errors = [System.Collections.Generic.List[string]]::new()

$rowByItem = @{}
foreach ($row in $rows) {
  if ($rowByItem.ContainsKey($row.Item)) {
    $errors.Add("Duplicate evidence row: $($row.Item)")
    continue
  }
  $rowByItem[$row.Item] = $row

  if ($allowedStatuses -notcontains $row.Status) {
    $errors.Add("Unknown status '$($row.Status)' for item '$($row.Item)'")
  }
  if ([string]::IsNullOrWhiteSpace($row.Evidence)) {
    $errors.Add("Missing evidence text for item '$($row.Item)'")
  }
  if ([string]::IsNullOrWhiteSpace($row.Remaining)) {
    $errors.Add("Missing remaining-proof text for item '$($row.Item)'")
  }
}

foreach ($item in $expectedItems) {
  if (!$rowByItem.ContainsKey($item)) {
    $errors.Add("Missing evidence row for normative item: $item")
  }
}

$expectedSet = @{}
foreach ($item in $expectedItems) {
  $expectedSet[$item] = $true
}
foreach ($row in $rows) {
  if (!$expectedSet.ContainsKey($row.Item)) {
    $errors.Add("Evidence row is not present in normative checklist: $($row.Item)")
  }
}

if ($rows.Count -ne $expectedItems.Count) {
  $errors.Add("Evidence row count $($rows.Count) does not match normative item count $($expectedItems.Count)")
}

if ($errors.Count -gt 0) {
  foreach ($errorItem in $errors) {
    Write-Error $errorItem
  }
  throw "v1 release checklist evidence validation failed with $($errors.Count) issue(s)."
}

Write-Host "v1 release checklist evidence validated: $($rows.Count) normative items mirrored."
