package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sort"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

type ExecOptions struct {
	Cmd        []string
	User       string
	WorkingDir string
	Env        map[string]string
	TTY        bool
	Cols       int
	Rows       int
}

type ExecInspect struct {
	ID          string
	ContainerID string
	Running     bool
	ExitCode    int
	PID         int
}

type ExecSession struct {
	ID     string
	hijack dockertypes.HijackedResponse
}

func (s *ExecSession) Read(p []byte) (int, error) {
	if s == nil || s.hijack.Reader == nil {
		return 0, io.ErrClosedPipe
	}
	return s.hijack.Reader.Read(p)
}

func (s *ExecSession) Write(p []byte) (int, error) {
	if s == nil || s.hijack.Conn == nil {
		return 0, io.ErrClosedPipe
	}
	return s.hijack.Conn.Write(p)
}

func (s *ExecSession) Close() error {
	if s == nil || s.hijack.Conn == nil {
		return nil
	}
	s.hijack.Close()
	return nil
}

func (c *Client) OpenContainerExec(ctx context.Context, containerID string, opts ExecOptions) (*ExecSession, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := api.ContainerExecCreate(callCtx, containerID, container.ExecOptions{
		User:         opts.User,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          opts.TTY,
		Cmd:          opts.Cmd,
		Env:          envSlice(opts.Env),
		WorkingDir:   opts.WorkingDir,
		ConsoleSize:  consoleSize(opts.Cols, opts.Rows),
	})
	if err != nil {
		return nil, mapDockerError("create container exec", err)
	}

	attachCtx := context.WithoutCancel(ctx)
	hijack, err := api.ContainerExecAttach(attachCtx, resp.ID, container.ExecAttachOptions{
		Tty:         opts.TTY,
		ConsoleSize: consoleSize(opts.Cols, opts.Rows),
	})
	if err != nil {
		return nil, mapDockerError("attach container exec", err)
	}
	return &ExecSession{ID: resp.ID, hijack: hijack}, nil
}

func (c *Client) RunContainerExec(ctx context.Context, containerID string, opts ExecOptions) (string, int, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", -1, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := api.ContainerExecCreate(callCtx, containerID, container.ExecOptions{
		User:         opts.User,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          opts.TTY,
		Cmd:          opts.Cmd,
		Env:          envSlice(opts.Env),
		WorkingDir:   opts.WorkingDir,
	})
	if err != nil {
		return "", -1, mapDockerError("create container exec", err)
	}
	hijack, err := api.ContainerExecAttach(callCtx, resp.ID, container.ExecAttachOptions{Tty: opts.TTY})
	if err != nil {
		return "", -1, mapDockerError("attach container exec", err)
	}
	defer func() {
		hijack.Close()
	}()

	var out bytes.Buffer
	if opts.TTY {
		_, err = io.Copy(&out, hijack.Reader)
	} else {
		_, err = stdcopy.StdCopy(&out, &out, hijack.Reader)
	}
	if err != nil && !isExpectedExecClose(err) {
		return out.String(), -1, mapDockerError("read container exec", err)
	}
	inspect, err := api.ContainerExecInspect(callCtx, resp.ID)
	if err != nil {
		return out.String(), -1, mapDockerError("inspect container exec", err)
	}
	return out.String(), inspect.ExitCode, nil
}

func isExpectedExecClose(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed)
}

func (c *Client) ResizeContainerExec(ctx context.Context, execID string, cols int, rows int) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := api.ContainerExecResize(callCtx, execID, container.ResizeOptions{Width: uint(cols), Height: uint(rows)}); err != nil {
		return mapDockerError("resize container exec", err)
	}
	return nil
}

func (c *Client) InspectContainerExec(ctx context.Context, execID string) (*ExecInspect, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	inspect, err := api.ContainerExecInspect(callCtx, execID)
	if err != nil {
		return nil, mapDockerError("inspect container exec", err)
	}
	return &ExecInspect{
		ID:          inspect.ExecID,
		ContainerID: inspect.ContainerID,
		Running:     inspect.Running,
		ExitCode:    inspect.ExitCode,
		PID:         inspect.Pid,
	}, nil
}

func (c *Client) DetectContainerShells(ctx context.Context, containerID string) ([]string, error) {
	raw, _, err := c.inspectContainer(ctx, containerID, false)
	if err != nil {
		return nil, err
	}
	imageID := raw.Image
	if imageID == "" && raw.Config != nil {
		imageID = raw.Config.Image
	}
	if cached, ok := c.cachedShells(imageID); ok {
		return cached, nil
	}

	candidates := []string{"/bin/bash", "/bin/sh", "/bin/ash", "/bin/zsh", "/usr/bin/bash", "/busybox/sh"}
	shells := make([]string, 0, len(candidates))
	for _, shell := range candidates {
		_, code, err := c.RunContainerExec(ctx, containerID, ExecOptions{Cmd: []string{shell, "-c", "exit 0"}})
		if err == nil && code == 0 {
			shells = append(shells, shell)
		}
	}
	if len(shells) == 0 {
		return nil, apperror.New(
			apperror.NotFound,
			"No interactive shell was found in this container",
			apperror.WithDetail("Tried /bin/bash, /bin/sh, /bin/ash, /bin/zsh, /usr/bin/bash, and /busybox/sh."),
			apperror.WithRepairHints("Use logs or exec a known binary for shell-less images."),
		)
	}
	c.setCachedShells(imageID, shells)
	return shells, nil
}

func (c *Client) cachedShells(imageID string) ([]string, bool) {
	if imageID == "" {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.shellCache == nil {
		return nil, false
	}
	shells, ok := c.shellCache[imageID]
	if !ok {
		return nil, false
	}
	return append([]string(nil), shells...), true
}

func (c *Client) setCachedShells(imageID string, shells []string) {
	if imageID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shellCache == nil {
		c.shellCache = map[string][]string{}
	}
	c.shellCache[imageID] = append([]string(nil), shells...)
}

func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func consoleSize(cols int, rows int) *[2]uint {
	if cols <= 0 && rows <= 0 {
		return nil
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 30
	}
	return &[2]uint{uint(rows), uint(cols)}
}
