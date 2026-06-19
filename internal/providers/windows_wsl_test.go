package providers

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

func TestParseWSLListVerboseUTF16ExcludesDockerDesktop(t *testing.T) {
	t.Parallel()
	raw := "\ufeff  NAME                   STATE           VERSION\n" +
		"* Ubuntu                 Running         2\n" +
		"  docker-desktop         Running         2\n" +
		"  docker-desktop-data    Stopped         2\n" +
		"  Ubuntu-22.04           Stopped         2\n" +
		"  cairn-dev              Running         2\n"

	distros, err := parseWSLListVerbose(utf16LE(raw))
	if err != nil {
		t.Fatalf("parseWSLListVerbose() error = %v", err)
	}

	names := make([]string, 0, len(distros))
	for _, distro := range distros {
		names = append(names, distro.Name)
		if strings.HasPrefix(distro.Name, "docker-desktop") {
			t.Fatalf("Docker Desktop distro was not excluded: %#v", distros)
		}
	}
	if got, want := names, []string{"Ubuntu", "Ubuntu-22.04", "cairn-dev"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %#v, want %#v", got, want)
	}
	if !distros[0].Default || distros[0].Version != 2 || distros[0].State != "Running" {
		t.Fatalf("first distro metadata = %#v", distros[0])
	}
}

func TestSelectWSLDistroHonorsSettingsAndCustomNames(t *testing.T) {
	t.Parallel()
	distros := []wslDistro{
		{Name: "dev-box", Version: 2, Default: true},
		{Name: "Ubuntu-24.04", Version: 2},
		{Name: "cairn-dev", Version: 2},
	}

	if got, ok := selectWSLDistro(distros, "cairn-dev"); !ok || got.Name != "cairn-dev" {
		t.Fatalf("select configured = %#v, %v; want cairn-dev, true", got, ok)
	}
	if got, ok := selectWSLDistro(distros, "Ubuntu"); !ok || got.Name != "Ubuntu-24.04" {
		t.Fatalf("select default Ubuntu family = %#v, %v; want Ubuntu-24.04, true", got, ok)
	}
	if got, ok := selectWSLDistro(distros, "missing-dev"); ok || got.Name != "" {
		t.Fatalf("select missing explicit = %#v, %v; want empty, false", got, ok)
	}
	if got, ok := selectWSLDistro([]wslDistro{{Name: "cairn-dev", Version: 2}}, ""); !ok || got.Name != "cairn-dev" {
		t.Fatalf("select custom default = %#v, %v; want cairn-dev, true", got, ok)
	}
}

func TestWindowsWSLDetectHealthyCustomDistro(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.paths[wslCommandName] = `C:\Windows\System32\wsl.exe`
	runner.outputs[wslCommandName+" --status"] = "Default Version: 2\n"
	runner.outputs[wslCommandName+" -l -v"] = utf16LE("  NAME       STATE      VERSION\n  cairn-dev  Running    2\n")
	runner.outputs[wslCommandName+" -d cairn-dev -- sh -lc cat /etc/os-release"] = "ID=arch\nID_LIKE=arch\n"
	runner.outputs[wslCommandName+" -d cairn-dev -- test -d /run/systemd/system"] = ""
	runner.outputs[wslCommandName+" -d cairn-dev -- sh -lc readlink -f /usr/bin/docker 2>/dev/null || true"] = "/usr/bin/docker\n"
	runner.outputs[wslCommandName+" -d cairn-dev -- sh -lc command -v docker >/dev/null 2>&1"] = "/usr/bin/docker\n"
	runner.outputs[wslCommandName+" -d cairn-dev -- docker context show"] = "default\n"
	runner.outputs[wslCommandName+" -d cairn-dev -- docker compose version --short"] = "v2.29.1\n"
	runner.outputs[wslCommandName+" -d cairn-dev -- docker buildx version"] = "github.com/docker/buildx v0.16.2 123456\n"
	runner.outputs[wslCommandName+" -d cairn-dev -- docker info --format {{.ServerVersion}}"] = "27.1.2\n"

	status, err := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-dev", Runner: runner}).Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !status.Healthy {
		t.Fatalf("Healthy = false, problems = %#v", status.Problems)
	}
	assertNoProblem(t, status.Problems, ProblemUbuntuMissing)
	if !status.DockerInstalled || !status.ComposeInstalled || !status.BuildxInstalled || !status.DockerRunning {
		t.Fatalf("status missing installed/running flags: %#v", status)
	}
	if status.DockerVersion != "27.1.2" || status.ComposeVersion != "2.29.1" || status.BackendVersion != "0.16.2" {
		t.Fatalf("versions = docker %q compose %q backend %q", status.DockerVersion, status.ComposeVersion, status.BackendVersion)
	}
	if status.DockerHost != "wsl+stdio://cairn-dev" {
		t.Fatalf("DockerHost = %q, want WSL stdio host marker", status.DockerHost)
	}
}

func TestWindowsWSLDetectProblemCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		configure func(*fakeRunner)
		want      string
	}{
		{
			name:      "missing wsl",
			configure: func(*fakeRunner) {},
			want:      ProblemWSLMissing,
		},
		{
			name: "no distro",
			configure: func(r *fakeRunner) {
				r.paths[wslCommandName] = `C:\Windows\System32\wsl.exe`
				r.outputs[wslCommandName+" --status"] = "Default Version: 2\n"
				r.outputs[wslCommandName+" -l -v"] = "  NAME              STATE      VERSION\n  docker-desktop    Running    2\n"
			},
			want: ProblemUbuntuMissing,
		},
		{
			name: "wsl1 distro",
			configure: func(r *fakeRunner) {
				r.paths[wslCommandName] = `C:\Windows\System32\wsl.exe`
				r.outputs[wslCommandName+" --status"] = "Default Version: 2\n"
				r.outputs[wslCommandName+" -l -v"] = "  NAME      STATE      VERSION\n  Ubuntu    Running    1\n"
			},
			want: ProblemWSL2Required,
		},
		{
			name: "systemd off",
			configure: func(r *fakeRunner) {
				seedWSLDetectThroughDockerProbe(r)
				delete(r.outputs, wslCommandName+" -d Ubuntu -- test -d /run/systemd/system")
				r.errors[wslCommandName+" -d Ubuntu -- test -d /run/systemd/system"] = errors.New("missing")
			},
			want: ProblemSystemdOff,
		},
		{
			name: "desktop integration conflict",
			configure: func(r *fakeRunner) {
				seedWSLDetectThroughDockerProbe(r)
				r.outputs[wslCommandName+" -d Ubuntu -- sh -lc readlink -f /usr/bin/docker 2>/dev/null || true"] = "/mnt/wsl/docker-desktop/cli-tools/usr/bin/docker\n"
			},
			want: ProblemDesktopIntegrationConflict,
		},
		{
			name: "custom non ubuntu distro missing docker",
			configure: func(r *fakeRunner) {
				r.paths[wslCommandName] = `C:\Windows\System32\wsl.exe`
				r.outputs[wslCommandName+" --status"] = "Default Version: 2\n"
				r.outputs[wslCommandName+" -l -v"] = "  NAME       STATE      VERSION\n  cairn-dev  Running    2\n"
				r.outputs[wslCommandName+" -d cairn-dev -- sh -lc cat /etc/os-release"] = "ID=arch\nID_LIKE=arch\n"
				r.outputs[wslCommandName+" -d cairn-dev -- test -d /run/systemd/system"] = ""
				r.outputs[wslCommandName+" -d cairn-dev -- sh -lc readlink -f /usr/bin/docker 2>/dev/null || true"] = ""
				r.errors[wslCommandName+" -d cairn-dev -- sh -lc command -v docker >/dev/null 2>&1"] = errors.New("missing")
			},
			want: ProblemDockerMissing,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runner := newFakeRunner()
			tt.configure(runner)
			status, err := NewWindowsWSL(WindowsWSLOptions{Runner: runner}).Detect(context.Background())
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			assertProblem(t, status.Problems, tt.want)
		})
	}
}

func TestWindowsWSLRunDockerComposeAndShellCommands(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.paths["pwsh"] = `C:\Program Files\PowerShell\7\pwsh.exe`
	runner.outputs[wslCommandName+" -d cairn-dev -- docker ps -a"] = "CONTAINER ID\n"
	runner.outputs[wslCommandName+" -d cairn-dev -- sh -lc cd '/mnt/c/Users/Ada/Project One' && exec docker 'compose' '-f' 'compose.yaml' 'config'"] = "services: {}\n"
	runner.outputs[wslCommandName+" -d cairn-dev -- sh -lc export COMPOSE_PROJECT_NAME='demo'; cd '/mnt/c/Users/Ada/Project One' && exec docker 'compose' '-f' 'compose.yaml' 'ps'"] = "[]\n"
	provider := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-dev", Runner: runner})

	result, err := provider.RunDocker(context.Background(), "ps", "-a")
	if err != nil {
		t.Fatalf("RunDocker() error = %v", err)
	}
	if got, want := result.Command, []string{wslCommandName, "-d", "cairn-dev", "--", "docker", "ps", "-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RunDocker command = %#v, want %#v", got, want)
	}

	result, err = provider.RunCompose(context.Background(), `C:\Users\Ada\Project One`, "-f", "compose.yaml", "config")
	if err != nil {
		t.Fatalf("RunCompose() error = %v", err)
	}
	if result.Workdir != "/mnt/c/Users/Ada/Project One" {
		t.Fatalf("RunCompose workdir = %q", result.Workdir)
	}

	result, err = provider.RunComposeEnv(context.Background(), `C:\Users\Ada\Project One`, []string{"COMPOSE_PROJECT_NAME=demo"}, "-f", "compose.yaml", "ps")
	if err != nil {
		t.Fatalf("RunComposeEnv() error = %v", err)
	}
	if result.Workdir != "/mnt/c/Users/Ada/Project One" {
		t.Fatalf("RunComposeEnv workdir = %q", result.Workdir)
	}

	hostShell, err := provider.HostShellCommand(models.TerminalOptions{})
	if err != nil {
		t.Fatalf("HostShellCommand() error = %v", err)
	}
	if got, want := hostShell, []string{"pwsh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("HostShellCommand() = %#v, want %#v", got, want)
	}
	backendShell, err := provider.BackendShellCommand(models.TerminalOptions{Shell: "/bin/zsh"})
	if err != nil {
		t.Fatalf("BackendShellCommand() error = %v", err)
	}
	if got, want := backendShell, []string{wslCommandName, "-d", "cairn-dev", "--", "/bin/zsh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BackendShellCommand() = %#v, want %#v", got, want)
	}

	backendShell, err = provider.BackendShellCommand(models.TerminalOptions{WorkingDir: `C:\Users\Ada\Project One`})
	if err != nil {
		t.Fatalf("BackendShellCommand(workdir) error = %v", err)
	}
	if got, want := backendShell, []string{wslCommandName, "-d", "cairn-dev", "--cd", "/mnt/c/Users/Ada/Project One", "--", "/bin/bash"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BackendShellCommand(workdir) = %#v, want %#v", got, want)
	}
}

func TestWindowsWSLDockerDialerUsesDockerDialStdio(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.outputs[wslCommandName+" -d cairn-dev -- docker system dial-stdio --help"] = "Usage: docker system dial-stdio\n"
	var captured []string
	provider := NewWindowsWSL(WindowsWSLOptions{
		Distro: "cairn-dev",
		Runner: runner,
		StdioDialer: func(_ context.Context, command []string) (net.Conn, error) {
			captured = append([]string(nil), command...)
			client, server := net.Pipe()
			_ = server.Close()
			return client, nil
		},
	})

	host, err := provider.DockerHost(context.Background())
	if err != nil {
		t.Fatalf("DockerHost() error = %v", err)
	}
	if host != "unix:///var/run/docker.sock" {
		t.Fatalf("DockerHost() = %q, want SDK unix host", host)
	}
	dialContext, err := provider.DockerDialContext(context.Background())
	if err != nil {
		t.Fatalf("DockerDialContext() error = %v", err)
	}
	conn, err := dialContext(context.Background(), "unix", "/var/run/docker.sock")
	if err != nil {
		t.Fatalf("dialContext() error = %v", err)
	}
	_ = conn.Close()
	if got, want := captured, []string{wslCommandName, "-d", "cairn-dev", "--", "docker", "system", "dial-stdio"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stdio command = %#v, want %#v", got, want)
	}
}

func TestWindowsWSLDockerDialerFallsBackToSocat(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	runner.outputs[wslCommandName+" -d cairn-dev -- sh -lc command -v socat >/dev/null 2>&1"] = ""
	var captured []string
	provider := NewWindowsWSL(WindowsWSLOptions{
		Distro: "cairn-dev",
		Runner: runner,
		StdioDialer: func(_ context.Context, command []string) (net.Conn, error) {
			captured = append([]string(nil), command...)
			client, server := net.Pipe()
			_ = server.Close()
			return client, nil
		},
	})

	dialContext, err := provider.DockerDialContext(context.Background())
	if err != nil {
		t.Fatalf("DockerDialContext() error = %v", err)
	}
	conn, err := dialContext(context.Background(), "unix", "/var/run/docker.sock")
	if err != nil {
		t.Fatalf("dialContext() error = %v", err)
	}
	_ = conn.Close()
	if got, want := captured, []string{wslCommandName, "-d", "cairn-dev", "--", "socat", "UNIX-CONNECT:/var/run/docker.sock", "-"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stdio command = %#v, want %#v", got, want)
	}
}

func TestWindowsWSLDockerDialerRequiresAvailableTransport(t *testing.T) {
	t.Parallel()
	provider := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-dev", Runner: newFakeRunner()})

	_, err := provider.DockerDialContext(context.Background())
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("DockerDialContext() error = %v, want E_PROVIDER_NOT_READY", err)
	}
}

func TestWindowsWSLInstallPlanAndExecution(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	provider := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-dev", Runner: runner})

	plan, err := provider.PlanInstall(context.Background(), models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	if plan.Risk != models.RiskNeedsConfirmation {
		t.Fatalf("plan risk = %q, want %q", plan.Risk, models.RiskNeedsConfirmation)
	}
	if plan.Title != "Install or update Docker Engine in Ubuntu on WSL" {
		t.Fatalf("plan title = %q", plan.Title)
	}
	if got, want := len(plan.Commands), 10; got != want {
		t.Fatalf("command count = %d, want %d", got, want)
	}
	if !strings.Contains(plan.Commands[1].Command, "--name") || !strings.Contains(plan.Commands[1].Command, "cairn-dev") {
		t.Fatalf("custom distro install command missing --name: %s", plan.Commands[1].Command)
	}
	if !strings.Contains(plan.Commands[2].Command, "already version 2") || !strings.Contains(plan.Commands[2].Command, "--set-version") {
		t.Fatalf("WSL2 conversion command is not idempotent: %s", plan.Commands[2].Command)
	}
	if !strings.Contains(plan.Commands[5].Command, "rm -f /etc/apt/sources.list.d/docker.list") || !strings.Contains(plan.Commands[5].Command, "Could not determine Ubuntu codename") {
		t.Fatalf("Docker apt install command does not clean stale source lists or validate codename: %s", plan.Commands[5].Command)
	}
	if !strings.Contains(plan.Commands[5].Command, "lsb_release -cs") || !strings.Contains(plan.Commands[5].Command, "24.04) codename=noble") {
		t.Fatalf("Docker apt install command missing codename fallbacks: %s", plan.Commands[5].Command)
	}
	if !strings.Contains(plan.Commands[5].Command, `\$codename`) || !strings.Contains(plan.Commands[5].Command, `\$(dpkg --print-architecture)`) {
		t.Fatalf("Docker apt install command does not escape dollars for wsl.exe: %s", plan.Commands[5].Command)
	}
	if !strings.Contains(plan.Commands[6].Command, `\$user`) || !strings.Contains(plan.Commands[6].Command, `\$(getent passwd 1000`) {
		t.Fatalf("Docker group command does not escape dollars for wsl.exe: %s", plan.Commands[6].Command)
	}
	if !strings.Contains(plan.Commands[5].Command, "docker-ce") || !strings.Contains(plan.Commands[9].Command, "hello-world") {
		t.Fatalf("plan commands missing Docker install/verify steps: %#v", plan.Commands)
	}
	steps := buildWSLInstallSteps("cairn-dev")
	if steps[8].Timeout <= commandTimeout {
		t.Fatalf("Docker service step timeout = %s, want longer than generic command timeout %s", steps[8].Timeout, commandTimeout)
	}
	for _, step := range steps {
		runner.outputs[strings.Join(step.Command, " ")] = "ok\n"
	}
	progress := make(chan InstallProgress, 32)
	for index := range steps {
		if err := provider.ExecuteInstallStep(context.Background(), plan.PlanID, index, progress); err != nil {
			t.Fatalf("ExecuteInstallStep(%d) error = %v", index, err)
		}
	}
	close(progress)
	var messages []string
	for item := range progress {
		messages = append(messages, item.Message)
	}
	if len(messages) != len(steps)*2 {
		t.Fatalf("progress messages = %d, want %d: %#v", len(messages), len(steps)*2, messages)
	}
	if err := provider.ExecuteInstallStep(context.Background(), plan.PlanID, 0, nil); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("ExecuteInstallStep after completion error = %v, want E_PLAN_EXPIRED", err)
	}
}

func TestWindowsWSLDetectWarnsWhenDockerPackagesAreOutdated(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	seedWSLDetectThroughDockerProbe(runner)
	runner.outputs[wslCommandName+" -d Ubuntu -- sh -lc command -v docker >/dev/null 2>&1"] = "/usr/bin/docker\n"
	runner.outputs[wslCommandName+" -d Ubuntu -- docker context show"] = "default\n"
	runner.outputs[wslCommandName+" -d Ubuntu -- docker compose version --short"] = "v2.29.1\n"
	runner.outputs[wslCommandName+" -d Ubuntu -- docker buildx version"] = "github.com/docker/buildx v0.16.2 123456\n"
	runner.outputs[wslCommandName+" -d Ubuntu -- sh -lc apt-cache policy docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin"] = `docker-ce:
  Installed: 5:27.1.2-1~ubuntu.24.04~noble
  Candidate: 5:29.0.3-1~ubuntu.24.04~noble
docker-compose-plugin:
  Installed: 2.29.1-1~ubuntu.24.04~noble
  Candidate: 2.40.3-1~ubuntu.24.04~noble
`
	runner.outputs[wslCommandName+" -d Ubuntu -- docker info --format {{.ServerVersion}}"] = "27.1.2\n"

	status, err := NewWindowsWSL(WindowsWSLOptions{Runner: runner}).Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	warning := assertWarning(t, status.Warnings, WarningDockerPackagesOutdated)
	if !strings.Contains(warning.Message, "docker-ce") || !strings.Contains(warning.Message, "docker-compose-plugin") {
		t.Fatalf("warning = %#v", warning)
	}
}

func TestWindowsWSLInstallFailureIncludesRepairHints(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	provider := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-dev", Runner: runner})
	plan, err := provider.PlanInstall(context.Background(), models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}

	steps := buildWSLInstallSteps("cairn-dev")
	const dockerInstallStep = 5
	key := strings.Join(steps[dockerInstallStep].Command, " ")
	runner.errors[key] = errors.New("temporary network failure")

	err = provider.ExecuteInstallStep(context.Background(), plan.PlanID, dockerInstallStep, nil)
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("ExecuteInstallStep() error = %v, want E_PROVIDER_NOT_READY", err)
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error type = %T, want AppError", err)
	}
	if len(appErr.RepairHints) == 0 || !strings.Contains(appErr.RepairHints[0], "internet access") {
		t.Fatalf("repair hints = %#v", appErr.RepairHints)
	}
	if !strings.Contains(appErr.Detail, "temporary network failure") {
		t.Fatalf("detail = %q, want failing command output", appErr.Detail)
	}
}

func TestWindowsWSLPathMapping(t *testing.T) {
	t.Parallel()
	provider := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-dev", Runner: newFakeRunner()})
	unicodeUser := "\u0420\u043e\u043c\u0430\u043d"
	unicodeHostPath := `C:\Users\` + unicodeUser + `\Projects`
	unicodeBackendPath := "/mnt/c/Users/" + unicodeUser + "/Projects"

	toBackend := []struct {
		name string
		in   string
		want string
	}{
		{name: "drive-root-backslash", in: `C:\`, want: "/mnt/c"},
		{name: "drive-root-slash", in: "C:/", want: "/mnt/c"},
		{name: "drive-folder", in: `C:\Users\Ada\Project`, want: "/mnt/c/Users/Ada/Project"},
		{name: "drive-lowercase", in: `d:\src\Cairn`, want: "/mnt/d/src/Cairn"},
		{name: "drive-spaces", in: `C:\Users\Ada Lovelace\Project One`, want: "/mnt/c/Users/Ada Lovelace/Project One"},
		{name: "drive-unicode", in: unicodeHostPath, want: unicodeBackendPath},
		{name: "mixed-separators", in: `E:/Development\projects/Cairn`, want: "/mnt/e/Development/projects/Cairn"},
		{name: "file-path", in: `C:\Users\Ada\compose.yaml`, want: "/mnt/c/Users/Ada/compose.yaml"},
		{name: "wsl-unc", in: `\\wsl$\cairn-dev\home\ada\project`, want: "/home/ada/project"},
		{name: "wsl-localhost-unc", in: `\\wsl.localhost\cairn-dev\home\ada\project one`, want: "/home/ada/project one"},
		{name: "wsl-unc-root", in: `\\wsl$\cairn-dev`, want: "/"},
		{name: "already-backend", in: "/home/ada/project", want: "/home/ada/project"},
		{name: "already-mnt", in: "/mnt/c/Users/Ada/project", want: "/mnt/c/Users/Ada/project"},
		{name: "windows-cleaned-mnt", in: `\mnt\e\Development\projects\apps\rcooler\Cairn\.scratch\cairn-test-projects\web-stack`, want: "/mnt/e/Development/projects/apps/rcooler/Cairn/.scratch/cairn-test-projects/web-stack"},
		{name: "windows-cleaned-home", in: `\home\ada\project`, want: "/home/ada/project"},
	}
	for _, tt := range toBackend {
		got, err := provider.MapPathToBackend(tt.in)
		if err != nil {
			t.Fatalf("%s MapPathToBackend() error = %v", tt.name, err)
		}
		if got != tt.want {
			t.Fatalf("%s MapPathToBackend() = %q, want %q", tt.name, got, tt.want)
		}
	}

	toHost := []struct {
		name string
		in   string
		want string
	}{
		{name: "mnt-root", in: "/mnt/c", want: `C:\`},
		{name: "mnt-folder", in: "/mnt/c/Users/Ada/Project", want: `C:\Users\Ada\Project`},
		{name: "mnt-lower-drive", in: "/mnt/d/src/Cairn", want: `D:\src\Cairn`},
		{name: "mnt-spaces", in: "/mnt/c/Users/Ada Lovelace/Project One", want: `C:\Users\Ada Lovelace\Project One`},
		{name: "mnt-unicode", in: unicodeBackendPath, want: unicodeHostPath},
		{name: "backend-home", in: "/home/ada/project", want: `\\wsl$\cairn-dev\home\ada\project`},
		{name: "windows-cleaned-backend-home", in: `\home\ada\project`, want: `\\wsl$\cairn-dev\home\ada\project`},
		{name: "backend-root", in: "/", want: `\\wsl$\cairn-dev`},
		{name: "already-drive", in: `C:\Users\Ada`, want: `C:\Users\Ada`},
		{name: "already-unc", in: `\\wsl$\cairn-dev\home\ada`, want: `\\wsl$\cairn-dev\home\ada`},
		{name: "file-path", in: "/mnt/c/Users/Ada/compose.yaml", want: `C:\Users\Ada\compose.yaml`},
		{name: "windows-cleaned-mnt", in: `\mnt\e\Development\projects\apps\rcooler\Cairn\.scratch\cairn-test-projects\web-stack`, want: `E:\Development\projects\apps\rcooler\Cairn\.scratch\cairn-test-projects\web-stack`},
	}
	for _, tt := range toHost {
		got, err := provider.MapPathToHost(tt.in)
		if err != nil {
			t.Fatalf("%s MapPathToHost() error = %v", tt.name, err)
		}
		if got != tt.want {
			t.Fatalf("%s MapPathToHost() = %q, want %q", tt.name, got, tt.want)
		}
	}

	if _, err := provider.MapPathToBackend(`relative\path`); err == nil {
		t.Fatalf("MapPathToBackend(relative) error = nil, want error")
	}
	if _, err := provider.MapPathToBackend(`\\wsl$\debian\home\ada`); err == nil {
		t.Fatalf("MapPathToBackend(other distro) error = nil, want error")
	}
}

func seedWSLDetectThroughDockerProbe(runner *fakeRunner) {
	runner.paths[wslCommandName] = `C:\Windows\System32\wsl.exe`
	runner.outputs[wslCommandName+" --status"] = "Default Version: 2\n"
	runner.outputs[wslCommandName+" -l -v"] = "  NAME      STATE      VERSION\n  Ubuntu    Running    2\n"
	runner.outputs[wslCommandName+" -d Ubuntu -- sh -lc cat /etc/os-release"] = "ID=ubuntu\n"
	runner.outputs[wslCommandName+" -d Ubuntu -- test -d /run/systemd/system"] = ""
	runner.outputs[wslCommandName+" -d Ubuntu -- sh -lc readlink -f /usr/bin/docker 2>/dev/null || true"] = "/usr/bin/docker\n"
}

func utf16LE(value string) string {
	units := utf16.Encode([]rune(value))
	raw := make([]byte, 2+len(units)*2)
	raw[0] = 0xff
	raw[1] = 0xfe
	for i, unit := range units {
		binary.LittleEndian.PutUint16(raw[2+i*2:], unit)
	}
	return string(raw)
}

func assertNoProblem(t *testing.T, problems []models.ProviderProblem, code string) {
	t.Helper()
	for _, problem := range problems {
		if problem.Code == code {
			t.Fatalf("unexpected problem %s in %#v", code, problems)
		}
	}
}
