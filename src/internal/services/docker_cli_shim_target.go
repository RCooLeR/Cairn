package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RCooLeR/Cairn/internal/store"
)

const (
	defaultWindowsDockerShimDistro     = "Ubuntu"
	maxWindowsDockerShimTargetSize     = 4096
	maxWindowsDockerShimTargetFileSize = maxWindowsDockerShimTargetSize + 2
)

func selectedWindowsShimDistro(ctx context.Context, settings *store.SettingsRepository) (string, error) {
	if settings == nil {
		return defaultWindowsDockerShimDistro, nil
	}
	distro, err := settings.GetString(ctx, "windows.wsl_distro")
	if err != nil {
		return "", err
	}
	return normalizeWindowsDockerShimTarget(distro)
}

func normalizeWindowsDockerShimTarget(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", errors.New("shim distro target is not valid UTF-8")
	}
	target := strings.TrimSpace(raw)
	if target == "" {
		return defaultWindowsDockerShimDistro, nil
	}
	if len(target) > maxWindowsDockerShimTargetSize {
		return "", fmt.Errorf("shim distro target exceeds %d bytes", maxWindowsDockerShimTargetSize)
	}
	for _, character := range target {
		if unicode.IsControl(character) || character == '\u2028' || character == '\u2029' {
			return "", errors.New("shim distro target must be a single line without control characters")
		}
	}
	return target, nil
}

// readWindowsDockerShimTarget reads the same distro target used by docker.ps1.
// A missing or blank file uses the script's default, while the size bound keeps
// status checks from consuming an arbitrarily large or malformed file.
func readWindowsDockerShimTarget(path string) (string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultWindowsDockerShimDistro, nil
	}
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()

	data, err := io.ReadAll(io.LimitReader(file, maxWindowsDockerShimTargetFileSize+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxWindowsDockerShimTargetFileSize {
		return "", fmt.Errorf("shim distro target file exceeds %d bytes", maxWindowsDockerShimTargetFileSize)
	}
	return normalizeWindowsDockerShimTarget(string(data))
}

func windowsDockerShimTargetMismatch(selected string, installed string) bool {
	selected = strings.TrimSpace(selected)
	installed = strings.TrimSpace(installed)
	return selected != "" && installed != "" && !strings.EqualFold(selected, installed)
}

func windowsDockerShimMismatchMessage(selected string, installed string) string {
	return fmt.Sprintf(
		"The installed shim targets WSL distro %q, but Cairn currently selects %q. Reinstall the shim to update its target.",
		strings.TrimSpace(installed),
		strings.TrimSpace(selected),
	)
}

// windowsDockerShimTargetStatus resolves the target exposed by status and any
// warning that must take precedence over ordinary PATH guidance. Keeping this
// filesystem-only logic platform-neutral makes the Windows status contract
// testable on every development and CI host.
func windowsDockerShimTargetStatus(path string, selected string, installed bool) (string, string) {
	target, err := readWindowsDockerShimTarget(path)
	if err != nil {
		if installed {
			return "", fmt.Sprintf(
				"The shim is installed, but Cairn could not verify its WSL distro target: %v. Reinstall the shim to restore a readable target.",
				err,
			)
		}
		return "", ""
	}
	if installed && windowsDockerShimTargetMismatch(selected, target) {
		return target, windowsDockerShimMismatchMessage(selected, target)
	}
	return target, ""
}
