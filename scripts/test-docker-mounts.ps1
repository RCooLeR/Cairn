param()

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "get-docker-mounts.ps1")

function Assert-Equal([string]$Actual, [string]$Expected, [string]$Name) {
  if ($Actual -cne $Expected) {
    throw "$Name failed: expected '$Expected', got '$Actual'."
  }
}

function Assert-Fails([scriptblock]$Action, [string]$ExpectedMessage) {
  try {
    & $Action | Out-Null
  } catch {
    if ($_.Exception.Message -like "*$ExpectedMessage*") { return }
    throw
  }
  throw "Expected failure containing '$ExpectedMessage'."
}

$temporaryParent = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$fixture = Join-Path $temporaryParent ("cairn-docker-mounts-" + [guid]::NewGuid().ToString("N"))
$repository = Join-Path $fixture "repository space"
$source = Join-Path $repository "src"
$inside = Join-Path $source "internal/module space"
$shared = Join-Path $repository "shared module"
$sibling = Join-Path $fixture "sibling module"
$absolute = Join-Path $fixture "absolute module"
$goPath = Join-Path $fixture "go path"
$secondGoPath = Join-Path $fixture "second go path"
$oldGoPath = $env:GOPATH
$oldToolchain = $env:GOTOOLCHAIN

try {
  foreach ($path in @($inside, $shared, $sibling, $absolute, $goPath, $secondGoPath)) {
    New-Item -ItemType Directory -Path $path -Force | Out-Null
  }
  $modulePath = Join-Path $source "go.mod"
  $absoluteLiteral = $absolute.Replace([IO.Path]::DirectorySeparatorChar, [char]'/')
  $moduleText = @"
module example.test/cairn

go 1.18

replace (
  example.test/inside => "./internal/./module space"
  example.test/shared => "../shared module"
  example.test/sibling => "../../sibling module"
  example.test/absolute => "$absoluteLiteral"
  example.test/missing => "../not checked out"
  example.test/file => "./not-a-directory"
  example.test/versioned => example.test/remote v1.2.3
  example.test/windows => "C:/not-a-linux-path"
)
"@
  [IO.File]::WriteAllText($modulePath, $moduleText, [Text.UTF8Encoding]::new($false))
  [IO.File]::WriteAllText((Join-Path $source "not-a-directory"), "fixture")
  $env:GOPATH = $goPath + [IO.Path]::PathSeparator + $secondGoPath
  $env:GOTOOLCHAIN = "local"

  $before = (Get-FileHash -LiteralPath $modulePath -Algorithm SHA256).Hash
  # Run from outside the fixture to verify that caller cwd is irrelevant.
  $actual = Get-CairnDockerMounts -Root $repository
  $expected = @(
    (ConvertTo-CairnMountFlag (Join-Path $goPath "pkg/mod") "/go/pkg/mod"),
    (ConvertTo-CairnMountFlag $inside "/app/src/internal/module space" -ReadOnly),
    (ConvertTo-CairnMountFlag $shared "/app/shared module" -ReadOnly),
    (ConvertTo-CairnMountFlag $sibling "/sibling module" -ReadOnly)
  )
  if ([IO.Path]::DirectorySeparatorChar -eq '/') {
    $expected += ConvertTo-CairnMountFlag $absolute $absoluteLiteral -ReadOnly
  }
  Assert-Equal $actual ($expected -join " ") "Layout-aware replacement mounts"
  Assert-Equal (Get-FileHash -LiteralPath $modulePath -Algorithm SHA256).Hash $before "Read-only go.mod parsing"
  Assert-Equal (ConvertTo-CairnContainerPath "../shared") "/app/shared" "Module parent mapping"
  Assert-Equal (ConvertTo-CairnContainerPath "../../sibling") "/sibling" "Repository sibling mapping"
  Assert-Equal (ConvertTo-CairnContainerPath "../../../rooted") "/rooted" "Container root traversal clamp"
  Assert-Equal (ConvertTo-CairnContainerPath "/opt/local module") "/opt/local module" "Literal Unix destination"
  Assert-Equal (ConvertTo-CairnMountFlag "/host with spaces/module" "/app/src/module" -ReadOnly) '-v "/host with spaces/module:/app/src/module:ro"' "Quoted path with spaces"
  Assert-Fails { Get-CairnDockerMounts -Root $fixture } "src/go.mod"
  Assert-Fails { ConvertTo-CairnMountFlag '/host/$(unsafe)' "/app/src/module" } "shell expansions"
  Assert-Fails { ConvertTo-CairnMountFlag '/host/"unsafe' "/app/src/module" } "quotes"

  [IO.File]::WriteAllText($modulePath, "module example.test/empty`n`ngo 1.18`n", [Text.UTF8Encoding]::new($false))
  Assert-Equal (Get-CairnDockerMounts -Root $repository) (ConvertTo-CairnMountFlag (Join-Path $goPath "pkg/mod") "/go/pkg/mod") "No local replacements"
} finally {
  [Environment]::SetEnvironmentVariable("GOPATH", $oldGoPath, "Process")
  [Environment]::SetEnvironmentVariable("GOTOOLCHAIN", $oldToolchain, "Process")
  # Delete only this uniquely named fixture, after checking its exact parent.
  $resolvedFixture = [IO.Path]::GetFullPath($fixture)
  if ([IO.Path]::GetFullPath((Split-Path -Parent $resolvedFixture)).TrimEnd('\', '/') -ne $temporaryParent.TrimEnd('\', '/') -or
      (Split-Path -Leaf $resolvedFixture) -notmatch '^cairn-docker-mounts-[0-9a-f]{32}$') {
    throw "Refusing to remove an unexpected Docker-mount fixture path."
  }
  if (Test-Path -LiteralPath $resolvedFixture -PathType Container) {
    Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
  }
}

Write-Host "Docker mount helper tests passed."
