package providers

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

const stdioDeadlineHelperEnvironment = "CAIRN_PROVIDER_STDIO_DEADLINE_HELPER"

func TestStdioStartupFailureDetectsWSLUTF16Text(t *testing.T) {
	t.Parallel()
	err := stdioStartupFailure(utf16LEBytes("operation could not be completed because WSL is not ready"))
	if err == nil {
		t.Fatal("stdioStartupFailure() error = nil, want WSL transport failure")
	}
	if !strings.Contains(err.Error(), "operation could not be completed") {
		t.Fatalf("error = %q", err)
	}
}

func TestStdioStartupFailureDetectsPlainWSLServiceText(t *testing.T) {
	t.Parallel()
	err := stdioStartupFailure([]byte("Wsl/Service/0x80072747: queue was full"))
	if err == nil {
		t.Fatal("stdioStartupFailure() error = nil, want WSL transport failure")
	}
}

func TestStdioStartupFailureIgnoresHTTPStatus(t *testing.T) {
	t.Parallel()
	if err := stdioStartupFailure([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n")); err != nil {
		t.Fatalf("stdioStartupFailure() error = %v, want nil", err)
	}
}

func TestTruncateSingleLineDoesNotSplitUTF8(t *testing.T) {
	t.Parallel()
	got := truncateSingleLine("привіт світ", 8)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncateSingleLine() = %q, want ellipsis", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("truncateSingleLine() split UTF-8: %q", got)
	}
}

func TestCommandStdioConnCloseAllowsGracefulExit(t *testing.T) {
	t.Parallel()
	done := make(chan error, 1)
	conn := &commandStdioConn{
		stdin:  nopWriteCloser{},
		stdout: io.NopCloser(strings.NewReader("")),
		done:   done,
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		done <- nil
		close(done)
	}()

	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
}

func TestCommandStdioConnReadDeadlineUnblocksWithTimeout(t *testing.T) {
	t.Parallel()
	stdout := newDeadlineBlockingPipe()
	conn := &commandStdioConn{
		stdin:  nopWriteCloser{},
		stdout: stdout,
		done:   completedStdioCommand(),
	}

	result := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		result <- err
	}()
	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	assertStdioDeadlineError(t, receiveStdioOperationError(t, result))
	if err := conn.SetReadDeadline(time.Time{}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("SetReadDeadline() after fatal timeout error = %v, want net.ErrClosed", err)
	}
}

func TestCommandStdioConnWriteDeadlineUnblocksWithTimeout(t *testing.T) {
	t.Parallel()
	stdin := newDeadlineBlockingPipe()
	conn := &commandStdioConn{
		stdin:  stdin,
		stdout: io.NopCloser(strings.NewReader("")),
		done:   completedStdioCommand(),
	}

	result := make(chan error, 1)
	go func() {
		_, err := conn.Write([]byte("blocked write"))
		result <- err
	}()
	if err := conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}
	assertStdioDeadlineError(t, receiveStdioOperationError(t, result))
}

func TestCommandStdioConnDeadlineUnblocksBothDirections(t *testing.T) {
	t.Parallel()
	stdin := newDeadlineBlockingPipe()
	stdout := newDeadlineBlockingPipe()
	conn := &commandStdioConn{
		stdin:  stdin,
		stdout: stdout,
		done:   completedStdioCommand(),
	}

	readResult := make(chan error, 1)
	writeResult := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		readResult <- err
	}()
	go func() {
		_, err := conn.Write([]byte("blocked write"))
		writeResult <- err
	}()
	if err := conn.SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	assertStdioDeadlineError(t, receiveStdioOperationError(t, readResult))
	assertStdioDeadlineError(t, receiveStdioOperationError(t, writeResult))
}

func TestCommandStdioConnClearedDeadlineDoesNotFire(t *testing.T) {
	t.Parallel()
	stdout := newDeadlineBlockingPipe()
	conn := &commandStdioConn{
		stdin:  nopWriteCloser{},
		stdout: stdout,
		done:   completedStdioCommand(),
	}
	result := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		result <- err
	}()

	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear SetReadDeadline() error = %v", err)
	}
	select {
	case err := <-result:
		t.Fatalf("cleared deadline unexpectedly unblocked Read(): %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("replacement SetReadDeadline() error = %v", err)
	}
	assertStdioDeadlineError(t, receiveStdioOperationError(t, result))
}

func TestCommandStdioDeadlineHelperProcess(t *testing.T) {
	if os.Getenv(stdioDeadlineHelperEnvironment) != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestCommandStdioConnDeadlineTerminatesAndReapsProcess(t *testing.T) {
	t.Setenv(stdioDeadlineHelperEnvironment, "1")
	connection, err := dialCommandStdio(context.Background(), []string{
		os.Args[0],
		"-test.run=^TestCommandStdioDeadlineHelperProcess$",
	})
	if err != nil {
		t.Fatalf("dialCommandStdio() error = %v", err)
	}
	conn := connection.(*commandStdioConn)
	defer func() { _ = conn.Close() }()

	result := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := conn.Read(make([]byte, 1))
		result <- err
	}()
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	assertStdioDeadlineError(t, receiveStdioOperationError(t, result))
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("deadline unblocked Read() after %s, want within 1s", elapsed)
	}

	_ = conn.Close()
	select {
	case _, ok := <-conn.done:
		if ok {
			t.Fatal("stdio Wait channel contained an unconsumed result after Close()")
		}
	case <-time.After(time.Second):
		t.Fatal("stdio command was not reaped after deadline")
	}
}

func TestStdioTransportDiagnosticsTracksLifecycle(t *testing.T) {
	resetStdioTransportDiagnosticsForTest()
	id := trackStdioOpen([]string{"wsl.exe", "-d", "Ubuntu", "docker", "system", "dial-stdio"})
	diagnostics := StdioTransportDiagnostics()
	if diagnostics.Opened != 1 || diagnostics.Closed != 0 || diagnostics.Active != 1 {
		t.Fatalf("open diagnostics = %#v", diagnostics)
	}
	if len(diagnostics.ActiveConnections) != 1 || diagnostics.ActiveConnections[0].ID != id {
		t.Fatalf("active connections = %#v", diagnostics.ActiveConnections)
	}

	trackStdioClose(id)
	diagnostics = StdioTransportDiagnostics()
	if diagnostics.Opened != 1 || diagnostics.Closed != 1 || diagnostics.Active != 0 {
		t.Fatalf("closed diagnostics = %#v", diagnostics)
	}
}

func utf16LEBytes(value string) []byte {
	units := utf16.Encode([]rune(value))
	out := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (nopWriteCloser) Close() error {
	return nil
}

type deadlineBlockingPipe struct {
	closed chan struct{}
	once   sync.Once
}

func newDeadlineBlockingPipe() *deadlineBlockingPipe {
	return &deadlineBlockingPipe{closed: make(chan struct{})}
}

func (p *deadlineBlockingPipe) Read([]byte) (int, error) {
	<-p.closed
	return 0, io.ErrClosedPipe
}

func (p *deadlineBlockingPipe) Write([]byte) (int, error) {
	<-p.closed
	return 0, io.ErrClosedPipe
}

func (p *deadlineBlockingPipe) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func completedStdioCommand() chan error {
	done := make(chan error, 1)
	done <- nil
	close(done)
	return done
}

func receiveStdioOperationError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("stdio operation remained blocked after its deadline")
		return nil
	}
}

func assertStdioDeadlineError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("operation error = %v, want os.ErrDeadlineExceeded", err)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("operation error = %v, want net.Error with Timeout()=true", err)
	}
}
