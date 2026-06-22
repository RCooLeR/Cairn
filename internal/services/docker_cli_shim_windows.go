//go:build windows

package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
	"golang.org/x/sys/windows/registry"
)

const (
	windowsDockerShimDirName = "Cairn"
	windowsDockerShimSubdir  = "cli"
	windowsDockerShimCmd     = "docker.cmd"
	windowsDockerShimPS1     = "docker.ps1"
	windowsDockerShimDistro  = "distro.txt"
	windowsUserEnvKey        = `Environment`
)

func windowsDockerCLIShimStatus(ctx context.Context, settings *store.SettingsRepository) (*models.WindowsDockerCLIShimStatus, error) {
	distro := selectedWindowsShimDistro(ctx, settings)
	dir, err := windowsDockerShimDir()
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Resolve Docker CLI shim directory failed", err, apperror.WithDetail(err.Error()))
	}
	status := &models.WindowsDockerCLIShimStatus{
		Supported:   true,
		Directory:   dir,
		CommandPath: filepath.Join(dir, windowsDockerShimCmd),
		ScriptPath:  filepath.Join(dir, windowsDockerShimPS1),
		Distro:      distro,
	}
	if commandExists(status.CommandPath) && commandExists(status.ScriptPath) {
		status.Installed = true
	}
	status.OnUserPath = userPathContainsDir(dir)
	if dockerPath, err := exec.LookPath("docker"); err == nil {
		status.DockerOnPath = dockerPath
	}
	status.NeedsNewShell = status.Installed && status.OnUserPath && !processPathContainsDir(dir)
	switch {
	case !status.Installed:
		status.Message = "Install the Cairn shim so Windows shells can run docker through the selected WSL distro."
	case !status.OnUserPath:
		status.Message = "The shim exists, but its directory is not on the user PATH."
	case status.NeedsNewShell:
		status.Message = "Open a new PowerShell window so the updated PATH is loaded."
	default:
		status.Message = "Windows shells can resolve docker through the Cairn WSL shim."
	}
	return status, nil
}

func installWindowsDockerCLIShim(ctx context.Context, settings *store.SettingsRepository) (*models.WindowsDockerCLIShimStatus, error) {
	distro := selectedWindowsShimDistro(ctx, settings)
	dir, err := windowsDockerShimDir()
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Resolve Docker CLI shim directory failed", err, apperror.WithDetail(err.Error()))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Create Docker CLI shim directory failed", err, apperror.WithDetail(err.Error()))
	}
	files := map[string]string{
		filepath.Join(dir, windowsDockerShimCmd):    dockerShimCMD(),
		filepath.Join(dir, windowsDockerShimPS1):    dockerShimPowerShell(),
		filepath.Join(dir, windowsDockerShimDistro): distro + "\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return nil, apperror.Wrap(apperror.Internal, "Write Docker CLI shim failed", err, apperror.WithDetail(err.Error()))
		}
	}
	if err := ensureUserPathDir(dir); err != nil {
		return nil, apperror.Wrap(
			apperror.Internal,
			"Update user PATH failed",
			err,
			apperror.WithDetail(err.Error()),
			apperror.WithRepairHints("Add "+dir+" to your user PATH manually, then open a new PowerShell window."),
		)
	}
	return windowsDockerCLIShimStatus(ctx, settings)
}

func selectedWindowsShimDistro(ctx context.Context, settings *store.SettingsRepository) string {
	if settings != nil {
		if distro, err := settings.GetString(ctx, "windows.wsl_distro"); err == nil && strings.TrimSpace(distro) != "" {
			return strings.TrimSpace(distro)
		}
	}
	return "Ubuntu"
}

func windowsDockerShimDir() (string, error) {
	root := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if root == "" {
		var err error
		root, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(root, windowsDockerShimDirName, windowsDockerShimSubdir), nil
}

func dockerShimCMD() string {
	return "@echo off\r\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File \"%~dp0docker.ps1\" %*\r\nexit /b %ERRORLEVEL%\r\n"
}

func dockerShimPowerShell() string {
	return `$ErrorActionPreference = 'Stop'
$distroPath = Join-Path $PSScriptRoot 'distro.txt'
$distro = 'Ubuntu'
if (Test-Path -LiteralPath $distroPath) {
  $configured = (Get-Content -LiteralPath $distroPath -Raw).Trim()
  if ($configured.Length -gt 0) {
    $distro = $configured
  }
}

$cwd = (Get-Location).ProviderPath
$backendCwd = $null
if ($cwd -match '^[A-Za-z]:\\') {
  $drive = $cwd.Substring(0, 1).ToLowerInvariant()
  $rest = $cwd.Substring(2).TrimStart('\') -replace '\\', '/'
  $backendCwd = "/mnt/$drive"
  if ($rest.Length -gt 0) {
    $backendCwd = "$backendCwd/$rest"
  }
} elseif ($cwd -match '^\\\\(?:wsl\$|wsl\.localhost)\\([^\\]+)(?:\\(.*))?$') {
  if ([string]::Equals($Matches[1], $distro, [System.StringComparison]::OrdinalIgnoreCase)) {
    $rest = ''
    if ($Matches.Count -gt 2) {
      $rest = $Matches[2]
    }
    $backendCwd = '/'
    if ($rest.Length -gt 0) {
      $backendCwd += ($rest -replace '\\', '/')
    }
  }
}

$wslArgs = @('-d', $distro)
if ($backendCwd) {
  $wslArgs += @('--cd', $backendCwd)
}
$wslArgs += @('--', 'docker')
$wslArgs += $args
& wsl.exe @wslArgs
exit $LASTEXITCODE
`
}

func commandExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func ensureUserPathDir(dir string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, windowsUserEnvKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() {
		_ = key.Close()
	}()
	current, _, err := key.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	if pathListContainsDir(current, dir) {
		return nil
	}
	next := dir
	if strings.TrimSpace(current) != "" {
		next += ";" + current
	}
	return key.SetStringValue("Path", next)
}

func userPathContainsDir(dir string) bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsUserEnvKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer func() {
		_ = key.Close()
	}()
	current, _, err := key.GetStringValue("Path")
	return err == nil && pathListContainsDir(current, dir)
}

func processPathContainsDir(dir string) bool {
	return pathListContainsDir(os.Getenv("PATH"), dir)
}

func pathListContainsDir(pathList string, dir string) bool {
	target := normalizePathForCompare(dir)
	if target == "" {
		return false
	}
	for _, item := range strings.Split(pathList, ";") {
		if strings.EqualFold(normalizePathForCompare(item), target) {
			return true
		}
	}
	return false
}

func normalizePathForCompare(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"`)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}
