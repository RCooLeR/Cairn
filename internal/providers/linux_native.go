package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/security"
)

const (
	linuxNativeID          = "linux_native"
	linuxNativeDisplayName = "Linux Native"
	defaultDockerSocket    = "/var/run/docker.sock"
	commandTimeout         = 2 * time.Second
	dockerOperationTimeout = 2 * time.Hour
	composeCommandTimeout  = 30 * time.Second
	socketTimeout          = time.Second
)

var dockerAptPackages = []string{
	"docker-ce",
	"docker-ce-cli",
	"containerd.io",
	"docker-buildx-plugin",
	"docker-compose-plugin",
}

type LinuxNativeOptions struct {
	SocketPath string
	Runner     CommandRunner
	Probe      LinuxProbe
	IDs        *security.IDSource
}

type LinuxProbe interface {
	Env(key string) string
	Stat(path string) (os.FileInfo, error)
	CanConnectUnixSocket(ctx context.Context, path string, timeout time.Duration) error
}

type OSProbe struct{}

func (OSProbe) Env(key string) string {
	return os.Getenv(key)
}

func (OSProbe) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (OSProbe) CanConnectUnixSocket(ctx context.Context, path string, timeout time.Duration) error {
	dialCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		dialCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(dialCtx, "unix", path)
	if err != nil {
		return err
	}
	return conn.Close()
}

type LinuxNativeProvider struct {
	socketPath string
	runner     CommandRunner
	probe      LinuxProbe
	ids        *security.IDSource
	installMu  sync.Mutex
	plans      map[string]linuxInstallPlan
}

type linuxInstallPlan struct {
	Steps     []linuxInstallStep
	ExpiresAt time.Time
}

type linuxInstallStep struct {
	Message     string
	Timeout     time.Duration
	Command     []string
	RepairHints []string
}

func NewLinuxNative(opts LinuxNativeOptions) *LinuxNativeProvider {
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	probe := opts.Probe
	if probe == nil {
		probe = OSProbe{}
	}
	return &LinuxNativeProvider{
		socketPath: opts.SocketPath,
		runner:     runner,
		probe:      probe,
		ids:        opts.IDs,
		plans:      map[string]linuxInstallPlan{},
	}
}

func (p *LinuxNativeProvider) ID() string {
	return linuxNativeID
}

func (p *LinuxNativeProvider) DisplayName() string {
	return linuxNativeDisplayName
}

func (p *LinuxNativeProvider) Type() string {
	return TypeLinuxNative
}

func (p *LinuxNativeProvider) Platform() string {
	return PlatformLinux
}

func (p *LinuxNativeProvider) Detect(ctx context.Context) (*models.ProviderStatus, error) {
	status := &models.ProviderStatus{}

	if !p.systemdAvailable() {
		status.Warnings = append(status.Warnings, models.ProviderWarning{
			Code:    WarningSystemdMissing,
			Message: "systemd was not detected; Cairn may not be able to start or stop the Docker service automatically.",
		})
	}

	if _, err := p.runner.LookPath("docker"); err != nil {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerMissing,
			"Docker CLI is not installed or not on PATH.",
			"Install Docker Engine and ensure the docker CLI is on PATH.",
			true,
		))
		return status, nil
	}
	status.DockerInstalled = true

	socketPath := p.detectSocketPath()
	if socketPath != "" {
		status.DockerHost = "unix://" + socketPath
	}

	socketOK := false
	if socketPath == "" {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerDown,
			"Docker socket was not found.",
			"Start Docker Engine or configure linux.socket_path in Settings.",
			true,
		))
	} else if err := p.probe.CanConnectUnixSocket(ctx, socketPath, socketTimeout); err != nil {
		if isPermissionError(err) {
			status.Problems = append(status.Problems, providerProblem(
				ProblemSocketPerm,
				"Cairn cannot access the Docker socket.",
				"Choose sudo-per-action in Settings or add your Linux user to the docker group, then sign out and back in.",
				true,
			))
		} else {
			status.Problems = append(status.Problems, providerProblem(
				ProblemDockerDown,
				"Docker daemon is not accepting connections on its socket.",
				"Start Docker Engine with systemctl or repair the Docker service.",
				true,
			))
		}
	} else {
		socketOK = true
	}

	status.CurrentContext = status.DockerHost

	if composeVersion, ok := p.runDockerText(ctx, "compose", "version", "--short"); ok {
		status.ComposeInstalled = true
		status.ComposeVersion = normalizeDockerVersion(composeVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemComposeMissing,
			"Docker Compose plugin is missing.",
			"Install the docker-compose-plugin package for this Linux distribution.",
			true,
		))
	}

	if buildxVersion, ok := p.runDockerText(ctx, "buildx", "version"); ok {
		status.BuildxInstalled = true
		status.BackendVersion = normalizeDockerVersion(buildxVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemBuildxMissing,
			"Docker Buildx plugin is missing.",
			"Install the docker-buildx-plugin package for this Linux distribution.",
			true,
		))
	}

	if status.DockerInstalled || status.ComposeInstalled || status.BuildxInstalled {
		if outdated, ok := p.detectDockerPackageUpdates(ctx); ok && len(outdated) > 0 {
			status.Warnings = append(status.Warnings, dockerPackagesOutdatedWarning(outdated))
		}
	}

	if socketOK {
		if dockerVersion, ok := p.runDockerText(ctx, "info", "--format", "{{.ServerVersion}}"); ok {
			status.DockerRunning = true
			status.DockerVersion = normalizeDockerVersion(dockerVersion)
			if status.BackendVersion == "" {
				status.BackendVersion = status.DockerVersion
			}
		} else {
			status.Problems = append(status.Problems, providerProblem(
				ProblemDockerDown,
				"Docker daemon ping failed.",
				"Start Docker Engine with systemctl or repair the Docker service.",
				true,
			))
		}
	}

	status.Installed = status.DockerInstalled && status.ComposeInstalled && status.BuildxInstalled
	status.Running = status.DockerRunning
	status.Healthy = status.Installed && status.Running && !hasBlockingProblem(status.Problems)
	return status, nil
}

func (p *LinuxNativeProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	steps := buildLinuxInstallSteps("unix://" + p.detectSocketPath())
	planID, err := p.ids.NewPlanID()
	if err != nil {
		return nil, err
	}
	commands := make([]models.PlannedCommand, 0, len(steps))
	for index, step := range steps {
		commands = append(commands, models.PlannedCommand{
			Order:       index + 1,
			Command:     displayCommand(step.Command),
			Risk:        models.RiskNeedsConfirmation,
			Explanation: step.Message,
		})
	}
	plan := &models.CommandPlan{
		PlanID:   planID,
		Title:    "Install or update Docker Engine on Linux",
		Risk:     models.RiskNeedsConfirmation,
		Commands: commands,
		Effects: []string{
			"Install apt prerequisites required by Docker's official repository.",
			"Add Docker's official apt signing key and repository for this distribution.",
			"Install or upgrade Docker Engine, containerd, Compose, and Buildx packages.",
			"Enable and start the Docker service with systemd.",
			"Verify Docker Engine, Compose, Buildx, and hello-world.",
		},
		ExpiresAt: time.Now().UTC().Add(security.DefaultPlanTTL),
	}
	p.installMu.Lock()
	p.pruneExpiredInstallPlansLocked(time.Now().UTC())
	p.plans[planID] = linuxInstallPlan{Steps: steps, ExpiresAt: plan.ExpiresAt}
	p.installMu.Unlock()
	return plan, nil
}

func (p *LinuxNativeProvider) ExecuteInstallStep(ctx context.Context, planID string, step int, progress chan<- InstallProgress) error {
	now := time.Now().UTC()
	p.installMu.Lock()
	p.pruneExpiredInstallPlansLocked(now)
	plan, ok := p.plans[planID]
	if ok && !plan.ExpiresAt.IsZero() && now.After(plan.ExpiresAt) {
		delete(p.plans, planID)
		ok = false
	}
	p.installMu.Unlock()
	if !ok || step < 0 || step >= len(plan.Steps) {
		return apperror.New(apperror.PlanExpired, "Install plan expired or was not found")
	}
	installStep := plan.Steps[step]
	sendInstallProgress(progress, step+1, len(plan.Steps), "Running: "+installStep.Message, false)
	result, err := p.runner.Run(ctx, installStep.Timeout, installStep.Command[0], installStep.Command[1:]...)
	if err != nil || result == nil || result.ExitCode != 0 {
		detail := commandFailureDetail(result, err)
		opts := []apperror.Option{apperror.WithDetail(detail)}
		if len(installStep.RepairHints) > 0 {
			opts = append(opts, apperror.WithRepairHints(installStep.RepairHints...))
		}
		return apperror.Wrap(apperror.ProviderNotReady, "Linux install step failed", err, opts...)
	}
	if step == len(plan.Steps)-1 {
		p.installMu.Lock()
		delete(p.plans, planID)
		p.installMu.Unlock()
	}
	sendInstallProgress(progress, step+1, len(plan.Steps), "Done: "+installStep.Message, false)
	return nil
}

func (p *LinuxNativeProvider) pruneExpiredInstallPlansLocked(now time.Time) {
	for id, plan := range p.plans {
		if !plan.ExpiresAt.IsZero() && now.After(plan.ExpiresAt) {
			delete(p.plans, id)
		}
	}
}

func (p *LinuxNativeProvider) Start(ctx context.Context) error {
	_, err := p.runner.Run(ctx, commandTimeout, "systemctl", "start", "docker")
	return err
}

func (p *LinuxNativeProvider) Stop(ctx context.Context) error {
	_, err := p.runner.Run(ctx, commandTimeout, "systemctl", "stop", "docker")
	return err
}

func (p *LinuxNativeProvider) Restart(ctx context.Context) error {
	_, err := p.runner.Run(ctx, commandTimeout, "systemctl", "restart", "docker")
	return err
}

func (p *LinuxNativeProvider) DockerHost(context.Context) (string, error) {
	socketPath := p.detectSocketPath()
	return "unix://" + socketPath, nil
}

func (p *LinuxNativeProvider) BackendIdentity(context.Context) (string, error) {
	return "unix://" + p.detectSocketPath(), nil
}

func (p *LinuxNativeProvider) DockerContext(context.Context) (string, error) {
	return "unix://" + p.detectSocketPath(), nil
}

func (p *LinuxNativeProvider) RunDocker(ctx context.Context, args ...string) (*CommandResult, error) {
	return p.runner.Run(ctx, dockerOperationTimeout, "docker", p.dockerHostArgs(args...)...)
}

func (p *LinuxNativeProvider) RunDockerWithInput(ctx context.Context, input string, args ...string) (*CommandResult, error) {
	if runner, ok := p.runner.(OptionsCommandRunner); ok {
		return runner.RunWithOptions(ctx, CommandRunOptions{
			Timeout: dockerOperationTimeout,
			Stdin:   input,
		}, "docker", p.dockerHostArgs(args...)...)
	}
	return p.RunDocker(ctx, args...)
}

func (p *LinuxNativeProvider) RunBackendCommand(ctx context.Context, input string, args ...string) (*CommandResult, error) {
	if len(args) == 0 {
		return nil, apperror.New(apperror.Conflict, "Backend command is required")
	}
	if runner, ok := p.runner.(OptionsCommandRunner); ok {
		return runner.RunWithOptions(ctx, CommandRunOptions{
			Timeout: commandTimeout,
			Stdin:   input,
		}, args[0], args[1:]...)
	}
	return p.runner.Run(ctx, commandTimeout, args[0], args[1:]...)
}

func (p *LinuxNativeProvider) RunCompose(ctx context.Context, workdir string, args ...string) (*CommandResult, error) {
	return p.RunComposeEnv(ctx, workdir, nil, args...)
}

func (p *LinuxNativeProvider) RunComposeEnv(ctx context.Context, workdir string, env []string, args ...string) (*CommandResult, error) {
	composeArgs := p.dockerHostArgs(append([]string{"compose"}, args...)...)
	timeout := composeTimeoutForArgs(args)
	if runner, ok := p.runner.(OptionsCommandRunner); ok {
		return runner.RunWithOptions(ctx, CommandRunOptions{
			Timeout: timeout,
			Workdir: workdir,
			Env:     env,
		}, "docker", composeArgs...)
	}
	result, err := p.runner.Run(ctx, timeout, "docker", composeArgs...)
	if result != nil && workdir != "" {
		result.Workdir = workdir
	}
	return result, err
}

func (p *LinuxNativeProvider) HostShellCommand(opts models.TerminalOptions) ([]string, error) {
	shell := opts.Shell
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/sh"
	}
	return []string{shell}, nil
}

func (p *LinuxNativeProvider) BackendShellCommand(opts models.TerminalOptions) ([]string, error) {
	return p.HostShellCommand(opts)
}

func (p *LinuxNativeProvider) MapPathToBackend(hostPath string) (string, error) {
	return hostPath, nil
}

func (p *LinuxNativeProvider) MapPathToHost(backendPath string) (string, error) {
	return backendPath, nil
}

func buildLinuxInstallSteps(dockerHost string) []linuxInstallStep {
	aptRefreshCommand := strings.Join([]string{
		"set -e",
		dockerAptSourceCleanupCommand(),
		"apt-get update",
	}, " && ")
	repositoryCommand := strings.Join([]string{
		"set -e",
		"install -m 0755 -d /etc/apt/keyrings",
		"rm -f /etc/apt/keyrings/docker.gpg",
		"curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg",
		"chmod a+r /etc/apt/keyrings/docker.gpg",
		dockerAptSourceWriteCommand(),
	}, " && ")
	dockerPrefix := "docker --host " + shellQuote(strings.TrimSpace(dockerHost))
	verifyCommand := strings.Join([]string{
		dockerPrefix + " info >/dev/null",
		dockerPrefix + " compose version",
		dockerPrefix + " buildx version",
		dockerPrefix + " run --rm hello-world",
	}, " && ")
	return []linuxInstallStep{
		{
			Message: "Refresh apt package indexes",
			Timeout: 10 * time.Minute,
			Command: []string{"sudo", "sh", "-lc", aptRefreshCommand},
			RepairHints: []string{
				"Check internet access and apt repository health, then retry.",
				"If apt is locked, wait for the other package operation to finish and retry.",
			},
		},
		{
			Message: "Install apt prerequisites for Docker's repository",
			Timeout: 10 * time.Minute,
			Command: []string{"sudo", "apt-get", "install", "-y", "ca-certificates", "curl", "gnupg"},
			RepairHints: []string{
				"Check internet access and retry the package installation.",
				"If sudo prompts fail, run Cairn from a session that can authenticate sudo.",
			},
		},
		{
			Message: "Add Docker's official apt signing key and repository",
			Timeout: 2 * time.Minute,
			Command: []string{"sudo", "sh", "-lc", repositoryCommand},
			RepairHints: []string{
				"Confirm this host is an Ubuntu or Debian-family distribution with /etc/os-release.",
				"Check network access to download.docker.com and retry.",
			},
		},
		{
			Message: "Refresh apt indexes after adding Docker's repository",
			Timeout: 10 * time.Minute,
			Command: []string{"sudo", "apt-get", "update"},
			RepairHints: []string{
				"Inspect Docker apt repository errors and retry after repository metadata is reachable.",
			},
		},
		{
			Message: "Install or upgrade Docker Engine, containerd, Compose, and Buildx",
			Timeout: 20 * time.Minute,
			Command: append([]string{"sudo", "apt-get", "install", "-y"}, dockerAptPackages...),
			RepairHints: []string{
				"If packages cannot be located, verify the Docker apt repository step completed successfully.",
				"If apt is locked, wait for the other package operation to finish and retry.",
			},
		},
		{
			Message: "Enable and start the Docker service",
			Timeout: commandTimeout,
			Command: []string{"sudo", "systemctl", "enable", "--now", "docker"},
			RepairHints: []string{
				"Make sure this Linux host uses systemd and retry.",
				"Run `systemctl status docker` to inspect service errors.",
			},
		},
		{
			Message: "Verify Docker Engine, Compose, Buildx, and hello-world",
			Timeout: 5 * time.Minute,
			Command: []string{"sh", "-lc", verifyCommand},
			RepairHints: []string{
				"If Docker access is denied, choose a Linux permission mode in Settings and retry.",
				"Run `docker info` to inspect daemon or socket errors.",
			},
		},
	}
}

func (p *LinuxNativeProvider) detectSocketPath() string {
	if configured := strings.TrimSpace(strings.TrimPrefix(p.socketPath, "unix://")); configured != "" {
		return path.Clean(configured)
	}
	candidates := make([]string, 0, 2)
	if runtimeDir := strings.TrimSpace(p.probe.Env("XDG_RUNTIME_DIR")); runtimeDir != "" {
		candidates = append(candidates, path.Join(runtimeDir, "docker.sock"))
	}
	candidates = append(candidates, defaultDockerSocket)
	for _, candidate := range candidates {
		if info, err := p.probe.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return defaultDockerSocket
}

func (p *LinuxNativeProvider) dockerHostArgs(args ...string) []string {
	host := "unix://" + p.detectSocketPath()
	return append([]string{"--host", host}, args...)
}

func (p *LinuxNativeProvider) systemdAvailable() bool {
	info, err := p.probe.Stat("/run/systemd/system")
	return err == nil && info.IsDir()
}

func (p *LinuxNativeProvider) runText(ctx context.Context, name string, args ...string) (string, bool) {
	result, err := p.runner.Run(ctx, commandTimeout, name, args...)
	if err != nil || result == nil || result.ExitCode != 0 || result.StdoutTruncated {
		return "", false
	}
	return strings.TrimSpace(result.Stdout), true
}

func (p *LinuxNativeProvider) runDockerText(ctx context.Context, args ...string) (string, bool) {
	return p.runText(ctx, "docker", p.dockerHostArgs(args...)...)
}

func (p *LinuxNativeProvider) detectDockerPackageUpdates(ctx context.Context) ([]string, bool) {
	args := append([]string{"policy"}, dockerAptPackages...)
	output, ok := p.runText(ctx, "apt-cache", args...)
	if !ok {
		return nil, false
	}
	return aptPolicyOutdatedPackages(output), true
}

func providerProblem(code, message, hint string, recoverable bool) models.ProviderProblem {
	return models.ProviderProblem{
		Code:        code,
		Message:     message,
		RepairHint:  hint,
		Recoverable: recoverable,
	}
}

func hasBlockingProblem(problems []models.ProviderProblem) bool {
	for _, problem := range problems {
		switch problem.Code {
		case ProblemWSLMissing,
			ProblemWSLUnavailable,
			ProblemUbuntuMissing,
			ProblemWSL2Required,
			ProblemSystemdOff,
			ProblemDesktopIntegrationConflict,
			ProblemDockerMissing,
			ProblemDockerDown,
			ProblemSocketPerm,
			ProblemComposeMissing,
			ProblemBuildxMissing,
			ProblemColimaMissing,
			ProblemColimaStopped,
			ProblemContextMissing,
			ProblemContextNotSelected:
			return true
		}
	}
	return false
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "permission denied") || strings.Contains(lower, "access is denied")
}

func normalizeDockerVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "Docker Compose version ")
	value = strings.TrimPrefix(value, "Docker Buildx version ")
	for _, field := range strings.Fields(value) {
		trimmed := strings.TrimPrefix(field, "v")
		if trimmed != "" && trimmed[0] >= '0' && trimmed[0] <= '9' {
			return trimmed
		}
	}
	return value
}

func aptPolicyOutdatedPackages(output string) []string {
	type packageState struct {
		name      string
		installed string
		candidate string
	}
	seen := map[string]bool{}
	outdated := []string{}
	current := packageState{}
	flush := func() {
		if current.name == "" || seen[current.name] {
			return
		}
		installed := strings.TrimSpace(current.installed)
		candidate := strings.TrimSpace(current.candidate)
		if installed == "" || candidate == "" || installed == "(none)" || candidate == "(none)" {
			return
		}
		if installed != candidate {
			outdated = append(outdated, current.name)
			seen[current.name] = true
		}
	}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " ") {
			flush()
			current = packageState{name: strings.TrimSuffix(trimmed, ":")}
			continue
		}
		if strings.HasPrefix(trimmed, "Installed:") {
			current.installed = strings.TrimSpace(strings.TrimPrefix(trimmed, "Installed:"))
			continue
		}
		if strings.HasPrefix(trimmed, "Candidate:") {
			current.candidate = strings.TrimSpace(strings.TrimPrefix(trimmed, "Candidate:"))
		}
	}
	flush()
	return outdated
}

func dockerPackagesOutdatedWarning(packages []string) models.ProviderWarning {
	return models.ProviderWarning{
		Code: WarningDockerPackagesOutdated,
		Message: fmt.Sprintf(
			"Docker packages have updates available: %s. Run Auto repair / update to install the current versions.",
			strings.Join(packages, ", "),
		),
	}
}
