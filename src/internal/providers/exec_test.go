package providers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	execHelperEnvironment = "CAIRN_PROVIDER_EXEC_HELPER"
	stdoutSecret          = "stdout-secret-value"
	stderrSecret          = "quoted stderr secret"
	errorSecret           = "error-secret-value"
	githubSecret          = "ghp_0123456789abcdefghijklmnopqrstuvwxyz"
	basicSecret           = "c3RkZXJyLXNlY3JldA=="
	jwtSecret             = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJjYWlybi11c2VyIn0.c2lnbmF0dXJlMTIzNDU2Nzg5"
	privateKeySecret      = "private-key-material-must-not-leak"
	argumentSecret        = "argv-secret-value"
	inlineArgumentSecret  = "inline-argv-secret-value"
)

func TestExecRunnerHelperProcess(t *testing.T) {
	mode := os.Getenv(execHelperEnvironment)
	if mode == "pipe-child" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if mode == "pipe-parent-exit" || mode == "pipe-parent-wait" {
		// exec.Cmd starts the stdin copy only after CreateProcess returns.
		// The child itself may already be running before that on Windows.
		if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
			os.Exit(26)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestExecRunnerHelperProcess$")
		child.Env = mergeEnv(os.Environ(), []string{execHelperEnvironment + "=pipe-child"})
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		configureBackgroundCommand(child)
		if err := child.Start(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(24)
		}
		childPID := child.Process.Pid
		_, _ = fmt.Fprintln(os.Stdout, childPID)
		_ = child.Process.Release()
		notify, err := net.DialTimeout("tcp", os.Getenv("CAIRN_PROVIDER_EXEC_NOTIFY"), 30*time.Second)
		if err != nil {
			os.Exit(25)
		}
		_, _ = fmt.Fprintln(notify, childPID)
		_, _ = io.ReadFull(notify, make([]byte, 1))
		_ = notify.Close()
		if mode == "pipe-parent-wait" {
			time.Sleep(30 * time.Second)
		}
		os.Exit(0)
	}
	if mode != "1" {
		return
	}
	stdout := strings.Join([]string{
		"stdout-head",
		"TOKEN=" + stdoutSecret,
		"-----BEGIN PRIVATE KEY-----",
		privateKeySecret,
		"-----END PRIVATE KEY-----",
		strings.Repeat("O", commandOutputLimitBytes*3),
		"stdout-tail " + githubSecret,
	}, "\n")
	stderr := strings.Join([]string{
		"stderr-head",
		`{"Secret":"` + stderrSecret + `"}`,
		"session " + jwtSecret,
		strings.Repeat("E", commandOutputLimitBytes*3),
		"stderr-tail Authorization: Basic " + basicSecret,
	}, "\n")
	_, _ = fmt.Fprint(os.Stdout, stdout)
	_, _ = fmt.Fprint(os.Stderr, stderr)
	os.Exit(23)
}

func TestExecRunnerBoundsInheritedOutputPipes(t *testing.T) {
	for _, mode := range []string{"pipe-parent-exit", "pipe-parent-wait"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			type outcome struct {
				result *CommandResult
				err    error
			}
			finished := make(chan outcome, 1)
			go func() {
				result, err := (ExecRunner{}).RunWithOptions(ctx, CommandRunOptions{
					Stdin: "1",
					Env:   []string{execHelperEnvironment + "=" + mode, "CAIRN_PROVIDER_EXEC_NOTIFY=" + listener.Addr().String()},
				}, os.Args[0], "-test.run=^TestExecRunnerHelperProcess$")
				finished <- outcome{result: result, err: err}
			}()
			// Process creation can be slow on a busy Windows host. Start timing
			// only after the helper has launched the pipe-holding descendant.
			var notify net.Conn
			startupDeadline := time.Now().Add(2 * time.Minute)
			for notify == nil {
				_ = listener.SetDeadline(time.Now().Add(100 * time.Millisecond))
				connection, acceptErr := listener.Accept()
				if acceptErr == nil {
					notify = connection
					break
				}
				select {
				case early := <-finished:
					t.Fatalf("helper exited before startup notification: error=%v result=%#v", early.err, early.result)
				default:
				}
				if timeout, ok := errors.AsType[net.Error](acceptErr); !ok || !timeout.Timeout() || time.Now().After(startupDeadline) {
					t.Fatalf("wait for helper startup: %v", acceptErr)
				}
			}
			t.Cleanup(func() { _ = notify.Close() })
			_ = notify.SetDeadline(time.Now().Add(30 * time.Second))
			pidText, err := bufio.NewReader(notify).ReadString('\n')
			if err != nil {
				t.Fatalf("read helper descendant PID: %v", err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(pidText))
			if err != nil {
				t.Fatalf("parse helper descendant PID: %v", err)
			}
			var stopDescendant func()
			if process, findErr := os.FindProcess(pid); findErr == nil {
				stopDescendant = func() {
					_ = process.Kill()
					_ = process.Release()
				}
				t.Cleanup(stopDescendant)
			}
			if _, err := notify.Write([]byte{1}); err != nil {
				t.Fatalf("release helper: %v", err)
			}
			if mode == "pipe-parent-wait" {
				cancel()
			}
			var got outcome
			select {
			case got = <-finished:
			case <-time.After(3 * time.Second):
				cancel()
				if stopDescendant != nil {
					stopDescendant()
				}
				t.Fatal("RunWithOptions() remained blocked on inherited pipes after helper was ready")
			}
			if got.err == nil {
				t.Error("RunWithOptions() = nil error despite inherited output pipes")
			}
			if mode == "pipe-parent-exit" && !errors.Is(got.err, exec.ErrWaitDelay) {
				t.Fatalf("RunWithOptions() error = %v, want bounded pipe-drain error", got.err)
			}
			if got.result == nil || strings.TrimSpace(got.result.Stdout) != strings.TrimSpace(pidText) {
				t.Fatalf("RunWithOptions() lost helper output: %#v", got.result)
			}
			if mode == "pipe-parent-wait" && !errors.Is(got.err, context.Canceled) {
				t.Fatalf("RunWithOptions() error = %v, want context cancellation", got.err)
			}
		})
	}
}

func TestExecRunnerBoundsDualStreamOutputAndRedactsFailureDetail(t *testing.T) {
	result, err := (ExecRunner{}).RunWithOptions(
		context.Background(),
		CommandRunOptions{
			Timeout: 10 * time.Second,
			Env:     []string{execHelperEnvironment + "=1"},
		},
		os.Args[0],
		"-test.run=^TestExecRunnerHelperProcess$",
		"--",
		"--token",
		argumentSecret,
		"--password="+inlineArgumentSecret,
	)
	if err == nil {
		t.Fatal("RunWithOptions() error = nil, want helper exit failure")
	}
	if result == nil || result.ExitCode != 23 {
		t.Fatalf("RunWithOptions() result = %#v, want exit code 23", result)
	}
	commandText := strings.Join(result.Command, " ")
	if strings.Contains(commandText, argumentSecret) || strings.Contains(commandText, inlineArgumentSecret) || !strings.Contains(commandText, "[REDACTED]") {
		t.Fatalf("CommandResult.Command was not safely redacted: %q", commandText)
	}
	if len(result.Stdout) > commandOutputLimitBytes || len(result.Stderr) > commandOutputLimitBytes {
		t.Fatalf("captured lengths = stdout %d, stderr %d; limit %d", len(result.Stdout), len(result.Stderr), commandOutputLimitBytes)
	}
	if !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("truncation flags = stdout %t, stderr %t; want both true", result.StdoutTruncated, result.StderrTruncated)
	}
	for label, output := range map[string]string{"stdout": result.Stdout, "stderr": result.Stderr} {
		if !strings.Contains(output, label+"-head") || !strings.Contains(output, label+"-tail") {
			t.Fatalf("%s did not preserve useful head/tail: %q", label, output)
		}
		if !strings.Contains(output, "[Cairn truncated ") {
			t.Fatalf("%s lacks explicit truncation marker", label)
		}
	}

	detail := commandFailureDetail(result, err)
	if len(detail) > commandFailureDetailLimit {
		t.Fatalf("failure detail length = %d, limit %d", len(detail), commandFailureDetailLimit)
	}
	for _, useful := range []string{"stdout-head", "stdout-tail", "stderr-head", "stderr-tail", "[Cairn truncated "} {
		if !strings.Contains(detail, useful) {
			t.Fatalf("failure detail lost %q: %q", useful, detail)
		}
	}
	for _, secret := range []string{stdoutSecret, stderrSecret, githubSecret, basicSecret, jwtSecret, privateKeySecret} {
		if strings.Contains(detail, secret) {
			t.Fatalf("failure detail leaked secret %q: %q", secret, detail)
		}
	}
	if !strings.Contains(detail, "[REDACTED") {
		t.Fatalf("failure detail does not identify redaction: %q", detail)
	}
}

func TestCommandOutputBufferPreservesSmallOutputExactly(t *testing.T) {
	buffer := newCommandOutputBuffer(commandOutputLimitBytes)
	for _, chunk := range []string{"first line\n", "second ", "line\n"} {
		if written, err := buffer.Write([]byte(chunk)); err != nil || written != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, written, err)
		}
	}
	if got, want := buffer.String(), "first line\nsecond line\n"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if buffer.Truncated() {
		t.Fatal("Truncated() = true for fully retained output")
	}
}

func TestCommandFailureDetailRedactsCommonSecretForms(t *testing.T) {
	result := &CommandResult{
		Stdout: strings.Join([]string{
			"useful stdout context",
			`{"api_key":"json-secret\"escaped-secret-tail","safe":"context"}`,
			"https://user:url-secret@example.test/v2/",
			"--password cli-secret",
		}, "\n"),
		Stderr: "Authorization: Bearer bearer-secret",
	}
	detail := commandFailureDetail(result, errors.New("request failed TOKEN="+errorSecret))
	for _, secret := range []string{"json-secret", "escaped-secret-tail", "url-secret", "cli-secret", "bearer-secret", errorSecret} {
		if strings.Contains(detail, secret) {
			t.Fatalf("failure detail leaked %q: %q", secret, detail)
		}
	}
	if !strings.Contains(detail, "useful stdout context") || !strings.Contains(detail, "request failed") {
		t.Fatalf("failure detail lost safe context: %q", detail)
	}
}

func TestCommandFailureTextRedactsTokenBeforeHeadTailTruncation(t *testing.T) {
	payloadLimit := commandFailureStreamLimit - commandOutputMarkerReserve
	headLimit := payloadLimit / 2
	redaction := "[REDACTED TOKEN]"
	leakedPrefix := githubSecret[:len(redaction)]
	value := strings.Repeat("H", headLimit-len(leakedPrefix)-1) + "\n" + githubSecret + "\n" + strings.Repeat("T", commandFailureStreamLimit*2)

	detail := safeCommandFailureText(value, commandFailureStreamLimit)
	if strings.Contains(detail, leakedPrefix) || strings.Contains(detail, githubSecret) {
		t.Fatalf("boundary-straddling token leaked after truncation: %q", detail)
	}
	if !strings.Contains(detail, redaction) {
		t.Fatalf("boundary-straddling token was not explicitly redacted: %q", detail)
	}
	if len(detail) > commandFailureStreamLimit {
		t.Fatalf("failure stream detail length = %d, limit %d", len(detail), commandFailureStreamLimit)
	}
}
