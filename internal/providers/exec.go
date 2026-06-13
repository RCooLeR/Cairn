package providers

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, timeout time.Duration, name string, args ...string) (*CommandResult, error)
}

type CommandRunOptions struct {
	Timeout time.Duration
	Workdir string
	Env     []string
}

type OptionsCommandRunner interface {
	RunWithOptions(ctx context.Context, opts CommandRunOptions, name string, args ...string) (*CommandResult, error)
}

type ExecRunner struct{}

func (ExecRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (ExecRunner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (*CommandResult, error) {
	return ExecRunner{}.RunWithOptions(ctx, CommandRunOptions{Timeout: timeout}, name, args...)
}

func (ExecRunner) RunWithOptions(ctx context.Context, opts CommandRunOptions, name string, args ...string) (*CommandResult, error) {
	runCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	started := time.Now()
	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Dir = opts.Workdir
	if len(opts.Env) > 0 {
		cmd.Env = mergeEnv(os.Environ(), opts.Env)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &CommandResult{
		Command:  append([]string{name}, args...),
		Workdir:  opts.Workdir,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: time.Since(started),
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, err
}

func mergeEnv(base []string, overrides []string) []string {
	if len(overrides) == 0 {
		return append([]string(nil), base...)
	}
	index := make(map[string]int, len(base))
	merged := append([]string(nil), base...)
	for i, entry := range merged {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			index[key] = i
		}
	}
	for _, entry := range overrides {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if i, exists := index[key]; exists {
			merged[i] = entry
		} else {
			index[key] = len(merged)
			merged = append(merged, entry)
		}
	}
	return merged
}
