package providers

import (
	"context"
	"io"
	"net"
	"os/exec"
	"sync"
	"time"
)

type commandStdioConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
	done   chan error
	once   sync.Once
}

type commandAddr string

func dialCommandStdio(ctx context.Context, command []string) (net.Conn, error) {
	if len(command) == 0 {
		return nil, exec.ErrNotFound
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	configureBackgroundCommand(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	conn := &commandStdioConn{
		stdin:  stdin,
		stdout: stdout,
		cmd:    cmd,
		done:   make(chan error, 1),
	}
	go func() {
		conn.done <- cmd.Wait()
		close(conn.done)
	}()
	return conn, nil
}

func (c *commandStdioConn) Read(b []byte) (int, error) {
	return c.stdout.Read(b)
}

func (c *commandStdioConn) Write(b []byte) (int, error) {
	return c.stdin.Write(b)
}

func (c *commandStdioConn) Close() error {
	c.once.Do(func() {
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-c.done
	})
	return nil
}

func (c *commandStdioConn) LocalAddr() net.Addr {
	return commandAddr("local")
}

func (c *commandStdioConn) RemoteAddr() net.Addr {
	return commandAddr("remote")
}

func (c *commandStdioConn) SetDeadline(time.Time) error {
	return nil
}

func (c *commandStdioConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *commandStdioConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (a commandAddr) Network() string {
	return "stdio"
}

func (a commandAddr) String() string {
	return string(a)
}
