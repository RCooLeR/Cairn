package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// stdout and stderr are bounded independently. The reserve keeps the
	// explicit truncation marker inside the advertised per-stream limit.
	commandOutputLimitBytes       = 64 << 10
	commandOutputMarkerReserve    = 96
	commandFailureCauseLimitBytes = 4 << 10
	commandFailureStreamLimit     = 8 << 10
	commandFailureDetailLimit     = 24 << 10
)

var (
	commandPrivateKeyPattern   = regexp.MustCompile(`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)
	commandPrivateKeyTail      = regexp.MustCompile(`(?is)^.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	commandURLSecretPattern    = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^/\s:@]+:)[^@\s/]+@`)
	commandQuotedSecretPattern = regexp.MustCompile(
		`(?im)((?:^|[^A-Za-z0-9_])["']?(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|auth(?:orization)?|credentials?|private[_-]?key|client[_-]?secret|refresh[_-]?token)["']?\s*[:=]\s*)["'][^\r\n]*`,
	)
	commandSecretFlagPattern = regexp.MustCompile(
		`(?i)(--?(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|auth(?:orization)?|credentials?|private[_-]?key|client[_-]?secret|refresh[_-]?token)(?:=|\s+))(?:["'][^"'\r\n]*["']|[^\s]+)`,
	)
	commandAuthorizationPattern  = regexp.MustCompile(`(?i)(\b(?:bearer|basic)\s+)[A-Za-z0-9._~+/=-]+`)
	commandUnquotedSecretPattern = regexp.MustCompile(
		`(?im)(\b(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|auth(?:orization)?|credentials?|private[_-]?key|client[_-]?secret|refresh[_-]?token)\b\s*[:=]\s*)[^\r\n]+`,
	)
	commandKnownTokenPattern = regexp.MustCompile(
		`(?i)\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})\b`,
	)
	commandJWTTokenPattern = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
)

type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, timeout time.Duration, name string, args ...string) (*CommandResult, error)
}

type CommandRunOptions struct {
	Timeout time.Duration
	Workdir string
	Env     []string
	Stdin   string
}

type OptionsCommandRunner interface {
	RunWithOptions(ctx context.Context, opts CommandRunOptions, name string, args ...string) (*CommandResult, error)
}

type ExecRunner struct{}

func (ExecRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (ExecRunner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (*CommandResult, error) {
	return ExecRunner{}.RunWithOptions(ctx, CommandRunOptions{Timeout: timeout}, name, args...)
}

func (ExecRunner) RunWithOptions(ctx context.Context, opts CommandRunOptions, name string, args ...string) (*CommandResult, error) {
	runCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	started := time.Now()
	cmd := exec.CommandContext(runCtx, name, args...)
	configureBackgroundCommand(cmd)
	cmd.Dir = opts.Workdir
	if len(opts.Env) > 0 {
		cmd.Env = mergeEnv(os.Environ(), opts.Env)
	}
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}
	stdout := newCommandOutputBuffer(commandOutputLimitBytes)
	stderr := newCommandOutputBuffer(commandOutputLimitBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	result := &CommandResult{
		Command:         safeCommandResultCommand(name, args),
		Workdir:         opts.Workdir,
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
		ExitCode:        0,
		Duration:        time.Since(started),
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, err
}

// commandOutputBuffer retains a bounded prefix and suffix while always
// accepting the full write. This lets the child continue draining both pipes
// without allowing either stream to grow memory without limit.
type commandOutputBuffer struct {
	mu        sync.Mutex
	head      []byte
	tail      []byte
	headLimit int
	tailLimit int
	dropped   int64
}

func newCommandOutputBuffer(limit int) *commandOutputBuffer {
	payloadLimit := limit - commandOutputMarkerReserve
	if payloadLimit < 0 {
		payloadLimit = 0
	}
	headLimit := payloadLimit / 2
	return &commandOutputBuffer{
		headLimit: headLimit,
		tailLimit: payloadLimit - headLimit,
	}
}

func (b *commandOutputBuffer) Write(p []byte) (int, error) {
	written := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()

	if remaining := b.headLimit - len(b.head); remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		b.head = append(b.head, p[:remaining]...)
		p = p[remaining:]
	}
	if len(p) == 0 {
		return written, nil
	}
	if b.tailLimit == 0 {
		b.dropped += int64(len(p))
		return written, nil
	}
	if len(p) >= b.tailLimit {
		b.dropped += int64(len(b.tail) + len(p) - b.tailLimit)
		b.tail = append(b.tail[:0], p[len(p)-b.tailLimit:]...)
		return written, nil
	}
	overflow := len(b.tail) + len(p) - b.tailLimit
	if overflow > 0 {
		b.dropped += int64(overflow)
		copy(b.tail, b.tail[overflow:])
		b.tail = b.tail[:len(b.tail)-overflow]
	}
	b.tail = append(b.tail, p...)
	return written, nil
}

func (b *commandOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dropped == 0 {
		return string(b.head) + string(b.tail)
	}
	return string(b.head) + commandOutputTruncationMarker(b.dropped) + string(b.tail)
}

func (b *commandOutputBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped > 0
}

func commandOutputTruncationMarker(dropped int64) string {
	return fmt.Sprintf("...[Cairn truncated %d bytes]...", dropped)
}

func mergeEnv(base []string, overrides []string) []string {
	if len(overrides) == 0 {
		return append([]string(nil), base...)
	}
	index := make(map[string]int, len(base))
	merged := append([]string(nil), base...)
	for i, entry := range merged {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			index[key] = i
		}
	}
	for _, entry := range overrides {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if i, exists := index[key]; exists {
			merged[i] = entry
		} else {
			index[key] = len(merged)
			merged = append(merged, entry)
		}
	}
	return merged
}

func commandFailureDetail(result *CommandResult, err error) string {
	parts := []string{}
	if err != nil {
		if cause := safeCommandFailureText(err.Error(), commandFailureCauseLimitBytes); cause != "" {
			parts = append(parts, cause)
		}
	}
	if result != nil {
		if stdout := safeCommandFailureText(result.Stdout, commandFailureStreamLimit); stdout != "" {
			parts = append(parts, "stdout:\n"+stdout)
		}
		if stderr := safeCommandFailureText(result.Stderr, commandFailureStreamLimit); stderr != "" {
			parts = append(parts, "stderr:\n"+stderr)
		}
	}
	return boundedHeadTailString(strings.Join(parts, "\n"), commandFailureDetailLimit)
}

func safeCommandFailureText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = redactCommandDiagnostic(value)
	return boundedHeadTailString(value, limit)
}

// SafeCommandDiagnostic redacts known credential forms and applies a bounded
// head/tail representation suitable for renderer-visible errors and progress.
// Structured parsers must continue to use the original CommandResult fields.
func SafeCommandDiagnostic(value string, limit int) string {
	return safeCommandFailureText(value, limit)
}

// RedactCommandDiagnostic removes known credential forms without changing the
// output's line structure. Callers should use it only for output that is
// already independently bounded, such as CommandResult stdout and stderr.
func RedactCommandDiagnostic(value string) string {
	return redactCommandDiagnostic(value)
}

func boundedHeadTailString(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	payloadLimit := limit - commandOutputMarkerReserve
	if payloadLimit < 0 {
		payloadLimit = 0
	}
	headLimit := payloadLimit / 2
	tailLimit := payloadLimit - headLimit
	dropped := len(value) - headLimit - tailLimit
	return value[:headLimit] + commandOutputTruncationMarker(int64(dropped)) + value[len(value)-tailLimit:]
}

func redactCommandDiagnostic(value string) string {
	value = commandPrivateKeyPattern.ReplaceAllString(value, "[REDACTED PRIVATE KEY]")
	value = commandPrivateKeyTail.ReplaceAllString(value, "[REDACTED PRIVATE KEY]")
	value = commandURLSecretPattern.ReplaceAllString(value, "$1[REDACTED]@")
	value = commandQuotedSecretPattern.ReplaceAllString(value, "$1[REDACTED]")
	value = commandSecretFlagPattern.ReplaceAllString(value, "$1[REDACTED]")
	value = commandAuthorizationPattern.ReplaceAllString(value, "$1[REDACTED]")
	value = commandUnquotedSecretPattern.ReplaceAllString(value, "$1[REDACTED]")
	value = commandKnownTokenPattern.ReplaceAllString(value, "[REDACTED TOKEN]")
	return commandJWTTokenPattern.ReplaceAllString(value, "[REDACTED TOKEN]")
}

func safeCommandResultCommand(name string, args []string) []string {
	command := make([]string, 0, len(args)+1)
	command = append(command, redactCommandDiagnostic(name))
	redactNext := false
	for _, arg := range args {
		if redactNext {
			command = append(command, "[REDACTED]")
			redactNext = false
			continue
		}
		command = append(command, redactCommandDiagnostic(arg))
		redactNext = commandArgumentExpectsSecret(arg)
	}
	return command
}

func commandArgumentExpectsSecret(arg string) bool {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if !strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
		return false
	}
	arg = strings.TrimLeft(arg, "-")
	switch arg {
	case "password", "passwd", "secret", "token", "api-key", "api_key", "access-key", "access_key", "auth", "authorization", "credential", "credentials", "private-key", "private_key", "client-secret", "client_secret", "refresh-token", "refresh_token":
		return true
	default:
		return false
	}
}
