package providers

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
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
		Command:  strings.Join(command, " "),
		OpenedAt: now,
	}
	t.mu.Unlock()
	return id
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
