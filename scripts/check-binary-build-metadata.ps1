param(
  [Parameter(Mandatory = $true)]
  [string]$Path,
  [string]$ExpectedCommit = "",
  [string]$ExpectedBuildDate = ""
)

$ErrorActionPreference = "Stop"

$resolvedPath = (Resolve-Path -LiteralPath $Path).Path
$expected = @(
  @{ Name = "commit"; Value = $ExpectedCommit },
  @{ Name = "build date"; Value = $ExpectedBuildDate }
) | Where-Object { ![string]::IsNullOrWhiteSpace([string]$_.Value) }

if ($expected.Count -eq 0) {
  Write-Host "Binary build metadata check skipped (no expected commit or build date): $resolvedPath"
  exit 0
}

# Go linker -X values are stored as UTF-8/ASCII strings in the executable. Reading
# once and using ordinal searches keeps this check portable across PE, ELF, and
# Mach-O artifacts without depending on platform-specific `strings` utilities.
$contents = [System.Text.Encoding]::ASCII.GetString(
  [System.IO.File]::ReadAllBytes($resolvedPath)
)
foreach ($entry in $expected) {
  $value = ([string]$entry.Value).Trim()
  if ($contents.IndexOf($value, [System.StringComparison]::Ordinal) -lt 0) {
    throw "Binary is missing expected embedded $($entry.Name) '$value': $resolvedPath"
  }
}

Write-Host (
  "Binary build metadata passed: {0} ({1})." -f
  $resolvedPath,
  (($expected | ForEach-Object { "$($_.Name) '$(([string]$_.Value).Trim())'" }) -join ", ")
)
