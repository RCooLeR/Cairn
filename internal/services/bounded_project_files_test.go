package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestReadBoundedRegularFileEnforcesTypeAndSize(t *testing.T) {
	root := t.TempDir()
	exactPath := filepath.Join(root, "exact")
	if err := os.WriteFile(exactPath, []byte("12345678"), 0o600); err != nil {
		t.Fatalf("WriteFile(exact): %v", err)
	}
	exact, err := readBoundedRegularFile(exactPath, 8, false)
	if err != nil || string(exact.Content) != "12345678" || exact.Truncated {
		t.Fatalf("exact bounded read = %#v, %v", exact, err)
	}

	oversizedPath := filepath.Join(root, "oversized")
	oversized, err := os.Create(oversizedPath)
	if err != nil {
		t.Fatalf("Create(oversized): %v", err)
	}
	if _, err := oversized.WriteString("prefix"); err != nil {
		t.Fatalf("Write(oversized): %v", err)
	}
	if err := oversized.Truncate(1 << 30); err != nil {
		t.Fatalf("Truncate(oversized): %v", err)
	}
	if err := oversized.Close(); err != nil {
		t.Fatalf("Close(oversized): %v", err)
	}
	if _, err := readBoundedRegularFile(oversizedPath, 8, false); !errors.Is(err, errBoundedFileTooLarge) {
		t.Fatalf("oversized exact read error = %v, want %v", err, errBoundedFileTooLarge)
	}
	prefix, err := readBoundedRegularFile(oversizedPath, 8, true)
	if err != nil || len(prefix.Content) != 8 || !prefix.Truncated || string(prefix.Content[:6]) != "prefix" {
		t.Fatalf("oversized prefix read = %#v, %v", prefix, err)
	}

	if _, err := readBoundedRegularFile(root, 8, false); !errors.Is(err, errBoundedFileNotRegular) {
		t.Fatalf("directory read error = %v, want %v", err, errBoundedFileNotRegular)
	}

	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(exactPath, linkPath); err == nil {
		if _, err := readBoundedRegularFile(linkPath, 8, false); !errors.Is(err, errBoundedFileNotRegular) {
			t.Fatalf("symlink read error = %v, want %v", err, errBoundedFileNotRegular)
		}
	}

	rootLink := filepath.Join(filepath.Dir(root), "linked-root")
	if err := os.Symlink(root, rootLink); err == nil {
		throughCanonicalPath, relPath, err := readBoundedRegularProjectFile(rootLink, exactPath, 8, false)
		if err != nil || relPath != "exact" || string(throughCanonicalPath.Content) != "12345678" {
			t.Fatalf("canonical path below symlinked root = %#v, %q, %v", throughCanonicalPath, relPath, err)
		}
	}
}

func TestReadBoundedRegularFileAccountsForOverflowBytesRead(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "growing")
	if err := os.WriteFile(path, []byte("12345678"), 0o600); err != nil {
		t.Fatalf("WriteFile(growing): %v", err)
	}

	result, err := readBoundedRegularFileVerified(path, 8, false, func(fs.FileInfo) error {
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if openErr != nil {
			return openErr
		}
		_, writeErr := file.WriteString("9")
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	})
	if !errors.Is(err, errBoundedFileTooLarge) {
		t.Fatalf("growing read error = %v, want %v", err, errBoundedFileTooLarge)
	}
	if got := result.ReadBytes; got != 9 {
		t.Fatalf("ReadBytes = %d, want the actual 9-byte read", got)
	}
	if got := string(result.Content); got != "123456789" {
		t.Fatalf("overflow content = %q, want the bounded limit+1 read", got)
	}
}

func TestReadBoundedRegularFileRejectsSameSizeMutationDuringRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutating")
	if err := os.WriteFile(path, []byte("12345678"), 0o600); err != nil {
		t.Fatalf("WriteFile(mutating): %v", err)
	}

	result, err := readBoundedRegularFileVerified(path, 8, false, func(opened fs.FileInfo) error {
		file, openErr := os.OpenFile(path, os.O_WRONLY, 0)
		if openErr != nil {
			return openErr
		}
		_, writeErr := file.WriteAt([]byte("87654321"), 0)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		changedAt := opened.ModTime().Add(2 * time.Second)
		return os.Chtimes(path, changedAt, changedAt)
	})
	if !errors.Is(err, errBoundedFileChanged) {
		t.Fatalf("same-size mutating read = %#v, %v; want %v", result, err, errBoundedFileChanged)
	}
}

func TestRunVerifiedComposeConfigRejectsOversizedInputBeforeRunner(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	file, err := os.Create(composePath)
	if err != nil {
		t.Fatalf("Create(compose): %v", err)
	}
	if err := file.Truncate(maxProjectPreviewFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("Truncate(compose): %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(compose): %v", err)
	}
	runner := newFakeComposeRunner()

	_, _, err = runVerifiedComposeConfig(context.Background(), composecore.NewClient(runner), composecore.ProjectOptions{
		Workdir: root,
		Files:   []string{composePath},
	})
	if !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("oversized config error = %v, want ComposeInvalid", err)
	}
	if calls := runner.callsSnapshot(); len(calls) != 0 {
		t.Fatalf("Compose runner was called with an unverified input: %#v", calls)
	}
}

func TestRunVerifiedComposeConfigUsesImmutableSnapshotAndCleansIt(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	original := []byte("services:\n  app:\n    image: nginx:alpine\n")
	if err := os.WriteFile(composePath, original, 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	envPath := filepath.Join(root, ".env")
	originalEnv := []byte("IMAGE_TAG=alpine\n")
	if err := os.WriteFile(envPath, originalEnv, 0o600); err != nil {
		t.Fatalf("WriteFile(.env): %v", err)
	}
	var snapshotPath string
	var envSnapshotPath string
	var snapshotDir string
	var runnerInput []byte
	var runnerEnvInput []byte
	runner := composeRunnerFunc(func(_ context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
		snapshotDir = workdir
		if workdir == root || !strings.Contains(filepath.Base(workdir), "cairn-compose-input-") {
			t.Errorf("runner workdir = %q, want a private Compose project mirror", workdir)
		}
		noInterpolate := false
		for index := 0; index < len(args); index++ {
			switch args[index] {
			case "--project-directory":
				if index+1 >= len(args) || args[index+1] != workdir {
					t.Errorf("project directory args = %#v, want private workdir %q", args, workdir)
				}
			case "-f":
				if index+1 < len(args) {
					snapshotPath = args[index+1]
				}
			case "--env-file":
				if index+1 < len(args) {
					envSnapshotPath = args[index+1]
				}
			case "--no-interpolate":
				noInterpolate = true
			}
		}
		if !noInterpolate {
			t.Errorf("verified config args = %#v, want --no-interpolate", args)
		}
		if snapshotPath == "" || snapshotPath == composePath {
			t.Errorf("runner Compose path = %q, want a private snapshot", snapshotPath)
		}
		if envSnapshotPath == "" || envSnapshotPath == envPath {
			t.Errorf("runner environment path = %q, want a private snapshot", envSnapshotPath)
		}
		if err := os.WriteFile(composePath, []byte("services: {}\n# changed after verification\n"), 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(envPath, []byte("IMAGE_TAG=changed\n"), 0o600); err != nil {
			return nil, err
		}
		var err error
		runnerInput, err = os.ReadFile(snapshotPath)
		if err != nil {
			return nil, err
		}
		runnerEnvInput, err = os.ReadFile(envSnapshotPath)
		if err != nil {
			return nil, err
		}
		return &providers.CommandResult{Stdout: string(original)}, nil
	})

	config, inputs, err := runVerifiedComposeConfig(context.Background(), composecore.NewClient(runner), composecore.ProjectOptions{
		Workdir: root,
		Files:   []string{composePath},
	})
	if err != nil || config == nil || !config.Valid {
		t.Fatalf("runVerifiedComposeConfig() = %#v, %v", config, err)
	}
	if string(runnerInput) != string(original) || len(inputs) != 1 || string(inputs[0].Content) != string(original) {
		t.Fatalf("runner input=%q verified inputs=%#v, want original verified bytes", runnerInput, inputs)
	}
	if string(runnerEnvInput) != string(originalEnv) {
		t.Fatalf("runner environment input=%q, want immutable verified bytes %q", runnerEnvInput, originalEnv)
	}
	if _, err := os.Stat(snapshotPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("snapshot still exists after Config: path=%q err=%v", snapshotPath, err)
	}
	if _, err := os.Stat(envSnapshotPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("environment snapshot still exists after Config: path=%q err=%v", envSnapshotPath, err)
	}
	if _, err := os.Stat(snapshotDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("private project mirror still exists after Config: path=%q err=%v", snapshotDir, err)
	}
}

func TestReadAgentProjectFilesUsesPerFileAndAggregateBudgets(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 9; i++ {
		name := filepath.Join(root, "compose."+string(rune('a'+i))+".yaml")
		if err := os.WriteFile(name, []byte(strings.Repeat("x", int(maxAgentProjectFileBytes))), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatalf("Mkdir(a): %v", err)
	}
	largePath := filepath.Join(root, "a", "Dockerfile")
	large, err := os.Create(largePath)
	if err != nil {
		t.Fatalf("Create(Dockerfile): %v", err)
	}
	if _, err := large.WriteString("FROM scratch\n"); err != nil {
		t.Fatalf("Write(Dockerfile): %v", err)
	}
	if err := large.Truncate(1 << 30); err != nil {
		t.Fatalf("Truncate(Dockerfile): %v", err)
	}
	if err := large.Close(); err != nil {
		t.Fatalf("Close(Dockerfile): %v", err)
	}

	files, err := readAgentProjectFiles(root)
	if err != nil {
		t.Fatalf("readAgentProjectFiles(): %v", err)
	}
	var total int64
	for _, file := range files {
		total += int64(len(file.Content))
		if int64(len(file.Content)) > maxAgentProjectFileBytes {
			t.Fatalf("%s preview is %d bytes, want at most %d", file.Path, len(file.Content), maxAgentProjectFileBytes)
		}
	}
	if total > maxAgentProjectAggregateBytes {
		t.Fatalf("aggregate preview is %d bytes, want at most %d", total, maxAgentProjectAggregateBytes)
	}
	foundSparse := false
	for _, file := range files {
		if file.Path == "a/Dockerfile" && strings.Contains(file.Content, "... file truncated ...") {
			foundSparse = true
			break
		}
	}
	if !foundSparse {
		t.Fatalf("large sparse file was not included as a bounded preview; files=%d", len(files))
	}
}

func TestReadAgentProjectFilesDeduplicatesHardlinksByOpenedIdentity(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "compose.yaml")
	second := filepath.Join(root, "docker-compose.yaml")
	if err := os.WriteFile(first, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}

	files, err := readAgentProjectFiles(root)
	if err != nil {
		t.Fatalf("readAgentProjectFiles(): %v", err)
	}
	if len(files) != 1 || files[0].Content != "services: {}\n" {
		t.Fatalf("hardlink Agent previews = %#v, want one opened file identity", files)
	}
}

func TestStructuredPreviewOutputIsBounded(t *testing.T) {
	envContent := strings.Repeat("A=x\n", int(maxProjectPreviewFileBytes/4))
	if preview := envStructurePreview(envContent); int64(len(preview)) > maxProjectPreviewFileBytes || !strings.Contains(preview, "exceeded its display limit") {
		t.Fatalf("expanded env preview was not replaced by a bounded marker: length=%d preview=%q", len(preview), preview)
	}
	composeContent := "value: " + strings.Repeat("x", int(maxProjectPreviewFileBytes))
	if preview := composeStructurePreview(composeContent); int64(len(preview)) > maxProjectPreviewFileBytes || !strings.Contains(preview, "structured preview was unavailable") {
		t.Fatalf("oversized Compose preview was not replaced by a bounded marker: length=%d preview=%q", len(preview), preview)
	}
}

func TestComposeStructurePreviewRejectsAdversarialTraversalShapes(t *testing.T) {
	const unavailable = "structured preview was unavailable"
	deep := "value"
	for range maxComposePreviewYAMLDepth + 2 {
		deep = "[" + deep + "]"
	}

	var tooManyKeys strings.Builder
	for index := 0; index < maxComposePreviewYAMLKeys+1; index++ {
		fmt.Fprintf(&tooManyKeys, "key-%d: value\n", index)
	}

	var tooManyNodes strings.Builder
	tooManyNodes.WriteString("items:\n")
	for index := 0; index < maxComposePreviewYAMLNodes; index++ {
		tooManyNodes.WriteString("  - value\n")
	}

	for name, content := range map[string]string{
		"depth": "services: " + deep + "\n",
		"keys":  tooManyKeys.String(),
		"nodes": tooManyNodes.String(),
	} {
		t.Run(name, func(t *testing.T) {
			preview := composeStructurePreview(content)
			if !strings.Contains(preview, unavailable) {
				t.Fatalf("adversarial %s preview was traversed: length=%d preview=%q", name, len(preview), preview)
			}
		})
	}
}

func TestAgentProjectOutputBudgetIncludesRedactionExpansion(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("TOKEN=\n", int(maxAgentProjectFileBytes/7))
	for i := 0; i < 12; i++ {
		path := filepath.Join(root, ".env."+string(rune('a'+i)))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	files, err := readAgentProjectFiles(root)
	if err != nil {
		t.Fatalf("readAgentProjectFiles(): %v", err)
	}
	var total int64
	for _, file := range files {
		total += int64(len(file.Content))
		if int64(len(file.Content)) > maxAgentProjectFileBytes {
			t.Fatalf("redacted %s preview is %d bytes", file.Path, len(file.Content))
		}
	}
	if total > maxAgentProjectAggregateBytes {
		t.Fatalf("redacted aggregate preview is %d bytes, want at most %d", total, maxAgentProjectAggregateBytes)
	}
}

func TestAgentProjectEnvironmentFilesMaskOpaqueValuesRegardlessOfKeyName(t *testing.T) {
	root := t.TempDir()
	value := "correct horse battery staple plus an opaque value"
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PUBLIC_DESCRIPTION="+value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := readAgentProjectFiles(root)
	if err != nil {
		t.Fatalf("readAgentProjectFiles() error = %v", err)
	}
	if len(files) != 1 || files[0].Path != ".env" {
		t.Fatalf("agent environment previews = %#v", files)
	}
	if strings.Contains(files[0].Content, value) || !strings.Contains(files[0].Content, "PUBLIC_DESCRIPTION=[REDACTED]") {
		t.Fatalf("agent environment preview leaked an opaque value: %q", files[0].Content)
	}
}

func TestAgentProjectStructuredAndConfigPreviewsHideInnocuousShortSecrets(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"compose.yaml":     "services:\n  app:\n    x-license: short-yaml-secret\n",
		"appsettings.json": `{"ConnectionName":"short-json-secret"}`,
		"settings.toml":    "license = 'short-toml-secret'\n",
		"Dockerfile":       "FROM scratch\nARG LICENSE=short-docker-secret\n",
		"config/app.conf":  "license=short-config-secret\n",
	}
	for relPath, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", relPath, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", relPath, err)
		}
	}

	previews, err := readAgentProjectFiles(root)
	if err != nil {
		t.Fatalf("readAgentProjectFiles() error = %v", err)
	}
	if len(previews) != len(files) {
		t.Fatalf("Agent project previews = %#v, want %d files", previews, len(files))
	}
	for _, preview := range previews {
		for _, secret := range []string{
			"short-yaml-secret", "short-json-secret", "short-toml-secret", "short-docker-secret", "short-config-secret",
		} {
			if strings.Contains(preview.Content, secret) {
				t.Fatalf("%s leaked innocuous-key secret %q: %q", preview.Path, secret, preview.Content)
			}
		}
	}
}

func TestProjectPreviewBudgetsCandidatesAttemptsFilesAndFinalDTOBytes(t *testing.T) {
	root := t.TempDir()
	emptyFiles := make([]string, 0, maxProjectPreviewFiles+8)
	for i := 0; i < maxProjectPreviewFiles+8; i++ {
		path := filepath.Join(root, fmt.Sprintf("empty-%03d.env", i))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
		emptyFiles = append(emptyFiles, path)
	}
	emptyBudget := newProjectPreviewBudget()
	emptyPreviews := readImportEnvFilesWithBudget(root, emptyFiles, emptyBudget)
	if len(emptyPreviews) != maxProjectPreviewFiles || emptyBudget.files != maxProjectPreviewFiles {
		t.Fatalf("empty previews=%d budget=%#v, want %d output files", len(emptyPreviews), emptyBudget, maxProjectPreviewFiles)
	}
	var dtoBytes int64
	for _, preview := range emptyPreviews {
		dtoBytes += int64(len(preview.Path) + len(preview.Content))
	}
	if dtoBytes != emptyBudget.dtoBytes || dtoBytes > maxProjectPreviewTotalBytes {
		t.Fatalf("final DTO bytes=%d budget=%d limit=%d", dtoBytes, emptyBudget.dtoBytes, maxProjectPreviewTotalBytes)
	}

	largePath := filepath.Join(root, "large.env")
	large, err := os.Create(largePath)
	if err != nil {
		t.Fatalf("Create(large.env): %v", err)
	}
	if err := large.Truncate(maxProjectPreviewFileBytes + 1); err != nil {
		t.Fatalf("Truncate(large.env): %v", err)
	}
	if err := large.Close(); err != nil {
		t.Fatalf("Close(large.env): %v", err)
	}
	duplicateRefs := make([]string, maxProjectPreviewCandidates+8)
	for i := range duplicateRefs {
		duplicateRefs[i] = largePath
	}
	duplicateBudget := newProjectPreviewBudget()
	if previews := readImportEnvFilesWithBudget(root, duplicateRefs, duplicateBudget); len(previews) != 0 {
		t.Fatalf("oversized duplicate previews = %#v", previews)
	}
	if duplicateBudget.candidates != maxProjectPreviewCandidates || duplicateBudget.attempts != 2 {
		t.Fatalf("duplicate candidate budget = %#v, want %d candidates and 2 attempts", duplicateBudget, maxProjectPreviewCandidates)
	}

	missingRefs := make([]string, maxProjectPreviewCandidates+8)
	for i := range missingRefs {
		missingRefs[i] = filepath.Join(root, fmt.Sprintf("missing-%03d.env", i))
	}
	missingBudget := newProjectPreviewBudget()
	_ = readImportEnvFilesWithBudget(root, missingRefs, missingBudget)
	if missingBudget.candidates > maxProjectPreviewCandidates || missingBudget.attempts != maxProjectPreviewAttempts {
		t.Fatalf("missing candidate budget = %#v", missingBudget)
	}

	original := filepath.Join(root, "hardlink-a.env")
	linked := filepath.Join(root, "hardlink-b.env")
	if err := os.WriteFile(original, []byte("VALUE=hidden\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(hardlink source): %v", err)
	}
	if err := os.Link(original, linked); err == nil {
		hardlinkBudget := newProjectPreviewBudget()
		previews := readImportEnvFilesWithBudget(root, []string{original, linked}, hardlinkBudget)
		if len(previews) != 1 || hardlinkBudget.readBytes != int64(len("VALUE=hidden\n")) {
			t.Fatalf("hardlink previews=%#v budget=%#v, want one opened identity", previews, hardlinkBudget)
		}
	}
}

func TestImportEnvPreviewPinsOneRootIdentityAcrossCandidates(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("FIRST=original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "second.env"), []byte("SECOND=original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replaced := false
	previews := readImportEnvFilesWithBudgetContextObserved(
		context.Background(),
		root,
		[]string{"second.env"},
		newProjectPreviewBudget(),
		func(attempt int) {
			if attempt != 1 || replaced {
				return
			}
			replaced = true
			oldRoot := filepath.Join(parent, "project-original")
			if err := os.Rename(root, oldRoot); err != nil {
				t.Fatalf("replace project root: %v", err)
			}
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "second.env"), []byte("SECOND=attacker\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	)
	if !replaced {
		t.Fatal("test did not replace the project root")
	}
	if len(previews) != 1 || previews[0].Path != ".env" {
		t.Fatalf("root-replacement previews = %#v, want only first pinned-root file", previews)
	}
	if strings.Contains(previews[0].Content, "attacker") {
		t.Fatalf("replacement-root content leaked into previews: %#v", previews)
	}
}

func TestAgentProjectWalkBoundsHugeBreadthAndSupportsSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	for i := 0; i < 256; i++ {
		path := filepath.Join(root, fmt.Sprintf("junk-%03d.txt", i))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	first, visited, err := boundedAgentProjectCandidatesWithLimit(root, 64)
	if err != nil || visited != 64 || len(first) > maxAgentProjectCandidates {
		t.Fatalf("bounded candidates=%#v visited=%d err=%v", first, visited, err)
	}
	if len(first) != 1 || first[0] != composePath {
		t.Fatalf("priority candidates=%#v, want compose.yaml despite incomplete directory enumeration", first)
	}
	second, secondVisited, err := boundedAgentProjectCandidatesWithLimit(root, 64)
	if err != nil || secondVisited != visited || strings.Join(second, "\n") != strings.Join(first, "\n") {
		t.Fatalf("bounded walk was not deterministic: first=%#v second=%#v visited=%d/%d err=%v", first, second, visited, secondVisited, err)
	}
	for i := 0; i < maxAgentProjectCandidates+32; i++ {
		path := filepath.Join(root, fmt.Sprintf("Dockerfile.%03d", i))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	complete, _, err := boundedAgentProjectCandidatesWithLimit(root, maxAgentProjectVisitedEntries)
	if err != nil {
		t.Fatalf("complete bounded walk: %v", err)
	}
	foundPriority := false
	for _, candidate := range complete {
		if candidate == composePath {
			foundPriority = true
			break
		}
	}
	if !foundPriority || len(complete) > maxAgentProjectCandidates {
		t.Fatalf("priority file was evicted from capped complete walk: count=%d found=%t", len(complete), foundPriority)
	}

	rootLink := filepath.Join(filepath.Dir(root), "agent-project-link")
	if err := os.Symlink(root, rootLink); err == nil {
		files, err := readAgentProjectFiles(rootLink)
		if err != nil || len(files) == 0 || files[0].Path != "compose.yaml" {
			t.Fatalf("agent files through symlinked root = %#v, %v", files, err)
		}
	}
}

func TestAgentProjectReadEmitsPriorityFilesBeforeFinalDTOCap(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxAgentProjectFiles+16; index++ {
		path := filepath.Join(root, fmt.Sprintf("Dockerfile.%03d", index))
		if err := os.WriteFile(path, []byte("FROM scratch\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := readAgentProjectFiles(root)
	if err != nil {
		t.Fatalf("readAgentProjectFiles() error = %v", err)
	}
	if len(files) != maxAgentProjectFiles {
		t.Fatalf("agent file count = %d, want %d", len(files), maxAgentProjectFiles)
	}
	if files[0].Path != "compose.yaml" {
		t.Fatalf("first agent file = %q, want priority compose.yaml", files[0].Path)
	}
}

func TestAgentDirectoryAndRootIdentityChangesAreRejected(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "project")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatalf("Mkdir(project): %v", err)
	}
	subPath := filepath.Join(rootPath, "config")
	if err := os.Mkdir(subPath, 0o755); err != nil {
		t.Fatalf("Mkdir(config): %v", err)
	}
	root, err := verifyProjectReadRoot(rootPath)
	if err != nil {
		t.Fatalf("verifyProjectReadRoot(): %v", err)
	}
	expected, err := os.Lstat(subPath)
	if err != nil {
		t.Fatalf("Lstat(config): %v", err)
	}
	if err := pinFileIdentity(expected); err != nil {
		t.Fatalf("pinFileIdentity(config): %v", err)
	}
	if err := os.Rename(subPath, subPath+"-old"); err != nil {
		t.Fatalf("Rename(config): %v", err)
	}
	if err := os.Mkdir(subPath, 0o755); err != nil {
		t.Fatalf("Mkdir(replacement config): %v", err)
	}
	if handle, err := openVerifiedAgentDirectory(root, agentWalkDirectory{path: subPath, depth: 1, expected: expected}); !errors.Is(err, errBoundedFileChanged) {
		if handle != nil {
			_ = handle.Close()
		}
		t.Fatalf("replacement directory error = %v, want %v", err, errBoundedFileChanged)
	}

	if err := os.Rename(rootPath, rootPath+"-old"); err != nil {
		t.Fatalf("Rename(project): %v", err)
	}
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatalf("Mkdir(replacement project): %v", err)
	}
	if err := root.verifyCurrent(); !errors.Is(err, errBoundedFileChanged) {
		t.Fatalf("replacement root error = %v, want %v", err, errBoundedFileChanged)
	}
}

func TestAgentProjectReadCancellationAndErrorsArePathFree(t *testing.T) {
	secretRoot := filepath.Join(t.TempDir(), "customer-secret-project")
	_, err := readAgentProjectFilesContext(context.Background(), secretRoot)
	if !apperror.IsCode(err, apperror.Conflict) || strings.Contains(err.Error(), secretRoot) || strings.Contains(err.Error(), "customer-secret") {
		t.Fatalf("missing-root error was not path-free: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = readAgentProjectFilesContext(ctx, secretRoot)
	if !apperror.IsCode(err, apperror.Cancelled) || strings.Contains(err.Error(), secretRoot) {
		t.Fatalf("cancelled read error = %v, want path-free Cancelled", err)
	}

	pathErr := &os.PathError{Op: "open", Path: secretRoot, Err: fs.ErrPermission}
	for name, safeErr := range map[string]error{
		"draft":    agentDraftFileReadError(pathErr),
		"editable": agentEditableFileReadError(pathErr),
		"write":    safeAgentFilesystemError(pathErr, "File edit could not be applied safely"),
	} {
		if strings.Contains(safeErr.Error(), secretRoot) || strings.Contains(safeErr.Error(), "customer-secret") {
			t.Fatalf("%s error leaked a path: %v", name, safeErr)
		}
	}
	_, _, err = resolveAgentProjectPath(secretRoot, "compose.yaml")
	if !apperror.IsCode(err, apperror.Conflict) || strings.Contains(err.Error(), secretRoot) || strings.Contains(err.Error(), "customer-secret") {
		t.Fatalf("path resolution error was not path-free: %v", err)
	}
}

func TestSafeComposeServiceNamesAreBoundedTruthfulAndDeterministic(t *testing.T) {
	services := []composecore.ServiceConfig{
		{Name: "z-control-plane"},
		{Name: "z-control-plane"},
		{Name: "a-database"},
		{Name: strings.Repeat("x", maxProjectPreviewNameBytes+1)},
		{Name: string([]byte{'b', 'a', 'd', 0xff})},
	}
	budget := newProjectPreviewBudget()
	names := safeComposeServiceNames(context.Background(), services, budget)
	if got := strings.Join(names, ","); got != "a-database,z-control-plane" {
		t.Fatalf("safe service names = %q", got)
	}
	if budget.dtoBytes != int64(len("a-database")+len("z-control-plane")) {
		t.Fatalf("service DTO budget = %d", budget.dtoBytes)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := safeComposeServiceNames(cancelled, services, newProjectPreviewBudget()); len(got) != 0 {
		t.Fatalf("cancelled service names = %#v, want none", got)
	}
}

func TestComposeStructurePreviewMasksCustomKeysAndTagsDeterministically(t *testing.T) {
	raw := `services:
  super-secret-service: !custom
    image: !vault private.example/secret-image
    x-secret-key: hidden-value
    environment:
      SUPER_SECRET_VARIABLE: opaque-token
    labels:
      com.example/secret-label: label-secret
`
	first := composeStructurePreview(raw)
	second := composeStructurePreview(raw)
	if first != second {
		t.Fatalf("Compose preview is not deterministic:\n%s\n---\n%s", first, second)
	}
	assertPreviewExcludes(t, first,
		"super-secret-service", "secret-image", "x-secret-key", "SUPER_SECRET_VARIABLE", "opaque-token", "secret-label", "label-secret", "!custom", "!vault")
	if strings.Contains(first, "!") {
		t.Fatalf("Compose preview retained a custom YAML tag: %q", first)
	}
	for _, expected := range []string{"services:", "image:", "environment:", "labels:", "redacted-key-", "[REDACTED]"} {
		if !strings.Contains(first, expected) {
			t.Fatalf("Compose preview %q does not contain %q", first, expected)
		}
	}
}

func TestProjectPreviewsStayInRootAndHideAllValues(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	composeContent := `# password=comment-secret
services:
  app:
    image: private.example/app:secret-tag
    environment:
      HARMLESS_NAME: opaque-value-123456789
      PRIVATE_KEY: |
        -----BEGIN PRIVATE KEY-----
        secret-pem-body
`
	if err := os.WriteFile(composePath, []byte(composeContent), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	outsideDir := t.TempDir()
	outsideCompose := filepath.Join(outsideDir, "outside.yaml")
	if err := os.WriteFile(outsideCompose, []byte("outside_secret: leaked"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside compose): %v", err)
	}
	composeLink := filepath.Join(root, "compose.link.yaml")
	_ = os.Symlink(outsideCompose, composeLink)

	rawFiles := readComposeRawFiles(store.ProjectRecord{
		WorkingDir:   root,
		ComposeFiles: []string{composePath, outsideCompose, composeLink},
	})
	if len(rawFiles) != 1 || rawFiles[0].Path != "compose.yaml" {
		t.Fatalf("raw Compose previews = %#v", rawFiles)
	}
	assertPreviewExcludes(t, rawFiles[0].Content,
		"comment-secret", "secret-tag", "opaque-value-123456789", "secret-pem-body", "BEGIN PRIVATE KEY")
	for _, key := range []string{"services:", "redacted-key-", "image:", "environment:", "[REDACTED]"} {
		if !strings.Contains(rawFiles[0].Content, key) {
			t.Fatalf("Compose preview %q does not retain structural key %q", rawFiles[0].Content, key)
		}
	}

	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("# token=comment-secret\nPUBLIC_NAME=opaque-value\nPRIVATE_KEY=\"first\nsecret-continuation\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.env): %v", err)
	}
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(config): %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "web.env"), []byte("export API_URL=https://secret.example\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(web.env): %v", err)
	}
	outsideEnv := filepath.Join(outsideDir, "outside.env")
	if err := os.WriteFile(outsideEnv, []byte("OUTSIDE_SECRET=leaked"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside env): %v", err)
	}
	envLink := filepath.Join(root, "linked.env")
	_ = os.Symlink(outsideEnv, envLink)
	oversizedEnv := filepath.Join(root, "oversized.env")
	oversized, err := os.Create(oversizedEnv)
	if err != nil {
		t.Fatalf("Create(oversized env): %v", err)
	}
	if err := oversized.Truncate(maxProjectPreviewFileBytes + 1); err != nil {
		t.Fatalf("Truncate(oversized env): %v", err)
	}
	if err := oversized.Close(); err != nil {
		t.Fatalf("Close(oversized env): %v", err)
	}

	envFiles := readImportEnvFiles(root, []string{
		".env",
		filepath.Join("config", "web.env"),
		filepath.Join("..", filepath.Base(outsideDir), "outside.env"),
		outsideEnv,
		envLink,
		oversizedEnv,
	})
	if len(envFiles) != 2 || envFiles[0].Path != ".env" || envFiles[1].Path != "config/web.env" {
		t.Fatalf("env previews = %#v", envFiles)
	}
	assertPreviewExcludes(t, envFiles[0].Content, "comment-secret", "opaque-value", "secret-continuation")
	assertPreviewExcludes(t, envFiles[1].Content, "https://secret.example")
	for _, preview := range envFiles {
		if filepath.IsAbs(preview.Path) || !strings.Contains(preview.Content, "[REDACTED]") {
			t.Fatalf("unsafe env preview = %#v", preview)
		}
	}

	config := models.ComposeConfigResult{
		ResolvedYAML: composeContent,
		EnvFiles:     []string{envPath, outsideEnv, envLink},
		Errors:       []string{"yaml: line 7: opaque-value-123456789 was invalid"},
	}
	sanitizeComposeConfigForDisplay(root, &config)
	assertPreviewExcludes(t, config.ResolvedYAML,
		"comment-secret", "secret-tag", "opaque-value-123456789", "secret-pem-body")
	if len(config.EnvFiles) != 1 || config.EnvFiles[0] != ".env" {
		t.Fatalf("sanitized config env file names = %#v", config.EnvFiles)
	}
	if len(config.Errors) != 1 || !strings.Contains(config.Errors[0], "line 7") {
		t.Fatalf("sanitized config errors = %#v", config.Errors)
	}
	assertPreviewExcludes(t, config.Errors[0], "opaque-value-123456789")
}

func TestReviewImportProjectReturnsOnlyRedactedInRootPreviews(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "redacted-review")
	if err := os.WriteFile(composeFile, []byte("services:\n  app:\n    image: private.example/app:raw-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("API_TOKEN=raw-env-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.env): %v", err)
	}
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  customer-secret-api:\n    image: private.example/app:resolved-secret\n    env_file: .env\n    environment:\n      API_TOKEN: raw-interpolated-secret\n",
	}
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}

	review, err := service.ReviewImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ReviewImportProject(): %v", err)
	}
	if len(review.Compose.RawFiles) != 1 || review.Compose.RawFiles[0].Path != "compose.yaml" {
		t.Fatalf("Compose raw previews = %#v", review.Compose.RawFiles)
	}
	if len(review.EnvFiles) != 1 || review.EnvFiles[0].Path != ".env" {
		t.Fatalf("env previews = %#v", review.EnvFiles)
	}
	if len(review.Compose.EnvFiles) != 1 || review.Compose.EnvFiles[0] != ".env" {
		t.Fatalf("Compose env file names = %#v", review.Compose.EnvFiles)
	}
	if len(review.Services) != 1 || review.Services[0] != "customer-secret-api" {
		t.Fatalf("bounded review services = %#v", review.Services)
	}
	for _, preview := range []string{
		review.Compose.ResolvedYAML,
		review.Compose.RawFiles[0].Content,
		review.EnvFiles[0].Content,
	} {
		assertPreviewExcludes(t, preview,
			"raw-secret", "resolved-secret", "raw-env-secret", "raw-interpolated-secret", "customer-secret-api", root)
		if !strings.Contains(preview, "[REDACTED]") {
			t.Fatalf("review preview is not explicitly redacted: %q", preview)
		}
	}
}

func TestReviewImportProjectKeepsActualPreviewsWhenEnvMetadataBudgetIsFull(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "metadata-budget-review")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DEFAULT_SECRET=hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	output.WriteString("services:\n  app:\n    env_file:\n")
	for index := 0; index < maxProjectPreviewFiles; index++ {
		name := fmt.Sprintf("env-%02d.env", index)
		if err := os.WriteFile(filepath.Join(root, name), []byte("SECRET_VALUE=hidden\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		output.WriteString("      - " + name + "\n")
	}
	runner := composeRunnerFunc(func(context.Context, string, ...string) (*providers.CommandResult, error) {
		return &providers.CommandResult{Stdout: output.String()}, nil
	})
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}
	review, err := service.ReviewImportProject(ctx, models.ImportProjectRequest{FolderPath: root, ComposeFilePaths: []string{composeFile}})
	if err != nil {
		t.Fatalf("ReviewImportProject() error = %v", err)
	}
	if got := len(review.Compose.EnvFiles); got != maxProjectPreviewFiles {
		t.Fatalf("metadata env files = %d, want %d", got, maxProjectPreviewFiles)
	}
	if len(review.Compose.RawFiles) != 1 || review.Compose.RawFiles[0].Path != "compose.yaml" {
		t.Fatalf("actual Compose preview was evicted by metadata: %#v", review.Compose.RawFiles)
	}
	if len(review.EnvFiles) == 0 || review.EnvFiles[0].Path != ".env" {
		t.Fatalf("actual env previews were evicted by metadata: %#v", review.EnvFiles)
	}
}

func TestReviewImportProjectPreservesComposeCancellation(t *testing.T) {
	root, composeFile := writeServiceComposeProject(t, "cancelled-review")
	db := openServiceTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	runner := composeRunnerFunc(func(context.Context, string, ...string) (*providers.CommandResult, error) {
		calls++
		cancel()
		return &providers.CommandResult{Stdout: "services: {}\n"}, context.Canceled
	})
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}

	_, err := service.ReviewImportProject(ctx, models.ImportProjectRequest{FolderPath: root, ComposeFilePaths: []string{composeFile}})
	if !apperror.IsCode(err, apperror.Cancelled) {
		t.Fatalf("ReviewImportProject(cancelled) error = %v, calls=%d, want Cancelled", err, calls)
	}
}

func TestComposeRendererFailuresPreserveCodeAndHidePathsAndSecrets(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "safe-compose-errors")
	secretPath := filepath.Join(t.TempDir(), "private-token.env")
	providerErr := apperror.New(
		apperror.ProviderNotReady,
		"provider failed at "+secretPath+" with raw-token-value",
		apperror.WithDetail("line 17: PASSWORD=raw-token-value"),
	)
	failingClient := composecore.NewClient(nilResultComposeRunner{err: providerErr})
	projectService := &ProjectService{
		Client:   failingClient,
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}
	missingSecretPath := filepath.Join(t.TempDir(), "missing-private-token-project")
	_, err := projectService.ReviewImportProject(ctx, models.ImportProjectRequest{FolderPath: missingSecretPath})
	assertSafeComposeError(t, err, apperror.ComposeInvalid, "Resolve Compose project files failed", missingSecretPath, "private-token")
	_, err = projectService.ImportProject(ctx, models.ImportProjectRequest{FolderPath: missingSecretPath})
	assertSafeComposeError(t, err, apperror.ComposeInvalid, "Resolve Compose project files failed", missingSecretPath, "private-token")

	_, err = projectService.ReviewImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	assertSafeComposeError(t, err, apperror.ProviderNotReady, "Compose project validation failed", secretPath, "raw-token-value", "PASSWORD")
	_, err = projectService.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	assertSafeComposeError(t, err, apperror.ProviderNotReady, "Compose project validation failed", secretPath, "raw-token-value", "PASSWORD")

	successRunner := newFakeComposeRunner()
	successRunner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{Stdout: "services:\n  app:\n    image: nginx:alpine\n"}
	successService := &ProjectService{
		Client:   composecore.NewClient(successRunner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}
	imported, err := successService.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject(success): %v", err)
	}
	composeService := &ComposeService{
		Client:   failingClient,
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}
	_, err = composeService.Config(ctx, imported.Summary.ID)
	assertSafeComposeError(t, err, apperror.ProviderNotReady, "Compose config failed", secretPath, "raw-token-value", "PASSWORD")

	invalidRunner := newFakeComposeRunner()
	invalidRunner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stderr:   "yaml line 9: failed at " + secretPath + " with API_TOKEN=raw-token-value",
		ExitCode: 1,
	}
	invalidService := &ProjectService{
		Client:   composecore.NewClient(invalidRunner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}
	review, err := invalidService.ReviewImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ReviewImportProject(invalid): %v", err)
	}
	if len(review.Compose.Errors) != 1 || !strings.Contains(review.Compose.Errors[0], "line 9") {
		t.Fatalf("safe review errors = %#v", review.Compose.Errors)
	}
	assertPreviewExcludes(t, review.Compose.Errors[0], secretPath, "raw-token-value", "API_TOKEN")
	invalidComposeService := &ComposeService{
		Client:   invalidService.Client,
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}
	invalidConfig, err := invalidComposeService.Config(ctx, imported.Summary.ID)
	if err != nil || invalidConfig == nil || len(invalidConfig.Errors) != 1 || !strings.Contains(invalidConfig.Errors[0], "line 9") {
		t.Fatalf("ComposeService.Config(invalid) = %#v, %v", invalidConfig, err)
	}
	assertPreviewExcludes(t, invalidConfig.Errors[0], secretPath, "raw-token-value", "API_TOKEN")
	_, err = invalidService.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	assertSafeComposeError(t, err, apperror.ComposeInvalid, "Compose project validation failed", secretPath, "raw-token-value", "API_TOKEN")
}

func TestAgentFilePlanRejectsOversizedAndSpecialExistingTargets(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	projectService, projectID, root := importAgentTestProject(t, ctx, db)
	plans := security.NewAgentFileEditPlanStore(nil)
	t.Cleanup(plans.Close)
	service := &AgentService{Project: projectService, Plans: plans, Audit: db.Audit(), writeFile: writeAgentPlanFile}

	largePath := filepath.Join(root, ".env.large")
	large, err := os.Create(largePath)
	if err != nil {
		t.Fatalf("Create(.env.large): %v", err)
	}
	if err := large.Truncate(maxAgentFileEditBytes + 1); err != nil {
		t.Fatalf("Truncate(.env.large): %v", err)
	}
	if err := large.Close(); err != nil {
		t.Fatalf("Close(.env.large): %v", err)
	}
	_, err = service.PlanFileEdit(ctx, models.AgentFileEditRequest{
		ProjectID: projectID,
		Path:      ".env.large",
		Content:   "SAFE=placeholder\n",
	})
	if !apperror.IsCode(err, apperror.Conflict) || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("PlanFileEdit(large) error = %v, want bounded conflict", err)
	}

	directoryPath := filepath.Join(root, ".env.directory")
	if err := os.Mkdir(directoryPath, 0o755); err != nil {
		t.Fatalf("Mkdir(.env.directory): %v", err)
	}
	_, err = service.PlanFileEdit(ctx, models.AgentFileEditRequest{
		ProjectID: projectID,
		Path:      ".env.directory",
		Content:   "SAFE=placeholder\n",
	})
	if !apperror.IsCode(err, apperror.Conflict) || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("PlanFileEdit(directory) error = %v, want regular-file conflict", err)
	}
}

func TestResolveImportFilesRejectsDirectoryAndSymlinkInputs(t *testing.T) {
	root := t.TempDir()
	if _, _, err := resolveImportFiles(models.ImportProjectRequest{ComposeFilePaths: []string{root}}); !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("resolveImportFiles(directory) error = %v, want ComposeInvalid", err)
	}

	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	linkPath := filepath.Join(root, "compose-link.yaml")
	if err := os.Symlink(composePath, linkPath); err == nil {
		if _, _, err := resolveImportFiles(models.ImportProjectRequest{ComposeFilePaths: []string{linkPath}}); !apperror.IsCode(err, apperror.ComposeInvalid) {
			t.Fatalf("resolveImportFiles(symlink) error = %v, want ComposeInvalid", err)
		}
	}
}

func TestResolveImportFilesRejectsSelectionsAcrossProjectFolders(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	first := filepath.Join(firstRoot, "compose.yaml")
	second := filepath.Join(secondRoot, "compose.override.yaml")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("services: {}\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	_, _, err := resolveImportFiles(models.ImportProjectRequest{ComposeFilePaths: []string{first, second}})
	if !apperror.IsCode(err, apperror.ComposeInvalid) || !strings.Contains(err.Error(), "share one project folder") {
		t.Fatalf("cross-folder selection error = %v, want explicit ComposeInvalid", err)
	}
}

func TestReviewImportProjectAcceptsNestedOverrideWithinSelectedProjectRoot(t *testing.T) {
	root := t.TempDir()
	composeFile := filepath.Join(root, "compose.yaml")
	overrideFile := filepath.Join(root, "overrides", "dev.yaml")
	if err := os.MkdirAll(filepath.Dir(overrideFile), 0o755); err != nil {
		t.Fatalf("MkdirAll(overrides): %v", err)
	}
	if err := os.WriteFile(composeFile, []byte("services:\n  app:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	if err := os.WriteFile(overrideFile, []byte("services:\n  app:\n    environment:\n      MODE: development\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(override): %v", err)
	}
	runner := composeRunnerFunc(func(context.Context, string, ...string) (*providers.CommandResult, error) {
		return &providers.CommandResult{Stdout: "services:\n  app:\n    image: nginx:alpine\n"}, nil
	})
	db := openServiceTestStore(t)
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}

	review, err := service.ReviewImportProject(context.Background(), models.ImportProjectRequest{
		ComposeFilePaths: []string{composeFile, overrideFile},
	})
	if err != nil {
		t.Fatalf("ReviewImportProject(nested override): %v", err)
	}
	if review.FolderPath != root {
		t.Fatalf("review folder = %q, want selected project root %q", review.FolderPath, root)
	}
	paths := make([]string, 0, len(review.Compose.RawFiles))
	for _, file := range review.Compose.RawFiles {
		paths = append(paths, filepath.ToSlash(file.Path))
	}
	if !slices.Equal(paths, []string{"compose.yaml", "overrides/dev.yaml"}) {
		t.Fatalf("review Compose files = %#v, want root file and nested override", paths)
	}
}

func assertPreviewExcludes(t *testing.T, preview string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(preview, value) {
			t.Fatalf("preview leaked %q: %q", value, preview)
		}
	}
}

func assertSafeComposeError(t *testing.T, err error, code apperror.Code, message string, forbidden ...string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %v, want AppError", err)
	}
	if appErr.Code != code || appErr.Message != message {
		t.Fatalf("safe error = %#v, want code=%s message=%q", appErr, code, message)
	}
	for _, value := range forbidden {
		if strings.Contains(appErr.Detail, value) || strings.Contains(appErr.Message, value) {
			t.Fatalf("safe error leaked %q: %#v", value, appErr)
		}
	}
}

type nilResultComposeRunner struct {
	err error
}

func (r nilResultComposeRunner) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	return nil, r.err
}

func (r nilResultComposeRunner) RunComposeEnv(context.Context, string, []string, ...string) (*providers.CommandResult, error) {
	return nil, r.err
}

type composeRunnerFunc func(context.Context, string, ...string) (*providers.CommandResult, error)

func (fn composeRunnerFunc) RunCompose(ctx context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
	return fn(ctx, workdir, args...)
}

func (fn composeRunnerFunc) RunComposeEnv(ctx context.Context, workdir string, _ []string, args ...string) (*providers.CommandResult, error) {
	return fn(ctx, workdir, args...)
}
