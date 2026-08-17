//go:build !windows

package backups

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveStableFileUnlinksPathAndKeepsHeldObjectReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, []byte("planned"), 0o600); err != nil {
		t.Fatalf("write planned file: %v", err)
	}
	handle, err := openStableDeleteFile(path)
	if err != nil {
		t.Fatalf("open stable delete handle: %v", err)
	}
	defer handle.Close()

	// POSIX offers no portable unlink-by-FD for regular files. The manager
	// verifies this held inode immediately before the pathname unlink; the FD
	// still pins the verified object and prevents inode reuse until cleanup.
	if err := removeStableFile(path, handle); err != nil {
		t.Fatalf("remove stable file: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path remains after unlink: %v", err)
	}
	if _, err := handle.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek held object: %v", err)
	}
	got, err := io.ReadAll(handle)
	if err != nil || string(got) != "planned" {
		t.Fatalf("held object content=%q error=%v", got, err)
	}
}
