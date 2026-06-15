//go:build windows

package providers

import (
	"os/exec"
	"testing"
)

func TestConfigureBackgroundCommandHidesWindowsConsole(t *testing.T) {
	cmd := exec.Command("wsl.exe", "--status")

	configureBackgroundCommand(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow is false")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}
