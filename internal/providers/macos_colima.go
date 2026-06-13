package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/security"
)

const (
	macOSColimaID          = "macos_colima"
	macOSColimaDisplayName = "macOS Colima"
	defaultColimaProfile   = "default"
	colimaCommandName      = "colima"
	brewCommandName        = "brew"
	colimaInstallTimeout   = 20 * time.Minute
)

type MacOSColimaOptions struct {
	Profile  string
	CPU      int
	MemoryGB int
	DiskGB   int
	Runner   CommandRunner
	HomeDir  string
}

type MacOSColimaProvider struct {
	profile  string
	cpu      int
	memoryGB int
	diskGB   int
	runner   CommandRunner
	homeDir  string

	installMu    sync.Mutex
	installPlans map[string]colimaInstallPlan
}

type colimaInstallPlan struct {
	Steps []colimaInstallStep
}

type colimaInstallStep struct {
	Message     string
	Timeout     time.Duration
	Command     []string
	RepairHints []string
}

type colimaStatusInfo struct {
	Status  string   `json:"status"`
	Runtime string   `json:"runtime"`
	Socket  string   `json:"socket"`
	Profile string   `json:"profile"`
	CPUs    int      `json:"cpus"`
	CPU     int      `json:"cpu"`
	Memory  int      `json:"memory"`
	Disk    int      `json:"disk"`
	Mounts  []string `json:"mounts"`
}

func NewMacOSColima(opts MacOSColimaOptions) *MacOSColimaProvider {
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return &MacOSColimaProvider{
		profile:      strings.TrimSpace(opts.Profile),
		cpu:          opts.CPU,
		memoryGB:     opts.MemoryGB,
		diskGB:       opts.DiskGB,
		runner:       runner,
		homeDir:      strings.TrimSpace(opts.HomeDir),
		installPlans: map[string]colimaInstallPlan{},
	}
}

func (p *MacOSColimaProvider) SetColimaConfig(profile string, cpu, memoryGB, diskGB int) {
	p.profile = strings.TrimSpace(profile)
	if cpu > 0 {
		p.cpu = cpu
	}
	if memoryGB > 0 {
		p.memoryGB = memoryGB
	}
	if diskGB > 0 {
		p.diskGB = diskGB
	}
}

func (p *MacOSColimaProvider) ID() string {
	return macOSColimaID
}

func (p *MacOSColimaProvider) DisplayName() string {
	return macOSColimaDisplayName
}

func (p *MacOSColimaProvider) Type() string {
	return TypeMacOSColima
}

func (p *MacOSColimaProvider) Platform() string {
	return PlatformMacOS
}

func (p *MacOSColimaProvider) Detect(ctx context.Context) (*models.ProviderStatus, error) {
	status := &models.ProviderStatus{}
	if _, err := p.runner.LookPath(brewCommandName); err != nil {
		status.Warnings = append(status.Warnings, models.ProviderWarning{
			Code:    WarningBrewMissing,
			Message: "Homebrew was not found; Cairn can use an existing Colima setup but cannot install packages automatically.",
		})
	}

	if _, err := p.runner.LookPath("docker"); err != nil {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerMissing,
			"Docker CLI is not installed or not on PATH.",
			"Install the Docker CLI with Homebrew, then rerun provider detection.",
			true,
		))
		return status, nil
	}
	status.DockerInstalled = true

	if composeVersion, ok := p.detectComposeVersion(ctx); ok {
		status.ComposeInstalled = true
		status.ComposeVersion = normalizeDockerVersion(composeVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemComposeMissing,
			"Docker Compose is missing.",
			"Install Docker Compose v2 with Homebrew.",
			true,
		))
	}

	if buildxVersion, ok := p.runText(ctx, "docker", "buildx", "version"); ok {
		status.BuildxInstalled = true
		status.BackendVersion = normalizeDockerVersion(buildxVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemBuildxMissing,
			"Docker Buildx plugin is missing.",
			"Install Docker Buildx through Docker CLI packages.",
			true,
		))
	}

	if _, err := p.runner.LookPath(colimaCommandName); err != nil {
		status.Problems = append(status.Problems, providerProblem(
			ProblemColimaMissing,
			"Colima is not installed or not on PATH.",
			"Install Colima with Homebrew, then rerun provider detection.",
			true,
		))
		status.Installed = status.DockerInstalled && status.ComposeInstalled && status.BuildxInstalled
		return status, nil
	}

	if colimaVersion, ok := p.runText(ctx, colimaCommandName, "version"); ok {
		status.BackendVersion = "Colima " + normalizeDockerVersion(colimaVersion)
	}

	colimaStatus, colimaRunning := p.colimaStatus(ctx)
	if !colimaRunning {
		status.Problems = append(status.Problems, providerProblem(
			ProblemColimaStopped,
			fmt.Sprintf("Colima profile %q is not running.", p.configuredProfile()),
			"Start Colima from Settings or run `colima start`.",
			true,
		))
	}

	contextName := p.contextName()
	contexts, contextsOK := p.listDockerContexts(ctx)
	contextFound := false
	contextSelected := false
	for _, dockerContext := range contexts {
		if dockerContext.Name != contextName {
			continue
		}
		contextFound = true
		contextSelected = dockerContext.Current
		status.CurrentContext = dockerContext.Name
		status.DockerHost = dockerContext.DockerHost
		break
	}
	if status.DockerHost == "" && colimaStatus.Socket != "" {
		status.DockerHost = colimaStatus.Socket
	}
	if status.DockerHost == "" {
		status.DockerHost = defaultColimaSocket(p.homeDir, p.configuredProfile())
	}
	if !contextsOK || !contextFound {
		status.Problems = append(status.Problems, providerProblem(
			ProblemContextMissing,
			fmt.Sprintf("Docker context %q was not found.", contextName),
			"Start Colima and allow it to create the Docker context.",
			true,
		))
	} else if !contextSelected {
		status.Problems = append(status.Problems, providerProblem(
			ProblemContextNotSelected,
			fmt.Sprintf("Docker context %q is not selected.", contextName),
			fmt.Sprintf("Run `docker context use %s` or start the Colima provider from Cairn.", contextName),
			true,
		))
	}

	if colimaRunning && contextFound {
		if dockerVersion, ok := p.runDockerContextText(ctx, contextName, "info", "--format", "{{.ServerVersion}}"); ok {
			status.DockerRunning = true
			status.DockerVersion = normalizeDockerVersion(dockerVersion)
		} else {
			status.Problems = append(status.Problems, providerProblem(
				ProblemDockerDown,
				"Docker daemon ping through the Colima context failed.",
				"Restart Colima and verify `docker --context "+contextName+" info`.",
				true,
			))
		}
	}

	status.Installed = status.DockerInstalled && status.ComposeInstalled && status.BuildxInstalled && contextFound
	status.Running = colimaRunning && status.DockerRunning
	status.Healthy = status.Installed && status.Running && !hasBlockingProblem(status.Problems)
	return status, nil
}

func (p *MacOSColimaProvider) PlanInstall(_ context.Context, opts models.InstallOptions) (*models.CommandPlan, error) {
	if _, err := p.runner.LookPath(brewCommandName); err != nil {
		return nil, apperror.New(
			apperror.ProviderNotReady,
			"Homebrew is required before Cairn can install Colima packages",
			apperror.WithRepairHints(
				"Install Homebrew from https://brew.sh, then rerun setup.",
				`Command preview: /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`,
			),
		)
	}

	profile := strings.TrimSpace(opts.Extra["profile"])
	if profile == "" {
		profile = p.configuredProfile()
	}
	cpu := optionInt(opts.Extra, "cpu", p.configuredCPU())
	memoryGB := optionInt(opts.Extra, "memoryGB", p.configuredMemoryGB())
	diskGB := optionInt(opts.Extra, "diskGB", p.configuredDiskGB())
	steps := buildColimaInstallSteps(profile, cpu, memoryGB, diskGB)
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
		Title:    "Install and start Colima",
		Risk:     models.RiskNeedsConfirmation,
		Commands: commands,
		Effects: []string{
			"Install Docker CLI, Docker Compose, and Colima with Homebrew.",
			"Start the selected Colima profile with the configured CPU, memory, and disk limits.",
			"Select the Colima Docker context and verify Docker, Compose, Buildx, and hello-world.",
		},
		ExpiresAt: time.Now().UTC().Add(security.DefaultPlanTTL),
	}
	p.installMu.Lock()
	p.installPlans[planID] = colimaInstallPlan{Steps: steps}
	p.installMu.Unlock()
	return plan, nil
}

func (p *MacOSColimaProvider) ExecuteInstallStep(ctx context.Context, planID string, step int, progress chan<- InstallProgress) error {
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
		return apperror.Wrap(apperror.ProviderNotReady, "Colima install step failed", err, opts...)
	}
	if step == len(plan.Steps)-1 {
		p.installMu.Lock()
		delete(p.installPlans, planID)
		p.installMu.Unlock()
	}
	sendInstallProgress(progress, step+1, len(plan.Steps), "Done: "+installStep.Message, false)
	return nil
}

func (p *MacOSColimaProvider) Start(ctx context.Context) error {
	_, err := p.runner.Run(ctx, colimaInstallTimeout, colimaCommandName, p.colimaStartArgs()...)
	if err != nil {
		return err
	}
	_, err = p.runner.Run(ctx, commandTimeout, "docker", "context", "use", p.contextName())
	return err
}

func (p *MacOSColimaProvider) Stop(ctx context.Context) error {
	_, err := p.runner.Run(ctx, commandTimeout, colimaCommandName, "stop", "-p", p.configuredProfile())
	return err
}

func (p *MacOSColimaProvider) Restart(ctx context.Context) error {
	_, err := p.runner.Run(ctx, colimaInstallTimeout, colimaCommandName, "restart", "-p", p.configuredProfile())
	if err != nil {
		return err
	}
	_, err = p.runner.Run(ctx, commandTimeout, "docker", "context", "use", p.contextName())
	return err
}

func (p *MacOSColimaProvider) DockerHost(ctx context.Context) (string, error) {
	if host, ok := p.contextDockerHost(ctx, p.contextName()); ok {
		return host, nil
	}
	if status, ok := p.colimaStatus(ctx); ok && status.Socket != "" {
		return status.Socket, nil
	}
	return "", apperror.New(apperror.ProviderNotReady, "Colima Docker context host is not available")
}

func (p *MacOSColimaProvider) DockerContext(context.Context) (string, error) {
	return p.contextName(), nil
}

func (p *MacOSColimaProvider) RunDocker(ctx context.Context, args ...string) (*CommandResult, error) {
	dockerArgs := append([]string{"--context", p.contextName()}, args...)
	return p.runner.Run(ctx, commandTimeout, "docker", dockerArgs...)
}

func (p *MacOSColimaProvider) RunCompose(ctx context.Context, workdir string, args ...string) (*CommandResult, error) {
	return p.RunComposeEnv(ctx, workdir, nil, args...)
}

func (p *MacOSColimaProvider) RunComposeEnv(ctx context.Context, workdir string, env []string, args ...string) (*CommandResult, error) {
	name, prefix := p.composeCommand(ctx)
	composeArgs := append(prefix, args...)
	if runner, ok := p.runner.(OptionsCommandRunner); ok {
		return runner.RunWithOptions(ctx, CommandRunOptions{
			Timeout: composeCommandTimeout,
			Workdir: workdir,
			Env:     env,
		}, name, composeArgs...)
	}
	result, err := p.runner.Run(ctx, composeCommandTimeout, name, composeArgs...)
	if result != nil && workdir != "" {
		result.Workdir = workdir
	}
	return result, err
}

func (p *MacOSColimaProvider) HostShellCommand(opts models.TerminalOptions) ([]string, error) {
	shell := strings.TrimSpace(opts.Shell)
	if shell == "" {
		shell = "/bin/zsh"
	}
	return []string{shell}, nil
}

func (p *MacOSColimaProvider) BackendShellCommand(opts models.TerminalOptions) ([]string, error) {
	command := []string{colimaCommandName, "ssh", "-p", p.configuredProfile()}
	if shell := strings.TrimSpace(opts.Shell); shell != "" {
		command = append(command, "--", shell)
	}
	return command, nil
}

func (p *MacOSColimaProvider) MapPathToBackend(hostPath string) (string, error) {
	value := strings.TrimSpace(hostPath)
	if value == "" {
		return "", errors.New("path is empty")
	}
	return value, nil
}

func (p *MacOSColimaProvider) MapPathToHost(backendPath string) (string, error) {
	value := strings.TrimSpace(backendPath)
	if value == "" {
		return "", errors.New("path is empty")
	}
	return value, nil
}

func (p *MacOSColimaProvider) detectComposeVersion(ctx context.Context) (string, bool) {
	if version, ok := p.runText(ctx, "docker", "compose", "version", "--short"); ok {
		return version, true
	}
	if _, err := p.runner.LookPath("docker-compose"); err == nil {
		return p.runText(ctx, "docker-compose", "version", "--short")
	}
	return "", false
}

func (p *MacOSColimaProvider) composeCommand(ctx context.Context) (string, []string) {
	contextName := p.contextName()
	if _, ok := p.runText(ctx, "docker", "--context", contextName, "compose", "version", "--short"); ok {
		return "docker", []string{"--context", contextName, "compose"}
	}
	return "docker-compose", []string{"--context", contextName}
}

func (p *MacOSColimaProvider) runDockerContextText(ctx context.Context, contextName string, args ...string) (string, bool) {
	dockerArgs := append([]string{"--context", contextName}, args...)
	return p.runText(ctx, "docker", dockerArgs...)
}

func (p *MacOSColimaProvider) runText(ctx context.Context, name string, args ...string) (string, bool) {
	result, err := p.runner.Run(ctx, commandTimeout, name, args...)
	if err != nil || result == nil || result.ExitCode != 0 {
		return "", false
	}
	return strings.TrimSpace(result.Stdout), true
}

func (p *MacOSColimaProvider) listDockerContexts(ctx context.Context) ([]models.DockerContextInfo, bool) {
	output, ok := p.runText(ctx, "docker", "context", "ls", "--format", "json")
	if !ok {
		return nil, false
	}
	contexts, err := parseDockerContextList(output)
	return contexts, err == nil
}

func (p *MacOSColimaProvider) contextDockerHost(ctx context.Context, contextName string) (string, bool) {
	contexts, ok := p.listDockerContexts(ctx)
	if ok {
		for _, dockerContext := range contexts {
			if dockerContext.Name == contextName && dockerContext.DockerHost != "" {
				return dockerContext.DockerHost, true
			}
		}
	}
	output, ok := p.runText(ctx, "docker", "context", "inspect", contextName, "--format", "{{json .Endpoints.docker.Host}}")
	if !ok {
		return "", false
	}
	return parseDockerHostValue(output)
}

func (p *MacOSColimaProvider) colimaStatus(ctx context.Context) (colimaStatusInfo, bool) {
	output, ok := p.runText(ctx, colimaCommandName, "status", "-p", p.configuredProfile(), "--json")
	if !ok {
		return colimaStatusInfo{}, false
	}
	status, err := parseColimaStatusJSON(output)
	if err != nil {
		return colimaStatusInfo{}, false
	}
	return status, strings.EqualFold(status.Status, "running")
}

func (p *MacOSColimaProvider) configuredProfile() string {
	if strings.TrimSpace(p.profile) != "" {
		return strings.TrimSpace(p.profile)
	}
	return defaultColimaProfile
}

func (p *MacOSColimaProvider) configuredCPU() int {
	if p.cpu > 0 {
		return p.cpu
	}
	return 2
}

func (p *MacOSColimaProvider) configuredMemoryGB() int {
	if p.memoryGB > 0 {
		return p.memoryGB
	}
	return 4
}

func (p *MacOSColimaProvider) configuredDiskGB() int {
	if p.diskGB > 0 {
		return p.diskGB
	}
	return 60
}

func (p *MacOSColimaProvider) contextName() string {
	profile := p.configuredProfile()
	if profile == defaultColimaProfile {
		return "colima"
	}
	return "colima-" + profile
}

func (p *MacOSColimaProvider) colimaStartArgs() []string {
	return []string{
		"start",
		"-p", p.configuredProfile(),
		"--cpu", strconv.Itoa(p.configuredCPU()),
		"--memory", strconv.Itoa(p.configuredMemoryGB()),
		"--disk", strconv.Itoa(p.configuredDiskGB()),
	}
}

func buildColimaInstallSteps(profile string, cpu, memoryGB, diskGB int) []colimaInstallStep {
	contextName := colimaContextName(profile)
	return []colimaInstallStep{
		{
			Message:     "Install Docker CLI with Homebrew",
			Timeout:     colimaInstallTimeout,
			Command:     []string{brewCommandName, "install", "docker"},
			RepairHints: []string{"Verify Homebrew is installed and network access is available."},
		},
		{
			Message:     "Install Docker Compose with Homebrew",
			Timeout:     colimaInstallTimeout,
			Command:     []string{brewCommandName, "install", "docker-compose"},
			RepairHints: []string{"Verify the docker-compose formula is available on this Homebrew installation."},
		},
		{
			Message:     "Install Colima with Homebrew",
			Timeout:     colimaInstallTimeout,
			Command:     []string{brewCommandName, "install", "colima"},
			RepairHints: []string{"Verify Homebrew can install Colima on this macOS version."},
		},
		{
			Message: "Start the Colima profile",
			Timeout: colimaInstallTimeout,
			Command: []string{
				colimaCommandName,
				"start",
				"-p", profile,
				"--cpu", strconv.Itoa(cpu),
				"--memory", strconv.Itoa(memoryGB),
				"--disk", strconv.Itoa(diskGB),
			},
			RepairHints: []string{"Inspect `colima status` and free disk/memory resources before retrying."},
		},
		{
			Message:     "Select the Colima Docker context",
			Timeout:     commandTimeout,
			Command:     []string{"docker", "context", "use", contextName},
			RepairHints: []string{"Start Colima again if the context was not created."},
		},
		{
			Message:     "Verify Docker, Compose, and Buildx",
			Timeout:     commandTimeout,
			Command:     []string{"docker", "--context", contextName, "compose", "version", "--short"},
			RepairHints: []string{"Install Docker Compose v2 and retry setup."},
		},
		{
			Message:     "Verify Buildx",
			Timeout:     commandTimeout,
			Command:     []string{"docker", "--context", contextName, "buildx", "version"},
			RepairHints: []string{"Install or update Docker Buildx and retry setup."},
		},
		{
			Message:     "Run hello-world through Colima",
			Timeout:     colimaInstallTimeout,
			Command:     []string{"docker", "--context", contextName, "run", "--rm", "hello-world"},
			RepairHints: []string{"Verify the Colima daemon can pull images from Docker Hub."},
		},
	}
}

func colimaContextName(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" || profile == defaultColimaProfile {
		return "colima"
	}
	return "colima-" + profile
}

func optionInt(values map[string]string, key string, fallback int) int {
	if values == nil {
		return fallback
	}
	value, err := strconv.Atoi(strings.TrimSpace(values[key]))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func parseColimaStatusJSON(output string) (colimaStatusInfo, error) {
	var status colimaStatusInfo
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &status); err != nil {
		return colimaStatusInfo{}, err
	}
	if status.Status == "" {
		return colimaStatusInfo{}, errors.New("missing status")
	}
	if status.CPUs == 0 {
		status.CPUs = status.CPU
	}
	return status, nil
}

func parseDockerContextList(output string) ([]models.DockerContextInfo, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, errors.New("empty Docker context list")
	}
	var rawContexts []dockerContextJSON
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &rawContexts); err != nil {
			return nil, err
		}
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var raw dockerContextJSON
			if err := json.Unmarshal([]byte(line), &raw); err != nil {
				return nil, err
			}
			rawContexts = append(rawContexts, raw)
		}
	}
	contexts := make([]models.DockerContextInfo, 0, len(rawContexts))
	for _, raw := range rawContexts {
		if raw.Name == "" {
			continue
		}
		contexts = append(contexts, models.DockerContextInfo{
			Name:        raw.Name,
			Description: raw.Description,
			Current:     raw.Current.Bool(),
			DockerHost:  raw.DockerEndpoint,
		})
	}
	if len(contexts) == 0 {
		return nil, errors.New("no Docker contexts parsed")
	}
	return contexts, nil
}

type dockerContextJSON struct {
	Name           string  `json:"Name"`
	Description    string  `json:"Description"`
	DockerEndpoint string  `json:"DockerEndpoint"`
	Current        boolish `json:"Current"`
}

type boolish struct {
	value bool
}

func (b *boolish) UnmarshalJSON(data []byte) error {
	var boolValue bool
	if err := json.Unmarshal(data, &boolValue); err == nil {
		b.value = boolValue
		return nil
	}
	var stringValue string
	if err := json.Unmarshal(data, &stringValue); err != nil {
		return err
	}
	normalized := strings.TrimSpace(strings.ToLower(stringValue))
	b.value = normalized == "true" || normalized == "*" || normalized == "current"
	return nil
}

func (b boolish) Bool() bool {
	return b.value
}

func parseDockerHostValue(output string) (string, bool) {
	value := strings.TrimSpace(output)
	if value == "" || value == "null" {
		return "", false
	}
	var decoded string
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		value = decoded
	}
	return strings.TrimSpace(value), strings.TrimSpace(value) != ""
}

func defaultColimaSocket(homeDir, profile string) string {
	homeDir = strings.TrimRight(strings.TrimSpace(homeDir), "/")
	if homeDir == "" {
		if detected, err := os.UserHomeDir(); err == nil {
			homeDir = strings.TrimRight(detected, "/")
		}
	}
	if homeDir == "" {
		return ""
	}
	return "unix://" + homeDir + "/.colima/" + strings.TrimSpace(profile) + "/docker.sock"
}
