param(
  [switch]$PreflightOnly
)

$ErrorActionPreference = "Stop"

$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$root = (Resolve-Path (Join-Path $scriptDir "..")).Path
$distro = "cairn-dev"
$runningOnWindows = [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)

function Invoke-Step([string]$Name, [scriptblock]$Step) {
  Write-Host "==> $Name"
  $started = Get-Date
  & $Step
  $elapsed = (Get-Date) - $started
  Write-Host ("<== {0} passed in {1}" -f $Name, $elapsed.ToString("c"))
}

function Invoke-Native([string]$Name, [string]$Exe, [string[]]$Arguments) {
  $output = & $Exe @Arguments 2>&1
  if ($LASTEXITCODE -ne 0) {
    throw "$Name failed with exit code ${LASTEXITCODE}: $($output -join "`n")"
  }
  return (($output -join "`n") -replace "`0", "")
}

function Get-WindowsDockerContextState {
  $docker = Get-Command docker -ErrorAction SilentlyContinue
  if ($null -eq $docker) {
    return $null
  }
  $show = Invoke-Native "docker context show" $docker.Source @("context", "show")
  $list = Invoke-Native "docker context ls" $docker.Source @("context", "ls")
  return [pscustomobject]@{
    Show = $show.Trim()
    List = $list.Trim()
  }
}

function Assert-WindowsDockerContextUnchanged($Before, $After) {
  if ($null -eq $Before -and $null -eq $After) {
    Write-Host "Windows docker CLI unavailable; skipped Windows context immutability check."
    return
  }
  if ($null -eq $Before -or $null -eq $After) {
    throw "Windows docker CLI availability changed during WSL validation."
  }
  if ($Before.Show -ne $After.Show) {
    throw "Windows docker context changed: before=$($Before.Show) after=$($After.Show)"
  }
  if ($Before.List -ne $After.List) {
    throw "Windows docker context list changed during WSL validation."
  }
}

function Set-LocalGoEnvironment {
  $env:GOTOOLCHAIN = "local"
  $env:GOPATH = Join-Path $root ".gopath"
  $env:GOMODCACHE = Join-Path $root ".gomodcache"
  $env:GOCACHE = Join-Path $root ".gocache"
  foreach ($path in @($env:GOPATH, $env:GOMODCACHE, $env:GOCACHE)) {
    New-Item -ItemType Directory -Force -Path $path | Out-Null
  }
}

function Assert-GoToolchain {
  Invoke-Step "Go toolchain check" {
    Set-LocalGoEnvironment
    $go = Get-Command go -ErrorAction SilentlyContinue
    if ($null -eq $go) {
      throw "go was not found on PATH."
    }
    $version = Invoke-Native "go env GOVERSION" $go.Source @("env", "GOVERSION")
    if ($version.Trim() -ne "go1.26.4") {
      throw "Go toolchain is $($version.Trim()), want go1.26.4"
    }
  }
}

function Invoke-GoTest([string]$Name, [string[]]$Packages, [string[]]$GoArgs, [hashtable]$EnvVars) {
  Invoke-Step $Name {
    Set-LocalGoEnvironment
    $previous = @{}
    foreach ($key in $EnvVars.Keys) {
      $previous[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
      [Environment]::SetEnvironmentVariable($key, [string]$EnvVars[$key], "Process")
    }
    $tmp = Join-Path $env:TEMP ("cairn-go-tmp-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    $previousGoTmp = $env:GOTMPDIR
    $env:GOTMPDIR = $tmp
    Push-Location $root
    try {
      & go test @Packages @GoArgs
      if ($LASTEXITCODE -ne 0) {
        throw "go test failed with exit code $LASTEXITCODE"
      }
    } finally {
      Pop-Location
      $env:GOTMPDIR = $previousGoTmp
      foreach ($key in $EnvVars.Keys) {
        [Environment]::SetEnvironmentVariable($key, $previous[$key], "Process")
      }
      Start-Sleep -Milliseconds 500
      Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
  }
}

if (!$runningOnWindows) {
  throw "Windows WSL provider validation must run on Windows with the dedicated $distro WSL distro."
}

$wsl = Get-Command wsl.exe -ErrorAction SilentlyContinue
if ($null -eq $wsl) {
  throw "wsl.exe was not found."
}

Invoke-Step "cairn-dev WSL preflight" {
  $list = Invoke-Native "wsl -l -v" $wsl.Source @("-l", "-v")
  if ($list -notmatch "(?m)\b$([regex]::Escape($distro))\b\s+\S+\s+2\b") {
    throw "$distro was not found as a WSL2 distro in wsl -l -v output:`n$list"
  }

  $null = Invoke-Native "systemd check" $wsl.Source @("-d", $distro, "--", "test", "-d", "/run/systemd/system")
  $dockerPath = Invoke-Native "docker path check" $wsl.Source @("-d", $distro, "--", "sh", "-lc", "readlink -f /usr/bin/docker 2>/dev/null || true")
  if ($dockerPath -match "/mnt/wsl/docker-desktop") {
    throw "$distro docker resolves to Docker Desktop integration: $dockerPath"
  }
  $engine = Invoke-Native "docker engine check" $wsl.Source @("-d", $distro, "--", "docker", "info", "--format", "{{.ServerVersion}}")
  $compose = Invoke-Native "docker compose check" $wsl.Source @("-d", $distro, "--", "docker", "compose", "version", "--short")
  $buildx = Invoke-Native "docker buildx check" $wsl.Source @("-d", $distro, "--", "docker", "buildx", "version")
  Write-Host ("$distro Docker Engine {0}; Compose {1}; {2}" -f $engine.Trim(), $compose.Trim(), $buildx.Trim())
}

if ($PreflightOnly) {
  Write-Host "Windows WSL provider validation preflight passed."
  return
}

$beforeContext = Get-WindowsDockerContextState
try {
  Assert-GoToolchain
  Invoke-GoTest "WSL Docker SDK connection smoke" @("./internal/docker") @("-run", "TestWindowsWSLDockerConnection$", "-count=1", "-timeout=2m") @{
    CAIRN_REAL_WSL_DOCKER = "1"
  }
  Invoke-GoTest "WSL backup and restore smoke" @("./internal/backups") @("-tags=wslintegration", "-run", "TestManagerRealWSLDockerBackupRestoreRoundTrip$", "-count=1", "-timeout=3m") @{
    CAIRN_REAL_WSL_DOCKER_BACKUPS = "1"
  }
  Invoke-GoTest "WSL registry tag and push smoke" @("./internal/docker") @("-tags=wslintegration", "-run", "TestClientRealWSLRegistryTagPushRoundTrip$", "-count=1", "-timeout=6m") @{
    CAIRN_REAL_WSL_DOCKER_REGISTRY = "1"
  }
  Invoke-GoTest "WSL update and rebuild smoke" @("./internal/updates") @("-tags=wslintegration", "-run", "TestManagerRealWSLUpdateAndRebuildSmoke$", "-count=1", "-timeout=9m") @{
    CAIRN_REAL_WSL_DOCKER_UPDATES = "1"
  }
} finally {
  $afterContext = Get-WindowsDockerContextState
  Assert-WindowsDockerContextUnchanged $beforeContext $afterContext
}

Write-Host "Windows WSL provider validation passed against the dedicated cairn-dev distro."
