package providers

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	stdioGracefulCloseTimeout = 2 * time.Second
	stdioForceCloseTimeout    = 2 * time.Second
)

type commandStdioConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
	done   chan error
	id     int64
	once   sync.Once
	readMu sync.Mutex
	peeked bool
	err    error
}

type commandAddr string

func dialCommandStdio(ctx context.Context, command []string) (net.Conn, error) {
	if len(command) == 0 {
		return nil, exec.ErrNotFound
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	cmd := exec.Command(command[0], command[1:]...)
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
	id := trackStdioOpen(command)
	conn := &commandStdioConn{
		stdin:  stdin,
		stdout: stdout,
		cmd:    cmd,
		done:   make(chan error, 1),
		id:     id,
	}
	go func() {
		err := cmd.Wait()
		trackStdioClose(id)
		conn.done <- err
		close(conn.done)
	}()
	select {
	case <-ctx.Done():
		_ = conn.Close()
		return nil, ctx.Err()
	default:
	}
	return conn, nil
}

func (c *commandStdioConn) Read(b []byte) (int, error) {
	n, err := c.stdout.Read(b)
	if n > 0 && c.shouldValidateFirstRead() {
		if failure := stdioStartupFailure(b[:n]); failure != nil {
			_ = c.abortStartupFailure()
			return 0, failure
		}
	}
	return n, err
}

func (c *commandStdioConn) abortStartupFailure() error {
	c.once.Do(func() {
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		if c.cmd != nil && c.cmd.Process != nil {
			trackStdioForcedKill()
			_ = c.cmd.Process.Kill()
		}
		select {
		case c.err = <-c.done:
		case <-time.After(stdioForceCloseTimeout):
			trackStdioCloseTimeout()
			c.err = fmt.Errorf("stdio command did not exit after startup failure")
		}
	})
	return c.err
}

func (c *commandStdioConn) Write(b []byte) (int, error) {
	return c.stdin.Write(b)
}

func (c *commandStdioConn) Close() error {
	c.once.Do(func() {
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		select {
		case c.err = <-c.done:
			return
		case <-time.After(stdioGracefulCloseTimeout):
		}
		if c.cmd != nil && c.cmd.Process != nil {
			trackStdioForcedKill()
			_ = c.cmd.Process.Kill()
		}
		select {
		case c.err = <-c.done:
		case <-time.After(stdioForceCloseTimeout):
			trackStdioCloseTimeout()
			c.err = fmt.Errorf("stdio command did not exit after close")
		}
	})
	return c.err
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

func (c *commandStdioConn) shouldValidateFirstRead() bool {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if c.peeked {
		return false
	}
	c.peeked = true
	return true
}

func stdioStartupFailure(data []byte) error {
	message := ""
	if looksLikeUTF16LEText(data) {
		message = decodeWSLOutput(string(data))
	} else {
		text := strings.TrimSpace(string(data))
		lower := strings.ToLower(text)
		if strings.HasPrefix(lower, "wsl/") || strings.Contains(lower, "wsl/service/") {
			message = text
		}
	}
	message = strings.TrimSpace(strings.ReplaceAll(message, "\x00", ""))
	if message == "" {
		return nil
	}
	return fmt.Errorf("Docker API stdio transport failed: %s", truncateSingleLine(message, 500))
}

func looksLikeUTF16LEText(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	limit := len(data)
	if limit > 48 {
		limit = 48
	}
	pairs := 0
	zeroHighBytes := 0
	for i := 0; i+1 < limit; i += 2 {
		pairs++
		if data[i] >= 0x20 && data[i] <= 0x7e && data[i+1] == 0 {
			zeroHighBytes++
		}
	}
	return pairs >= 2 && zeroHighBytes*100/pairs >= 75
}

func truncateSingleLine(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return string([]rune(value)[:min(max, utf8.RuneCountInString(value))])
	}
	limit := max - 3
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes) + "..."
}
