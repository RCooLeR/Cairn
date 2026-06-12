package providers

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
