package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprintConfigInputsCoversVerifiedDependencyClosure(t *testing.T) {
	root := t.TempDir()
	includedDir := filepath.Join(root, "included")
	if err := os.MkdirAll(includedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	composeFile := filepath.Join(root, "compose.yaml")
	includedFile := filepath.Join(includedDir, "compose.yaml")
	envFile := filepath.Join(root, "app.env")
	write := func(path string, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", filepath.Base(path), err)
		}
	}
	write(composeFile, "include:\n  - included/compose.yaml\nservices:\n  app:\n    image: nginx:alpine\n    env_file:\n      - app.env\n")
	write(includedFile, "services:\n  worker:\n    image: busybox:stable\n")
	write(envFile, "MODE=one\n")
	opts := ProjectOptions{Workdir: root, Files: []string{composeFile}}

	baseline, err := FingerprintConfigInputs(context.Background(), opts)
	if err != nil {
		t.Fatalf("FingerprintConfigInputs() error = %v", err)
	}
	repeated, err := FingerprintConfigInputs(context.Background(), opts)
	if err != nil {
		t.Fatalf("FingerprintConfigInputs(repeated) error = %v", err)
	}
	if baseline == "" || repeated != baseline {
		t.Fatalf("repeated fingerprint = %q, want deterministic %q", repeated, baseline)
	}

	write(envFile, "MODE=two\n")
	envChanged, err := FingerprintConfigInputs(context.Background(), opts)
	if err != nil {
		t.Fatalf("FingerprintConfigInputs(env changed) error = %v", err)
	}
	if envChanged == baseline {
		t.Fatal("referenced env_file content did not change the fingerprint")
	}

	write(envFile, "MODE=one\n")
	write(includedFile, "services:\n  worker:\n    image: busybox:latest\n")
	includedChanged, err := FingerprintConfigInputs(context.Background(), opts)
	if err != nil {
		t.Fatalf("FingerprintConfigInputs(include changed) error = %v", err)
	}
	if includedChanged == baseline {
		t.Fatal("included Compose file content did not change the fingerprint")
	}
}
