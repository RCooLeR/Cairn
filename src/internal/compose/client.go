package compose

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

const (
	commandDetailOutputLimit     = 6000
	maxComposeParseOutputBytes   = 64 << 10
	composeOutputTruncationToken = "...[Cairn truncated "
)

func (c *Client) Version(ctx context.Context) (*Version, error) {
	result, err := c.run(ctx, "", nil, "version", "--format", "json")
	if commandFailed(result, err) {
		return nil, composeCommandError(apperror.ComposeNotFound, "Docker Compose v2 plugin was not found", result, err)
	}
	if err := composeParseOutputError(result); err != nil {
		return nil, err
	}
	version, err := ParseVersionJSON(result.Stdout)
	if contextErr := composeContextError(ctx, nil); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, apperror.Wrap(apperror.ComposeInvalid, "Parse Docker Compose version failed", err, apperror.WithDetail(providers.SafeCommandDiagnostic(result.Stdout, commandDetailOutputLimit)))
	}
	if !VersionAtLeast(version.Version, MinimumVersion) {
		return nil, apperror.New(
			apperror.ComposeNotFound,
			"Docker Compose v2.20 or newer is required",
			apperror.WithDetail("Detected Docker Compose "+version.Version),
			apperror.WithRepairHints("Install or upgrade the official Docker Compose v2 plugin."),
		)
	}
	return version, nil
}

func (c *Client) Ls(ctx context.Context, opts ListOptions) ([]Project, error) {
	args := []string{"ls", "--format", "json"}
	if opts.All {
		args = append(args, "--all")
	}
	result, err := c.run(ctx, "", nil, args...)
	if commandFailed(result, err) {
		return nil, composeCommandError(apperror.ComposeInvalid, "List Compose projects failed", result, err)
	}
	if err := composeParseOutputError(result); err != nil {
		return nil, err
	}
	projects, err := ParseProjectsJSON(result.Stdout)
	if contextErr := composeContextError(ctx, nil); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, apperror.Wrap(apperror.ComposeInvalid, "Parse Compose project list failed", err, apperror.WithDetail(providers.SafeCommandDiagnostic(result.Stdout, commandDetailOutputLimit)))
	}
	return projects, nil
}

func (c *Client) Ps(ctx context.Context, opts ProjectOptions) ([]models.ComposeServiceStatus, error) {
	var err error
	opts, err = c.strictBackendProjectOptions(ctx, opts)
	if err != nil {
		return nil, err
	}
	args := append(projectArgs(opts), "ps", "--format", "json", "--all")
	result, err := c.run(ctx, opts.Workdir, projectEnv(opts), args...)
	if commandFailed(result, err) {
		return nil, composeCommandError(apperror.ComposeInvalid, "List Compose service status failed", result, err)
	}
	if err := composeParseOutputError(result); err != nil {
		return nil, err
	}
	containers, err := ParsePSJSON(result.Stdout)
	if contextErr := composeContextError(ctx, nil); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, apperror.Wrap(apperror.ComposeInvalid, "Parse Compose service status failed", err, apperror.WithDetail(providers.SafeCommandDiagnostic(result.Stdout, commandDetailOutputLimit)))
	}
	return ServiceStatuses(containers), nil
}

func (c *Client) Config(ctx context.Context, opts ProjectOptions) (*ConfigResult, error) {
	var err error
	opts, err = c.strictBackendProjectOptions(ctx, opts)
	if err != nil {
		return nil, err
	}
	return c.configWithBackendProjectOptions(ctx, opts)
}

func (c *Client) configWithBackendProjectOptions(ctx context.Context, opts ProjectOptions) (*ConfigResult, error) {
	return c.configWithBackendProjectOptionsFlags(ctx, opts)
}

func (c *Client) configWithBackendProjectOptionsFlags(ctx context.Context, opts ProjectOptions, configFlags ...string) (*ConfigResult, error) {
	args := append(projectArgs(opts), "config")
	args = append(args, configFlags...)
	result, err := c.run(ctx, opts.Workdir, projectEnv(opts), args...)
	if contextErr := composeContextError(ctx, err); contextErr != nil {
		return nil, contextErr
	}
	if commandFailed(result, err) {
		if result == nil {
			if _, ok := apperror.CodeOf(err); ok {
				return nil, err
			}
		}
		detail := commandDetail(result, err)
		config := &ConfigResult{
			Raw:    stdout(result),
			Valid:  false,
			Errors: []string{detail},
			API: models.ComposeConfigResult{
				ResolvedYAML: stdout(result),
				Valid:        false,
				Errors:       []string{detail},
			},
		}
		return config, apperror.New(apperror.ComposeInvalid, "Compose config validation failed", apperror.WithDetail(detail))
	}
	if err := composeParseOutputError(result); err != nil {
		return nil, err
	}
	config, err := ParseConfigYAML(result.Stdout)
	if contextErr := composeContextError(ctx, nil); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		config.API = models.ComposeConfigResult{
			ResolvedYAML: result.Stdout,
			Valid:        false,
			Errors:       append([]string(nil), config.Errors...),
		}
		return config, apperror.Wrap(apperror.ComposeInvalid, "Parse Compose config failed", err, apperror.WithDetail(providers.SafeCommandDiagnostic(result.Stdout, commandDetailOutputLimit)))
	}
	return config, nil
}

func (c *Client) Start(ctx context.Context, opts ProjectOptions) (*providers.CommandResult, error) {
	return c.runProjectCommand(ctx, opts, "start")
}

func (c *Client) StartServices(ctx context.Context, opts ProjectOptions, services []string) (*providers.CommandResult, error) {
	services, err := serviceOperands(services)
	if err != nil {
		return nil, err
	}
	args := append([]string{"start"}, services...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Stop(ctx context.Context, opts ProjectOptions) (*providers.CommandResult, error) {
	return c.runProjectCommand(ctx, opts, "stop")
}

func (c *Client) StopServices(ctx context.Context, opts ProjectOptions, services []string) (*providers.CommandResult, error) {
	services, err := serviceOperands(services)
	if err != nil {
		return nil, err
	}
	args := append([]string{"stop"}, services...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Restart(ctx context.Context, opts ProjectOptions) (*providers.CommandResult, error) {
	return c.runProjectCommand(ctx, opts, "restart")
}

func (c *Client) RestartServices(ctx context.Context, opts ProjectOptions, services []string) (*providers.CommandResult, error) {
	services, err := serviceOperands(services)
	if err != nil {
		return nil, err
	}
	args := append([]string{"restart"}, services...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Pull(ctx context.Context, opts ProjectOptions) (*providers.CommandResult, error) {
	return c.PullServices(ctx, opts, nil)
}

func (c *Client) PullServices(ctx context.Context, opts ProjectOptions, services []string) (*providers.CommandResult, error) {
	services, err := serviceOperands(services)
	if err != nil {
		return nil, err
	}
	args := append([]string{"pull"}, services...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Build(ctx context.Context, opts ProjectOptions, build BuildOptions) (*providers.CommandResult, error) {
	services, err := serviceOperands(build.Services)
	if err != nil {
		return nil, err
	}
	args := []string{"build"}
	if build.Pull {
		args = append(args, "--pull")
	}
	labelKeys := make([]string, 0, len(build.Labels))
	for key := range build.Labels {
		if strings.TrimSpace(key) != "" {
			labelKeys = append(labelKeys, key)
		}
	}
	sort.Strings(labelKeys)
	for _, key := range labelKeys {
		value := strings.TrimSpace(build.Labels[key])
		if value == "" {
			continue
		}
		args = append(args, "--label", key+"="+value)
	}
	args = append(args, services...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Up(ctx context.Context, opts ProjectOptions, forceRecreate bool) (*providers.CommandResult, error) {
	return c.UpServices(ctx, opts, UpOptions{ForceRecreate: forceRecreate})
}

func (c *Client) UpServices(ctx context.Context, opts ProjectOptions, up UpOptions) (*providers.CommandResult, error) {
	services, err := serviceOperands(up.Services)
	if err != nil {
		return nil, err
	}
	args := []string{"up", "-d"}
	if up.ForceRecreate {
		args = append(args, "--force-recreate")
	}
	if up.NoBuild {
		args = append(args, "--no-build")
	}
	args = append(args, services...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) ScaleService(ctx context.Context, opts ProjectOptions, service string, replicas int) (*providers.CommandResult, error) {
	service = strings.TrimSpace(service)
	if service == "" {
		return nil, apperror.New(apperror.Conflict, "Service name is required")
	}
	if strings.HasPrefix(service, "-") {
		return nil, invalidServiceOperandError()
	}
	if replicas < 0 {
		return nil, apperror.New(apperror.Conflict, "Replica count cannot be negative")
	}
	args := []string{"up", "-d", "--scale", fmt.Sprintf("%s=%d", service, replicas), service}
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Down(ctx context.Context, opts ProjectOptions, removeVolumes bool) (*providers.CommandResult, error) {
	args := []string{"down"}
	if removeVolumes {
		args = append(args, "--volumes")
	}
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) runProjectCommand(ctx context.Context, opts ProjectOptions, args ...string) (*providers.CommandResult, error) {
	var err error
	opts, err = c.strictBackendProjectOptions(ctx, opts)
	if err != nil {
		return nil, err
	}
	fullArgs := append(projectArgs(opts), args...)
	result, err := c.run(ctx, opts.Workdir, projectEnv(opts), fullArgs...)
	if commandFailed(result, err) {
		return result, composeCommandError(apperror.ComposeInvalid, "Compose project action failed", result, err)
	}
	return result, nil
}

func (c *Client) run(ctx context.Context, workdir string, env []string, args ...string) (*providers.CommandResult, error) {
	if err := composeContextError(ctx, nil); err != nil {
		return nil, err
	}
	if c == nil || c.runner == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Compose runner is not ready")
	}
	c.mu.RLock()
	runtimeScope := c.scope
	scopeProvider := c.scopeProvider
	c.mu.RUnlock()
	if runtimeScope.Valid() {
		currentScope, err := providers.ResolveRuntimeScope(ctx, scopeProvider)
		if err != nil {
			if contextErr := composeContextError(ctx, err); contextErr != nil {
				return nil, contextErr
			}
			return nil, err
		}
		if !currentScope.Equal(runtimeScope) {
			return nil, apperror.New(apperror.NotFound, "Compose runtime context changed; reconnect the provider")
		}
	}
	if len(env) > 0 {
		if runner, ok := c.runner.(EnvRunner); ok {
			result, err := runner.RunComposeEnv(ctx, workdir, env, args...)
			if contextErr := composeContextError(ctx, err); contextErr != nil {
				return result, contextErr
			}
			return result, err
		}
		return nil, apperror.New(apperror.Internal, "Compose runner does not support environment passthrough")
	}
	result, err := c.runner.RunCompose(ctx, workdir, args...)
	if contextErr := composeContextError(ctx, err); contextErr != nil {
		return result, contextErr
	}
	return result, err
}

func projectArgs(opts ProjectOptions) []string {
	args := make([]string, 0, len(opts.Files)*2+len(opts.InterpolationEnvFiles)*2+len(opts.Profiles)*2+2)
	if projectDirectory := strings.TrimSpace(opts.ProjectDirectory); projectDirectory != "" {
		args = append(args, "--project-directory", projectDirectory)
	}
	for _, file := range opts.InterpolationEnvFiles {
		if file = strings.TrimSpace(file); file != "" {
			args = append(args, "--env-file", file)
		}
	}
	for _, file := range opts.Files {
		file = strings.TrimSpace(file)
		if file != "" {
			args = append(args, "-f", file)
		}
	}
	for _, profile := range opts.Profiles {
		profile = strings.TrimSpace(profile)
		if profile != "" {
			args = append(args, "--profile", profile)
		}
	}
	return args
}

func serviceOperands(services []string) ([]string, error) {
	result := make([]string, 0, len(services))
	for _, service := range services {
		service = strings.TrimSpace(service)
		if service != "" {
			if strings.HasPrefix(service, "-") {
				return nil, invalidServiceOperandError()
			}
			result = append(result, service)
		}
	}
	return result, nil
}

func invalidServiceOperandError() error {
	return apperror.New(apperror.Conflict, "Service names cannot start with a hyphen")
}

func projectEnv(opts ProjectOptions) []string {
	env := append([]string(nil), opts.Env...)
	if strings.TrimSpace(opts.ProjectName) != "" {
		env = setEnv(env, "COMPOSE_PROJECT_NAME", strings.TrimSpace(opts.ProjectName))
	}
	return env
}

func setEnv(env []string, key string, value string) []string {
	entry := key + "=" + value
	for i, existing := range env {
		existingKey, _, ok := strings.Cut(existing, "=")
		if ok && existingKey == key {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func commandFailed(result *providers.CommandResult, err error) bool {
	if err != nil {
		return true
	}
	return result == nil || result.ExitCode != 0
}

func composeCommandError(code apperror.Code, message string, result *providers.CommandResult, err error) error {
	if contextErr := composeContextError(context.Background(), err); contextErr != nil {
		return contextErr
	}
	// Scope/provider failures happen before a Compose process is started and
	// must retain their authorization/readiness taxonomy.
	if result == nil {
		if _, ok := apperror.CodeOf(err); ok {
			return err
		}
	}
	detail := commandDetail(result, err)
	hints := []string{}
	if code == apperror.ComposeNotFound {
		hints = append(hints, "Install or upgrade the official Docker Compose v2 plugin.")
	}
	if composeNVIDIARuntimeUnavailable(detail) {
		message = "Compose project requires NVIDIA GPU runtime"
		hints = append(hints,
			"Install the NVIDIA driver on the host and the NVIDIA Container Toolkit in the active Docker backend, then restart Docker.",
			"On WSL, verify `nvidia-smi` works inside the selected distro and test Docker with `docker run --rm --gpus all nvidia/cuda:12.6.3-base-ubuntu24.04 nvidia-smi`.",
			"If GPU acceleration is optional, disable the service's `gpus` or NVIDIA device reservation in Compose and redeploy the CPU-only variant.",
		)
	}
	return apperror.New(code, message, apperror.WithDetail(detail), apperror.WithRepairHints(hints...))
}

func composeContextError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

func composeParseOutputError(result *providers.CommandResult) error {
	if result == nil {
		return apperror.New(apperror.ComposeInvalid, "Compose returned no output result")
	}
	if !result.StdoutTruncated && len(result.Stdout) <= maxComposeParseOutputBytes && !strings.Contains(result.Stdout, composeOutputTruncationToken) {
		return nil
	}
	return apperror.New(
		apperror.ComposeInvalid,
		"Compose output exceeded the safe processing limit",
		apperror.WithDetail("Reduce the resolved Compose configuration size and try again."),
	)
}

func composeNVIDIARuntimeUnavailable(detail string) bool {
	normalized := strings.ToLower(detail)
	return strings.Contains(normalized, `could not select device driver "nvidia"`) &&
		strings.Contains(normalized, "capabilities") &&
		strings.Contains(normalized, "gpu")
}

func commandDetail(result *providers.CommandResult, err error) string {
	parts := []string{}
	if result != nil {
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			parts = append(parts, trimCommandDetailPart(providers.RedactCommandDiagnostic(stderr)))
		}
		if stdout := strings.TrimSpace(result.Stdout); stdout != "" {
			parts = append(parts, trimCommandDetailPart(providers.RedactCommandDiagnostic(stdout)))
		}
	}
	if err != nil {
		parts = append(parts, trimCommandDetailPart(providers.RedactCommandDiagnostic(err.Error())))
	}
	if len(parts) == 0 {
		return "docker compose exited without output"
	}
	return strings.Join(parts, "\n")
}

func trimCommandDetailPart(value string) string {
	if len(value) <= commandDetailOutputLimit {
		return value
	}
	half := commandDetailOutputLimit / 2
	head := strings.TrimSpace(value[:half])
	tail := strings.TrimSpace(value[len(value)-half:])
	return head + "\n... command output truncated; open the action output for the full transcript ...\n" + tail
}

func stdout(result *providers.CommandResult) string {
	if result == nil {
		return ""
	}
	return result.Stdout
}

func (v Version) String() string {
	if v.GitCommit == "" {
		return v.Version
	}
	return fmt.Sprintf("%s (%s)", v.Version, v.GitCommit)
}
