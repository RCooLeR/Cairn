//go:build windows

package logsvc

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPrivateExportACLsAreProtectedAndCurrentUserOnly(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "exports")
	if err := ensurePrivateExportDirectory(directory); err != nil {
		t.Fatalf("ensurePrivateExportDirectory() error = %v", err)
	}
	assertPrivateCurrentUserDACL(t, directory)

	file, err := os.CreateTemp(directory, ".export-*.tmp")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := securePrivateExportFile(path); err != nil {
		t.Fatalf("securePrivateExportFile() error = %v", err)
	}
	assertPrivateCurrentUserDACL(t, path)
}

func TestEnsurePrivateExportDirectoryRejectsReparseAncestor(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real-cairn")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatalf("Mkdir(real parent) error = %v", err)
	}
	reparseParent := filepath.Join(root, "Cairn")
	if err := os.Symlink(realParent, reparseParent); err != nil {
		t.Skipf("creating a Windows directory symlink requires host permission: %v", err)
	}
	if err := ensurePrivateExportDirectory(filepath.Join(reparseParent, "exports")); err == nil {
		t.Fatal("ensurePrivateExportDirectory(reparse ancestor) error = nil")
	}
	if _, err := os.Stat(filepath.Join(realParent, "exports")); !os.IsNotExist(err) {
		t.Fatalf("export directory followed reparse ancestor: error=%v", err)
	}
}

func assertPrivateCurrentUserDACL(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%q) error = %v", path, err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("Control(%q) error = %v", path, err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("DACL for %q is not protected: control=%#x", path, control)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("DACL(%q) error = %v", path, err)
	}
	if dacl == nil {
		t.Fatalf("DACL for %q is nil", path)
	}
	if dacl.AceCount != 1 {
		t.Fatalf("DACL for %q has %d ACEs, want 1", path, dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("GetAce(%q) error = %v", path, err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Fatalf("ACE type for %q = %d, want allow", path, ace.Header.AceType)
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.Equals(currentUser.User.Sid) {
		t.Fatalf("ACE SID for %q = %s, want current user %s", path, aceSID, currentUser.User.Sid)
	}
}
