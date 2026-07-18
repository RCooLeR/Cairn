package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestClientRunsProviderWithArgvWorkdirEnv(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.outputs["/tmp/app|-f compose.yaml --profile dev config"] = providers.CommandResult{
		Stdout: "services:\n  web:\n    image: nginx:alpine\n",
	}
	client := NewClient(runner)

	config, err := client.Config(context.Background(), ProjectOptions{
		Workdir:     "/tmp/app",
		Files:       []string{"compose.yaml"},
		ProjectName: "demo",
		Profiles:    []string{"dev"},
		Env:         []string{"FOO=bar"},
	})
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if !config.Valid {
		t.Fatalf("config invalid: %#v", config.Errors)
	}
	if got, want := runner.calls[0].args, []string{"-f", "compose.yaml", "--profile", "dev", "config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if got := runner.calls[0].workdir; got != "/tmp/app" {
		t.Fatalf("workdir = %q", got)
	}
	if !contains(runner.calls[0].env, "COMPOSE_PROJECT_NAME=demo") || !contains(runner.calls[0].env, "FOO=bar") {
		t.Fatalf("env = %#v", runner.calls[0].env)
	}
}

func TestClientMapsHostProjectPathsBeforeComposeRun(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	hostWorkdir := `E:\Development\project`
	hostProjectDirectory := `E:\Development\project-source`
	hostFile := `E:\Development\project\compose.yaml`
	backendWorkdir := "/mnt/e/Development/project"
	backendProjectDirectory := "/mnt/e/Development/project-source"
	backendFile := "/mnt/e/Development/project/compose.yaml"
	runner.hostToBackend[hostWorkdir] = backendWorkdir
	runner.hostToBackend[hostProjectDirectory] = backendProjectDirectory
	runner.hostToBackend[hostFile] = backendFile
	runner.outputs[backendWorkdir+"|--project-directory "+backendProjectDirectory+" -f "+backendFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  web:\n    image: nginx:alpine\n",
	}
	client := NewClient(runner)

	if _, err := client.Config(context.Background(), ProjectOptions{
		Workdir:          hostWorkdir,
		ProjectDirectory: hostProjectDirectory,
		Files:            []string{hostFile},
	}); err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if got := runner.calls[0].workdir; got != backendWorkdir {
		t.Fatalf("workdir = %q, want %q", got, backendWorkdir)
	}
	if got, want := runner.calls[0].args, []string{"--project-directory", backendProjectDirectory, "-f", backendFile, "config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestConfigVerifiedFailsClosedWhenBackendPathMappingFails(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	runner := newFakeRunner()
	runner.mapBackendErr = errors.New("mapping failed for a private host path")
	client := NewClient(runner)

	_, _, err := client.ConfigVerified(context.Background(), ProjectOptions{Workdir: root, Files: []string{composePath}})
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("ConfigVerified() error = %v, want ProviderNotReady", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner was called after path mapping failed: %#v", runner.calls)
	}
}

func TestConfigVerifiedPreservesCancellationWhenRunnerReturnsAResult(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := &cancelingConfigRunner{cancel: cancel}

	config, _, err := NewClient(runner).ConfigVerified(ctx, ProjectOptions{Workdir: root, Files: []string{composePath}})
	if config != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ConfigVerified(cancelled) = %#v, %v; want nil, context.Canceled", config, err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
}

func TestComposeCommandsPreserveCancellationAfterRunnerResult(t *testing.T) {
	tests := map[string]func(context.Context, *Client) error{
		"version": func(ctx context.Context, client *Client) error {
			_, err := client.Version(ctx)
			return err
		},
		"list": func(ctx context.Context, client *Client) error {
			_, err := client.Ls(ctx, ListOptions{All: true})
			return err
		},
		"ps": func(ctx context.Context, client *Client) error {
			_, err := client.Ps(ctx, ProjectOptions{})
			return err
		},
		"config": func(ctx context.Context, client *Client) error {
			_, err := client.Config(ctx, ProjectOptions{})
			return err
		},
		"action": func(ctx context.Context, client *Client) error {
			_, err := client.Start(ctx, ProjectOptions{})
			return err
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			runner := &cancelAfterResultRunner{cancel: cancel}
			err := call(ctx, NewClient(runner))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("command error = %v, want context.Canceled", err)
			}
			if runner.calls != 1 {
				t.Fatalf("runner calls = %d, want 1", runner.calls)
			}
		})
	}
}

func TestComposeServiceOperandsRejectLeadingHyphensBeforeRunner(t *testing.T) {
	tests := map[string]func(context.Context, *Client) error{
		"start": func(ctx context.Context, client *Client) error {
			_, err := client.StartServices(ctx, ProjectOptions{}, []string{"--wait"})
			return err
		},
		"stop": func(ctx context.Context, client *Client) error {
			_, err := client.StopServices(ctx, ProjectOptions{}, []string{"--timeout"})
			return err
		},
		"restart": func(ctx context.Context, client *Client) error {
			_, err := client.RestartServices(ctx, ProjectOptions{}, []string{"--no-deps"})
			return err
		},
		"pull": func(ctx context.Context, client *Client) error {
			_, err := client.PullServices(ctx, ProjectOptions{}, []string{"--ignore-pull-failures"})
			return err
		},
		"build": func(ctx context.Context, client *Client) error {
			_, err := client.Build(ctx, ProjectOptions{}, BuildOptions{Services: []string{"--no-cache"}})
			return err
		},
		"up": func(ctx context.Context, client *Client) error {
			_, err := client.UpServices(ctx, ProjectOptions{}, UpOptions{Services: []string{"--remove-orphans"}})
			return err
		},
		"scale": func(ctx context.Context, client *Client) error {
			_, err := client.ScaleService(ctx, ProjectOptions{}, "--scale", 2)
			return err
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			runner := newFakeRunner()
			err := call(context.Background(), NewClient(runner))
			if !apperror.IsCode(err, apperror.Conflict) || !strings.Contains(err.Error(), "cannot start with a hyphen") {
				t.Fatalf("service operand error = %v", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner was called with a flag-like service: %#v", runner.calls)
			}
		})
	}
}

func TestConfigVerifiedRejectsOversizedDefaultEnvBeforeRunner(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	env, err := os.Create(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("Create(.env): %v", err)
	}
	if err := env.Truncate(maxVerifiedConfigFileBytes + 1); err != nil {
		_ = env.Close()
		t.Fatalf("Truncate(.env): %v", err)
	}
	if err := env.Close(); err != nil {
		t.Fatalf("Close(.env): %v", err)
	}
	runner := newFakeRunner()

	_, _, err = NewClient(runner).ConfigVerified(context.Background(), ProjectOptions{Workdir: root, Files: []string{composePath}})
	if !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("ConfigVerified(oversized .env) error = %v, want ComposeInvalid", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner was called with an oversized default environment: %#v", runner.calls)
	}
}

func TestVerifiedComposeDiscoveryPreservesDefaultOverride(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"compose.yaml", "compose.override.yaml", "docker-compose.yml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("services: {}\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	files := discoverVerifiedComposeFiles(root)
	want := []string{filepath.Join(root, "compose.yaml"), filepath.Join(root, "compose.override.yaml")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("discovered verified files = %#v, want %#v", files, want)
	}
}

func TestConfigVerifiedRejectsEscapingOrDynamicDependenciesBeforeRunner(t *testing.T) {
	tests := map[string]string{
		"escaping env file":   "services:\n  app:\n    env_file: ../outside.env\n",
		"dynamic include":     "include:\n  - ${INCLUDE_ROOT}/compose.yaml\nservices: {}\n",
		"merged env file":     "x-defaults: &defaults\n  env_file: ../outside.env\nservices:\n  app:\n    <<: *defaults\n",
		"merged services map": "x-services: &shared-services\n  app:\n    env_file: ../outside.env\nservices:\n  <<: *shared-services\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			composePath := filepath.Join(root, "compose.yaml")
			if err := os.WriteFile(composePath, []byte(content), 0o600); err != nil {
				t.Fatalf("WriteFile(compose): %v", err)
			}
			runner := newFakeRunner()
			_, _, err := NewClient(runner).ConfigVerified(context.Background(), ProjectOptions{Workdir: root, Files: []string{composePath}})
			if !apperror.IsCode(err, apperror.ComposeInvalid) || !strings.Contains(err.Error(), "static paths inside the project") {
				t.Fatalf("ConfigVerified() error = %v, want safe dependency rejection", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner received an unsafe dependency: %#v", runner.calls)
			}
		})
	}
}

func TestConfigVerifiedRejectsOversizedReferencedFileBeforeRunner(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services:\n  app:\n    env_file: app.env\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(compose): %v", err)
	}
	env, err := os.Create(filepath.Join(root, "app.env"))
	if err != nil {
		t.Fatalf("Create(app.env): %v", err)
	}
	if err := env.Truncate(maxVerifiedConfigFileBytes + 1); err != nil {
		_ = env.Close()
		t.Fatalf("Truncate(app.env): %v", err)
	}
	if err := env.Close(); err != nil {
		t.Fatalf("Close(app.env): %v", err)
	}
	runner := newFakeRunner()

	_, _, err = NewClient(runner).ConfigVerified(context.Background(), ProjectOptions{Workdir: root, Files: []string{composePath}})
	if !apperror.IsCode(err, apperror.ComposeInvalid) || !strings.Contains(err.Error(), "bounded regular files") {
		t.Fatalf("ConfigVerified(oversized dependency) error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner received an oversized dependency: %#v", runner.calls)
	}
}

func TestConfigVerifiedSnapshotsRecursiveDependencyClosureAndPreservesProjectBase(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, content string) string {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", rel, err)
		}
		return path
	}
	base := write("main/compose.yaml", `include:
  - path: ../included/compose.yaml
services:
  app:
    extends:
      file: extended.yaml
      service: base
    env_file: app.env
    label_file: app.labels
configs:
  app:
    file: app.conf
secrets:
  app:
    file: app.secret
`)
	override := write("overrides/compose.override.yaml", "services:\n  app:\n    image: example/app:latest\n")
	write("main/extended.yaml", "services:\n  base:\n    env_file: extended.env\n")
	write("main/app.env", "APP_VALUE=before\n")
	write("main/extended.env", "EXTENDED_VALUE=before\n")
	write("main/app.labels", "com.example.review=before\n")
	write("main/app.conf", "before\n")
	write("main/app.secret", "before\n")
	write("included/compose.yaml", "services:\n  worker:\n    env_file: worker.env\n")
	write("included/worker.env", "WORKER_VALUE=before\n")
	write("included/.env", "INCLUDED_DEFAULT=before\n")
	write(".env", "TOP_DEFAULT=before\n")

	runner := &verifiedClosureRunner{
		originalDependency: filepath.Join(root, "main", "app.env"),
		wantSnapshotFiles: []string{
			".env",
			"included/.env",
			"included/compose.yaml",
			"included/worker.env",
			"main/app.conf",
			"main/app.env",
			"main/app.labels",
			"main/app.secret",
			"main/compose.yaml",
			"main/extended.env",
			"main/extended.yaml",
			"overrides/compose.override.yaml",
		},
	}
	config, inputs, err := NewClient(runner).ConfigVerified(context.Background(), ProjectOptions{
		Workdir: root,
		Files:   []string{base, override},
	})
	if err != nil {
		t.Fatalf("ConfigVerified() error = %v", err)
	}
	if got, want := len(inputs), 2; got != want {
		t.Fatalf("top-level input count = %d, want %d", got, want)
	}
	if runner.projectDirectory != filepath.Join(runner.snapshotDir, "main") {
		t.Fatalf("project directory = %q, want private first-file directory", runner.projectDirectory)
	}
	if len(runner.configFiles) != 2 || runner.configFiles[0] != filepath.Join(runner.snapshotDir, "main", "compose.yaml") || runner.configFiles[1] != filepath.Join(runner.snapshotDir, "overrides", "compose.override.yaml") {
		t.Fatalf("private config files = %#v", runner.configFiles)
	}
	if got := config.Services[0].EnvFiles[0]; got != filepath.Join(root, "main", "app.env") {
		t.Fatalf("restored env file = %q", got)
	}
	if _, err := os.Stat(runner.snapshotDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private snapshot still exists after ConfigVerified: %v", err)
	}
}

func TestConfigVerifiedRejectsRecursiveIncludeCyclesBeforeRunner(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "included"), 0o700); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("include:\n  - included/compose.yaml\nservices: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "included", "compose.yaml"), []byte("include:\n  - ../compose.yaml\nservices: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newFakeRunner()
	_, _, err := NewClient(runner).ConfigVerified(context.Background(), ProjectOptions{Workdir: root, Files: []string{composePath}})
	if !apperror.IsCode(err, apperror.ComposeInvalid) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("ConfigVerified(include cycle) error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner was called for a cyclic include: %#v", runner.calls)
	}
}

func TestConfigVerifiedBoundsAggregateDependencyTraversal(t *testing.T) {
	root := t.TempDir()
	var content strings.Builder
	content.WriteString("services:\n  app:\n    env_file:\n")
	for index := 0; index < maxVerifiedConfigCandidates+1; index++ {
		content.WriteString("      - repeated.env\n")
	}
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "repeated.env"), []byte("VALUE=safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newFakeRunner()
	_, _, err := NewClient(runner).ConfigVerified(context.Background(), ProjectOptions{Workdir: root, Files: []string{composePath}})
	if !apperror.IsCode(err, apperror.ComposeInvalid) || !strings.Contains(err.Error(), "traversal exceeds") {
		t.Fatalf("ConfigVerified(dependency fanout) error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner was called after traversal limit: %#v", runner.calls)
	}
}

func TestClientRuntimeScopeBindingIsOneShotAndRejectsTargetChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runner := &scopeRunner{id: "provider", contextName: "context-a"}
	client := NewClient(runner)
	scopeA := runtimescope.Must("provider", "context-a")
	if err := client.BindRuntimeScope(scopeA); err != nil {
		t.Fatalf("BindRuntimeScope(A) error = %v", err)
	}
	if err := client.BindRuntimeScope(scopeA); err != nil {
		t.Fatalf("BindRuntimeScope(A again) error = %v", err)
	}
	if err := client.BindRuntimeScope(runtimescope.Must("provider", "context-b")); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("BindRuntimeScope(B) error = %v, want conflict", err)
	}
	if _, err := client.Config(ctx, ProjectOptions{}); err != nil {
		t.Fatalf("Config(A) error = %v", err)
	}
	if got := runner.callCount(); got != 1 {
		t.Fatalf("runner calls = %d, want 1", got)
	}

	runner.setContext("context-b")
	if _, err := client.Config(ctx, ProjectOptions{}); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("Config(after target change) error = %v, want not found", err)
	}
	if got := runner.callCount(); got != 1 {
		t.Fatalf("runner called after scope mismatch: %d", got)
	}
}

func TestClientRuntimeScopeBindingIsRaceSafe(t *testing.T) {
	t.Parallel()
	runner := &scopeRunner{id: "provider", contextName: "context-a"}
	client := NewClient(runner)
	scope := runtimescope.Must("provider", "context-a")
	if err := client.BindRuntimeScope(scope); err != nil {
		t.Fatalf("BindRuntimeScope() error = %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := client.BindRuntimeScope(scope); err != nil {
				t.Errorf("concurrent BindRuntimeScope() error = %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := client.Config(context.Background(), ProjectOptions{}); err != nil {
				t.Errorf("concurrent Config() error = %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestClientBuildAddsCairnLabelsDeterministically(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.outputs["/tmp/app|-f compose.yaml build --pull --label io.cairn.base.name=node:20-alpine --label io.cairn.project=linux_native/apps api"] = providers.CommandResult{}
	client := NewClient(runner)

	_, err := client.Build(context.Background(), ProjectOptions{
		Workdir: "/tmp/app",
		Files:   []string{"compose.yaml"},
	}, BuildOptions{
		Pull: true,
		Labels: map[string]string{
			"io.cairn.project":   "linux_native/apps",
			"io.cairn.base.name": "node:20-alpine",
			"empty":              "",
		},
		Services: []string{"api"},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{
		"-f", "compose.yaml", "build", "--pull",
		"--label", "io.cairn.base.name=node:20-alpine",
		"--label", "io.cairn.project=linux_native/apps",
		"api",
	}
	if got := runner.calls[0].args; !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestClientVersionRequiresMinimum(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.outputs["|version --format json"] = providers.CommandResult{
		Stdout: `{"version":"v2.19.9"}`,
	}
	client := NewClient(runner)

	_, err := client.Version(context.Background())
	if !apperror.IsCode(err, apperror.ComposeNotFound) {
		t.Fatalf("Version() error = %v, want %s", err, apperror.ComposeNotFound)
	}
}

func TestClientReturnsComposeInvalidWithDetail(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.outputs["/bad|config"] = providers.CommandResult{
		Stderr:   "services.app.image must be a string",
		ExitCode: 15,
	}
	runner.errors["/bad|config"] = errors.New("exit status 15")
	client := NewClient(runner)

	config, err := client.Config(context.Background(), ProjectOptions{Workdir: "/bad"})
	if !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("Config() error = %v, want %s", err, apperror.ComposeInvalid)
	}
	if config == nil || config.Valid {
		t.Fatalf("config = %#v, want invalid result", config)
	}
	if len(config.Errors) != 1 || !strings.Contains(config.Errors[0], "image must be a string") {
		t.Fatalf("errors = %#v", config.Errors)
	}
}

func TestComposeCommandErrorTrimsHugeOutput(t *testing.T) {
	t.Parallel()
	hugeOutput := strings.Repeat("pulling ollama layer\n", 1000)

	err := composeCommandError(
		apperror.ComposeInvalid,
		"Docker Compose failed",
		&providers.CommandResult{Stdout: hugeOutput, ExitCode: 1},
		errors.New("exit status 1"),
	)

	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %T, want AppError", err)
	}
	if len(appErr.Detail) > commandDetailOutputLimit+200 {
		t.Fatalf("detail length = %d, want trimmed near %d", len(appErr.Detail), commandDetailOutputLimit)
	}
	if !strings.Contains(appErr.Detail, "command output truncated") || !strings.Contains(appErr.Detail, "exit status 1") {
		t.Fatalf("detail did not include truncation marker and exit status: %q", appErr.Detail)
	}
}

func TestComposeCommandErrorRedactsCapturedSecrets(t *testing.T) {
	const secret = "compose-command-secret-value"
	err := composeCommandError(
		apperror.ComposeInvalid,
		"Docker Compose failed",
		&providers.CommandResult{Stderr: "TOKEN=" + secret, ExitCode: 1},
		errors.New("exit status 1"),
	)
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %T, want AppError", err)
	}
	if strings.Contains(appErr.Detail, secret) || !strings.Contains(appErr.Detail, "[REDACTED]") {
		t.Fatalf("Compose error detail was not redacted: %q", appErr.Detail)
	}
}

func TestComposeCommandErrorAddsNVIDIARuntimeHints(t *testing.T) {
	t.Parallel()

	err := composeCommandError(
		apperror.ComposeInvalid,
		"Compose project action failed",
		&providers.CommandResult{Stderr: `Error response from daemon: could not select device driver "nvidia" with capabilities: [[gpu]]`, ExitCode: 1},
		errors.New("exit status 1"),
	)

	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %T, want AppError", err)
	}
	if appErr.Message != "Compose project requires NVIDIA GPU runtime" {
		t.Fatalf("message = %q", appErr.Message)
	}
	if len(appErr.RepairHints) != 3 {
		t.Fatalf("repair hints = %#v", appErr.RepairHints)
	}
	if !strings.Contains(appErr.RepairHints[0], "NVIDIA Container Toolkit") || !strings.Contains(appErr.RepairHints[2], "CPU-only") {
		t.Fatalf("repair hints = %#v", appErr.RepairHints)
	}
}

func TestClientConfigAllTestdataProjectsIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real docker compose config integration runs only on Linux")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}

	client := NewClient(providers.NewLinuxNative(providers.LinuxNativeOptions{}))
	if _, err := client.Version(context.Background()); err != nil {
		t.Skipf("docker compose v2.20+ unavailable: %v", err)
	}

	for _, project := range testdataProjects(t) {
		project := project
		t.Run(project.expected.Project, func(t *testing.T) {
			config, err := client.Config(context.Background(), ProjectOptions{Workdir: absPath(t, project.dir)})
			if err != nil {
				t.Fatalf("Config() error = %v", err)
			}
			if !config.Valid {
				t.Fatalf("config invalid: %#v", config.Errors)
			}
			if project.expected.ServiceCount > 0 {
				if got := len(config.Services); got != project.expected.ServiceCount {
					t.Fatalf("service count = %d, want %d", got, project.expected.ServiceCount)
				}
				return
			}
			for _, expected := range project.expected.Services {
				service := findServiceConfig(t, config.Services, expected.Name)
				if expected.Image != "" && service.Image != expected.Image {
					t.Fatalf("%s image = %q, want %q", expected.Name, service.Image, expected.Image)
				}
				if expected.Healthcheck && !service.HasHealthcheck {
					t.Fatalf("%s healthcheck was not detected", expected.Name)
				}
			}
		})
	}
}

type fakeRunner struct {
	outputs       map[string]providers.CommandResult
	errors        map[string]error
	calls         []fakeCall
	hostToBackend map[string]string
	backendToHost map[string]string
	mapBackendErr error
}

type scopeRunner struct {
	mu          sync.Mutex
	id          string
	contextName string
	calls       int
}

type cancelingConfigRunner struct {
	cancel context.CancelFunc
	calls  int
}

type cancelAfterResultRunner struct {
	cancel context.CancelFunc
	calls  int
}

type verifiedClosureRunner struct {
	originalDependency string
	wantSnapshotFiles  []string
	snapshotDir        string
	projectDirectory   string
	configFiles        []string
}

func (r *verifiedClosureRunner) RunCompose(_ context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
	r.snapshotDir = workdir
	if !contains(args, "config") || !contains(args, "--no-interpolate") {
		return nil, fmt.Errorf("verified config args = %#v, want config --no-interpolate", args)
	}
	for index := 0; index+1 < len(args); index++ {
		switch args[index] {
		case "--project-directory":
			r.projectDirectory = args[index+1]
		case "-f":
			r.configFiles = append(r.configFiles, args[index+1])
		}
	}
	var files []string
	err := filepath.WalkDir(workdir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(workdir, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	want := append([]string(nil), r.wantSnapshotFiles...)
	sort.Strings(want)
	if !reflect.DeepEqual(files, want) {
		return nil, fmt.Errorf("snapshot files = %#v, want %#v", files, want)
	}
	if err := os.WriteFile(r.originalDependency, []byte("APP_VALUE=after\n"), 0o600); err != nil {
		return nil, err
	}
	snapshottedDependency := filepath.Join(workdir, "main", "app.env")
	content, err := os.ReadFile(snapshottedDependency)
	if err != nil {
		return nil, err
	}
	if string(content) != "APP_VALUE=before\n" {
		return nil, fmt.Errorf("snapshotted dependency changed: %q", content)
	}
	resolvedPath := filepath.ToSlash(snapshottedDependency)
	return &providers.CommandResult{Stdout: "services:\n  app:\n    env_file:\n      - " + resolvedPath + "\n"}, nil
}

func (r *cancelAfterResultRunner) RunCompose(_ context.Context, _ string, args ...string) (*providers.CommandResult, error) {
	r.calls++
	r.cancel()
	stdout := ""
	if len(args) > 0 {
		switch args[0] {
		case "version":
			stdout = `{"version":"v2.20.0"}`
		case "ls", "ps":
			stdout = "[]"
		case "config":
			stdout = "services: {}\n"
		}
	}
	return &providers.CommandResult{Stdout: stdout}, nil
}

func (r *cancelingConfigRunner) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	r.calls++
	r.cancel()
	return &providers.CommandResult{Stdout: "services: {}\n"}, context.Canceled
}

func (r *scopeRunner) ID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.id
}

func (r *scopeRunner) DockerContext(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.contextName, nil
}

func (r *scopeRunner) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return &providers.CommandResult{Stdout: "services: {}\n"}, nil
}

func (r *scopeRunner) setContext(value string) {
	r.mu.Lock()
	r.contextName = value
	r.mu.Unlock()
}

func (r *scopeRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type fakeCall struct {
	workdir string
	env     []string
	args    []string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		outputs:       map[string]providers.CommandResult{},
		errors:        map[string]error{},
		hostToBackend: map[string]string{},
		backendToHost: map[string]string{},
	}
}

func (r *fakeRunner) RunCompose(_ context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
	return r.RunComposeEnv(context.Background(), workdir, nil, args...)
}

func (r *fakeRunner) MapPathToBackend(path string) (string, error) {
	if r.mapBackendErr != nil {
		return "", r.mapBackendErr
	}
	if mapped, ok := r.hostToBackend[path]; ok {
		return mapped, nil
	}
	return path, nil
}

func (r *fakeRunner) MapPathToHost(path string) (string, error) {
	if mapped, ok := r.backendToHost[path]; ok {
		return mapped, nil
	}
	return path, nil
}

func (r *fakeRunner) RunComposeEnv(_ context.Context, workdir string, env []string, args ...string) (*providers.CommandResult, error) {
	r.calls = append(r.calls, fakeCall{
		workdir: workdir,
		env:     append([]string(nil), env...),
		args:    append([]string(nil), args...),
	})
	key := workdir + "|" + strings.Join(args, " ")
	lookupKey := key
	result, ok := r.outputs[lookupKey]
	if !ok && composeConfigCommand(args) {
		matches := make([]string, 0, len(r.outputs))
		for configuredKey := range r.outputs {
			if strings.HasSuffix(configuredKey, " config") {
				matches = append(matches, configuredKey)
			}
		}
		sort.Strings(matches)
		if len(matches) > 0 {
			lookupKey = matches[0]
			result = r.outputs[lookupKey]
		}
	}
	result.Command = append([]string{"docker", "compose"}, args...)
	result.Workdir = workdir
	if result.ExitCode == 0 {
		result.ExitCode = 0
	}
	return &result, r.errors[lookupKey]
}

func composeConfigCommand(args []string) bool {
	for _, arg := range args {
		if arg == "config" {
			return true
		}
	}
	return false
}

func (r *fakeRunner) hasCall(key string) bool {
	for _, call := range r.calls {
		if call.workdir+"|"+strings.Join(call.args, " ") == key {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func absPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs(%q): %v", path, err)
	}
	return abs
}
