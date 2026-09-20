//go:build windows

package terminal

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWindowsPTYStarterRunsCommand(t *testing.T) {
	t.Parallel()
	starter := newDefaultPTYStarter()
	session, err := starter.Start(context.Background(), PTYSpec{
		Argv: []string{"cmd.exe", "/c", "echo cairn-conpty"},
		Cols: 80,
		Rows: 24,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	output := make(chan string, 1)
	go func() {
		var builder strings.Builder
		buf := make([]byte, 1024)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				builder.Write(buf[:n])
				if strings.Contains(builder.String(), "cairn-conpty") {
					output <- builder.String()
					return
				}
			}
			if err != nil {
				output <- builder.String()
				return
			}
		}
	}()

	select {
	case got := <-output:
		if !strings.Contains(got, "cairn-conpty") {
			t.Fatalf("terminal output = %q, want marker", got)
		}
	case <-time.After(5 * time.Second):
		exit := make(chan int, 1)
		go func() {
			exit <- session.Wait()
		}()
		select {
		case code := <-exit:
			t.Fatalf("timed out waiting for terminal output; process exited with code %d", code)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timed out waiting for terminal output; process is still running")
		}
	}

	if code := session.Wait(); code != 0 {
		t.Fatalf("Wait() = %d, want 0", code)
	}
}

func TestWindowsPTYStarterRejectsEmptyArgv(t *testing.T) {
	t.Parallel()
	starter := newDefaultPTYStarter()
	if _, err := starter.Start(context.Background(), PTYSpec{}); err == nil {
		t.Fatal("Start() error = nil, want error")
	}
}
