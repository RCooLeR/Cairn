param()

$ErrorActionPreference = "Stop"

$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$checker = Join-Path $scriptDir "check-soak-status.ps1"
if (!(Test-Path -LiteralPath $checker -PathType Leaf)) {
  throw "Soak checker not found: $checker"
}

function Write-TestFile([string]$Path, [string]$Content) {
  Set-Content -LiteralPath $Path -Encoding UTF8 -Value $Content.Trim()
}

function Invoke-CheckerSuccess([string]$Name, [hashtable]$CheckerParams) {
  & $checker @CheckerParams
  if (!$?) {
    throw "Expected success but checker failed: $Name"
  }
  Write-Host "PASS: $Name"
}

function Invoke-CheckerFailure([string]$Name, [hashtable]$CheckerParams, [string]$ExpectedMessage) {
  try {
    & $checker @CheckerParams
  } catch {
    if ($_.Exception.Message -like "*$ExpectedMessage*") {
      Write-Host "PASS: $Name"
      return
    }
    throw "Checker failed with unexpected message for $Name`: $($_.Exception.Message)"
  }
  throw "Expected checker failure but it passed: $Name"
}

$tmpRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("cairn-soak-checker-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmpRoot | Out-Null
try {
  $runningLog = Join-Path $tmpRoot "running.log"
  $runningStatus = Join-Path $tmpRoot "running.status"
  Write-TestFile $runningLog @"
phase 3 soak heartbeat: logs=1 stats=1 terminal_bytes=1 dashboard_reads=1 goroutines=20
phase 3 soak heartbeat: logs=2 stats=2 terminal_bytes=2 dashboard_reads=2 goroutines=21
"@
  Write-TestFile $runningStatus @"
state=running
"@
  Invoke-CheckerSuccess -Name "running soak heartbeat growth" -CheckerParams @{
    LogPath = $runningLog
    StatusPath = $runningStatus
  }

  $completeLog = Join-Path $tmpRoot "complete.log"
  $completeStatus = Join-Path $tmpRoot "complete.status"
  Write-TestFile $completeLog @"
phase 3 soak complete: duration=24h0m1s logs=10 stats=10 terminal_bytes=10 dashboard_reads=10 baseline_goroutines=20 peak_goroutines=24 final_goroutines=22
"@
  Write-TestFile $completeStatus @"
state=finished
exit_code=0
"@
  Invoke-CheckerSuccess -Name "completed 24h soak accepted" -CheckerParams @{
    LogPath = $completeLog
    StatusPath = $completeStatus
    RequireComplete = $true
  }

  $shortLog = Join-Path $tmpRoot "short.log"
  $shortStatus = Join-Path $tmpRoot "short.status"
  Write-TestFile $shortLog @"
phase 3 soak complete: duration=23h59m59s logs=10 stats=10 terminal_bytes=10 dashboard_reads=10 baseline_goroutines=20 peak_goroutines=24 final_goroutines=22
"@
  Write-TestFile $shortStatus @"
state=finished
exit_code=0
"@
  Invoke-CheckerFailure -Name "short completed soak rejected" -CheckerParams @{
    LogPath = $shortLog
    StatusPath = $shortStatus
    RequireComplete = $true
  } -ExpectedMessage "before required duration"

  $leakLog = Join-Path $tmpRoot "leak.log"
  $leakStatus = Join-Path $tmpRoot "leak.status"
  Write-TestFile $leakLog @"
phase 3 soak complete: duration=24h1s logs=10 stats=10 terminal_bytes=10 dashboard_reads=10 baseline_goroutines=20 peak_goroutines=40 final_goroutines=30
"@
  Write-TestFile $leakStatus @"
state=finished
exit_code=0
"@
  Invoke-CheckerFailure -Name "leaky completed soak rejected" -CheckerParams @{
    LogPath = $leakLog
    StatusPath = $leakStatus
    RequireComplete = $true
  } -ExpectedMessage "final goroutines exceed threshold"

  $failedStatus = Join-Path $tmpRoot "failed.status"
  Write-TestFile $failedStatus @"
state=finished
exit_code=1
"@
  Invoke-CheckerFailure -Name "failed status rejected" -CheckerParams @{
    LogPath = $completeLog
    StatusPath = $failedStatus
    RequireComplete = $true
  } -ExpectedMessage "non-zero exit_code=1"
} finally {
  Remove-Item -LiteralPath $tmpRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "Soak status checker smoke passed."
