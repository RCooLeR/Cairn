//go:build windows

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const windowsAutostartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

type windowsAutostartManager struct{}

func NewAutostartManager() AutostartManager {
	return windowsAutostartManager{}
}

func (windowsAutostartManager) Enabled(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsAutostartRunKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open Windows startup registry key: %w", err)
	}
	defer func() {
		_ = key.Close()
	}()

	command, _, err := key.GetStringValue(appAutostartName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Windows startup registry value: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("resolve executable path: %w", err)
	}
	return windowsAutostartCommandMatches(command, exe), nil
}

func (windowsAutostartManager) SetEnabled(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, windowsAutostartRunKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("open Windows startup registry key: %w", err)
	}
	defer func() {
		_ = key.Close()
	}()

	if !enabled {
		if err := key.DeleteValue(appAutostartName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("delete Windows startup registry value: %w", err)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if err := key.SetStringValue(appAutostartName, windowsAutostartCommand(exe)); err != nil {
		return fmt.Errorf("write Windows startup registry value: %w", err)
	}
	return nil
}

func windowsAutostartCommand(exe string) string {
	return `"` + exe + `"`
}

func windowsAutostartCommandMatches(command string, exe string) bool {
	return strings.EqualFold(normalizeWindowsPath(firstWindowsCommandArg(command)), normalizeWindowsPath(exe))
}

func firstWindowsCommandArg(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if command[0] != '"' {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			return ""
		}
		return fields[0]
	}
	end := strings.Index(command[1:], `"`)
	if end < 0 {
		return strings.Trim(command, `"`)
	}
	return command[1 : 1+end]
}

func normalizeWindowsPath(path string) string {
	path = strings.TrimPrefix(path, `\\?\`)
	return filepath.Clean(path)
}
