package compose

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

func (c *Client) Version(ctx context.Context) (*Version, error) {
	result, err := c.run(ctx, "", nil, "version", "--format", "json")
	if commandFailed(result, err) {
		return nil, composeCommandError(apperror.ComposeNotFound, "Docker Compose v2 plugin was not found", result, err)
	}
	version, err := ParseVersionJSON(result.Stdout)
	if err != nil {
		return nil, apperror.Wrap(apperror.ComposeInvalid, "Parse Docker Compose version failed", err, apperror.WithDetail(result.Stdout))
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
	projects, err := ParseProjectsJSON(result.Stdout)
	if err != nil {
		return nil, apperror.Wrap(apperror.ComposeInvalid, "Parse Compose project list failed", err, apperror.WithDetail(result.Stdout))
	}
	return projects, nil
}

func (c *Client) Ps(ctx context.Context, opts ProjectOptions) ([]models.ComposeServiceStatus, error) {
	opts = c.backendProjectOptions(opts)
	args := append(projectArgs(opts), "ps", "--format", "json", "--all")
	result, err := c.run(ctx, opts.Workdir, projectEnv(opts), args...)
	if commandFailed(result, err) {
		return nil, composeCommandError(apperror.ComposeInvalid, "List Compose service status failed", result, err)
	}
	containers, err := ParsePSJSON(result.Stdout)
	if err != nil {
		return nil, apperror.Wrap(apperror.ComposeInvalid, "Parse Compose service status failed", err, apperror.WithDetail(result.Stdout))
	}
	return ServiceStatuses(containers), nil
}

func (c *Client) Config(ctx context.Context, opts ProjectOptions) (*ConfigResult, error) {
	opts = c.backendProjectOptions(opts)
	args := append(projectArgs(opts), "config")
	result, err := c.run(ctx, opts.Workdir, projectEnv(opts), args...)
	if commandFailed(result, err) {
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
	config, err := ParseConfigYAML(result.Stdout)
	if err != nil {
		config.API = models.ComposeConfigResult{
			ResolvedYAML: result.Stdout,
			Valid:        false,
			Errors:       append([]string(nil), config.Errors...),
		}
		return config, apperror.Wrap(apperror.ComposeInvalid, "Parse Compose config failed", err, apperror.WithDetail(result.Stdout))
	}
	return config, nil
}

func (c *Client) Start(ctx context.Context, opts ProjectOptions) (*providers.CommandResult, error) {
	return c.runProjectCommand(ctx, opts, "start")
}

func (c *Client) StartServices(ctx context.Context, opts ProjectOptions, services []string) (*providers.CommandResult, error) {
	args := append([]string{"start"}, nonEmptyServices(services)...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Stop(ctx context.Context, opts ProjectOptions) (*providers.CommandResult, error) {
	return c.runProjectCommand(ctx, opts, "stop")
}

func (c *Client) StopServices(ctx context.Context, opts ProjectOptions, services []string) (*providers.CommandResult, error) {
	args := append([]string{"stop"}, nonEmptyServices(services)...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Restart(ctx context.Context, opts ProjectOptions) (*providers.CommandResult, error) {
	return c.runProjectCommand(ctx, opts, "restart")
}

func (c *Client) RestartServices(ctx context.Context, opts ProjectOptions, services []string) (*providers.CommandResult, error) {
	args := append([]string{"restart"}, nonEmptyServices(services)...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Pull(ctx context.Context, opts ProjectOptions) (*providers.CommandResult, error) {
	return c.PullServices(ctx, opts, nil)
}

func (c *Client) PullServices(ctx context.Context, opts ProjectOptions, services []string) (*providers.CommandResult, error) {
	args := append([]string{"pull"}, nonEmptyServices(services)...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Build(ctx context.Context, opts ProjectOptions, build BuildOptions) (*providers.CommandResult, error) {
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
	args = append(args, build.Services...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) Up(ctx context.Context, opts ProjectOptions, forceRecreate bool) (*providers.CommandResult, error) {
	return c.UpServices(ctx, opts, UpOptions{ForceRecreate: forceRecreate})
}

func (c *Client) UpServices(ctx context.Context, opts ProjectOptions, up UpOptions) (*providers.CommandResult, error) {
	args := []string{"up", "-d"}
	if up.ForceRecreate {
		args = append(args, "--force-recreate")
	}
	if up.NoBuild {
		args = append(args, "--no-build")
	}
	args = append(args, nonEmptyServices(up.Services)...)
	return c.runProjectCommand(ctx, opts, args...)
}

func (c *Client) ScaleService(ctx context.Context, opts ProjectOptions, service string, replicas int) (*providers.CommandResult, error) {
	service = strings.TrimSpace(service)
	if service == "" {
		return nil, apperror.New(apperror.Conflict, "Service name is required")
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
	opts = c.backendProjectOptions(opts)
	fullArgs := append(projectArgs(opts), args...)
	result, err := c.run(ctx, opts.Workdir, projectEnv(opts), fullArgs...)
	if commandFailed(result, err) {
		return result, composeCommandError(apperror.ComposeInvalid, "Compose project action failed", result, err)
	}
	return result, nil
}

func (c *Client) run(ctx context.Context, workdir string, env []string, args ...string) (*providers.CommandResult, error) {
	if c == nil || c.runner == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Compose runner is not ready")
	}
	if len(env) > 0 {
		if runner, ok := c.runner.(EnvRunner); ok {
			return runner.RunComposeEnv(ctx, workdir, env, args...)
		}
		return nil, apperror.New(apperror.Internal, "Compose runner does not support environment passthrough")
	}
	return c.runner.RunCompose(ctx, workdir, args...)
}

func (c *Client) backendProjectOptions(opts ProjectOptions) ProjectOptions {
	mapper, ok := c.runner.(PathMapper)
	if !ok || mapper == nil {
		return opts
	}
	if mapped, err := mapper.MapPathToBackend(opts.Workdir); err == nil && strings.TrimSpace(mapped) != "" {
		opts.Workdir = mapped
	}
	if len(opts.Files) > 0 {
		files := make([]string, 0, len(opts.Files))
		for _, file := range opts.Files {
			if mapped, err := mapper.MapPathToBackend(file); err == nil && strings.TrimSpace(mapped) != "" {
				files = append(files, mapped)
				continue
			}
			files = append(files, file)
		}
		opts.Files = files
	}
	return opts
}

func projectArgs(opts ProjectOptions) []string {
	args := make([]string, 0, len(opts.Files)*2+len(opts.Profiles)*2)
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

func nonEmptyServices(services []string) []string {
	result := make([]string, 0, len(services))
	for _, service := range services {
		service = strings.TrimSpace(service)
		if service != "" {
			result = append(result, service)
		}
	}
	return result
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
	detail := commandDetail(result, err)
	hints := []string{}
	if code == apperror.ComposeNotFound {
		hints = append(hints, "Install or upgrade the official Docker Compose v2 plugin.")
	}
	return apperror.New(code, message, apperror.WithDetail(detail), apperror.WithRepairHints(hints...))
}

func commandDetail(result *providers.CommandResult, err error) string {
	parts := []string{}
	if result != nil {
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			parts = append(parts, stderr)
		}
		if stdout := strings.TrimSpace(result.Stdout); stdout != "" {
			parts = append(parts, stdout)
		}
	}
	if err != nil {
		parts = append(parts, err.Error())
	}
	if len(parts) == 0 {
		return "docker compose exited without output"
	}
	return strings.Join(parts, "\n")
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
