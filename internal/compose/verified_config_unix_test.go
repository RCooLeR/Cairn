//go:build linux || darwin || freebsd || openbsd || netbsd

package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"golang.org/x/sys/unix"
)

func TestConfigVerifiedRejectsReferencedFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services:\n  app:\n    env_file: app.env\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "app.env"), 0o600); err != nil {
		t.Fatalf("Mkfifo(app.env): %v", err)
	}
	runner := newFakeRunner()
	started := time.Now()

	_, _, err := NewClient(runner).ConfigVerified(context.Background(), ProjectOptions{Workdir: root, Files: []string{composePath}})
	if !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("ConfigVerified(FIFO dependency) error = %v, want ComposeInvalid", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FIFO dependency rejection took %s", elapsed)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner received a FIFO dependency: %#v", runner.calls)
	}
}

func TestVerifiedConfigParseScopesDoNotCollideOnPathDelimiters(t *testing.T) {
	rootPath := t.TempDir()
	firstPath := filepath.Join(rootPath, "a|b")
	secondPath := filepath.Join(rootPath, "a")
	firstProject := filepath.Join(rootPath, "c")
	secondProject := filepath.Join(rootPath, "b|c")
	for _, directory := range []string{firstProject, secondProject} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", filepath.Base(directory), err)
		}
	}
	if err := os.WriteFile(firstPath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write first Compose file: %v", err)
	}
	secondContent := []byte("services:\n  app:\n    env_file: secret.env\n")
	if err := os.WriteFile(secondPath, secondContent, 0o600); err != nil {
		t.Fatalf("write second Compose file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secondProject, "secret.env"), []byte("TOKEN=hidden\n"), 0o600); err != nil {
		t.Fatalf("write dependency: %v", err)
	}
	root, err := verifyConfigRoot(rootPath)
	if err != nil {
		t.Fatalf("verifyConfigRoot(): %v", err)
	}
	closure := &verifiedConfigClosure{
		ctx:         context.Background(),
		root:        root,
		entries:     make(map[string][]byte),
		directories: make(map[string]struct{}),
		parsed:      make(map[verifiedConfigParseKey]struct{}),
		visiting:    make(map[verifiedConfigParseKey]struct{}),
	}
	if err := closure.scanCompose(firstPath, []byte("services: {}\n"), firstProject, 0); err != nil {
		t.Fatalf("scan first scope: %v", err)
	}
	if err := closure.scanCompose(secondPath, secondContent, secondProject, 0); err != nil {
		t.Fatalf("scan delimiter-colliding scope: %v", err)
	}
	dependencyRel := filepath.Clean(filepath.Join("b|c", "secret.env"))
	if _, exists := closure.entries[dependencyRel]; !exists {
		t.Fatalf("dependency %q was skipped after a parse-scope key collision", dependencyRel)
	}
}
