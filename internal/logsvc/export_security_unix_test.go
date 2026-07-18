//go:build !windows

package logsvc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateExportDirectoryRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(real) error = %v", err)
	}
	symlink := filepath.Join(root, "exports")
	if err := os.Symlink(realDirectory, symlink); err != nil {
		t.Fatalf("Symlink(exports) error = %v", err)
	}
	if err := ensurePrivateExportDirectory(symlink); err == nil {
		t.Fatal("ensurePrivateExportDirectory(symlink) error = nil")
	}
}

func TestEnsurePrivateExportDirectoryRejectsSymlinkedOwnedParent(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real-cairn")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatalf("Mkdir(real parent) error = %v", err)
	}
	symlinkedParent := filepath.Join(root, "Cairn")
	if err := os.Symlink(realParent, symlinkedParent); err != nil {
		t.Fatalf("Symlink(Cairn) error = %v", err)
	}
	if err := ensurePrivateExportDirectory(filepath.Join(symlinkedParent, "exports")); err == nil {
		t.Fatal("ensurePrivateExportDirectory(symlinked parent) error = nil")
	}
	if _, err := os.Stat(filepath.Join(realParent, "exports")); !os.IsNotExist(err) {
		t.Fatalf("export directory followed symlinked parent: error=%v", err)
	}
}
