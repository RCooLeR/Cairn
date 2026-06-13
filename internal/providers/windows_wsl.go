package providers

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	windowsWSLID          = "windows_wsl_ubuntu"
	windowsWSLDisplayName = "Windows WSL Ubuntu"
	defaultWSLDistro      = "Ubuntu"
	wslCommandName        = "wsl.exe"
	wslCommandTimeout     = 10 * commandTimeout
)

var windowsDrivePathPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]?`)

type WindowsWSLOptions struct {
	Distro string
	Runner CommandRunner
}

type WindowsWSLProvider struct {
	distro string
	runner CommandRunner
}

type wslDistro struct {
	Name    string
	State   string
	Version int
	Default bool
}

func NewWindowsWSL(opts WindowsWSLOptions) *WindowsWSLProvider {
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return &WindowsWSLProvider{
		distro: strings.TrimSpace(opts.Distro),
		runner: runner,
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

func (p *WindowsWSLProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return nil, apperror.New(apperror.ProviderNotReady, "WSL install plans land in Phase 5.2")
}

func (p *WindowsWSLProvider) ExecuteInstallStep(context.Context, string, int, chan<- InstallProgress) error {
	return apperror.New(apperror.ProviderNotReady, "WSL install execution lands in Phase 5.2")
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
	return "", apperror.New(apperror.ProviderNotReady, "WSL Docker API transport lands in Phase 5.3")
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
