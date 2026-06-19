package compose

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/providers"
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
	hostFile := `E:\Development\project\compose.yaml`
	backendWorkdir := "/mnt/e/Development/project"
	backendFile := "/mnt/e/Development/project/compose.yaml"
	runner.hostToBackend[hostWorkdir] = backendWorkdir
	runner.hostToBackend[hostFile] = backendFile
	runner.outputs[backendWorkdir+"|-f "+backendFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  web:\n    image: nginx:alpine\n",
	}
	client := NewClient(runner)

	if _, err := client.Config(context.Background(), ProjectOptions{Workdir: hostWorkdir, Files: []string{hostFile}}); err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if got := runner.calls[0].workdir; got != backendWorkdir {
		t.Fatalf("workdir = %q, want %q", got, backendWorkdir)
	}
	if got, want := runner.calls[0].args, []string{"-f", backendFile, "config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
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
	result := r.outputs[key]
	result.Command = append([]string{"docker", "compose"}, args...)
	result.Workdir = workdir
	if result.ExitCode == 0 {
		result.ExitCode = 0
	}
	return &result, r.errors[key]
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
