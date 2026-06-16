param(
  [ValidateSet("checklist", "manual-matrix", "soak-checker", "upgrade-fixtures", "security", "performance", "soak-smoke", "soak-24h", "ui-release", "wsl-provider", "debian-deb-container")]
  [string[]]$Suite = @("checklist", "manual-matrix", "soak-checker", "security", "performance", "soak-smoke"),
  [string]$SoakDuration = "30s",
  [string]$SoakTimeout = "5m"
)

$ErrorActionPreference = "Stop"

$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$root = (Resolve-Path (Join-Path $scriptDir "..")).Path
$runningOnLinux = [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Linux)

function Invoke-ReleaseStep([string]$Name, [scriptblock]$Step) {
  Write-Host "==> $Name"
  $started = Get-Date
  & $Step
  $elapsed = (Get-Date) - $started
  Write-Host ("<== {0} passed in {1}" -f $Name, $elapsed.ToString("c"))
}

function Invoke-GoTest([string[]]$Packages, [string[]]$GoArgs) {
  Push-Location $root
  try {
    & go test @Packages @GoArgs
    if ($LASTEXITCODE -ne 0) {
      throw "go test failed with exit code $LASTEXITCODE"
    }
  } finally {
    Pop-Location
  }
}

function Invoke-GoTestNames([string]$Package, [string[]]$TestNames, [string]$Timeout = "3m") {
  if ($TestNames.Count -eq 0) {
    throw "No tests configured for $Package"
  }
  $escapedNames = $TestNames | ForEach-Object { [regex]::Escape($_) }
  $runPattern = "^({0})$" -f ($escapedNames -join "|")
  $started = New-Object "System.Collections.Generic.HashSet[string]"
  $output = @()

  Push-Location $root
  try {
    $output = & go test $Package -json -run $runPattern -count=1 "-timeout=$Timeout" 2>&1
    $exitCode = $LASTEXITCODE
  } finally {
    Pop-Location
  }

  foreach ($line in $output) {
    if ([string]::IsNullOrWhiteSpace($line)) {
      continue
    }
    try {
      $event = $line | ConvertFrom-Json -ErrorAction Stop
    } catch {
      continue
    }
    if ($event.Action -eq "run" -and $event.Test) {
      [void]$started.Add([string]$event.Test)
    }
  }

  if ($exitCode -ne 0) {
    $output | ForEach-Object { Write-Host $_ }
    throw "go test failed for $Package with exit code $exitCode"
  }

  $missing = @($TestNames | Where-Object { !$started.Contains($_) })
  if ($missing.Count -gt 0) {
    throw "Security suite for $Package did not start expected test(s): $($missing -join ', ')"
  }
}

function Invoke-FrontendNpm([string[]]$NpmArgs) {
  Push-Location (Join-Path $root "frontend")
  try {
    & npm @NpmArgs
    if ($LASTEXITCODE -ne 0) {
      throw "npm failed with exit code $LASTEXITCODE"
    }
  } finally {
    Pop-Location
  }
}

foreach ($item in $Suite) {
  switch ($item) {
    "checklist" {
      Invoke-ReleaseStep "v1 release checklist evidence ledger" {
        & (Join-Path $scriptDir "check-v1-release-checklist.ps1")
        if (!$?) {
          throw "check-v1-release-checklist failed"
        }
      }
    }
    "manual-matrix" {
      Invoke-ReleaseStep "manual platform matrix TODO ledger" {
        & (Join-Path $scriptDir "check-manual-platform-matrix.ps1")
        if (!$?) {
          throw "check-manual-platform-matrix failed"
        }
      }
    }
    "soak-checker" {
      Invoke-ReleaseStep "soak status checker smoke" {
        & (Join-Path $scriptDir "test-soak-status-checker.ps1")
        if (!$?) {
          throw "test-soak-status-checker failed"
        }
      }
    }
    "upgrade-fixtures" {
      Invoke-ReleaseStep "release DB upgrade fixtures" {
        Invoke-GoTest -Packages @("./internal/store") -GoArgs @("-run", "TestReleaseDBFixtureUpgrade$", "-count=1", "-timeout=30s")
      }
    }
    "security" {
      Invoke-ReleaseStep "security policy review tests" {
        $securityTests = @(
          @{
            Package = "./internal/security"
            Tests = @(
              "TestContainerRiskMapping",
              "TestPlanStoreExpiresAndRequiresTypedName",
              "TestRequireConfirmationTrimsTypedNameAndAllowsSafePlans",
              "TestPlanStoreContextAndExpiry",
              "TestNewContainerActionPlanCommandsEffectsAndRisks",
              "TestNewContainerActionPlanValidationAndFallbackLabels",
              "TestProjectPlanStoreTake",
              "TestPlanStoresPruneExpiredEntriesOnSave",
              "TestPlanStoreRejectsHighRiskWithoutTypedName",
              "TestDockerObjectPlansDeclareTypedConfirmationForHighRisk",
              "TestNewProjectActionPlanRequiresTypedConfirmationForHighRisk"
            )
          }
          @{
            Package = "./internal/providers"
            Tests = @("TestExistingContextDetectHealthyWithUnencryptedTCPWarning")
          }
          @{
            Package = "./internal/registry"
            Tests = @(
              "TestLoginPipesSecretThroughStdin",
              "TestRegistryCLIArgRejectsFlagLikeHosts",
              "TestPlainHTTPRegistryRequiresExactLoopbackHost"
            )
          }
          @{
            Package = "./internal/docker"
            Tests = @("TestClientObjectsDTOsRawInspectAndCacheReconcile")
          }
          @{
            Package = "./internal/services"
            Tests = @(
              "TestSettingsServiceGetCheatsheetSafetyContract",
              "TestDockerServiceLifecycleAuditsAndPlans",
              "TestProjectServicePlanDownWithVolumesRequiresTypedName"
            )
          }
          @{
            Package = "./internal/store"
            Tests = @(
              "TestOpenCreatesPrivateStoreDirectory",
              "TestSettingsDefaultsAndRoundTrip",
              "TestAuditListEscapesTopicAndPreservesZeroDuration"
            )
          }
          @{
            Package = "./internal/updates"
            Tests = @("TestManagerApplyUpdateHealthFailureRollsBack")
          }
          @{
            Package = "./internal/backups"
            Tests = @("TestRestoreOverwriteRequiresTypedNameAndRunsHelper")
          }
          @{
            Package = "./internal/terminal"
            Tests = @("TestCheatsheetRisksMatchSecurityPolicy")
          }
        )
        foreach ($entry in $securityTests) {
          Invoke-GoTestNames -Package $entry.Package -TestNames $entry.Tests -Timeout "3m"
        }
      }
    }
    "performance" {
      Invoke-ReleaseStep "seed-scale performance tests" {
        Invoke-GoTest -Packages @("./internal/metrics") -GoArgs @("-run", "TestManagerSeedScaleDashboardPerformanceAndGoroutines$", "-count=1", "-timeout=30s")
      }
    }
    "soak-smoke" {
      Invoke-ReleaseStep "short active-stream soak smoke" {
        if (!$runningOnLinux) {
          Write-Host "Skipping soak-smoke on non-Linux runner; the soak harness is Linux-native."
          return
        }
        $env:CAIRN_PHASE3_SOAK = "1"
        $env:CAIRN_PHASE3_SOAK_DURATION = $SoakDuration
        try {
          Invoke-GoTest -Packages @("./internal/soak") -GoArgs @("-run", "TestPhase3StreamsTerminalDashboardSoak$", "-count=1", "-timeout=$SoakTimeout")
        } finally {
          Remove-Item Env:\CAIRN_PHASE3_SOAK -ErrorAction SilentlyContinue
          Remove-Item Env:\CAIRN_PHASE3_SOAK_DURATION -ErrorAction SilentlyContinue
        }
      }
    }
    "soak-24h" {
      Invoke-ReleaseStep "24h active-stream soak" {
        if (!$runningOnLinux) {
          throw "soak-24h must run on a Linux host with Docker Engine available"
        }
        $env:CAIRN_PHASE3_SOAK = "1"
        $env:CAIRN_PHASE3_SOAK_DURATION = $SoakDuration
        try {
          Invoke-GoTest -Packages @("./internal/soak") -GoArgs @("-run", "TestPhase3StreamsTerminalDashboardSoak$", "-count=1", "-timeout=$SoakTimeout")
        } finally {
          Remove-Item Env:\CAIRN_PHASE3_SOAK -ErrorAction SilentlyContinue
          Remove-Item Env:\CAIRN_PHASE3_SOAK_DURATION -ErrorAction SilentlyContinue
        }
      }
    }
    "ui-release" {
      Invoke-ReleaseStep "release UI visual and axe smoke" {
        Invoke-FrontendNpm -NpmArgs @("run", "test:release-ui")
      }
    }
    "wsl-provider" {
      Invoke-ReleaseStep "Windows WSL provider validation" {
        & (Join-Path $scriptDir "run-wsl-provider-validation.ps1")
        if (!$?) {
          throw "run-wsl-provider-validation failed"
        }
      }
    }
    "debian-deb-container" {
      Invoke-ReleaseStep "Debian stable container deb install/uninstall smoke" {
        & (Join-Path $scriptDir "test-debian-container-deb-install.ps1")
        if (!$?) {
          throw "test-debian-container-deb-install failed"
        }
      }
    }
  }
}
