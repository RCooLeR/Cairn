package providers

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
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

	deadlineMu              sync.Mutex
	readDeadline            time.Time
	writeDeadline           time.Time
	readDeadlineTimer       *time.Timer
	writeDeadlineTimer      *time.Timer
	readDeadlineGeneration  uint64
	writeDeadlineGeneration uint64
	readTimedOut            bool
	writeTimedOut           bool
	closed                  bool
	forceClose              bool
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
	if c.operationTimedOut(true) {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.stdout.Read(b)
	if n > 0 && c.shouldValidateFirstRead() {
		if failure := stdioStartupFailure(b[:n]); failure != nil {
			_ = c.abortStartupFailure()
			return 0, failure
		}
	}
	if err != nil && c.operationTimedOut(true) {
		return n, os.ErrDeadlineExceeded
	}
	return n, err
}

func (c *commandStdioConn) abortStartupFailure() error {
	return c.close(true, "stdio command did not exit after startup failure")
}

func (c *commandStdioConn) Write(b []byte) (int, error) {
	if c.operationTimedOut(false) {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.stdin.Write(b)
	if err != nil && c.operationTimedOut(false) {
		return n, os.ErrDeadlineExceeded
	}
	return n, err
}

func (c *commandStdioConn) Close() error {
	return c.close(false, "stdio command did not exit after close")
}

func (c *commandStdioConn) close(force bool, timeoutMessage string) error {
	c.requestClose(force)
	c.once.Do(func() {
		force := c.forceCloseRequested()
		if force {
			c.killProcess()
		}
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		if !force {
			select {
			case c.err = <-c.done:
				return
			case <-time.After(stdioGracefulCloseTimeout):
			}
			c.killProcess()
		}
		select {
		case c.err = <-c.done:
		case <-time.After(stdioForceCloseTimeout):
			trackStdioCloseTimeout()
			c.err = fmt.Errorf("%s", timeoutMessage)
		}
	})
	return c.err
}

func (c *commandStdioConn) killProcess() {
	if c.cmd != nil && c.cmd.Process != nil {
		trackStdioForcedKill()
		_ = c.cmd.Process.Kill()
	}
}

func (c *commandStdioConn) LocalAddr() net.Addr {
	return commandAddr("local")
}

func (c *commandStdioConn) RemoteAddr() net.Addr {
	return commandAddr("remote")
}

// The anonymous pipes owned by a commandStdioConn cannot carry recoverable
// socket deadlines. Expiration is therefore terminal: it closes both pipes,
// kills and reaps the child, and reports a timeout from the affected I/O.
func (c *commandStdioConn) SetDeadline(deadline time.Time) error {
	return c.setDeadline(deadline, true, true)
}

func (c *commandStdioConn) SetReadDeadline(deadline time.Time) error {
	return c.setDeadline(deadline, true, false)
}

func (c *commandStdioConn) SetWriteDeadline(deadline time.Time) error {
	return c.setDeadline(deadline, false, true)
}

func (c *commandStdioConn) setDeadline(deadline time.Time, read, write bool) error {
	expired := false

	c.deadlineMu.Lock()
	if c.closed {
		c.deadlineMu.Unlock()
		return net.ErrClosed
	}
	now := time.Now()
	if read {
		c.readDeadlineGeneration++
		generation := c.readDeadlineGeneration
		stopTimer(&c.readDeadlineTimer)
		c.readDeadline = deadline
		c.readTimedOut = false
		if !deadline.IsZero() {
			if !deadline.After(now) {
				c.readTimedOut = true
				expired = true
			} else {
				c.readDeadlineTimer = time.AfterFunc(deadline.Sub(now), func() {
					c.expireDeadline(true, generation)
				})
			}
		}
	}
	if write {
		c.writeDeadlineGeneration++
		generation := c.writeDeadlineGeneration
		stopTimer(&c.writeDeadlineTimer)
		c.writeDeadline = deadline
		c.writeTimedOut = false
		if !deadline.IsZero() {
			if !deadline.After(now) {
				c.writeTimedOut = true
				expired = true
			} else {
				c.writeDeadlineTimer = time.AfterFunc(deadline.Sub(now), func() {
					c.expireDeadline(false, generation)
				})
			}
		}
	}
	expired = expired || deadlineReached(c.readDeadline, now) || deadlineReached(c.writeDeadline, now)
	if expired {
		c.readTimedOut = deadlineReached(c.readDeadline, now)
		c.writeTimedOut = deadlineReached(c.writeDeadline, now)
		c.closed = true
		c.forceClose = true
		c.stopDeadlineTimersLocked()
	}
	c.deadlineMu.Unlock()

	if expired {
		go func() {
			_ = c.close(true, "stdio command did not exit after deadline")
		}()
	}
	return nil
}

func (c *commandStdioConn) expireDeadline(read bool, generation uint64) {
	now := time.Now()
	c.deadlineMu.Lock()
	if c.closed || (read && generation != c.readDeadlineGeneration) || (!read && generation != c.writeDeadlineGeneration) {
		c.deadlineMu.Unlock()
		return
	}
	readExpired := deadlineReached(c.readDeadline, now)
	writeExpired := deadlineReached(c.writeDeadline, now)
	if !readExpired && !writeExpired {
		// A deadline without a monotonic component can move into the future when
		// the wall clock changes. Re-arm instead of silently losing the deadline.
		if read && !c.readDeadline.IsZero() {
			c.readDeadlineTimer = time.AfterFunc(time.Until(c.readDeadline), func() {
				c.expireDeadline(true, generation)
			})
		} else if !read && !c.writeDeadline.IsZero() {
			c.writeDeadlineTimer = time.AfterFunc(time.Until(c.writeDeadline), func() {
				c.expireDeadline(false, generation)
			})
		}
		c.deadlineMu.Unlock()
		return
	}
	c.readTimedOut = readExpired
	c.writeTimedOut = writeExpired
	c.closed = true
	c.forceClose = true
	c.stopDeadlineTimersLocked()
	c.deadlineMu.Unlock()

	_ = c.close(true, "stdio command did not exit after deadline")
}

func deadlineReached(deadline, now time.Time) bool {
	return !deadline.IsZero() && !deadline.After(now)
}

func (c *commandStdioConn) operationTimedOut(read bool) bool {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if read {
		return c.readTimedOut
	}
	return c.writeTimedOut
}

func (c *commandStdioConn) requestClose(force bool) {
	c.deadlineMu.Lock()
	c.closed = true
	c.forceClose = c.forceClose || force
	c.stopDeadlineTimersLocked()
	c.deadlineMu.Unlock()
}

func (c *commandStdioConn) forceCloseRequested() bool {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return c.forceClose
}

func (c *commandStdioConn) stopDeadlineTimersLocked() {
	c.readDeadlineGeneration++
	c.writeDeadlineGeneration++
	stopTimer(&c.readDeadlineTimer)
	stopTimer(&c.writeDeadlineTimer)
}

func stopTimer(timer **time.Timer) {
	if *timer != nil {
		(*timer).Stop()
		*timer = nil
	}
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
	return fmt.Errorf("docker API stdio transport failed: %s", truncateSingleLine(message, 500))
}

func looksLikeUTF16LEText(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	limit := min(len(data), 48)
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
