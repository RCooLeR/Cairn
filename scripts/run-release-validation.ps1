param(
  [ValidateSet("checklist", "manual-matrix", "soak-checker", "security", "performance", "soak-smoke", "soak-24h", "ui-release")]
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
    "security" {
      Invoke-ReleaseStep "security policy review tests" {
        $packages = @(
          "./internal/security",
          "./internal/providers",
          "./internal/registry",
          "./internal/docker",
          "./internal/services",
          "./internal/store",
          "./internal/updates",
          "./internal/backups",
          "./internal/terminal"
        )
        $run = "Test(ContainerRiskMapping|PlanStoreExpiresAndRequiresTypedName|ExistingContextDetectHealthyWithUnencryptedTCPWarning|LoginPipesSecretThroughStdin|ClientObjectsDTOsRawInspectAndCacheReconcile|DockerServiceLifecycleAuditsAndPlans|ProjectServicePlanDownWithVolumesRequiresTypedName|SettingsDefaultsAndRoundTrip|ManagerApplyUpdateHealthFailureRollsBack|RestoreOverwriteRequiresTypedNameAndRunsHelper|CheatsheetRisksMatchSecurityPolicy)$"
        Invoke-GoTest -Packages $packages -GoArgs @("-run", $run, "-count=1", "-timeout=3m")
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
  }
}
