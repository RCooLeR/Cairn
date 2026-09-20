param(
  [string]$Root = ""
)

$ErrorActionPreference = "Stop"

function ConvertTo-CairnContainerPath([string]$Replacement) {
  # These are Linux container paths even when the host is Windows.
  if ($Replacement.StartsWith("/")) {
    return $Replacement
  }
  $parts = [System.Collections.Generic.List[string]]::new()
  foreach ($part in ("/app/src/" + $Replacement.Replace("\", "/")).Split("/")) {
    if ($part -eq "" -or $part -eq ".") { continue }
    if ($part -eq "..") {
      if ($parts.Count -gt 0) { $parts.RemoveAt($parts.Count - 1) }
    } else {
      $parts.Add($part)
    }
  }
  return "/" + ($parts -join "/")
}

function ConvertTo-CairnMountFlag([string]$HostPath, [string]$ContainerPath, [switch]$ReadOnly) {
  $hostDockerPath = $HostPath.Replace([IO.Path]::DirectorySeparatorChar, [char]'/')
  # The flags are interpolated into a Task shell command. Keep spaces quoted
  # and fail closed on characters whose expansion differs between host shells.
  if ($hostDockerPath -match '["$`\r\n]' -or $ContainerPath -match '["$`\r\n]') {
    throw "Docker mount paths cannot contain quotes, shell expansions, or line breaks."
  }
  $access = if ($ReadOnly) { ":ro" } else { "" }
  return '-v "' + $hostDockerPath + ':' + $ContainerPath + $access + '"'
}

function Invoke-CairnMountGo([string]$ModuleRoot, [string[]]$Arguments) {
  $output = & go -C $ModuleRoot @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "Reading Docker mount metadata from src/go.mod failed with exit code $LASTEXITCODE."
  }
  return ($output -join "`n").Trim()
}

function Get-CairnDockerMounts([string]$Root) {
  if ([string]::IsNullOrWhiteSpace($Root)) {
    $Root = Split-Path -Parent $PSScriptRoot
  }
  $moduleRoot = [IO.Path]::GetFullPath((Join-Path $Root "src"))
  if (!(Test-Path -LiteralPath (Join-Path $moduleRoot "go.mod") -PathType Leaf)) {
    throw "Docker mounts require a repository containing src/go.mod."
  }

  # Go's parser handles quoted paths and replace blocks without a second
  # module parser or downloads. With only -json, mod edit does not write files.
  $module = Invoke-CairnMountGo $moduleRoot @("mod", "edit", "-json") | ConvertFrom-Json
  $goPaths = (Invoke-CairnMountGo $moduleRoot @("env", "GOPATH")).Split([IO.Path]::PathSeparator)
  $mounts = [System.Collections.Generic.List[string]]::new()
  if ($goPaths.Count -gt 0 -and $goPaths[0] -ne "") {
    $cache = Join-Path $goPaths[0] "pkg/mod"
    $mounts.Add((ConvertTo-CairnMountFlag $cache "/go/pkg/mod"))
  }

  foreach ($replace in $module.Replace) {
    if (![string]::IsNullOrEmpty($replace.New.Version)) { continue }
    $replacement = [string]$replace.New.Path
    # Drive-letter paths cannot resolve to Linux container destinations.
    if ($replacement -match '^[A-Za-z]:') { continue }
    $hostPath = if ([IO.Path]::IsPathRooted($replacement)) {
      [IO.Path]::GetFullPath($replacement)
    } else {
      [IO.Path]::GetFullPath((Join-Path $moduleRoot $replacement))
    }
    # Match Wails' existing-only policy: optional sibling checkouts and files
    # are not mounted. Docker must not create missing replacement directories.
    if (!(Test-Path -LiteralPath $hostPath -PathType Container)) { continue }
    $destination = ConvertTo-CairnContainerPath $replacement
    $mounts.Add((ConvertTo-CairnMountFlag $hostPath $destination -ReadOnly))
  }
  return $mounts -join " "
}

if ($MyInvocation.InvocationName -ne ".") {
  Get-CairnDockerMounts -Root $Root
}
