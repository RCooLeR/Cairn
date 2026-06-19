package providers

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/security"
)

const (
	windowsWSLID          = "windows_wsl_ubuntu"
	windowsWSLDisplayName = "Windows WSL Ubuntu"
	defaultWSLDistro      = "Ubuntu"
	wslCommandName        = "wsl.exe"
	wslCommandTimeout     = 10 * commandTimeout
	wslServiceTimeout     = 2 * time.Minute
	wslInstallTimeout     = 15 * time.Minute
)

const (
	wslNVIDIAGPUCheckCommand     = "command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi -L >/dev/null 2>&1"
	wslNVIDIARuntimeCheckCommand = "docker info 2>/dev/null | sed -n 's/^ Runtimes: //p' | grep -Eq '(^|[[:space:]])nvidia([[:space:]]|$)'"
)

var windowsDrivePathPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]?`)

type WindowsWSLOptions struct {
	Distro      string
	Runner      CommandRunner
	StdioDialer WSLStdioDialer
}

type WindowsWSLProvider struct {
	distro       string
	runner       CommandRunner
	stdioDialer  WSLStdioDialer
	installMu    sync.Mutex
	installPlans map[string]wslInstallPlan
}

type WSLStdioDialer func(context.Context, []string) (net.Conn, error)

type wslDistro struct {
	Name    string
	State   string
	Version int
	Default bool
}

type wslInstallPlan struct {
	Steps []wslInstallStep
}

type wslInstallStep struct {
	Message     string
	Timeout     time.Duration
	Command     []string
	RepairHints []string
}

func NewWindowsWSL(opts WindowsWSLOptions) *WindowsWSLProvider {
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	stdioDialer := opts.StdioDialer
	if stdioDialer == nil {
		stdioDialer = dialCommandStdio
	}
	return &WindowsWSLProvider{
		distro:       strings.TrimSpace(opts.Distro),
		runner:       runner,
		stdioDialer:  stdioDialer,
		installPlans: map[string]wslInstallPlan{},
	}
}

func (p *WindowsWSLProvider) SetDistro(distro string) {
	p.distro = strings.TrimSpace(distro)
}

func (p *WindowsWSLProvider) ID() string {
	return windowsWSLID
}

func (p *WindowsWSLProvider) DisplayName() string {
	return windowsWSLDisplayName
}

func (p *WindowsWSLProvider) Type() string {
	return TypeWindowsWSL
}

func (p *WindowsWSLProvider) Platform() string {
	return PlatformWindows
}

func (p *WindowsWSLProvider) Detect(ctx context.Context) (*models.ProviderStatus, error) {
	status := &models.ProviderStatus{}
	if _, err := p.runner.LookPath(wslCommandName); err != nil {
		status.Problems = append(status.Problems, providerProblem(
			ProblemWSLMissing,
			"WSL is not installed or wsl.exe is not on PATH.",
			"Install WSL 2 with a Docker-capable Linux distribution, then reopen Cairn.",
			true,
		))
		return status, nil
	}

	defaultVersion, statusOK := p.detectDefaultWSLVersion(ctx)
	if statusOK && defaultVersion > 0 {
		status.BackendVersion = fmt.Sprintf("WSL %d", defaultVersion)
	}

	distros, ok := p.listDistros(ctx)
	if !ok || len(distros) == 0 {
		status.Problems = append(status.Problems, providerProblem(
			ProblemUbuntuMissing,
			"No usable WSL distro was found.",
			"Install a WSL 2 Linux distro with Docker, or select one in Settings.",
			true,
		))
		return status, nil
	}

	selected, ok := selectWSLDistro(distros, p.distro)
	if !ok {
		status.Problems = append(status.Problems, providerProblem(
			ProblemUbuntuMissing,
			fmt.Sprintf("Configured WSL distro %q was not found.", p.configuredDistro()),
			"Select an installed WSL 2 distro in Settings.",
			true,
		))
		return status, nil
	}
	p.distro = selected.Name

	if selected.Version != 2 {
		status.Problems = append(status.Problems, providerProblem(
			ProblemWSL2Required,
			fmt.Sprintf("WSL distro %q is version %d; WSL 2 is required.", selected.Name, selected.Version),
			fmt.Sprintf("Run `wsl --set-version %s 2`, then reopen Cairn.", selected.Name),
			true,
		))
		return status, nil
	}

	ubuntuFamily := false
	if release, ok := p.runWSLText(ctx, selected.Name, "sh", "-lc", "cat /etc/os-release"); ok {
		ubuntuFamily = isUbuntuOSRelease(release)
	}
	dockerInstallHint := "Install Docker Engine and the docker CLI inside the selected WSL distro."
	composeInstallHint := "Install the Docker Compose plugin inside the selected WSL distro."
	buildxInstallHint := "Install the Docker Buildx plugin inside the selected WSL distro."
	dockerStartHint := "Start Docker Engine inside the selected WSL distro with `sudo systemctl start docker`."
	if !ubuntuFamily {
		dockerInstallHint = "Install Docker Engine manually for this WSL distro, or select an Ubuntu-family distro if you want Cairn's automatic installer."
		composeInstallHint = "Install the Docker Compose plugin for this WSL distro, or use an Ubuntu-family distro for Cairn's automatic installer."
		buildxInstallHint = "Install the Docker Buildx plugin for this WSL distro, or use an Ubuntu-family distro for Cairn's automatic installer."
		dockerStartHint = "Start Docker Engine inside the selected WSL distro, or check that its service manager is configured."
	}

	if !p.runWSLOK(ctx, selected.Name, "test", "-d", "/run/systemd/system") {
		status.Problems = append(status.Problems, providerProblem(
			ProblemSystemdOff,
			fmt.Sprintf("systemd is not enabled in WSL distro %q.", selected.Name),
			"Enable systemd in /etc/wsl.conf, run `wsl --shutdown`, then reopen Cairn.",
			true,
		))
	}

	if target, ok := p.runWSLText(ctx, selected.Name, "sh", "-lc", "readlink -f /usr/bin/docker 2>/dev/null || true"); ok && isDockerDesktopWSLProxy(target) {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDesktopIntegrationConflict,
			"Docker Desktop WSL integration is active for the selected distro.",
			"Disable Docker Desktop WSL integration for this distro and install Docker Engine inside it.",
			true,
		))
		return status, nil
	}

	if !p.runWSLOK(ctx, selected.Name, "sh", "-lc", "command -v docker >/dev/null 2>&1") {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerMissing,
			fmt.Sprintf("Docker CLI is missing inside WSL distro %q.", selected.Name),
			dockerInstallHint,
			true,
		))
		return status, nil
	}
	status.DockerInstalled = true

	if contextName, ok := p.runWSLText(ctx, selected.Name, "docker", "context", "show"); ok {
		status.CurrentContext = contextName
	}
	if composeVersion, ok := p.runWSLText(ctx, selected.Name, "docker", "compose", "version", "--short"); ok {
		status.ComposeInstalled = true
		status.ComposeVersion = normalizeDockerVersion(composeVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemComposeMissing,
			"Docker Compose plugin is missing inside the selected WSL distro.",
			composeInstallHint,
			true,
		))
	}
	if buildxVersion, ok := p.runWSLText(ctx, selected.Name, "docker", "buildx", "version"); ok {
		status.BuildxInstalled = true
		status.BackendVersion = normalizeDockerVersion(buildxVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemBuildxMissing,
			"Docker Buildx plugin is missing inside the selected WSL distro.",
			buildxInstallHint,
			true,
		))
	}
	if ubuntuFamily && (status.DockerInstalled || status.ComposeInstalled || status.BuildxInstalled) {
		if outdated, ok := p.detectDockerPackageUpdates(ctx, selected.Name); ok && len(outdated) > 0 {
			status.Warnings = append(status.Warnings, dockerPackagesOutdatedWarning(outdated))
		}
	}
	if dockerVersion, ok := p.runWSLText(ctx, selected.Name, "docker", "info", "--format", "{{.ServerVersion}}"); ok {
		status.DockerRunning = true
		status.DockerVersion = normalizeDockerVersion(dockerVersion)
		status.DockerHost = "wsl+stdio://" + selected.Name
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerDown,
			"Docker daemon is not reachable inside the selected WSL distro.",
			dockerStartHint,
			true,
		))
	}
	status.NVIDIAGPUDetected = p.wslNVIDIAGPUAvailable(ctx, selected.Name)
	if status.NVIDIAGPUDetected && status.DockerRunning {
		status.NVIDIAContainerRuntime = p.wslDockerNVIDIARuntimeAvailable(ctx, selected.Name)
		if !status.NVIDIAContainerRuntime {
			status.Warnings = append(status.Warnings, models.ProviderWarning{
				Code:    WarningNVIDIARuntimeMissing,
				Message: "An NVIDIA GPU is visible in WSL, but Docker is not configured with the NVIDIA container runtime.",
			})
		}
	}

	status.Installed = status.DockerInstalled && status.ComposeInstalled && status.BuildxInstalled
	status.Running = status.DockerRunning
	status.Healthy = status.Installed && status.Running && !hasBlockingProblem(status.Problems)
	return status, nil
}

func (p *WindowsWSLProvider) ListDistros(ctx context.Context) ([]models.WSLDistroInfo, error) {
	if _, err := p.runner.LookPath(wslCommandName); err != nil {
		return nil, apperror.New(apperror.ProviderNotReady, "WSL is not installed or wsl.exe is not on PATH")
	}
	distros, ok := p.listDistros(ctx)
	if !ok {
		return nil, apperror.New(apperror.ProviderNotReady, "WSL distros are not available")
	}
	result := make([]models.WSLDistroInfo, 0, len(distros))
	for _, distro := range distros {
		result = append(result, models.WSLDistroInfo{
			Name:    distro.Name,
			State:   distro.State,
			Version: distro.Version,
			Default: distro.Default,
		})
	}
	return result, nil
}

func (p *WindowsWSLProvider) PlanInstall(_ context.Context, opts models.InstallOptions) (*models.CommandPlan, error) {
	distro := strings.TrimSpace(opts.Extra["distro"])
	if distro == "" {
		distro = p.configuredDistro()
	}
	distribution := strings.TrimSpace(opts.Extra["distribution"])
	if distribution == "" {
		distribution = defaultWSLDistro
	}
	steps := buildWSLInstallStepsFor(distro, distribution)
	planID := security.NewPlanID()
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
		Title:    "Install or update Docker Engine in Ubuntu on WSL",
		Risk:     models.RiskNeedsConfirmation,
		Commands: commands,
		Effects: []string{
			"Enable or verify WSL and the selected Ubuntu distro.",
			"Enable systemd in the selected distro and restart WSL if needed.",
			"Install or upgrade Docker Engine, containerd, Compose, and Buildx from Docker's official apt repository.",
			"Add the WSL user to the docker group; a WSL restart may be required before group membership applies.",
			"Configure NVIDIA Container Toolkit when an NVIDIA GPU is exposed to WSL.",
			"Enable and start the Docker service, then verify Docker, Compose, Buildx, and hello-world.",
		},
		ExpiresAt: time.Now().UTC().Add(security.DefaultPlanTTL),
	}
	p.installMu.Lock()
	p.installPlans[planID] = wslInstallPlan{Steps: steps}
	p.installMu.Unlock()
	return plan, nil
}

func (p *WindowsWSLProvider) ExecuteInstallStep(ctx context.Context, planID string, step int, progress chan<- InstallProgress) error {
	p.installMu.Lock()
	plan, ok := p.installPlans[planID]
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
		return apperror.Wrap(apperror.ProviderNotReady, "WSL install step failed", err, opts...)
	}
	if step == len(plan.Steps)-1 {
		p.installMu.Lock()
		delete(p.installPlans, planID)
		p.installMu.Unlock()
	}
	sendInstallProgress(progress, step+1, len(plan.Steps), "Done: "+installStep.Message, false)
	return nil
}

func (p *WindowsWSLProvider) Start(ctx context.Context) error {
	_, err := p.runWSL(ctx, p.configuredDistro(), "systemctl", "start", "docker")
	return err
}

func (p *WindowsWSLProvider) Stop(ctx context.Context) error {
	_, err := p.runWSL(ctx, p.configuredDistro(), "systemctl", "stop", "docker")
	return err
}

func (p *WindowsWSLProvider) Restart(ctx context.Context) error {
	_, err := p.runWSL(ctx, p.configuredDistro(), "systemctl", "restart", "docker")
	return err
}

func (p *WindowsWSLProvider) DockerHost(context.Context) (string, error) {
	return "unix:///var/run/docker.sock", nil
}

func (p *WindowsWSLProvider) DockerDialContext(ctx context.Context) (func(context.Context, string, string) (net.Conn, error), error) {
	command, err := p.dockerDialCommand(ctx)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return p.stdioDialer(ctx, command)
	}, nil
}

func (p *WindowsWSLProvider) DockerContext(ctx context.Context) (string, error) {
	contextName, ok := p.runWSLText(ctx, p.configuredDistro(), "docker", "context", "show")
	if !ok {
		return "", apperror.New(apperror.ProviderNotReady, "WSL Docker context is not available")
	}
	return contextName, nil
}

func (p *WindowsWSLProvider) BackendIdentity(context.Context) (string, error) {
	return "wsl:" + p.configuredDistro(), nil
}

func (p *WindowsWSLProvider) RunDocker(ctx context.Context, args ...string) (*CommandResult, error) {
	return p.runWSLWithTimeout(ctx, dockerOperationTimeout, p.configuredDistro(), append([]string{"docker"}, args...)...)
}

func (p *WindowsWSLProvider) RunDockerWithInput(ctx context.Context, input string, args ...string) (*CommandResult, error) {
	return p.runWSLWithOptions(ctx, dockerOperationTimeout, input, p.configuredDistro(), append([]string{"docker"}, args...)...)
}

func (p *WindowsWSLProvider) RunBackendCommand(ctx context.Context, input string, args ...string) (*CommandResult, error) {
	if len(args) == 0 {
		return nil, apperror.New(apperror.Conflict, "Backend command is required")
	}
	return p.runWSLWithOptions(ctx, wslCommandTimeout, input, p.configuredDistro(), args...)
}

func (p *WindowsWSLProvider) RunCompose(ctx context.Context, workdir string, args ...string) (*CommandResult, error) {
	return p.RunComposeEnv(ctx, workdir, nil, args...)
}

func (p *WindowsWSLProvider) RunComposeEnv(ctx context.Context, workdir string, env []string, args ...string) (*CommandResult, error) {
	composeArgs := append([]string{"compose"}, args...)
	timeout := composeTimeoutForArgs(args)
	if strings.TrimSpace(workdir) == "" {
		if len(env) == 0 {
			return p.runWSLWithTimeout(ctx, timeout, p.configuredDistro(), append([]string{"docker"}, composeArgs...)...)
		}
		shellCommand := shellEnvExports(env) + "exec docker " + shellJoin(composeArgs)
		return p.runWSLWithTimeout(ctx, timeout, p.configuredDistro(), "sh", "-lc", shellCommand)
	}
	backendWorkdir, err := p.MapPathToBackend(workdir)
	if err != nil {
		return nil, err
	}
	shellCommand := shellEnvExports(env) + "cd " + shellQuote(backendWorkdir) + " && exec docker " + shellJoin(composeArgs)
	result, err := p.runWSLWithTimeout(ctx, timeout, p.configuredDistro(), "sh", "-lc", shellCommand)
	if result != nil {
		result.Workdir = backendWorkdir
	}
	return result, err
}

func (p *WindowsWSLProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	if _, err := p.runner.LookPath("pwsh"); err == nil {
		return []string{"pwsh"}, nil
	}
	return []string{"powershell.exe"}, nil
}

func (p *WindowsWSLProvider) BackendShellCommand(opts models.TerminalOptions) ([]string, error) {
	shell := strings.TrimSpace(opts.Shell)
	if shell == "" {
		shell = "/bin/bash"
	}
	argv := []string{wslCommandName, "-d", p.configuredDistro()}
	if workdir := strings.TrimSpace(opts.WorkingDir); workdir != "" {
		backendWorkdir, err := p.MapPathToBackend(workdir)
		if err != nil {
			return nil, err
		}
		argv = append(argv, "--cd", backendWorkdir)
	}
	argv = append(argv, "--", shell)
	return argv, nil
}

func (p *WindowsWSLProvider) MapPathToBackend(hostPath string) (string, error) {
	value := strings.TrimSpace(hostPath)
	if value == "" {
		return "", errors.New("path is empty")
	}
	if strings.HasPrefix(value, "/") {
		return value, nil
	}
	if backend, ok := p.mapWSLUNCToBackend(value); ok {
		return backend, nil
	}
	if backend, ok := normalizeBackslashBackendPath(value); ok {
		return backend, nil
	}
	if windowsDrivePathPattern.MatchString(value) {
		drive := strings.ToLower(value[:1])
		rest := strings.TrimLeft(value[2:], `\/`)
		rest = strings.ReplaceAll(rest, `\`, "/")
		if rest == "" {
			return "/mnt/" + drive, nil
		}
		return "/mnt/" + drive + "/" + rest, nil
	}
	return "", fmt.Errorf("cannot map host path %q to WSL backend path", hostPath)
}

func (p *WindowsWSLProvider) MapPathToHost(backendPath string) (string, error) {
	value := strings.TrimSpace(backendPath)
	if value == "" {
		return "", errors.New("path is empty")
	}
	if windowsDrivePathPattern.MatchString(value) || strings.HasPrefix(value, `\\`) {
		return value, nil
	}
	if backend, ok := normalizeBackslashBackendPath(value); ok {
		value = backend
	}
	if strings.HasPrefix(value, "/mnt/") && len(value) >= len("/mnt/x") {
		drive := value[len("/mnt/")]
		if isASCIILetter(drive) && (len(value) == len("/mnt/x") || value[len("/mnt/x")] == '/') {
			prefix := strings.ToUpper(string(drive)) + `:\`
			rest := strings.TrimPrefix(value[len("/mnt/x"):], "/")
			if rest == "" {
				return prefix, nil
			}
			return prefix + strings.ReplaceAll(rest, "/", `\`), nil
		}
	}
	if strings.HasPrefix(value, "/") {
		trimmed := strings.TrimPrefix(value, "/")
		if trimmed == "" {
			return `\\wsl$\` + p.configuredDistro(), nil
		}
		return `\\wsl$\` + p.configuredDistro() + `\` + strings.ReplaceAll(trimmed, "/", `\`), nil
	}
	return "", fmt.Errorf("cannot map backend path %q to Windows host path", backendPath)
}

func normalizeBackslashBackendPath(value string) (string, bool) {
	if !strings.HasPrefix(value, `\`) || strings.HasPrefix(value, `\\`) {
		return "", false
	}
	normalized := "/" + strings.ReplaceAll(strings.TrimLeft(value, `\`), `\`, "/")
	if normalized == "/" {
		return normalized, true
	}
	return normalized, true
}

func (p *WindowsWSLProvider) configuredDistro() string {
	if strings.TrimSpace(p.distro) != "" {
		return strings.TrimSpace(p.distro)
	}
	return defaultWSLDistro
}

func (p *WindowsWSLProvider) detectDefaultWSLVersion(ctx context.Context) (int, bool) {
	result, err := p.runner.Run(ctx, wslCommandTimeout, wslCommandName, "--status")
	if err != nil || result == nil || result.ExitCode != 0 {
		return 0, false
	}
	return parseWSLDefaultVersion(result.Stdout)
}

func (p *WindowsWSLProvider) listDistros(ctx context.Context) ([]wslDistro, bool) {
	result, err := p.runner.Run(ctx, wslCommandTimeout, wslCommandName, "-l", "-v")
	if err != nil || result == nil || result.ExitCode != 0 {
		return nil, false
	}
	distros, err := parseWSLListVerbose(result.Stdout)
	return distros, err == nil
}

func (p *WindowsWSLProvider) runWSLOK(ctx context.Context, distro string, args ...string) bool {
	result, err := p.runWSL(ctx, distro, args...)
	return err == nil && result != nil && result.ExitCode == 0
}

func (p *WindowsWSLProvider) runWSLText(ctx context.Context, distro string, args ...string) (string, bool) {
	result, err := p.runWSL(ctx, distro, args...)
	if err != nil || result == nil || result.ExitCode != 0 {
		return "", false
	}
	return strings.TrimSpace(decodeWSLOutput(result.Stdout)), true
}

func (p *WindowsWSLProvider) detectDockerPackageUpdates(ctx context.Context, distro string) ([]string, bool) {
	command := "apt-cache policy " + strings.Join(dockerAptPackages, " ")
	output, ok := p.runWSLText(ctx, distro, "sh", "-lc", command)
	if !ok {
		return nil, false
	}
	return aptPolicyOutdatedPackages(output), true
}

func (p *WindowsWSLProvider) wslNVIDIAGPUAvailable(ctx context.Context, distro string) bool {
	return p.runWSLOK(ctx, distro, "sh", "-lc", wslNVIDIAGPUCheckCommand)
}

func (p *WindowsWSLProvider) wslDockerNVIDIARuntimeAvailable(ctx context.Context, distro string) bool {
	return p.runWSLOK(ctx, distro, "sh", "-lc", wslNVIDIARuntimeCheckCommand)
}

func (p *WindowsWSLProvider) runWSL(ctx context.Context, distro string, args ...string) (*CommandResult, error) {
	return p.runWSLWithTimeout(ctx, wslCommandTimeout, distro, args...)
}

func (p *WindowsWSLProvider) runWSLWithTimeout(ctx context.Context, timeout time.Duration, distro string, args ...string) (*CommandResult, error) {
	return p.runWSLWithOptions(ctx, timeout, "", distro, args...)
}

func (p *WindowsWSLProvider) runWSLWithOptions(ctx context.Context, timeout time.Duration, input string, distro string, args ...string) (*CommandResult, error) {
	wslArgs := append([]string{"-d", distro, "--"}, args...)
	if runner, ok := p.runner.(OptionsCommandRunner); ok {
		return runner.RunWithOptions(ctx, CommandRunOptions{
			Timeout: timeout,
			Stdin:   input,
		}, wslCommandName, wslArgs...)
	}
	return p.runner.Run(ctx, timeout, wslCommandName, wslArgs...)
}

func (p *WindowsWSLProvider) dockerDialCommand(ctx context.Context) ([]string, error) {
	distro := p.configuredDistro()
	if p.runWSLOK(ctx, distro, "docker", "system", "dial-stdio", "--help") {
		return []string{wslCommandName, "-d", distro, "--", "docker", "system", "dial-stdio"}, nil
	}
	if p.runWSLOK(ctx, distro, "sh", "-lc", "command -v socat >/dev/null 2>&1") {
		return []string{wslCommandName, "-d", distro, "--", "socat", "UNIX-CONNECT:/var/run/docker.sock", "-"}, nil
	}
	return nil, apperror.New(
		apperror.ProviderNotReady,
		"WSL Docker API transport is not available",
		apperror.WithRepairHints(
			"Install or upgrade the Docker CLI inside the selected WSL distro.",
			"Install socat inside the selected WSL distro if docker system dial-stdio is unavailable.",
		),
	)
}

func (p *WindowsWSLProvider) mapWSLUNCToBackend(value string) (string, bool) {
	normalized := strings.ReplaceAll(value, "/", `\`)
	if !strings.HasPrefix(normalized, `\\`) {
		return "", false
	}
	parts := strings.Split(normalized, `\`)
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	if len(filtered) < 2 {
		return "", false
	}
	server := strings.ToLower(filtered[0])
	if server != "wsl$" && server != "wsl.localhost" {
		return "", false
	}
	if !strings.EqualFold(filtered[1], p.configuredDistro()) {
		return "", false
	}
	if len(filtered) == 2 {
		return "/", true
	}
	return "/" + strings.Join(filtered[2:], "/"), true
}

func parseWSLDefaultVersion(output string) (int, bool) {
	decoded := decodeWSLOutput(output)
	for _, line := range strings.Split(decoded, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Default Version") {
			continue
		}
		version, err := strconv.Atoi(strings.TrimSpace(value))
		return version, err == nil
	}
	return 0, false
}

func parseWSLListVerbose(output string) ([]wslDistro, error) {
	decoded := decodeWSLOutput(output)
	distros := []wslDistro{}
	for _, line := range strings.Split(decoded, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "\ufeff"))
		if line == "" || strings.HasPrefix(strings.ToUpper(line), "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		distro := wslDistro{}
		if fields[0] == "*" {
			distro.Default = true
			fields = fields[1:]
		} else if strings.HasPrefix(fields[0], "*") {
			distro.Default = true
			fields[0] = strings.TrimPrefix(fields[0], "*")
		}
		if len(fields) < 3 {
			continue
		}
		version, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			continue
		}
		distro.Version = version
		distro.State = fields[len(fields)-2]
		distro.Name = strings.Join(fields[:len(fields)-2], " ")
		if isDockerDesktopDistro(distro.Name) {
			continue
		}
		distros = append(distros, distro)
	}
	if len(distros) == 0 {
		return nil, errors.New("no WSL distros parsed")
	}
	return distros, nil
}

func selectWSLDistro(distros []wslDistro, configured string) (wslDistro, bool) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		for _, distro := range distros {
			if strings.EqualFold(distro.Name, configured) {
				return distro, true
			}
		}
		if !strings.EqualFold(configured, defaultWSLDistro) {
			return wslDistro{}, false
		}
	}
	for _, distro := range distros {
		if strings.EqualFold(distro.Name, defaultWSLDistro) {
			return distro, true
		}
	}
	for _, distro := range distros {
		if strings.Contains(strings.ToLower(distro.Name), "ubuntu") {
			return distro, true
		}
	}
	for _, distro := range distros {
		if distro.Default {
			return distro, true
		}
	}
	return distros[0], len(distros) > 0
}

func isDockerDesktopDistro(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	return normalized == "docker-desktop" || normalized == "docker-desktop-data"
}

func isUbuntuOSRelease(output string) bool {
	values := map[string]string{}
	for _, line := range strings.Split(decodeWSLOutput(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		values[strings.ToUpper(key)] = strings.ToLower(value)
	}
	if values["ID"] == "ubuntu" {
		return true
	}
	return strings.Contains(values["ID_LIKE"], "ubuntu")
}

func isDockerDesktopWSLProxy(target string) bool {
	return strings.HasPrefix(strings.TrimSpace(target), "/mnt/wsl/docker-desktop/")
}

func decodeWSLOutput(output string) string {
	raw := []byte(output)
	if len(raw) < 2 || !looksUTF16LE(raw) {
		return strings.ReplaceAll(output, "\x00", "")
	}
	if len(raw)%2 == 1 {
		raw = raw[:len(raw)-1]
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		unit := binary.LittleEndian.Uint16(raw[i : i+2])
		if unit == utf8.RuneError || unit == 0 {
			continue
		}
		if len(units) == 0 && unit == 0xfeff {
			continue
		}
		units = append(units, unit)
	}
	return string(utf16.Decode(units))
}

func looksUTF16LE(raw []byte) bool {
	if len(raw) >= 2 && raw[0] == 0xff && raw[1] == 0xfe {
		return true
	}
	nuls := 0
	for i := 1; i < len(raw); i += 2 {
		if raw[i] == 0 {
			nuls++
		}
	}
	return nuls > len(raw)/4
}

func isASCIILetter(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellEnvExports(env []string) string {
	exports := make([]string, 0, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || !isShellEnvName(key) {
			continue
		}
		exports = append(exports, "export "+key+"="+shellQuote(value))
	}
	if len(exports) == 0 {
		return ""
	}
	return strings.Join(exports, "; ") + "; "
}

func isShellEnvName(value string) bool {
	if value == "" {
		return false
	}
	for index, r := range value {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if index > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func buildWSLInstallSteps(distro string) []wslInstallStep {
	return buildWSLInstallStepsFor(distro, defaultWSLDistro)
}

func buildWSLInstallStepsFor(distro string, distribution string) []wslInstallStep {
	if strings.TrimSpace(distro) == "" {
		distro = defaultWSLDistro
	}
	if strings.TrimSpace(distribution) == "" {
		distribution = defaultWSLDistro
	}
	systemdCommand := "printf '[boot]\\nsystemd=true\\n' > /etc/wsl.conf"
	dockerAptCommand := strings.Join([]string{
		"set -e",
		dockerAptSourceCleanupCommand(),
		"apt-get update",
		"apt-get install -y ca-certificates curl gnupg",
		"install -m 0755 -d /etc/apt/keyrings",
		"rm -f /etc/apt/keyrings/docker.gpg",
		"curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg",
		"chmod a+r /etc/apt/keyrings/docker.gpg",
		dockerAptSourceWriteCommand(),
		"apt-get update",
		"apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin",
		"if ! docker system dial-stdio --help >/dev/null 2>&1; then apt-get install -y socat; fi",
	}, " && ")
	nvidiaToolkitCommand := strings.Join([]string{
		"set -e",
		`if ! ` + wslNVIDIAGPUCheckCommand + `; then echo "No NVIDIA GPU is exposed to WSL; skipping NVIDIA Container Toolkit."; exit 0; fi`,
		`if ` + wslNVIDIARuntimeCheckCommand + `; then echo "NVIDIA container runtime is already configured."; exit 0; fi`,
		"apt-get update",
		"apt-get install -y --no-install-recommends ca-certificates curl gnupg",
		"rm -f /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg /etc/apt/sources.list.d/nvidia-container-toolkit.list",
		"curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg",
		`curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' > /etc/apt/sources.list.d/nvidia-container-toolkit.list`,
		"apt-get update",
		"apt-get install -y --no-install-recommends nvidia-container-toolkit",
		"nvidia-ctk runtime configure --runtime=docker",
		"systemctl restart docker",
		wslNVIDIARuntimeCheckCommand,
	}, " && ")
	groupCommand := `user="$(getent passwd 1000 | cut -d: -f1)"; if [ -z "$user" ]; then echo "No non-root WSL user was found for docker group membership." >&2; exit 1; fi; usermod -aG docker "$user"`
	verifyCommand := strings.Join([]string{
		"docker info >/dev/null",
		"docker compose version",
		"docker buildx version",
		"docker run --rm hello-world",
	}, " && ")
	return []wslInstallStep{
		{
			Message: "Enable WSL without installing a distribution",
			Timeout: 5 * time.Minute,
			Command: powerShellCommand(wslEnableScript()),
			RepairHints: []string{
				"Approve the Windows elevation prompt if it appears.",
				"Restart Windows if WSL reports that a reboot is required, then reopen Cairn and run the installer again.",
			},
		},
		{
			Message: "Install the selected Ubuntu WSL distribution",
			Timeout: 20 * time.Minute,
			Command: powerShellCommand(wslInstallDistroScript(distro, distribution)),
			RepairHints: []string{
				"Check network access to the Microsoft Store or use the WSL web-download option outside Cairn.",
				"If the distro setup window asks for a UNIX user, finish that setup and then retry the install plan.",
			},
		},
		{
			Message: "Ensure the selected distribution runs as WSL2",
			Timeout: 10 * time.Minute,
			Command: powerShellCommand(wslEnsureVersion2Script(distro)),
			RepairHints: []string{
				"Enable virtualization in firmware and make sure the Virtual Machine Platform Windows feature is installed.",
				"Restart Windows after changing WSL feature or virtualization settings.",
			},
		},
		{
			Message: "Enable systemd inside the selected distribution",
			Timeout: wslCommandTimeout,
			Command: []string{wslCommandName, "-d", distro, "-u", "root", "--", "sh", "-lc", systemdCommand},
			RepairHints: []string{
				"Initialize the Ubuntu distro if WSL reports that the distribution is not ready.",
				"Retry after closing all shells that are currently using the distro.",
			},
		},
		{
			Message: "Restart WSL so systemd configuration takes effect",
			Timeout: wslCommandTimeout,
			Command: []string{wslCommandName, "--terminate", distro},
			RepairHints: []string{
				"Close running shells or terminals attached to the selected WSL distro, then retry.",
			},
		},
		{
			Message: "Install or upgrade Docker Engine, containerd, Compose, and Buildx from Docker's apt repository",
			Timeout: wslInstallTimeout,
			Command: []string{wslCommandName, "-d", distro, "-u", "root", "--", "sh", "-lc", escapeWSLCommandDollars(dockerAptCommand)},
			RepairHints: []string{
				"Check internet access from inside WSL and retry.",
				"If apt is locked, wait for the other package operation to finish and retry.",
			},
		},
		{
			Message: "Add the WSL user to the docker group",
			Timeout: wslCommandTimeout,
			Command: []string{wslCommandName, "-d", distro, "-u", "root", "--", "sh", "-lc", escapeWSLCommandDollars(groupCommand)},
			RepairHints: []string{
				"Finish the Ubuntu first-run user setup, then retry.",
				"Restart WSL after the group change if Docker access is still denied.",
			},
		},
		{
			Message: "Restart the selected WSL distro so docker group membership applies",
			Timeout: wslCommandTimeout,
			Command: []string{wslCommandName, "--terminate", distro},
			RepairHints: []string{
				"Close running shells or terminals attached to the selected WSL distro, then retry.",
			},
		},
		{
			Message: "Enable and start the Docker service",
			Timeout: wslServiceTimeout,
			Command: []string{wslCommandName, "-d", distro, "-u", "root", "--", "sh", "-lc", "systemctl enable --now docker"},
			RepairHints: []string{
				"Make sure systemd is enabled in /etc/wsl.conf, terminate the distro, and retry.",
			},
		},
		{
			Message: "Install NVIDIA Container Toolkit when a WSL GPU is available",
			Timeout: wslInstallTimeout,
			Command: []string{wslCommandName, "-d", distro, "-u", "root", "--", "sh", "-lc", escapeWSLCommandDollars(nvidiaToolkitCommand)},
			RepairHints: []string{
				"Install or update the Windows NVIDIA driver; do not install Linux NVIDIA display drivers inside WSL.",
				"Check internet access to nvidia.github.io from inside WSL and retry.",
				"Run `nvidia-smi` inside WSL to confirm the GPU is exposed before retrying.",
			},
		},
		{
			Message: "Verify Docker Engine, Compose, Buildx, and hello-world",
			Timeout: 5 * time.Minute,
			Command: []string{wslCommandName, "-d", distro, "--", "sh", "-lc", verifyCommand},
			RepairHints: []string{
				"Restart the selected WSL distro so docker group membership applies, then retry verification.",
				"Run `docker info` inside the WSL distro to inspect daemon or permission errors.",
			},
		},
	}
}

func displayCommand(command []string) string {
	return shellJoin(command)
}

func powerShellCommand(script string) []string {
	return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script}
}

func wslEnableScript() string {
	return strings.Join([]string{
		`$ErrorActionPreference = 'Stop'`,
		`$wsl = (Get-Command wsl.exe -ErrorAction Stop).Source`,
		`& $wsl --status *> $null`,
		`if ($LASTEXITCODE -eq 0) { Write-Output 'WSL is already available.'; exit 0 }`,
		`& $wsl --install --no-distribution`,
		`exit $LASTEXITCODE`,
	}, "; ")
}

func wslInstallDistroScript(distro string, distribution string) string {
	name := psSingleQuote(distro)
	source := psSingleQuote(distribution)
	args := fmt.Sprintf(`$wslArgs = @('--install', %s)`, source)
	if !strings.EqualFold(strings.TrimSpace(distro), strings.TrimSpace(distribution)) {
		args += fmt.Sprintf(`; $wslArgs += @('--name', %s)`, name)
	}
	return strings.Join([]string{
		`$ErrorActionPreference = 'Stop'`,
		`$wsl = (Get-Command wsl.exe -ErrorAction Stop).Source`,
		fmt.Sprintf(`$name = %s`, name),
		`$distros = @(& $wsl -l -q 2>$null | ForEach-Object { ($_ -replace [char]0, '').Trim() } | Where-Object { $_ })`,
		`if ($distros -contains $name) { Write-Output "WSL distro '$name' is already installed."; exit 0 }`,
		args,
		`& $wsl @wslArgs`,
		`exit $LASTEXITCODE`,
	}, "; ")
}

func wslEnsureVersion2Script(distro string) string {
	name := psSingleQuote(distro)
	return strings.Join([]string{
		`$ErrorActionPreference = 'Stop'`,
		`$wsl = (Get-Command wsl.exe -ErrorAction Stop).Source`,
		fmt.Sprintf(`$name = %s`, name),
		`$found = $null`,
		`for ($i = 0; $i -lt 60; $i++) { $rows = @(& $wsl -l -v 2>$null | ForEach-Object { ($_ -replace [char]0, '').Trim() } | Where-Object { $_ }); foreach ($row in $rows) { $clean = ($row -replace '^\*\s*', '').Trim(); if ($clean -eq $name -or $clean -like "$name *") { $found = $clean; break } }; if ($found) { break }; Start-Sleep -Seconds 1 }`,
		`if (-not $found) { Write-Error "WSL distro '$name' was not found after installation."; exit 1 }`,
		`$columns = @($found -split '\s+' | Where-Object { $_ })`,
		`$versionText = if ($columns.Count -gt 0) { $columns[$columns.Count - 1] } else { '' }`,
		`if ($versionText -eq '2') { Write-Output "WSL distro '$name' is already version 2."; exit 0 }`,
		`& $wsl --set-version $name 2`,
		`exit $LASTEXITCODE`,
	}, "; ")
}

func psSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func escapeWSLCommandDollars(command string) string {
	return strings.ReplaceAll(command, "$", `\$`)
}

func sendInstallProgress(progress chan<- InstallProgress, step int, totalSteps int, message string, done bool) {
	if progress == nil {
		return
	}
	progress <- InstallProgress{Step: step, TotalSteps: totalSteps, Message: message, Done: done}
}
