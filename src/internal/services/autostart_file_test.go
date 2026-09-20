package services

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAutostartFileRejectsUnboundedAndSpecialInputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	valid := filepath.Join(root, "cairn.autostart")
	want := []byte("autostart entry")
	if err := os.WriteFile(valid, want, 0o600); err != nil {
		t.Fatalf("write valid autostart file: %v", err)
	}
	got, err := readAutostartFile(valid)
	if err != nil {
		t.Fatalf("readAutostartFile(valid) error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("readAutostartFile(valid) = %q, want %q", got, want)
	}

	oversized := filepath.Join(root, "oversized.autostart")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatalf("create oversized autostart file: %v", err)
	}
	if err := file.Truncate(maxAutostartFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("truncate oversized autostart file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close oversized autostart file: %v", err)
	}
	if _, err := readAutostartFile(oversized); err == nil {
		t.Fatal("readAutostartFile(oversized) error = nil")
	}

	if _, err := readAutostartFile(root); err == nil {
		t.Fatal("readAutostartFile(directory) error = nil")
	}
}
