package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

const existingContextIDPrefix = "ctx:"

type ExistingContextOptions struct {
	ContextName string
	Runner      CommandRunner
}

type ExistingContextProvider struct {
	contextName string
	runner      CommandRunner
}

func NewExistingContext(opts ExistingContextOptions) *ExistingContextProvider {
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return &ExistingContextProvider{
		contextName: strings.TrimSpace(opts.ContextName),
		runner:      runner,
	}
}

func ExistingContextProviderID(contextName string) string {
	return existingContextIDPrefix + strings.TrimSpace(contextName)
}

func (p *ExistingContextProvider) ID() string {
	return ExistingContextProviderID(p.configuredContext())
}

func (p *ExistingContextProvider) DisplayName() string {
	return "Docker context: " + p.configuredContext()
}

func (p *ExistingContextProvider) Type() string {
	return TypeExistingContext
}

func (p *ExistingContextProvider) Platform() string {
	return PlatformAny
}

func (p *ExistingContextProvider) Detect(ctx context.Context) (*models.ProviderStatus, error) {
	status := &models.ProviderStatus{}
	if _, err := p.runner.LookPath("docker"); err != nil {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerMissing,
			"Docker CLI is not installed or not on PATH.",
			"Install Docker CLI or choose a platform-managed provider.",
			true,
		))
		return status, nil
	}
	status.DockerInstalled = true

	contextInfo, ok := p.findContext(ctx)
	if !ok {
		status.Problems = append(status.Problems, providerProblem(
			ProblemContextMissing,
			fmt.Sprintf("Docker context %q was not found.", p.configuredContext()),
			"Create the Docker context outside Cairn or choose a different context.",
			true,
		))
		return status, nil
	}
	status.CurrentContext = contextInfo.Name
	status.DockerHost = contextInfo.DockerHost
	if isUnencryptedTCPHost(contextInfo.DockerHost) {
		status.Warnings = append(status.Warnings, models.ProviderWarning{
			Code:    WarningUnencryptedTCP,
			Message: "This Docker context uses unencrypted tcp:// transport.",
		})
	}

	if composeVersion, ok := p.runDockerContextText(ctx, "compose", "version", "--short"); ok {
		status.ComposeInstalled = true
		status.ComposeVersion = normalizeDockerVersion(composeVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemComposeMissing,
			"Docker Compose is missing for the selected context.",
			"Install Docker Compose v2 for this Docker CLI.",
			true,
		))
	}
	if buildxVersion, ok := p.runDockerContextText(ctx, "buildx", "version"); ok {
		status.BuildxInstalled = true
		status.BackendVersion = normalizeDockerVersion(buildxVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemBuildxMissing,
			"Docker Buildx is missing for the selected context.",
			"Install or update Docker Buildx for this Docker CLI.",
			true,
		))
	}
	if dockerVersion, ok := p.runDockerContextText(ctx, "info", "--format", "{{.ServerVersion}}"); ok {
		status.DockerRunning = true
		status.DockerVersion = normalizeDockerVersion(dockerVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerDown,
			"Docker daemon ping through the selected context failed.",
			"Start the backend for this context or choose a reachable context.",
			true,
		))
	}

	status.Installed = status.DockerInstalled && status.ComposeInstalled && status.BuildxInstalled
	status.Running = status.DockerRunning
	status.Healthy = status.Installed && status.Running && !hasBlockingProblem(status.Problems)
	return status, nil
}

func (p *ExistingContextProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return nil, apperror.New(apperror.ProviderNotReady, "Existing Docker contexts are managed outside Cairn")
}

func (p *ExistingContextProvider) ExecuteInstallStep(context.Context, string, int, chan<- InstallProgress) error {
	return apperror.New(apperror.ProviderNotReady, "Existing Docker contexts are managed outside Cairn")
}

func (p *ExistingContextProvider) Start(context.Context) error {
	return apperror.New(apperror.ProviderNotReady, "Cairn cannot start an existing Docker context")
}

func (p *ExistingContextProvider) Stop(context.Context) error {
	return apperror.New(apperror.ProviderNotReady, "Cairn cannot stop an existing Docker context")
}

func (p *ExistingContextProvider) Restart(context.Context) error {
	return apperror.New(apperror.ProviderNotReady, "Cairn cannot restart an existing Docker context")
}

func (p *ExistingContextProvider) DockerHost(ctx context.Context) (string, error) {
	contextInfo, ok := p.findContext(ctx)
	if !ok || contextInfo.DockerHost == "" {
		return "", apperror.New(apperror.ProviderNotReady, "Docker context host is not available")
	}
	return contextInfo.DockerHost, nil
}

func (p *ExistingContextProvider) DockerContext(context.Context) (string, error) {
	return p.configuredContext(), nil
}

func (p *ExistingContextProvider) RunDocker(ctx context.Context, args ...string) (*CommandResult, error) {
	dockerArgs := append([]string{"--context", p.configuredContext()}, args...)
	return p.runner.Run(ctx, dockerOperationTimeout, "docker", dockerArgs...)
}

func (p *ExistingContextProvider) RunDockerWithInput(ctx context.Context, input string, args ...string) (*CommandResult, error) {
	dockerArgs := append([]string{"--context", p.configuredContext()}, args...)
	if runner, ok := p.runner.(OptionsCommandRunner); ok {
		return runner.RunWithOptions(ctx, CommandRunOptions{
			Timeout: dockerOperationTimeout,
			Stdin:   input,
		}, "docker", dockerArgs...)
	}
	return p.RunDocker(ctx, args...)
}

func (p *ExistingContextProvider) RunBackendCommand(ctx context.Context, input string, args ...string) (*CommandResult, error) {
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

func (p *ExistingContextProvider) RunCompose(ctx context.Context, workdir string, args ...string) (*CommandResult, error) {
	return p.RunComposeEnv(ctx, workdir, nil, args...)
}

func (p *ExistingContextProvider) RunComposeEnv(ctx context.Context, workdir string, env []string, args ...string) (*CommandResult, error) {
	composeArgs := append([]string{"--context", p.configuredContext(), "compose"}, args...)
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

func (p *ExistingContextProvider) HostShellCommand(opts models.TerminalOptions) ([]string, error) {
	shell := strings.TrimSpace(opts.Shell)
	if shell != "" {
		return []string{shell}, nil
	}
	switch runtime.GOOS {
	case "windows":
		if _, err := p.runner.LookPath("pwsh"); err == nil {
			return []string{"pwsh"}, nil
		}
		return []string{"powershell.exe"}, nil
	case "darwin":
		return []string{"/bin/zsh"}, nil
	default:
		if envShell := os.Getenv("SHELL"); envShell != "" {
			return []string{envShell}, nil
		}
		return []string{"/bin/sh"}, nil
	}
}

func (p *ExistingContextProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, apperror.New(apperror.ProviderNotReady, "Backend shell is not available for generic existing Docker contexts")
}

func (p *ExistingContextProvider) MapPathToBackend(hostPath string) (string, error) {
	value := strings.TrimSpace(hostPath)
	if value == "" {
		return "", errors.New("path is empty")
	}
	return value, nil
}

func (p *ExistingContextProvider) MapPathToHost(backendPath string) (string, error) {
	value := strings.TrimSpace(backendPath)
	if value == "" {
		return "", errors.New("path is empty")
	}
	return value, nil
}

func (p *ExistingContextProvider) configuredContext() string {
	if strings.TrimSpace(p.contextName) != "" {
		return strings.TrimSpace(p.contextName)
	}
	return "default"
}

func (p *ExistingContextProvider) findContext(ctx context.Context) (models.DockerContextInfo, bool) {
	contexts, ok := listDockerContexts(ctx, p.runner)
	if !ok {
		return models.DockerContextInfo{}, false
	}
	for _, contextInfo := range contexts {
		if contextInfo.Name == p.configuredContext() {
			return contextInfo, true
		}
	}
	return models.DockerContextInfo{}, false
}

func (p *ExistingContextProvider) runDockerContextText(ctx context.Context, args ...string) (string, bool) {
	dockerArgs := append([]string{"--context", p.configuredContext()}, args...)
	result, err := p.runner.Run(ctx, commandTimeout, "docker", dockerArgs...)
	if err != nil || result == nil || result.ExitCode != 0 {
		return "", false
	}
	return strings.TrimSpace(result.Stdout), true
}

func listDockerContexts(ctx context.Context, runner CommandRunner) ([]models.DockerContextInfo, bool) {
	if runner == nil {
		runner = ExecRunner{}
	}
	result, err := runner.Run(ctx, commandTimeout, "docker", "context", "ls", "--format", "json")
	if err != nil || result == nil || result.ExitCode != 0 {
		return nil, false
	}
	contexts, err := parseDockerContextList(result.Stdout)
	return contexts, err == nil
}

func isUnencryptedTCPHost(host string) bool {
	normalized := strings.ToLower(strings.TrimSpace(host))
	return strings.HasPrefix(normalized, "tcp://")
}
