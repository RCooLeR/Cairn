//go:build linux

package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type linuxAutostartManager struct{}

func NewAutostartManager() AutostartManager {
	return linuxAutostartManager{}
}

func (linuxAutostartManager) Enabled(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path, err := linuxAutostartFile()
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read desktop autostart file: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("resolve executable path: %w", err)
	}
	content := string(raw)
	return strings.Contains(content, "Exec="+desktopExecValue(exe)) || strings.Contains(content, "Exec="+exe), nil
}

func (linuxAutostartManager) SetEnabled(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := linuxAutostartFile()
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove desktop autostart file: %w", err)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create desktop autostart directory: %w", err)
	}
	content := strings.Join([]string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=Cairn",
		"Comment=Compose-first Docker manager",
		"Exec=" + desktopExecValue(exe),
		"Terminal=false",
		"X-GNOME-Autostart-enabled=true",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write desktop autostart file: %w", err)
	}
	return nil
}

func linuxAutostartFile() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(configDir, "autostart", "cairn.desktop"), nil
}

func desktopExecValue(path string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return `"` + replacer.Replace(path) + `"`
}
