package providers

import (
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

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
