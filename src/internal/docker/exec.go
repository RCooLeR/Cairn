package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sort"
	"strings"
	"sync"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/moby/moby/api/pkg/stdcopy"
	dockerclient "github.com/moby/moby/client"
)

const maxContainerExecOutput = 4 << 20

var errContainerExecOutputLimit = errors.New("container exec output exceeds capture limit")

type execOutputBuffer struct {
	buffer bytes.Buffer
}

func (b *execOutputBuffer) Write(data []byte) (int, error) {
	remaining := maxContainerExecOutput - b.buffer.Len()
	if len(data) > remaining {
		n, _ := b.buffer.Write(data[:remaining])
		return n, errContainerExecOutputLimit
	}
	return b.buffer.Write(data)
}

func (b *execOutputBuffer) String() string { return b.buffer.String() }

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
	ID           string
	hijack       dockerclient.HijackedResponse
	cancelAttach context.CancelFunc
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
	if s == nil {
		return nil
	}
	if s.cancelAttach != nil {
		s.cancelAttach()
	}
	if s.hijack.Conn != nil {
		s.hijack.Close()
	}
	return nil
}

func (c *Client) OpenContainerExec(ctx context.Context, containerID string, opts ExecOptions) (*ExecSession, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := api.ExecCreate(callCtx, containerID, dockerclient.ExecCreateOptions{
		User:         opts.User,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          opts.TTY,
		Cmd:          opts.Cmd,
		Env:          envSlice(opts.Env),
		WorkingDir:   opts.WorkingDir,
		ConsoleSize:  consoleSize(opts.TTY, opts.Cols, opts.Rows),
	})
	if err != nil {
		return nil, mapDockerError("create container exec", err)
	}

	hijack, cancelAttach, err := attachContainerExec(callCtx, api, resp.ID, dockerclient.ExecAttachOptions{
		TTY:         opts.TTY,
		ConsoleSize: consoleSize(opts.TTY, opts.Cols, opts.Rows),
	})
	if err != nil {
		return nil, mapDockerError("attach container exec", err)
	}
	return &ExecSession{ID: resp.ID, hijack: hijack.HijackedResponse, cancelAttach: cancelAttach}, nil
}

func (c *Client) RunContainerExec(ctx context.Context, containerID string, opts ExecOptions) (string, int, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", -1, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := api.ExecCreate(callCtx, containerID, dockerclient.ExecCreateOptions{
		User:         opts.User,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          opts.TTY,
		Cmd:          opts.Cmd,
		Env:          envSlice(opts.Env),
		WorkingDir:   opts.WorkingDir,
	})
	if err != nil {
		return "", -1, mapDockerError("create container exec", err)
	}
	hijack, cancelAttach, err := attachContainerExec(callCtx, api, resp.ID, dockerclient.ExecAttachOptions{TTY: opts.TTY})
	if err != nil {
		return "", -1, mapDockerError("attach container exec", err)
	}
	defer func() {
		cancelAttach()
		hijack.Close()
	}()
	// A hijacked connection is no longer managed by net/http, so canceling
	// callCtx alone does not interrupt the following stream read.
	stopClose := context.AfterFunc(callCtx, hijack.Close)
	defer stopClose()

	var out execOutputBuffer
	if opts.TTY {
		_, err = io.Copy(&out, hijack.Reader)
	} else {
		_, err = stdcopy.StdCopy(&out, &out, hijack.Reader)
	}
	if ctxErr := callCtx.Err(); ctxErr != nil {
		return out.String(), -1, mapDockerError("read container exec", ctxErr)
	}
	if errors.Is(err, errContainerExecOutputLimit) {
		return out.String(), -1, apperror.New(
			apperror.Conflict,
			"Container command output is too large",
			apperror.WithDetail("Captured output is limited to 4 MiB per command."),
			apperror.WithRepairHints("Use a narrower directory or an interactive terminal for large output."),
		)
	}
	if err != nil && !isExpectedExecClose(err) {
		return out.String(), -1, mapDockerError("read container exec", err)
	}
	inspect, err := api.ExecInspect(callCtx, resp.ID, dockerclient.ExecInspectOptions{})
	if err != nil {
		return out.String(), -1, mapDockerError("inspect container exec", err)
	}
	return out.String(), inspect.ExitCode, nil
}

type execAttachContextKey struct{}

// The SDK's hijack handshake reads directly from its socket rather than using
// net/http's context-aware transport. Track sockets until the handshake
// completes so cancellation can interrupt a stalled native or WSL/SSH upgrade.
// Once attached, the session owns the connection independently of the RPC.
type execAttachGuard struct {
	mu        sync.Mutex
	ctx       context.Context
	completed bool
	conns     []net.Conn
}

func execAttachDialer(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		guard, _ := ctx.Value(execAttachContextKey{}).(*execAttachGuard)
		if guard == nil {
			return conn, nil
		}
		guard.mu.Lock()
		ctxErr := guard.ctx.Err()
		if !guard.completed && ctxErr == nil {
			guard.conns = append(guard.conns, conn)
		}
		guard.mu.Unlock()
		if ctxErr != nil {
			_ = conn.Close()
			return nil, ctxErr
		}
		return conn, nil
	}
}

func attachContainerExec(ctx context.Context, api APIClient, id string, opts dockerclient.ExecAttachOptions) (dockerclient.ExecAttachResult, context.CancelFunc, error) {
	guard := &execAttachGuard{ctx: ctx}
	attachCtx, cancelAttach := context.WithCancel(context.WithoutCancel(ctx))
	attachCtx = context.WithValue(attachCtx, execAttachContextKey{}, guard)
	stopCancel := context.AfterFunc(ctx, func() {
		guard.mu.Lock()
		if guard.completed {
			guard.mu.Unlock()
			return
		}
		conns := guard.conns
		guard.conns = nil
		guard.mu.Unlock()
		cancelAttach()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	response, err := api.ExecAttach(attachCtx, id, opts)
	guard.mu.Lock()
	guard.completed = true
	guard.conns = nil
	ctxErr := ctx.Err()
	guard.mu.Unlock()
	stopCancel()
	if ctxErr != nil {
		err = ctxErr
	}
	if err != nil {
		cancelAttach()
		if response.Conn != nil {
			response.Close()
		}
		return dockerclient.ExecAttachResult{}, nil, err
	}
	return response, cancelAttach, nil
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
	if _, err := api.ExecResize(callCtx, execID, dockerclient.ExecResizeOptions{Width: uint(cols), Height: uint(rows)}); err != nil {
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
	inspect, err := api.ExecInspect(callCtx, execID, dockerclient.ExecInspectOptions{})
	if err != nil {
		return nil, mapDockerError("inspect container exec", err)
	}
	return &ExecInspect{
		ID:          inspect.ID,
		ContainerID: inspect.ContainerID,
		Running:     inspect.Running,
		ExitCode:    inspect.ExitCode,
		PID:         inspect.PID,
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
		if err != nil && !missingContainerShell(err, shell) {
			// A broken transport, canceled request, or disappearing container
			// is not evidence of a shell-less image. Preserve the real failure.
			return nil, err
		}
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

func missingContainerShell(err error, shell string) bool {
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		message := strings.ToLower(cause.Error())
		if strings.Contains(message, strings.ToLower(shell)) &&
			(strings.Contains(message, "no such file or directory") ||
				strings.Contains(message, "executable file not found")) {
			return true
		}
	}
	return false
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

func consoleSize(tty bool, cols int, rows int) dockerclient.ConsoleSize {
	if !tty || cols <= 0 && rows <= 0 {
		return dockerclient.ConsoleSize{}
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 30
	}
	return dockerclient.ConsoleSize{Height: uint(rows), Width: uint(cols)}
}
