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
	wslInstallTimeout     = 15 * time.Minute
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
			"Install WSL 2 with an Ubuntu distribution, then reopen Cairn.",
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
			"Install Ubuntu on WSL 2, or select a Docker-capable Ubuntu distro in Settings.",
			true,
		))
		return status, nil
	}

	selected, ok := selectWSLDistro(distros, p.distro)
	if !ok {
		status.Problems = append(status.Problems, providerProblem(
			ProblemUbuntuMissing,
			fmt.Sprintf("Configured WSL distro %q was not found.", p.configuredDistro()),
			"Select an installed Ubuntu WSL 2 distro in Settings.",
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

	if release, ok := p.runWSLText(ctx, selected.Name, "sh", "-lc", "cat /etc/os-release"); !ok || !isUbuntuOSRelease(release) {
		status.Problems = append(status.Problems, providerProblem(
			ProblemUbuntuMissing,
			fmt.Sprintf("WSL distro %q is not an Ubuntu-family distro.", selected.Name),
			"Install Ubuntu on WSL 2, or select an Ubuntu-family distro in Settings.",
			true,
		))
		return status, nil
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
			"Install Docker Engine and the docker CLI inside the selected Ubuntu WSL distro.",
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
			"Install the docker-compose-plugin package inside Ubuntu.",
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
			"Install the docker-buildx-plugin package inside Ubuntu.",
			true,
		))
	}
	if dockerVersion, ok := p.runWSLText(ctx, selected.Name, "docker", "info", "--format", "{{.ServerVersion}}"); ok {
		status.DockerRunning = true
		status.DockerVersion = normalizeDockerVersion(dockerVersion)
		status.DockerHost = "wsl+stdio://" + selected.Name
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerDown,
			"Docker daemon is not reachable inside the selected WSL distro.",
			"Start Docker Engine inside Ubuntu with `sudo systemctl start docker`.",
			true,
		))
	}

	status.Installed = status.DockerInstalled && status.ComposeInstalled && status.BuildxInstalled
	status.Running = status.DockerRunning
	status.Healthy = status.Installed && status.Running && !hasBlockingProblem(status.Problems)
	return status, nil
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
		Title:    "Install Docker Engine in Ubuntu on WSL",
		Risk:     models.RiskNeedsConfirmation,
		Commands: commands,
		Effects: []string{
			"Enable or verify WSL and the selected Ubuntu distro.",
			"Enable systemd in the selected distro and restart WSL if needed.",
			"Install Docker Engine, containerd, Compose, and Buildx from Docker's official apt repository.",
			"Add the WSL user to the docker group; a WSL restart may be required before group membership applies.",
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
		detail := ""
		if result != nil {
			detail = strings.TrimSpace(result.Stderr)
			if detail == "" {
				detail = strings.TrimSpace(result.Stdout)
			}
		}
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

func (p *WindowsWSLProvider) RunDocker(ctx context.Context, args ...string) (*CommandResult, error) {
	return p.runWSL(ctx, p.configuredDistro(), append([]string{"docker"}, args...)...)
}

func (p *WindowsWSLProvider) RunCompose(ctx context.Context, workdir string, args ...string) (*CommandResult, error) {
	composeArgs := append([]string{"compose"}, args...)
	if strings.TrimSpace(workdir) == "" {
		return p.RunDocker(ctx, composeArgs...)
	}
	backendWorkdir, err := p.MapPathToBackend(workdir)
	if err != nil {
		return nil, err
	}
	shellCommand := "cd " + shellQuote(backendWorkdir) + " && exec docker " + shellJoin(composeArgs)
	result, err := p.runWSL(ctx, p.configuredDistro(), "sh", "-lc", shellCommand)
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
	return []string{wslCommandName, "-d", p.configuredDistro(), "--", shell}, nil
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

func (p *WindowsWSLProvider) runWSL(ctx context.Context, distro string, args ...string) (*CommandResult, error) {
	wslArgs := append([]string{"-d", distro, "--"}, args...)
	return p.runner.Run(ctx, wslCommandTimeout, wslCommandName, wslArgs...)
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
		"apt-get update",
		"apt-get install -y ca-certificates curl gnupg",
		"install -m 0755 -d /etc/apt/keyrings",
		"rm -f /etc/apt/keyrings/docker.gpg",
		"curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg",
		"chmod a+r /etc/apt/keyrings/docker.gpg",
		`. /etc/os-release`,
		`echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" > /etc/apt/sources.list.d/docker.list`,
		"apt-get update",
		"apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin",
		"if ! docker system dial-stdio --help >/dev/null 2>&1; then apt-get install -y socat; fi",
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
			Command: []string{wslCommandName, "--set-version", distro, "2"},
			RepairHints: []string{
				"Enable virtualization in firmware and make sure the Virtual Machine Platform Windows feature is installed.",
				"Restart Windows after changing WSL feature or virtualization settings.",
			},
		},
		{
			Message: "Enable systemd inside the selected distribution",
			Timeout: commandTimeout,
			Command: []string{wslCommandName, "-d", distro, "-u", "root", "--", "sh", "-lc", systemdCommand},
			RepairHints: []string{
				"Initialize the Ubuntu distro if WSL reports that the distribution is not ready.",
				"Retry after closing all shells that are currently using the distro.",
			},
		},
		{
			Message: "Restart WSL so systemd configuration takes effect",
			Timeout: commandTimeout,
			Command: []string{wslCommandName, "--terminate", distro},
			RepairHints: []string{
				"Close running shells or terminals attached to the selected WSL distro, then retry.",
			},
		},
		{
			Message: "Install Docker Engine, containerd, Compose, and Buildx from Docker's apt repository",
			Timeout: wslInstallTimeout,
			Command: []string{wslCommandName, "-d", distro, "-u", "root", "--", "sh", "-lc", dockerAptCommand},
			RepairHints: []string{
				"Check internet access from inside WSL and retry.",
				"If apt is locked, wait for the other package operation to finish and retry.",
			},
		},
		{
			Message: "Add the WSL user to the docker group",
			Timeout: commandTimeout,
			Command: []string{wslCommandName, "-d", distro, "-u", "root", "--", "sh", "-lc", groupCommand},
			RepairHints: []string{
				"Finish the Ubuntu first-run user setup, then retry.",
				"Restart WSL after the group change if Docker access is still denied.",
			},
		},
		{
			Message: "Restart the selected WSL distro so docker group membership applies",
			Timeout: commandTimeout,
			Command: []string{wslCommandName, "--terminate", distro},
			RepairHints: []string{
				"Close running shells or terminals attached to the selected WSL distro, then retry.",
			},
		},
		{
			Message: "Enable and start the Docker service",
			Timeout: commandTimeout,
			Command: []string{wslCommandName, "-d", distro, "-u", "root", "--", "sh", "-lc", "systemctl enable --now docker"},
			RepairHints: []string{
				"Make sure systemd is enabled in /etc/wsl.conf, terminate the distro, and retry.",
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

func psSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func sendInstallProgress(progress chan<- InstallProgress, step int, totalSteps int, message string, done bool) {
	if progress == nil {
		return
	}
	progress <- InstallProgress{Step: step, TotalSteps: totalSteps, Message: message, Done: done}
}
