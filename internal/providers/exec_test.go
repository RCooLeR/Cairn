package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	if os.Getenv(execHelperEnvironment) != "1" {
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
