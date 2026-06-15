//go:build !windows

package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type localPTYStarter struct{}

type localPTYSession struct {
	file *os.File
	cmd  *exec.Cmd
}

func newDefaultPTYStarter() PTYStarter {
	return localPTYStarter{}
}

func (localPTYStarter) Start(_ context.Context, spec PTYSpec) (PTYSession, error) {
	if len(spec.Argv) == 0 || strings.TrimSpace(spec.Argv[0]) == "" {
		return nil, errors.New("terminal argv is empty")
	}
	cols, rows := normalizeDimensions(spec.Cols, spec.Rows)
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.WorkingDir
	cmd.Env = append(os.Environ(), envEntries(spec.Env)...)
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &localPTYSession{file: file, cmd: cmd}, nil
}

func (s *localPTYSession) Read(p []byte) (int, error) {
	return s.file.Read(p)
}

func (s *localPTYSession) Write(p []byte) (int, error) {
	return s.file.Write(p)
}

func (s *localPTYSession) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		_ = s.cmd.Process.Kill()
	}
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}

func (s *localPTYSession) Resize(cols int, rows int) error {
	cols, rows = normalizeDimensions(cols, rows)
	return pty.Setsize(s.file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *localPTYSession) Wait() int {
	if s.cmd == nil {
		return -1
	}
	err := s.cmd.Wait()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func envEntries(env map[string]string) []string {
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
