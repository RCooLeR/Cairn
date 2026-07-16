package providers

import (
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	stdioExecutableSummaryLimitBytes = 96
	stdioCommandSummaryLimitBytes    = 192
)

var globalStdioTracker = newStdioTransportTracker()

type stdioTransportTracker struct {
	nextID        atomic.Int64
	opened        atomic.Int64
	closed        atomic.Int64
	forcedKills   atomic.Int64
	closeTimeouts atomic.Int64

	mu           sync.Mutex
	active       map[int64]models.StdioConnectionDiagnostic
	lastOpenedAt time.Time
	lastClosedAt time.Time
	now          func() time.Time
}

func newStdioTransportTracker() *stdioTransportTracker {
	return &stdioTransportTracker{
		active: map[int64]models.StdioConnectionDiagnostic{},
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func trackStdioOpen(command []string) int64 {
	return globalStdioTracker.open(command)
}

func trackStdioClose(id int64) {
	globalStdioTracker.close(id)
}

func trackStdioForcedKill() {
	globalStdioTracker.forcedKills.Add(1)
}

func trackStdioCloseTimeout() {
	globalStdioTracker.closeTimeouts.Add(1)
}

func StdioTransportDiagnostics() models.StdioTransportDiagnostics {
	return globalStdioTracker.diagnostics()
}

func (t *stdioTransportTracker) open(command []string) int64 {
	if t == nil {
		return 0
	}
	id := t.nextID.Add(1)
	now := t.now()
	t.opened.Add(1)
	t.mu.Lock()
	t.lastOpenedAt = now
	t.active[id] = models.StdioConnectionDiagnostic{
		ID:       id,
		Command:  stdioCommandSummary(command),
		OpenedAt: now,
	}
	t.mu.Unlock()
	return id
}

// stdioCommandSummary deliberately does not retain argv. Stdio transports are
// long-lived, so their diagnostic metadata can be copied into screenshots and
// support bundles long after the invocation was constructed. Keeping only the
// executable basename and a fixed, recognized operation prevents flag values,
// environment assignments, credential-bearing URLs, context names, socket
// paths, and shell snippets from reaching the diagnostics DTO.
func stdioCommandSummary(command []string) string {
	if len(command) == 0 {
		return ""
	}
	executable := stdioExecutableBasename(command[0])
	operation := recognizedStdioOperation(command)
	if operation == "" {
		return truncateStdioDiagnostic(executable, stdioCommandSummaryLimitBytes)
	}

	operationExecutable, operationSuffix, _ := strings.Cut(operation, " ")
	if normalizedExecutableName(executable) == operationExecutable {
		if operationSuffix == "" {
			return truncateStdioDiagnostic(executable, stdioCommandSummaryLimitBytes)
		}
		return truncateStdioDiagnostic(executable+" "+operationSuffix, stdioCommandSummaryLimitBytes)
	}
	return truncateStdioDiagnostic(executable+" ("+operation+")", stdioCommandSummaryLimitBytes)
}

func stdioExecutableBasename(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	if value == "" || strings.Contains(value, "://") {
		return "command"
	}
	// path.Base is intentionally used after normalizing separators so a Linux
	// build also minimizes Windows command paths supplied by WSL integrations.
	value = path.Base(strings.ReplaceAll(value, `\`, "/"))
	value = strings.ToValidUTF8(value, "")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || value == "." || value == "/" {
		return "command"
	}
	return truncateStdioDiagnostic(value, stdioExecutableSummaryLimitBytes)
}

func recognizedStdioOperation(command []string) string {
	for index, arg := range command {
		switch normalizedExecutableName(stdioExecutableBasename(arg)) {
		case "docker":
			if commandContainsOrderedTokens(command[index+1:], "system", "dial-stdio") {
				return "docker system dial-stdio"
			}
			return "docker stdio transport"
		case "socat":
			return "socat socket relay"
		case "ssh":
			return "ssh stdio transport"
		}
	}
	return ""
}

func normalizedExecutableName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.TrimSuffix(value, ".exe")
}

func commandContainsOrderedTokens(command []string, tokens ...string) bool {
	if len(tokens) == 0 {
		return true
	}
	next := 0
	for _, arg := range command {
		arg = strings.ToLower(strings.Trim(strings.TrimSpace(arg), `"'`))
		if arg != tokens[next] {
			continue
		}
		next++
		if next == len(tokens) {
			return true
		}
	}
	return false
}

func truncateStdioDiagnostic(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	const marker = "..."
	if limit <= len(marker) {
		return marker[:limit]
	}
	end := limit - len(marker)
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + marker
}

func (t *stdioTransportTracker) close(id int64) {
	if t == nil || id == 0 {
		return
	}
	now := t.now()
	t.mu.Lock()
	if _, ok := t.active[id]; ok {
		delete(t.active, id)
		t.lastClosedAt = now
		t.closed.Add(1)
	}
	t.mu.Unlock()
}

func (t *stdioTransportTracker) diagnostics() models.StdioTransportDiagnostics {
	if t == nil {
		return models.StdioTransportDiagnostics{}
	}
	now := t.now()
	t.mu.Lock()
	active := make([]models.StdioConnectionDiagnostic, 0, len(t.active))
	for _, item := range t.active {
		item.AgeMS = now.Sub(item.OpenedAt).Milliseconds()
		active = append(active, item)
	}
	lastOpenedAt := t.lastOpenedAt
	lastClosedAt := t.lastClosedAt
	t.mu.Unlock()
	sort.Slice(active, func(i int, j int) bool {
		return active[i].OpenedAt.Before(active[j].OpenedAt)
	})
	return models.StdioTransportDiagnostics{
		Opened:            t.opened.Load(),
		Closed:            t.closed.Load(),
		Active:            len(active),
		ForcedKills:       t.forcedKills.Load(),
		CloseTimeouts:     t.closeTimeouts.Load(),
		LastOpenedAt:      lastOpenedAt,
		LastClosedAt:      lastClosedAt,
		ActiveConnections: active,
	}
}

func resetStdioTransportDiagnosticsForTest() {
	globalStdioTracker = newStdioTransportTracker()
}
