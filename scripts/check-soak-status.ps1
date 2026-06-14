param(
  [Parameter(Mandatory = $true)]
  [string]$LogPath,
  [string]$StatusPath = "",
  [switch]$RequireComplete,
  [int]$MaxFinalGoroutineDelta = 8,
  [int]$MaxHeartbeatGoroutines = 128,
  [int]$MinLiveHeartbeats = 2,
  [int]$MaxLiveLogAgeMinutes = 10
)

$ErrorActionPreference = "Stop"

function Read-Status([string]$Path) {
  if ([string]::IsNullOrWhiteSpace($Path)) {
    return @{}
  }
  if (!(Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "Status file not found: $Path"
  }
  $status = @{}
  foreach ($line in Get-Content -LiteralPath $Path) {
    if ($line -match "^([^=]+)=(.*)$") {
      $status[$matches[1]] = $matches[2]
    }
  }
  return $status
}

function New-HeartbeatSample([System.Text.RegularExpressions.Match]$Match) {
  return [pscustomobject]@{
    Logs           = [int64]$Match.Groups[1].Value
    Stats          = [int64]$Match.Groups[2].Value
    TerminalBytes  = [int64]$Match.Groups[3].Value
    DashboardReads = [int64]$Match.Groups[4].Value
    Goroutines     = [int64]$Match.Groups[5].Value
  }
}

function Assert-Increased([string]$Name, [int64]$Previous, [int64]$Current) {
  if ($Current -le $Previous) {
    throw "$Name did not increase between the last two heartbeats: previous=$Previous current=$Current"
  }
}

if (!(Test-Path -LiteralPath $LogPath -PathType Leaf)) {
  throw "Soak log not found: $LogPath"
}

$logFile = Get-Item -LiteralPath $LogPath
$logText = Get-Content -LiteralPath $LogPath -Raw
$status = Read-Status $StatusPath

if ($status.ContainsKey("exit_code") -and $status["exit_code"] -ne "0") {
  throw "Soak status reports non-zero exit_code=$($status["exit_code"])"
}

$heartbeatPattern = "phase 3 soak heartbeat: logs=(\d+) stats=(\d+) terminal_bytes=(\d+) dashboard_reads=(\d+) goroutines=(\d+)"
$completePattern = "phase 3 soak complete: duration=([^\s]+) logs=(\d+) stats=(\d+) terminal_bytes=(\d+) dashboard_reads=(\d+) baseline_goroutines=(\d+) peak_goroutines=(\d+) final_goroutines=(\d+)"
$heartbeats = [regex]::Matches($logText, $heartbeatPattern)
$completions = [regex]::Matches($logText, $completePattern)

if ($completions.Count -gt 0) {
  $complete = $completions[$completions.Count - 1]
  $logs = [int64]$complete.Groups[2].Value
  $stats = [int64]$complete.Groups[3].Value
  $terminalBytes = [int64]$complete.Groups[4].Value
  $dashboardReads = [int64]$complete.Groups[5].Value
  $baselineGoroutines = [int64]$complete.Groups[6].Value
  $peakGoroutines = [int64]$complete.Groups[7].Value
  $finalGoroutines = [int64]$complete.Groups[8].Value
  $allowedFinal = $baselineGoroutines + $MaxFinalGoroutineDelta

  if ($logs -le 0 -or $stats -le 0 -or $terminalBytes -le 0 -or $dashboardReads -le 0) {
    throw "Soak completed without activity across every stream: logs=$logs stats=$stats terminal_bytes=$terminalBytes dashboard_reads=$dashboardReads"
  }
  if ($finalGoroutines -gt $allowedFinal) {
    throw "Soak final goroutines exceed threshold: baseline=$baselineGoroutines final=$finalGoroutines allowed=$allowedFinal"
  }
  if ($status.ContainsKey("state") -and $status["state"] -ne "finished") {
    throw "Soak log completed, but status state=$($status["state"])"
  }

  Write-Host ("Soak complete validated: duration={0} logs={1} stats={2} terminal_bytes={3} dashboard_reads={4} baseline_goroutines={5} peak_goroutines={6} final_goroutines={7}" -f
    $complete.Groups[1].Value, $logs, $stats, $terminalBytes, $dashboardReads, $baselineGoroutines, $peakGoroutines, $finalGoroutines)
  exit 0
}

if ($RequireComplete) {
  if ($status.ContainsKey("state")) {
    throw "Soak has not completed; status state=$($status["state"])"
  }
  throw "Soak has not completed; no 'phase 3 soak complete' line found."
}

if ($status.ContainsKey("state") -and $status["state"] -eq "finished") {
  throw "Soak status is finished but no completion line was found."
}
if ($heartbeats.Count -lt $MinLiveHeartbeats) {
  throw "Need at least $MinLiveHeartbeats heartbeat lines for live validation; found $($heartbeats.Count)."
}

$age = [DateTime]::UtcNow - $logFile.LastWriteTimeUtc
if ($age.TotalMinutes -gt $MaxLiveLogAgeMinutes) {
  throw ("Soak log is stale: age={0:n1}m max={1}m path={2}" -f $age.TotalMinutes, $MaxLiveLogAgeMinutes, $LogPath)
}

$previous = New-HeartbeatSample $heartbeats[$heartbeats.Count - 2]
$current = New-HeartbeatSample $heartbeats[$heartbeats.Count - 1]
Assert-Increased "logs" $previous.Logs $current.Logs
Assert-Increased "stats" $previous.Stats $current.Stats
Assert-Increased "terminal_bytes" $previous.TerminalBytes $current.TerminalBytes
Assert-Increased "dashboard_reads" $previous.DashboardReads $current.DashboardReads
if ($current.Goroutines -gt $MaxHeartbeatGoroutines) {
  throw "Heartbeat goroutines exceed live threshold: current=$($current.Goroutines) max=$MaxHeartbeatGoroutines"
}

Write-Host ("Soak running validated: logs={0} stats={1} terminal_bytes={2} dashboard_reads={3} goroutines={4} heartbeat_age={5:n1}m" -f
  $current.Logs, $current.Stats, $current.TerminalBytes, $current.DashboardReads, $current.Goroutines, $age.TotalMinutes)
