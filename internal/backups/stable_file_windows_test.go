//go:build windows

package backups

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"golang.org/x/sys/windows"
)

func TestRemoveStableFileDeletesHeldObjectNotReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.gz")
	displacedPath := path + ".displaced"
	if err := os.WriteFile(path, []byte("planned"), 0o600); err != nil {
		t.Fatalf("write planned file: %v", err)
	}
	handle, err := openStableDeleteFile(path)
	if err != nil {
		t.Fatalf("open stable delete handle: %v", err)
	}
	defer handle.Close()

	if err := os.Rename(path, displacedPath); err != nil {
		t.Fatalf("displace held file: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := removeStableFile(path, handle); err != nil {
		t.Fatalf("remove stable file: %v", err)
	}

	if got, err := os.ReadFile(path); err != nil || string(got) != "replacement" {
		t.Fatalf("replacement changed: content=%q error=%v", got, err)
	}
	if _, err := os.Stat(displacedPath); !os.IsNotExist(err) {
		t.Fatalf("held original remains after exact-object delete: %v", err)
	}
}

func TestRemoveStableFileBasicDispositionFallbackClosesHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, []byte("planned"), 0o600); err != nil {
		t.Fatalf("write planned file: %v", err)
	}
	handle, err := openStableDeleteFile(path)
	if err != nil {
		t.Fatalf("open stable delete handle: %v", err)
	}
	defer handle.Close()

	classes := []uint32{}
	err = removeStableFileWithDisposition(
		handle,
		func(file windows.Handle, class uint32, buffer *byte, length uint32) error {
			classes = append(classes, class)
			if class == windows.FileDispositionInfoEx {
				return windows.ERROR_INVALID_PARAMETER
			}
			return windows.SetFileInformationByHandle(file, class, buffer, length)
		},
	)
	if err != nil {
		t.Fatalf("remove stable file through basic disposition: %v", err)
	}
	if len(classes) != 2 || classes[0] != windows.FileDispositionInfoEx || classes[1] != windows.FileDispositionInfo {
		t.Fatalf("disposition classes = %v, want Ex then basic", classes)
	}
	if _, err := handle.Stat(); err == nil {
		t.Fatal("stable handle remains open after basic disposition")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path remains after basic disposition and close: %v", err)
	}
}

func TestRemoveStableFileDispositionFailureDoesNotDeletePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.gz")
	displacedPath := path + ".displaced"
	if err := os.WriteFile(path, []byte("planned"), 0o600); err != nil {
		t.Fatalf("write planned file: %v", err)
	}
	handle, err := openStableDeleteFile(path)
	if err != nil {
		t.Fatalf("open stable delete handle: %v", err)
	}
	defer handle.Close()
	if err := os.Rename(path, displacedPath); err != nil {
		t.Fatalf("displace held file: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}

	err = removeStableFileWithDisposition(
		handle,
		func(windows.Handle, uint32, *byte, uint32) error {
			return windows.ERROR_ACCESS_DENIED
		},
	)
	if err == nil {
		t.Fatal("remove stable file error = nil, want disposition failure")
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != "replacement" {
		t.Fatalf("replacement changed after disposition failure: content=%q error=%v", got, readErr)
	}
	if _, seekErr := handle.Seek(0, io.SeekStart); seekErr != nil {
		t.Fatalf("seek held original after disposition failure: %v", seekErr)
	}
	if got, readErr := io.ReadAll(handle); readErr != nil || string(got) != "planned" {
		t.Fatalf("held original changed after disposition failure: content=%q error=%v", got, readErr)
	}
	if _, statErr := os.Stat(displacedPath); statErr != nil {
		t.Fatalf("held original path changed after disposition failure: %v", statErr)
	}
}

func TestRemovePlannedBackupArtifactsPreservesSwapAfterFinalVerification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.gz")
	displacedPath := path + ".displaced"
	if err := os.WriteFile(path, []byte("planned"), 0o600); err != nil {
		t.Fatalf("write planned file: %v", err)
	}
	handle, identity, err := openPlannedBackupArtifact(path, "archive")
	if err != nil {
		t.Fatalf("open planned artifact: %v", err)
	}
	defer handle.Close()
	record := planRecord{
		ArchivePath:     path,
		ArchiveIdentity: identity,
		ArchiveHandle:   handle,
	}

	removeCalled := false
	err = removePlannedBackupArtifactsWithRemove(record, func(removePath string, stable *os.File) error {
		removeCalled = true
		if removePath != path || stable != handle {
			t.Fatalf("remove callback = (%q, %p), want (%q, %p)", removePath, stable, path, handle)
		}
		// The callback runs after the manager's final identity verification.
		if err := os.Rename(path, displacedPath); err != nil {
			t.Fatalf("displace held file: %v", err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
			t.Fatalf("write replacement: %v", err)
		}
		return removeStableFile(removePath, stable)
	})
	if !removeCalled {
		t.Fatal("stable remover was not called")
	}
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("remove planned artifacts error = %v, want conflict", err)
	}
	if diagnostic := string(apperror.Marshal(err)); strings.Contains(diagnostic, dir) {
		t.Fatalf("delete diagnostic exposed filesystem path: %s", diagnostic)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != "replacement" {
		t.Fatalf("replacement changed: content=%q error=%v", got, readErr)
	}
	if _, statErr := os.Stat(displacedPath); !os.IsNotExist(statErr) {
		t.Fatalf("held original remains after exact-object delete: %v", statErr)
	}
}
