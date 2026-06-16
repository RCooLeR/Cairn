package providers

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestParseColimaStatusJSON(t *testing.T) {
	t.Parallel()
	status, err := parseColimaStatusJSON(`{
		"status": "Running",
		"runtime": "docker",
		"socket": "unix:///Users/ada/.colima/default/docker.sock",
		"cpus": 4,
		"memory": 8,
		"disk": 80,
		"mounts": ["/Users:w"]
	}`)
	if err != nil {
		t.Fatalf("parseColimaStatusJSON() error = %v", err)
	}
	if status.Status != "Running" || status.Runtime != "docker" || status.CPUs != 4 || status.Socket == "" {
		t.Fatalf("status = %#v", status)
	}
}

func TestParseColimaStatusJSONRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	if _, err := parseColimaStatusJSON("{"); err == nil {
		t.Fatalf("parseColimaStatusJSON() error = nil, want malformed JSON error")
	}
}

func TestParseDockerContextListJSONLinesAndArray(t *testing.T) {
	t.Parallel()
	lines := `{"Name":"default","Description":"Default","DockerEndpoint":"unix:///var/run/docker.sock","Current":false}
{"Name":"colima","Description":"Colima","DockerEndpoint":"unix:///Users/ada/.colima/default/docker.sock","Current":"*"}`
	contexts, err := parseDockerContextList(lines)
	if err != nil {
		t.Fatalf("parseDockerContextList(lines) error = %v", err)
	}
	if len(contexts) != 2 || !contexts[1].Current || contexts[1].DockerHost == "" {
		t.Fatalf("contexts = %#v", contexts)
	}

	array := `[{"Name":"colima-dev","Description":"Dev","DockerEndpoint":"unix:///Users/ada/.colima/dev/docker.sock","Current":true}]`
	contexts, err = parseDockerContextList(array)
	if err != nil {
		t.Fatalf("parseDockerContextList(array) error = %v", err)
	}
	if len(contexts) != 1 || contexts[0].Name != "colima-dev" || !contexts[0].Current {
		t.Fatalf("contexts = %#v", contexts)
	}
}

func TestMacOSColimaDetectHealthy(t *testing.T) {
	t.Parallel()
	runner := healthyColimaRunner()

	status, err := NewMacOSColima(MacOSColimaOptions{Runner: runner, HomeDir: "/Users/ada"}).Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !status.Healthy {
		t.Fatalf("Healthy = false, problems = %#v warnings = %#v", status.Problems, status.Warnings)
	}
	if !status.DockerInstalled || !status.ComposeInstalled || !status.BuildxInstalled || !status.DockerRunning {
		t.Fatalf("status flags = %#v", status)
	}
	if status.CurrentContext != "colima" || status.DockerHost != "unix:///Users/ada/.colima/default/docker.sock" {
		t.Fatalf("context/host = %q/%q", status.CurrentContext, status.DockerHost)
	}
	if status.DockerVersion != "29.0.1" || status.ComposeVersion != "2.40.3" {
		t.Fatalf("versions = docker %q compose %q", status.DockerVersion, status.ComposeVersion)
	}
}

func TestMacOSColimaDetectProblemCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		configure func(*fakeRunner)
		want      string
	}{
		{
			name: "missing docker cli",
			configure: func(r *fakeRunner) {
				r.paths[brewCommandName] = "/opt/homebrew/bin/brew"
			},
			want: ProblemDockerMissing,
		},
		{
			name: "missing colima",
			configure: func(r *fakeRunner) {
				seedColimaCommon(r)
				delete(r.paths, colimaCommandName)
			},
			want: ProblemColimaMissing,
		},
		{
			name: "stopped profile",
			configure: func(r *fakeRunner) {
				seedColimaCommon(r)
				r.outputs[colimaCommandName+" status -p default --json"] = `{"status":"Stopped","socket":"unix:///Users/ada/.colima/default/docker.sock"}`
			},
			want: ProblemColimaStopped,
		},
		{
			name: "context missing",
			configure: func(r *fakeRunner) {
				seedColimaCommon(r)
				r.outputs["docker context ls --format json"] = `{"Name":"default","DockerEndpoint":"unix:///var/run/docker.sock","Current":true}`
			},
			want: ProblemContextMissing,
		},
		{
			name: "context not selected",
			configure: func(r *fakeRunner) {
				seedColimaCommon(r)
				r.outputs["docker context ls --format json"] = `{"Name":"colima","DockerEndpoint":"unix:///Users/ada/.colima/default/docker.sock","Current":false}`
			},
			want: ProblemContextNotSelected,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runner := newFakeRunner()
			tt.configure(runner)
			status, err := NewMacOSColima(MacOSColimaOptions{Runner: runner, HomeDir: "/Users/ada"}).Detect(context.Background())
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			assertProblem(t, status.Problems, tt.want)
			if status.Healthy {
				t.Fatalf("status should not be healthy: %#v", status)
			}
		})
	}
}

func TestMacOSColimaPlanInstallAndExecute(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.paths[brewCommandName] = "/opt/homebrew/bin/brew"
	provider := NewMacOSColima(MacOSColimaOptions{
		Profile:  "dev",
		CPU:      6,
		MemoryGB: 12,
		DiskGB:   100,
		Runner:   runner,
	})

	plan, err := provider.PlanInstall(context.Background(), models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	if plan.Risk != models.RiskNeedsConfirmation || len(plan.Commands) != 8 {
		t.Fatalf("plan = %#v", plan)
	}
	if !strings.Contains(plan.Commands[3].Command, "'--cpu' '6' '--memory' '12' '--disk' '100'") {
		t.Fatalf("colima start command = %q", plan.Commands[3].Command)
	}
	provider.installMu.Lock()
	steps := append([]colimaInstallStep(nil), provider.installPlans[plan.PlanID].Steps...)
	provider.installMu.Unlock()
	for _, step := range steps {
		runner.outputs[strings.Join(step.Command, " ")] = "ok\n"
	}

	progress := make(chan InstallProgress, len(plan.Commands)*2)
	for index := range plan.Commands {
		if err := provider.ExecuteInstallStep(context.Background(), plan.PlanID, index, progress); err != nil {
			t.Fatalf("ExecuteInstallStep(%d) error = %v", index, err)
		}
	}
	close(progress)
	seen := 0
	for event := range progress {
		seen++
		if event.TotalSteps != len(plan.Commands) {
			t.Fatalf("progress total = %d, want %d", event.TotalSteps, len(plan.Commands))
		}
	}
	if seen != len(plan.Commands)*2 {
		t.Fatalf("progress events = %d, want %d", seen, len(plan.Commands)*2)
	}
}

func TestMacOSColimaPlanInstallRequiresHomebrew(t *testing.T) {
	t.Parallel()
	_, err := NewMacOSColima(MacOSColimaOptions{Runner: newFakeRunner()}).PlanInstall(context.Background(), models.InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "Homebrew") {
		t.Fatalf("PlanInstall() error = %v, want Homebrew guidance", err)
	}
}

func TestMacOSColimaRunComposeUsesContextWorkdirAndEnv(t *testing.T) {
	t.Parallel()
	runner := &colimaOptionsRunner{versionOK: true}
	provider := NewMacOSColima(MacOSColimaOptions{Profile: "dev", Runner: runner})

	result, err := provider.RunComposeEnv(context.Background(), "/Users/ada/app", []string{"COMPOSE_PROJECT_NAME=demo"}, "-f", "compose.yaml", "config")
	if err != nil {
		t.Fatalf("RunComposeEnv() error = %v", err)
	}
	wantCommand := []string{"docker", "--context", "colima-dev", "compose", "-f", "compose.yaml", "config"}
	if !reflect.DeepEqual(result.Command, wantCommand) {
		t.Fatalf("command = %#v, want %#v", result.Command, wantCommand)
	}
	if runner.opts.Workdir != "/Users/ada/app" {
		t.Fatalf("workdir = %q", runner.opts.Workdir)
	}
	if got := runner.opts.Env; len(got) != 1 || got[0] != "COMPOSE_PROJECT_NAME=demo" {
		t.Fatalf("env = %#v", got)
	}
}

func TestMacOSColimaLifecycleAndShellCommands(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.outputs["colima start -p dev --cpu 4 --memory 8 --disk 80"] = "started\n"
	runner.outputs["docker context use colima-dev"] = "colima-dev\n"
	runner.outputs["colima restart -p dev"] = "restarted\n"
	runner.outputs["colima stop -p dev"] = "stopped\n"
	provider := NewMacOSColima(MacOSColimaOptions{Profile: "dev", CPU: 4, MemoryGB: 8, DiskGB: 80, Runner: runner})

	if err := provider.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := provider.Restart(context.Background()); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	if err := provider.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	hostShell, err := provider.HostShellCommand(models.TerminalOptions{})
	if err != nil || !reflect.DeepEqual(hostShell, []string{"/bin/zsh"}) {
		t.Fatalf("HostShellCommand() = %#v, %v", hostShell, err)
	}
	backendShell, err := provider.BackendShellCommand(models.TerminalOptions{Shell: "/bin/bash"})
	if err != nil || !reflect.DeepEqual(backendShell, []string{"colima", "ssh", "-p", "dev", "--", "/bin/bash"}) {
		t.Fatalf("BackendShellCommand() = %#v, %v", backendShell, err)
	}
}

func healthyColimaRunner() *fakeRunner {
	runner := newFakeRunner()
	seedColimaCommon(runner)
	return runner
}

func seedColimaCommon(runner *fakeRunner) {
	runner.paths[brewCommandName] = "/opt/homebrew/bin/brew"
	runner.paths["docker"] = "/opt/homebrew/bin/docker"
	runner.paths[colimaCommandName] = "/opt/homebrew/bin/colima"
	runner.outputs["docker compose version --short"] = "v2.40.3\n"
	runner.outputs["docker buildx version"] = "github.com/docker/buildx v0.34.1 123456\n"
	runner.outputs[colimaCommandName+" version"] = "colima version 0.9.1\n"
	runner.outputs[colimaCommandName+" status -p default --json"] = `{"status":"Running","runtime":"docker","socket":"unix:///Users/ada/.colima/default/docker.sock","cpus":4,"memory":8,"disk":80}` + "\n"
	runner.outputs["docker context ls --format json"] = `{"Name":"colima","Description":"Colima","DockerEndpoint":"unix:///Users/ada/.colima/default/docker.sock","Current":true}` + "\n"
	runner.outputs["docker --context colima info --format {{.ServerVersion}}"] = "29.0.1\n"
}

type colimaOptionsRunner struct {
	opts      CommandRunOptions
	versionOK bool
}

func (r *colimaOptionsRunner) LookPath(file string) (string, error) {
	return "/opt/homebrew/bin/" + file, nil
}

func (r *colimaOptionsRunner) Run(_ context.Context, _ time.Duration, name string, args ...string) (*CommandResult, error) {
	command := append([]string{name}, args...)
	if r.versionOK && strings.Join(command, " ") == "docker --context colima-dev compose version --short" {
		return &CommandResult{Command: command, Stdout: "v2.40.3\n"}, nil
	}
	return &CommandResult{Command: command, ExitCode: 1, Stderr: "not configured"}, errors.New("not configured")
}

func (r *colimaOptionsRunner) RunWithOptions(_ context.Context, opts CommandRunOptions, name string, args ...string) (*CommandResult, error) {
	r.opts = opts
	return &CommandResult{Command: append([]string{name}, args...), Workdir: opts.Workdir}, nil
}
