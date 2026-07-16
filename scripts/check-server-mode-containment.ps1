[CmdletBinding()]
param(
    [switch]$CompileCheck
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$root = Split-Path -Parent $PSScriptRoot
$failures = [System.Collections.Generic.List[string]]::new()

function Add-Failure {
    param([Parameter(Mandatory = $true)][string]$Message)
    $failures.Add($Message)
}

function Assert-FileMatches {
    param(
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][string]$Pattern,
        [Parameter(Mandatory = $true)][string]$Description
    )

    $path = Join-Path $root $RelativePath
    if (!(Test-Path -LiteralPath $path -PathType Leaf)) {
        Add-Failure "$RelativePath is missing ($Description)."
        return
    }
    $content = Get-Content -LiteralPath $path -Raw -Encoding UTF8
    if ($content -notmatch $Pattern) {
        Add-Failure "$RelativePath does not enforce $Description."
    }
}

$serverDockerfile = Join-Path $root "build/docker/Dockerfile.server"
if (Test-Path -LiteralPath $serverDockerfile) {
    Add-Failure "build/docker/Dockerfile.server must not exist. Cairn does not publish a server container."
}

$stableSurfaceFiles = [System.Collections.Generic.List[string]]::new()
foreach ($relativePath in @("Taskfile.yml", "build/Taskfile.yml", ".goreleaser.yaml", "README.md")) {
    $path = Join-Path $root $relativePath
    if (Test-Path -LiteralPath $path -PathType Leaf) {
        $stableSurfaceFiles.Add($path)
    }
}
foreach ($directory in @(".github/workflows", "build/docker", "docs", "scripts")) {
    $path = Join-Path $root $directory
    if (!(Test-Path -LiteralPath $path -PathType Container)) {
        continue
    }
    Get-ChildItem -LiteralPath $path -File -Recurse |
        Where-Object { $_.Extension -in @("", ".json", ".md", ".ps1", ".sh", ".yaml", ".yml") } |
        Where-Object { $_.FullName -notin @(
                (Join-Path $root "docs/development-server-mode.md"),
                (Join-Path $root "scripts/check-server-mode-containment.ps1")
            ) } |
        ForEach-Object { $stableSurfaceFiles.Add($_.FullName) }
}

$forbiddenPatterns = @(
    @{ Pattern = '(?im)^\s*(?:build|run):(server|docker):\s*$'; Description = "an advertised server/container task" },
    @{ Pattern = '(?i)Dockerfile\.server'; Description = "a server-container build path" },
    @{ Pattern = '(?im)\bgo\s+(?:build|run)\b[^\r\n]*-tags(?:=|\s+)["'']?server(?:[,"''\s]|$)'; Description = "a plain or stable server build command" },
    @{ Pattern = '(?i)WAILS_SERVER_HOST\s*=\s*0\.0\.0\.0'; Description = "a wildcard server bind" }
)

foreach ($path in $stableSurfaceFiles) {
    $content = Get-Content -LiteralPath $path -Raw -Encoding UTF8
    foreach ($forbidden in $forbiddenPatterns) {
        if ($content -match $forbidden.Pattern) {
            $relativePath = $path.Substring($root.Length).TrimStart([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
            Add-Failure "$relativePath contains $($forbidden.Description)."
        }
    }
}

Assert-FileMatches "main.go" '(?m)^//go:build !server \|\| cairn_server_dev\r?$' "the fail-closed server build constraint"
Assert-FileMatches "server_mode_disabled.go" '(?m)^//go:build server && !cairn_server_dev\r?$' "the plain-server exclusion guard"
Assert-FileMatches "server_mode_development.go" '(?m)^//go:build server && cairn_server_dev\r?$' "the explicit development-only server constraint"
Assert-FileMatches "server_mode_development.go" 'CAIRN_ENABLE_UNSAFE_SERVER_DEVELOPMENT' "an explicit unsafe-development acknowledgement"
Assert-FileMatches "server_mode_development.go" 'serverDevelopmentHost\s*=\s*"127\.0\.0\.1"' "a forced IPv4 loopback bind"

if ($CompileCheck) {
    $temporaryDirectory = Join-Path ([IO.Path]::GetTempPath()) ("cairn-server-containment-" + [Guid]::NewGuid().ToString("N"))
    $blockedOutputPath = Join-Path $temporaryDirectory "blocked-server"
    $developmentOutputPath = Join-Path $temporaryDirectory "development-server"
    New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null

    $environmentNames = @("GOOS", "GOARCH", "CGO_ENABLED")
    $savedEnvironment = @{}
    foreach ($name in $environmentNames) {
        $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
    }

    try {
        $env:GOOS = "linux"
        $env:GOARCH = "amd64"
        $env:CGO_ENABLED = "0"

        Push-Location $root
        $savedErrorActionPreference = $null
        try {
            $savedErrorActionPreference = $ErrorActionPreference
            $ErrorActionPreference = "Continue"
            $blockedBuildOutput = (& go build -tags=server -o $blockedOutputPath . 2>&1 | Out-String)
            $blockedBuildExitCode = $LASTEXITCODE
            if ($blockedBuildExitCode -eq 0) {
                Add-Failure "go build -tags=server unexpectedly succeeded."
            }
            elseif ($blockedBuildOutput -notmatch 'function main is undeclared') {
                Add-Failure "go build -tags=server failed for an unexpected reason: $($blockedBuildOutput.Trim())"
            }

            $developmentBuildOutput = (& go build '-tags=server,cairn_server_dev' -o $developmentOutputPath . 2>&1 | Out-String)
            $developmentBuildExitCode = $LASTEXITCODE
            $ErrorActionPreference = $savedErrorActionPreference
            if ($developmentBuildExitCode -ne 0) {
                Add-Failure "the explicit development-only server build failed: $($developmentBuildOutput.Trim())"
            }
        }
        finally {
            if ($null -ne $savedErrorActionPreference) {
                $ErrorActionPreference = $savedErrorActionPreference
            }
            Pop-Location
        }
    }
    finally {
        foreach ($name in $environmentNames) {
            $savedValue = $savedEnvironment[$name]
            if ($null -eq $savedValue) {
                Remove-Item -LiteralPath "Env:$name" -ErrorAction SilentlyContinue
            }
            else {
                Set-Item -LiteralPath "Env:$name" -Value $savedValue
            }
        }
        foreach ($path in @($blockedOutputPath, $developmentOutputPath)) {
            if (Test-Path -LiteralPath $path -PathType Leaf) {
                Remove-Item -LiteralPath $path -Force
            }
        }
        if (Test-Path -LiteralPath $temporaryDirectory -PathType Container) {
            Remove-Item -LiteralPath $temporaryDirectory -Force
        }
    }
}

if ($failures.Count -gt 0) {
    foreach ($failure in $failures) {
        Write-Error $failure -ErrorAction Continue
    }
    throw "Server mode containment check failed with $($failures.Count) issue(s)."
}

Write-Host "Server mode containment checks passed."
