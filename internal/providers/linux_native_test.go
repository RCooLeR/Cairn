package providers

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestLinuxNativeDetectHealthy(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.paths["docker"] = "/usr/bin/docker"
	runner.outputs["docker context show"] = "default\n"
	runner.outputs["docker compose version --short"] = "v2.29.1\n"
	runner.outputs["docker buildx version"] = "github.com/docker/buildx v0.16.2 123456\n"
	runner.outputs["docker info --format {{.ServerVersion}}"] = "27.1.2\n"
	probe := &fakeLinuxProbe{
		env: map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"},
		paths: map[string]fakeFileInfo{
			"/run/systemd/system":        {isDir: true},
			"/run/user/1000/docker.sock": {},
		},
	}

	status, err := NewLinuxNative(LinuxNativeOptions{Runner: runner, Probe: probe}).Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if !status.Healthy {
		t.Fatalf("Healthy = false, problems = %#v", status.Problems)
	}
	if status.DockerHost != "unix:///run/user/1000/docker.sock" {
		t.Fatalf("DockerHost = %q", status.DockerHost)
	}
	if status.DockerVersion != "27.1.2" || status.ComposeVersion != "2.29.1" || status.BackendVersion != "0.16.2" {
		t.Fatalf("versions = docker %q compose %q backend %q", status.DockerVersion, status.ComposeVersion, status.BackendVersion)
	}
}

func TestLinuxNativeDetectDockerMissing(t *testing.T) {
	t.Parallel()
	status, err := NewLinuxNative(LinuxNativeOptions{
		Runner: newFakeRunner(),
		Probe:  &fakeLinuxProbe{},
	}).Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	assertProblem(t, status.Problems, ProblemDockerMissing)
	if status.DockerInstalled || status.Healthy {
		t.Fatalf("status = %#v", status)
	}
}

func TestLinuxNativeDetectSocketPermission(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.paths["docker"] = "/usr/bin/docker"
	runner.outputs["docker context show"] = "default\n"
	runner.outputs["docker compose version --short"] = "v2.29.1\n"
	runner.outputs["docker buildx version"] = "github.com/docker/buildx v0.16.2 123456\n"
	probe := &fakeLinuxProbe{
		paths: map[string]fakeFileInfo{
			defaultDockerSocket: {},
		},
		connectErr: os.ErrPermission,
	}

	status, err := NewLinuxNative(LinuxNativeOptions{Runner: runner, Probe: probe}).Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	problem := assertProblem(t, status.Problems, ProblemSocketPerm)
	if !problem.Recoverable || !strings.Contains(problem.RepairHint, "docker group") {
		t.Fatalf("PERM_SOCKET problem missing repair hint: %#v", problem)
	}
	if status.DockerRunning || status.Healthy {
		t.Fatalf("status = %#v", status)
	}
}

func TestLinuxNativeDetectRealDockerIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-native provider integration runs only on Linux")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}

	status, err := NewLinuxNative(LinuxNativeOptions{}).Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !status.DockerInstalled {
		t.Fatalf("DockerInstalled = false: %#v", status)
	}
	if !status.DockerRunning {
		t.Fatalf("DockerRunning = false: %#v", status.Problems)
	}
	if status.DockerVersion == "" || status.DockerHost == "" {
		t.Fatalf("status missing docker version/host: %#v", status)
	}
}

func TestLinuxNativePlanInstallBuildsUbuntuDockerAptSteps(t *testing.T) {
	t.Parallel()
	provider := NewLinuxNative(LinuxNativeOptions{
		Runner: newFakeRunner(),
		Probe:  &fakeLinuxProbe{},
	})

	plan, err := provider.PlanInstall(context.Background(), models.InstallOptions{Backend: linuxNativeID})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}

	if plan.Title != "Install Docker Engine on Linux" {
		t.Fatalf("Title = %q", plan.Title)
	}
	if plan.Risk != models.RiskNeedsConfirmation {
		t.Fatalf("Risk = %q, want %q", plan.Risk, models.RiskNeedsConfirmation)
	}
	if len(plan.Commands) != 7 {
		t.Fatalf("commands = %d, want 7: %#v", len(plan.Commands), plan.Commands)
	}
	wantCommands := []string{
		"'sudo' 'apt-get' 'update'",
		"'sudo' 'apt-get' 'install' '-y' 'ca-certificates' 'curl' 'gnupg'",
		"'sudo' 'sh' '-lc'",
		"'sudo' 'apt-get' 'update'",
		"'sudo' 'apt-get' 'install' '-y' 'docker-ce' 'docker-ce-cli' 'containerd.io' 'docker-buildx-plugin' 'docker-compose-plugin'",
		"'sudo' 'systemctl' 'enable' '--now' 'docker'",
		"'sh' '-lc'",
	}
	for index, want := range wantCommands {
		if !strings.Contains(plan.Commands[index].Command, want) {
			t.Fatalf("command[%d] = %q, want it to contain %q", index, plan.Commands[index].Command, want)
		}
		if plan.Commands[index].Risk != models.RiskNeedsConfirmation {
			t.Fatalf("command[%d].Risk = %q", index, plan.Commands[index].Risk)
		}
	}
	if !strings.Contains(plan.Commands[2].Command, "download.docker.com/linux/ubuntu") {
		t.Fatalf("repository command missing Docker apt source: %q", plan.Commands[2].Command)
	}
	if !strings.Contains(plan.Commands[6].Command, "docker run --rm hello-world") {
		t.Fatalf("verify command missing hello-world: %q", plan.Commands[6].Command)
	}
	if len(plan.Effects) != 5 {
		t.Fatalf("effects = %#v", plan.Effects)
	}
	if plan.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt was not set")
	}
}

func TestLinuxNativeExecuteInstallStepRunsAndClearsPlan(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	provider := NewLinuxNative(LinuxNativeOptions{
		Runner: runner,
		Probe:  &fakeLinuxProbe{},
	})
	plan, err := provider.PlanInstall(context.Background(), models.InstallOptions{Backend: linuxNativeID})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	provider.installMu.Lock()
	stored := provider.plans[plan.PlanID]
	provider.installMu.Unlock()
	for _, step := range stored.Steps {
		key := strings.Join(step.Command, " ")
		runner.outputs[key] = "ok\n"
	}

	progress := make(chan InstallProgress, len(stored.Steps)*2)
	for index := range stored.Steps {
		if err := provider.ExecuteInstallStep(context.Background(), plan.PlanID, index, progress); err != nil {
			t.Fatalf("ExecuteInstallStep(%d) error = %v", index, err)
		}
	}

	provider.installMu.Lock()
	_, ok := provider.plans[plan.PlanID]
	provider.installMu.Unlock()
	if ok {
		t.Fatalf("install plan %q was not cleared", plan.PlanID)
	}
	if got, want := len(progress), len(stored.Steps)*2; got != want {
		t.Fatalf("progress entries = %d, want %d", got, want)
	}
}

func TestLinuxNativeRunComposeUsesWorkdirEnvAndArgv(t *testing.T) {
	t.Parallel()
	runner := &composeOptionsRunner{}
	provider := NewLinuxNative(LinuxNativeOptions{Runner: runner, Probe: &fakeLinuxProbe{}})

	result, err := provider.RunComposeEnv(context.Background(), "/workspace/app", []string{"COMPOSE_PROJECT_NAME=demo"}, "-f", "compose.yaml", "config")
	if err != nil {
		t.Fatalf("RunComposeEnv() error = %v", err)
	}
	if got, want := result.Command, []string{"docker", "compose", "-f", "compose.yaml", "config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
	if runner.opts.Workdir != "/workspace/app" {
		t.Fatalf("workdir = %q", runner.opts.Workdir)
	}
	if runner.opts.Timeout != composeCommandTimeout {
		t.Fatalf("timeout = %s, want %s", runner.opts.Timeout, composeCommandTimeout)
	}
	if got := runner.opts.Env; len(got) != 1 || got[0] != "COMPOSE_PROJECT_NAME=demo" {
		t.Fatalf("env = %#v", got)
	}
}

func TestLinuxNativeRunDockerWithInputUsesOptionsRunner(t *testing.T) {
	t.Parallel()
	runner := &composeOptionsRunner{}
	provider := NewLinuxNative(LinuxNativeOptions{Runner: runner, Probe: &fakeLinuxProbe{}})

	result, err := provider.RunDockerWithInput(context.Background(), "secret\n", "login", "docker.io", "-u", "ada", "--password-stdin")
	if err != nil {
		t.Fatalf("RunDockerWithInput() error = %v", err)
	}
	if got, want := result.Command, []string{"docker", "login", "docker.io", "-u", "ada", "--password-stdin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
	if runner.opts.Stdin != "secret\n" {
		t.Fatalf("stdin = %q", runner.opts.Stdin)
	}
	if runner.opts.Timeout != dockerOperationTimeout {
		t.Fatalf("timeout = %s, want %s", runner.opts.Timeout, dockerOperationTimeout)
	}
}

type fakeRunner struct {
	paths   map[string]string
	outputs map[string]string
	errors  map[string]error
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		paths:   map[string]string{},
		outputs: map[string]string{},
		errors:  map[string]error{},
	}
}

func (r *fakeRunner) LookPath(file string) (string, error) {
	if path, ok := r.paths[file]; ok {
		return path, nil
	}
	return "", exec.ErrNotFound
}

func (r *fakeRunner) Run(_ context.Context, _ time.Duration, name string, args ...string) (*CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	result := &CommandResult{Command: append([]string{name}, args...), Stdout: r.outputs[key], ExitCode: 0}
	if err, ok := r.errors[key]; ok {
		result.ExitCode = 1
		result.Stderr = err.Error()
		return result, err
	}
	if _, ok := r.outputs[key]; !ok {
		result.ExitCode = 1
		result.Stderr = "not configured"
		return result, errors.New("not configured")
	}
	return result, nil
}

type fakeLinuxProbe struct {
	env        map[string]string
	paths      map[string]fakeFileInfo
	connectErr error
}

func (p *fakeLinuxProbe) Env(key string) string {
	return p.env[key]
}

func (p *fakeLinuxProbe) Stat(path string) (os.FileInfo, error) {
	info, ok := p.paths[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	info.name = path
	return info, nil
}

func (p *fakeLinuxProbe) CanConnectUnixSocket(context.Context, string, time.Duration) error {
	return p.connectErr
}

type fakeFileInfo struct {
	name  string
	isDir bool
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.isDir }
func (f fakeFileInfo) Sys() any           { return nil }

func assertProblem(t *testing.T, problems []models.ProviderProblem, code string) models.ProviderProblem {
	t.Helper()
	for _, problem := range problems {
		if problem.Code == code {
			return problem
		}
	}
	t.Fatalf("problem %s not found in %#v", code, problems)
	return models.ProviderProblem{}
}

type composeOptionsRunner struct {
	opts CommandRunOptions
}

func (r *composeOptionsRunner) LookPath(file string) (string, error) {
	return "/usr/bin/" + file, nil
}

func (r *composeOptionsRunner) Run(context.Context, time.Duration, string, ...string) (*CommandResult, error) {
	return nil, errors.New("Run should not be used for compose when RunWithOptions is available")
}

func (r *composeOptionsRunner) RunWithOptions(_ context.Context, opts CommandRunOptions, name string, args ...string) (*CommandResult, error) {
	r.opts = opts
	return &CommandResult{
		Command: append([]string{name}, args...),
		Workdir: opts.Workdir,
	}, nil
}
