//go:build linux || darwin || freebsd || openbsd || netbsd

package services

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"golang.org/x/sys/unix"
)

func TestBoundedProjectReadsRejectFIFOAndDeviceWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, ".env.pipe")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("Mkfifo(): %v", err)
	}

	started := time.Now()
	if _, _, err := readBoundedRegularProjectFile(root, fifo, 64, false); !errors.Is(err, errBoundedFileNotRegular) {
		t.Fatalf("FIFO read error = %v, want %v", err, errBoundedFileNotRegular)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FIFO rejection took %s; special file may have been opened", elapsed)
	}

	files, err := readAgentProjectFiles(root)
	if err != nil {
		t.Fatalf("readAgentProjectFiles(): %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("agent project files included FIFO: %#v", files)
	}

	if _, err := readBoundedRegularFile("/dev/null", 64, false); !errors.Is(err, errBoundedFileNotRegular) {
		t.Fatalf("device read error = %v, want %v", err, errBoundedFileNotRegular)
	}
	if _, _, err := resolveImportFiles(models.ImportProjectRequest{ComposeFilePaths: []string{fifo}}); !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("resolveImportFiles(FIFO) error = %v, want ComposeInvalid", err)
	}

	outside := filepath.Join(t.TempDir(), "outside.env")
	if err := os.WriteFile(outside, []byte("SECRET=outside\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside): %v", err)
	}
	link := filepath.Join(root, ".env.link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink(file): %v", err)
	}
	if _, _, err := readBoundedRegularProjectFile(root, link, 64, false); !errors.Is(err, errBoundedFileNotRegular) {
		t.Fatalf("symlink read error = %v, want %v", err, errBoundedFileNotRegular)
	}

	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	rootLink := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatalf("Symlink(root): %v", err)
	}
	agentFiles, err := readAgentProjectFiles(rootLink)
	if err != nil {
		t.Fatalf("readAgentProjectFiles(symlink root): %v", err)
	}
	paths := make([]string, 0, len(agentFiles))
	for _, file := range agentFiles {
		paths = append(paths, file.Path)
	}
	if !slices.Contains(paths, "compose.yaml") {
		t.Fatalf("agent project files through symlink root = %#v", agentFiles)
	}
}
