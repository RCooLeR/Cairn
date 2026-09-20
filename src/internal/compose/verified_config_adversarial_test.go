package compose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RCooLeR/Cairn/internal/providers"
)

type adversarialConfigRunner struct {
	calls int
}

func (runner *adversarialConfigRunner) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	runner.calls++
	return &providers.CommandResult{Stdout: "services: {}\n"}, nil
}

func TestConfigVerifiedRejectsRecursiveStaticPathAliasesBeforeRunner(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	content := strings.Join([]string{
		"services:",
		"  app:",
		"    image: example/app",
		"    env_file: &recursive",
		"      - *recursive",
		"",
	}, "\n")
	if err := os.WriteFile(composePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write Compose file: %v", err)
	}
	runner := &adversarialConfigRunner{}
	client := NewClient(runner)
	if _, _, err := client.ConfigVerified(context.Background(), ProjectOptions{Workdir: root, Files: []string{composePath}}); err == nil {
		t.Fatal("ConfigVerified accepted a recursive dependency-path alias")
	}
	if runner.calls != 0 {
		t.Fatalf("Compose runner calls = %d, want 0", runner.calls)
	}
}

func TestConfigVerifiedRejectsOversizedCallerCollectionsBeforeAllocationOrRunner(t *testing.T) {
	runner := &adversarialConfigRunner{}
	client := NewClient(runner)
	files := make([]string, maxVerifiedConfigCandidates+1)
	for index := range files {
		files[index] = "compose.yaml"
	}
	if _, _, err := client.ConfigVerified(context.Background(), ProjectOptions{Files: files}); err == nil {
		t.Fatal("ConfigVerified accepted an oversized selected-file collection")
	}
	if runner.calls != 0 {
		t.Fatalf("Compose runner calls = %d, want 0", runner.calls)
	}
}
