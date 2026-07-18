package logsvc

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
)

func TestFetchLogPageUsesImmutableSnapshotForDuplicateLines(t *testing.T) {
	docker := newFakeLogDocker()
	docker.logs["container-1"] = strings.Repeat("2026-06-13T09:00:00Z duplicate\n", 3)
	manager := NewManager(docker, nil, Options{})
	req := models.LogPageRequest{Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: 1}

	first, err := manager.FetchLogPage(context.Background(), req)
	if err != nil {
		t.Fatalf("FetchLogPage(first) error = %v", err)
	}
	if len(first.Lines) != 1 || first.Lines[0].Sequence != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	docker.mu.Lock()
	docker.logs["container-1"] = "2026-06-13T09:00:00Z replacement\n"
	docker.mu.Unlock()

	req.Cursor = first.NextCursor
	second, err := manager.FetchLogPage(context.Background(), req)
	if err != nil {
		t.Fatalf("FetchLogPage(second) error = %v", err)
	}
	if len(second.Lines) != 1 || second.Lines[0].Text != "duplicate" || second.Lines[0].Sequence != 2 || second.NextCursor == "" {
		t.Fatalf("second page = %#v", second)
	}
	req.Cursor = second.NextCursor
	third, err := manager.FetchLogPage(context.Background(), req)
	if err != nil {
		t.Fatalf("FetchLogPage(third) error = %v", err)
	}
	if len(third.Lines) != 1 || third.Lines[0].Text != "duplicate" || third.Lines[0].Sequence != 3 || third.NextCursor != "" {
		t.Fatalf("third page = %#v", third)
	}
	docker.mu.Lock()
	requests := len(docker.requests)
	docker.mu.Unlock()
	if requests != 1 {
		t.Fatalf("Docker log requests = %d, want one immutable snapshot read", requests)
	}
	manager.mu.Lock()
	remainingSnapshots := len(manager.pageSnapshots)
	retainedBytes := manager.pageSnapshotBytesInUse
	manager.mu.Unlock()
	if remainingSnapshots != 0 || retainedBytes != 0 {
		t.Fatalf("completed snapshot retained: count=%d bytes=%d", remainingSnapshots, retainedBytes)
	}
	if _, err := manager.FetchLogPage(context.Background(), req); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("reused completed cursor error = %v, want %s", err, apperror.PlanExpired)
	}
}

func TestFetchLogPageRejectsInvalidAndCrossScopeCursors(t *testing.T) {
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "one\ntwo\n"
	manager := NewManager(docker, nil, Options{})
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Cursor: "not-a-cursor", Limit: 1,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("invalid cursor error = %v, want %s", err, apperror.Conflict)
	}
	first, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: 1,
	})
	if err != nil {
		t.Fatalf("FetchLogPage(first) error = %v", err)
	}
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Cursor: first.NextCursor, Limit: 1,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("cross-scope cursor error = %v, want %s", err, apperror.Conflict)
	}
	tampered := first.NextCursor
	if tampered[0] == 'A' {
		tampered = "B" + tampered[1:]
	} else {
		tampered = "A" + tampered[1:]
	}
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Cursor: tampered, Limit: 1,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("tampered cursor error = %v, want %s", err, apperror.Conflict)
	}
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: maximumPageLimit + 1,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("oversized page error = %v, want %s", err, apperror.Conflict)
	}
}

func TestFetchLogPageReclaimsExpiredSnapshotWhileIdle(t *testing.T) {
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "one\ntwo\n"
	manager := NewManager(docker, nil, Options{PageSnapshotTTL: 20 * time.Millisecond})
	first, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: 1,
	})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("FetchLogPage(first) = %#v, %v", first, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		count := len(manager.pageSnapshots)
		bytes := manager.pageSnapshotBytesInUse
		manager.mu.Unlock()
		if count == 0 && bytes == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle snapshot was not reclaimed: count=%d bytes=%d", count, bytes)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFetchLogPageRejectsScopeBeyondReaderLimit(t *testing.T) {
	docker := newFakeLogDocker()
	containers := make([]models.ContainerSummary, 3)
	for index := range containers {
		containers[index] = docker.containers[0]
		containers[index].ID = fmt.Sprintf("container-%d", index+1)
		containers[index].Name = fmt.Sprintf("app-%d", index+1)
	}
	docker.setContainers(containers)
	manager := NewManager(docker, nil, Options{MaxReaders: 4, MaxReadersPerStream: 2})
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeAll, Limit: 1,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("oversized scope error = %v, want %s", err, apperror.Conflict)
	}
	docker.mu.Lock()
	requests := len(docker.requests)
	docker.mu.Unlock()
	if requests != 0 {
		t.Fatalf("oversized scope opened %d Docker readers", requests)
	}
}

func TestOneShotRequestsRejectMalformedServiceScope(t *testing.T) {
	docker := newFakeLogDocker()
	manager := NewManager(docker, nil, Options{ExportDirectory: t.TempDir()})

	t.Run("fetch", func(t *testing.T) {
		if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
			Scope: ScopeService, IDs: []string{"::"}, Limit: 1,
		}); !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("FetchLogPage(malformed service) error = %v, want %s", err, apperror.Conflict)
		}
	})
	t.Run("export", func(t *testing.T) {
		if _, err := manager.ExportLogs(context.Background(), models.ExportLogsRequest{
			Scope: ScopeService, IDs: []string{"::"}, Format: "jsonl",
		}); !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("ExportLogs(malformed service) error = %v, want %s", err, apperror.Conflict)
		}
	})
	docker.mu.Lock()
	requests := len(docker.requests)
	docker.mu.Unlock()
	if requests != 0 {
		t.Fatalf("malformed service scope opened %d Docker readers", requests)
	}
}

func TestLogScopeBoundsRawIdentifiersBeforeDeduplication(t *testing.T) {
	docker := newFakeLogDocker()
	manager := NewManager(docker, nil, Options{MaxReaders: 2, MaxReadersPerStream: 2})
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeContainer,
		IDs:   []string{"container-1", "container-1", ""},
		Limit: 1,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("duplicate identifier flood error = %v, want %s", err, apperror.Conflict)
	}
	docker.mu.Lock()
	requests := len(docker.requests)
	docker.mu.Unlock()
	if requests != 0 {
		t.Fatalf("duplicate identifier flood opened %d Docker readers", requests)
	}
}

func TestFetchLogPageSupportsNameOnlyContainerAndRejectsNilReader(t *testing.T) {
	docker := newFakeLogDocker()
	nameOnly := docker.containers[0]
	nameOnly.ID = ""
	nameOnly.Name = "name-only"
	docker.setContainers([]models.ContainerSummary{nameOnly})
	docker.logs["name-only"] = "name-only log\n"
	manager := NewManager(docker, nil, Options{})
	page, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: 1,
	})
	if err != nil || len(page.Lines) != 1 || page.Lines[0].Text != "name-only log" {
		t.Fatalf("name-only page = %#v, error = %v", page, err)
	}

	nilDocker := newScriptedLogDocker(logReaderStep{})
	nilManager := NewManager(nilDocker, nil, Options{})
	if _, err := nilManager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeAll, Limit: 1,
	}); !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("nil reader error = %v, want %s", err, apperror.Internal)
	}
}

func TestOneShotReadersShareGlobalReaderCapacity(t *testing.T) {
	docker := newFakeLogDocker()
	reader := newBlockingLogReader()
	docker.readers["container-1"] = reader
	manager := NewManager(docker, nil, Options{MaxReaders: 1, MaxReadersPerStream: 1})
	if _, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true,
	}); err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	reader.waitStarted(t)
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Limit: 1,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("shared reader capacity error = %v, want %s", err, apperror.Conflict)
	}
	if diagnostics := manager.Diagnostics(); diagnostics.ReservedReaders != 1 {
		t.Fatalf("reserved readers = %d, want existing stream reader only", diagnostics.ReservedReaders)
	}
	manager.StopAll()
}

func TestFetchLogPageExpiresAndBoundsSnapshots(t *testing.T) {
	now := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "one\ntwo\nthree\n"
	manager := NewManager(docker, nil, Options{
		Now:               func() time.Time { return now },
		PageSnapshotTTL:   time.Second,
		PageSnapshotLines: 2,
		PageSnapshotBytes: 1 << 20,
	})
	first, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: 1,
	})
	if err != nil {
		t.Fatalf("FetchLogPage(first) error = %v", err)
	}
	if !first.Truncated || first.NextCursor == "" {
		t.Fatalf("bounded first page = %#v", first)
	}
	now = now.Add(2 * time.Second)
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Cursor: first.NextCursor, Limit: 1,
	}); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("expired cursor error = %v, want %s", err, apperror.PlanExpired)
	}
	manager.mu.Lock()
	remainingSnapshots := len(manager.pageSnapshots)
	retainedBytes := manager.pageSnapshotBytesInUse
	manager.mu.Unlock()
	if remainingSnapshots != 0 || retainedBytes != 0 {
		t.Fatalf("expired snapshot retained: count=%d bytes=%d", remainingSnapshots, retainedBytes)
	}
}

func TestFetchLogPageEvictsOldestSnapshotAtCapacity(t *testing.T) {
	docker := newFakeLogDocker()
	firstContainer := docker.containers[0]
	secondContainer := firstContainer
	secondContainer.ID = "container-2"
	secondContainer.Name = "app-2"
	secondContainer.ProjectID = "linux_native/other"
	docker.setContainers([]models.ContainerSummary{firstContainer, secondContainer})
	docker.logs["container-1"] = "first-one\nfirst-two\n"
	docker.logs["container-2"] = "second-one\nsecond-two\n"
	manager := NewManager(docker, nil, Options{MaxPageSnapshots: 1})

	first, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: 1,
	})
	if err != nil {
		t.Fatalf("FetchLogPage(first snapshot) error = %v", err)
	}
	second, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/other"}, Limit: 1,
	})
	if err != nil {
		t.Fatalf("FetchLogPage(second snapshot) error = %v", err)
	}
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Cursor: first.NextCursor, Limit: 1,
	}); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("evicted cursor error = %v, want %s", err, apperror.PlanExpired)
	}
	last, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/other"}, Cursor: second.NextCursor, Limit: 1,
	})
	if err != nil {
		t.Fatalf("FetchLogPage(retained snapshot) error = %v", err)
	}
	if len(last.Lines) != 1 || last.Lines[0].Text != "second-two" {
		t.Fatalf("retained snapshot page = %#v", last)
	}
}

func TestStopAllClearsPageSnapshots(t *testing.T) {
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "one\ntwo\n"
	manager := NewManager(docker, nil, Options{})
	first, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: 1,
	})
	if err != nil {
		t.Fatalf("FetchLogPage(first) error = %v", err)
	}
	if first.NextCursor == "" || manager.Diagnostics().RetainedBytes == 0 {
		t.Fatalf("page snapshot was not retained: %#v", first)
	}
	manager.StopAll()
	manager.mu.Lock()
	snapshotCount := len(manager.pageSnapshots)
	snapshotBytes := manager.pageSnapshotBytesInUse
	manager.mu.Unlock()
	if snapshotCount != 0 || snapshotBytes != 0 {
		t.Fatalf("snapshots after StopAll: count=%d bytes=%d", snapshotCount, snapshotBytes)
	}
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Cursor: first.NextCursor, Limit: 1,
	}); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("cursor after StopAll error = %v, want %s", err, apperror.ProviderNotReady)
	}
}

func TestOneShotOperationCapacityIsBounded(t *testing.T) {
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "one\n"
	blocked := make(chan struct{})
	docker.blockLogs = blocked
	docker.logsCalled = make(chan struct{})
	manager := NewManager(docker, nil, Options{MaxOperations: 1})
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
			Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: 1,
		})
		firstDone <- err
	}()
	select {
	case <-docker.logsCalled:
	case <-time.After(time.Second):
		close(blocked)
		t.Fatal("first log fetch did not reach Docker")
	}
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Limit: 1,
	}); !apperror.IsCode(err, apperror.Conflict) {
		close(blocked)
		t.Fatalf("capacity error = %v, want %s", err, apperror.Conflict)
	}
	close(blocked)
	if err := <-firstDone; err != nil {
		t.Fatalf("first fetch error = %v", err)
	}
	if diagnostics := manager.Diagnostics(); diagnostics.ActiveOperations != 0 {
		t.Fatalf("active operations after completion = %d", diagnostics.ActiveOperations)
	}
}

func TestExportLogsIsBoundedPrivateAndAtomic(t *testing.T) {
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "one\ntwo\nthree\n"
	directory := t.TempDir()
	unrelated := filepath.Join(directory, "existing.jsonl")
	if err := os.WriteFile(unrelated, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(old export) error = %v", err)
	}
	manager := NewManager(docker, nil, Options{
		ExportLines: 2, ExportBytes: 1 << 20, ExportDirectory: directory,
	})

	result, err := manager.ExportLogs(context.Background(), models.ExportLogsRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Format: "jsonl",
	})
	if err != nil {
		t.Fatalf("ExportLogs() error = %v", err)
	}
	if !result.Truncated || result.LineCount != 2 || result.Bytes <= 0 || result.DurabilityWarning != "" {
		t.Fatalf("ExportLogs() result = %#v", result)
	}
	if filepath.Dir(result.Path) != directory || filepath.Ext(result.Path) != ".jsonl" || result.Path == unrelated {
		t.Fatalf("ExportLogs() destination = %q, want unique JSONL inside %q", result.Path, directory)
	}
	content, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile(export) error = %v", err)
	}
	if bytes := strings.Count(string(content), "\n"); bytes != 2 || strings.Contains(string(content), "three") || strings.Contains(string(content), "old") {
		t.Fatalf("bounded export content = %q", content)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(result.Path)
		if err != nil {
			t.Fatalf("Stat(export) error = %v", err)
		}
		if permissions := info.Mode().Perm(); permissions != 0o600 {
			t.Fatalf("export permissions = %o, want 600", permissions)
		}
		directoryInfo, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("Stat(export directory) error = %v", err)
		}
		if permissions := directoryInfo.Mode().Perm(); permissions != 0o700 {
			t.Fatalf("export directory permissions = %o, want 700", permissions)
		}
	}
	if preserved, err := os.ReadFile(unrelated); err != nil || string(preserved) != "old\n" {
		t.Fatalf("unrelated destination changed: content=%q error=%v", preserved, err)
	}
	temporary, err := filepath.Glob(filepath.Join(directory, ".cairn-logs-*.tmp"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("temporary exports = %v, error = %v", temporary, err)
	}
}

func TestExportLogsCreatesDurablePrivateDirectoryChain(t *testing.T) {
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "one\n"
	directory := filepath.Join(t.TempDir(), "Cairn", "exports")
	manager := NewManager(docker, nil, Options{ExportDirectory: directory})
	result, err := manager.ExportLogs(context.Background(), models.ExportLogsRequest{
		Scope: ScopeAll, Format: "jsonl",
	})
	if err != nil {
		t.Fatalf("ExportLogs() error = %v", err)
	}
	if filepath.Dir(result.Path) != directory || result.DurabilityWarning != "" {
		t.Fatalf("ExportLogs() result = %#v", result)
	}
	if info, err := os.Stat(result.Path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("export file info = %v, error = %v", info, err)
	}
}

func TestPlainTextExportAttributesEveryLineToItsSource(t *testing.T) {
	docker := newFakeLogDocker()
	second := docker.containers[0]
	second.ID = "container-2"
	second.Name = "worker-1"
	second.Service = "worker"
	docker.setContainers([]models.ContainerSummary{docker.containers[0], second})
	docker.logs["container-1"] = "web line\n"
	docker.logs["container-2"] = "worker line\n"
	manager := NewManager(docker, nil, Options{ExportDirectory: t.TempDir()})
	result, err := manager.ExportLogs(context.Background(), models.ExportLogsRequest{
		Scope: ScopeAll, Format: "log",
	})
	if err != nil {
		t.Fatalf("ExportLogs() error = %v", err)
	}
	content, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile(export) error = %v", err)
	}
	text := string(content)
	for _, expected := range []string{
		`container="app-1" container_id="container-1" service="app" web line`,
		`container="worker-1" container_id="container-2" service="worker" worker line`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("plain export missing %q: %q", expected, text)
		}
	}
}

func TestExportLogsFailureLeavesNoTemporaryFile(t *testing.T) {
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "one\n"
	directory := t.TempDir()
	invalidDirectory := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(invalidDirectory, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("WriteFile(export directory) error = %v", err)
	}
	manager := NewManager(docker, nil, Options{ExportDirectory: invalidDirectory})
	if _, err := manager.ExportLogs(context.Background(), models.ExportLogsRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Format: "jsonl",
	}); err == nil {
		t.Fatal("ExportLogs() error = nil, want private directory failure")
	}
	if content, err := os.ReadFile(invalidDirectory); err != nil || string(content) != "preserve" {
		t.Fatalf("original export location changed: content=%q error=%v", content, err)
	}
	temporary, err := filepath.Glob(filepath.Join(directory, ".cairn-logs-*.tmp"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("temporary exports = %v, error = %v", temporary, err)
	}
}

func TestExportLogsRejectsUnsupportedFormatBeforeReadingDocker(t *testing.T) {
	docker := newFakeLogDocker()
	manager := NewManager(docker, nil, Options{ExportDirectory: t.TempDir()})
	if _, err := manager.ExportLogs(context.Background(), models.ExportLogsRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Format: "csv",
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ExportLogs(csv) error = %v, want %s", err, apperror.Conflict)
	}
	docker.mu.Lock()
	requests := len(docker.requests)
	docker.mu.Unlock()
	if requests != 0 {
		t.Fatalf("Docker log requests = %d, want 0", requests)
	}
}

func TestExportLogsTimeoutDoesNotPublishPartialTarget(t *testing.T) {
	docker := &contextBlockingLogDocker{fakeLogDocker: newFakeLogDocker()}
	directory := t.TempDir()
	manager := NewManager(docker, nil, Options{ExportTimeout: 20 * time.Millisecond, ExportDirectory: directory})
	if _, err := manager.ExportLogs(context.Background(), models.ExportLogsRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Format: "jsonl",
	}); !apperror.IsCode(err, apperror.Timeout) {
		t.Fatalf("ExportLogs(timeout) error = %v, want %s", err, apperror.Timeout)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial export entries = %v, error = %v", entries, err)
	}
	if diagnostics := manager.Diagnostics(); diagnostics.ActiveOperations != 0 {
		t.Fatalf("active operations after timeout = %d", diagnostics.ActiveOperations)
	}
}

func TestExportTimeoutClosesReaderThatRequiresCloseToUnblock(t *testing.T) {
	docker := newFakeLogDocker()
	reader := newBlockingLogReader()
	docker.readers["container-1"] = reader
	manager := NewManager(docker, nil, Options{ExportTimeout: 20 * time.Millisecond, ExportDirectory: t.TempDir()})
	started := time.Now()
	if _, err := manager.ExportLogs(context.Background(), models.ExportLogsRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Format: "jsonl",
	}); !apperror.IsCode(err, apperror.Timeout) {
		t.Fatalf("ExportLogs(timeout) error = %v, want %s", err, apperror.Timeout)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("ExportLogs(timeout) returned after %s", elapsed)
	}
	select {
	case <-reader.closed:
	case <-time.After(time.Second):
		t.Fatal("timeout did not close the Docker reader")
	}
	waitForLogDiagnostics(t, manager, func(value models.LogRuntimeDiagnostics) bool {
		return value.ActiveOperations == 0 && value.ReservedReaders == 0
	})
}

func TestBlockingOneShotCloseRetainsCapacityWithoutBlockingCaller(t *testing.T) {
	docker := newFakeLogDocker()
	reader := newBlockingCloseLogReader()
	docker.readers["container-1"] = reader
	manager := NewManager(docker, nil, Options{ExportTimeout: 20 * time.Millisecond, ExportDirectory: t.TempDir()})
	if _, err := manager.ExportLogs(context.Background(), models.ExportLogsRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Format: "jsonl",
	}); !apperror.IsCode(err, apperror.Timeout) {
		t.Fatalf("ExportLogs(timeout) error = %v, want %s", err, apperror.Timeout)
	}
	reader.waitCloseStarted(t)
	if diagnostics := manager.Diagnostics(); diagnostics.ActiveOperations != 1 || diagnostics.ReservedReaders != 1 {
		t.Fatalf("blocked close diagnostics = %#v", diagnostics)
	}
	reader.releaseClose()
	waitForLogDiagnostics(t, manager, func(value models.LogRuntimeDiagnostics) bool {
		return value.ActiveOperations == 0 && value.ReservedReaders == 0
	})
}

func TestNonCooperativeContainerResolutionReturnsAtDeadlineAndRetainsCapacity(t *testing.T) {
	base := newFakeLogDocker()
	docker := &blockingResolverLogDocker{
		fakeLogDocker: base,
		started:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	manager := NewManager(docker, nil, Options{FetchTimeout: 20 * time.Millisecond})
	released := false
	defer func() {
		if !released {
			close(docker.release)
		}
	}()

	startedAt := time.Now()
	if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
		Scope: ScopeAll, Limit: 1,
	}); !apperror.IsCode(err, apperror.Timeout) {
		t.Fatalf("FetchLogPage(blocked resolution) error = %v, want %s", err, apperror.Timeout)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("FetchLogPage(blocked resolution) returned after %s", elapsed)
	}
	select {
	case <-docker.started:
	default:
		t.Fatal("container resolution did not start")
	}
	if diagnostics := manager.Diagnostics(); diagnostics.ActiveOperations != 1 || diagnostics.ReservedReaders != 0 {
		t.Fatalf("blocked resolution diagnostics = %#v", diagnostics)
	}
	close(docker.release)
	released = true
	waitForLogDiagnostics(t, manager, func(value models.LogRuntimeDiagnostics) bool {
		return value.ActiveOperations == 0 && value.ReservedReaders == 0
	})
}

func TestCooperativeContainerResolutionPreservesTimeoutTaxonomy(t *testing.T) {
	docker := &deadlineResolverLogDocker{fakeLogDocker: newFakeLogDocker()}
	// Keep admission out of this taxonomy test. A timed-out resolver is joined
	// asynchronously, so scheduler lag may briefly retain otherwise cooperative
	// operations after their caller has returned.
	manager := NewManager(docker, nil, Options{FetchTimeout: time.Millisecond, MaxOperations: 32})
	for attempt := 0; attempt < 20; attempt++ {
		if _, err := manager.FetchLogPage(context.Background(), models.LogPageRequest{
			Scope: ScopeAll, Limit: 1,
		}); !apperror.IsCode(err, apperror.Timeout) {
			t.Fatalf("attempt %d error = %v, want %s", attempt, err, apperror.Timeout)
		}
	}
	waitForLogDiagnostics(t, manager, func(value models.LogRuntimeDiagnostics) bool {
		return value.ActiveOperations == 0 && value.ReservedReaders == 0
	})
}

type deadlineResolverLogDocker struct {
	*fakeLogDocker
}

func (d *deadlineResolverLogDocker) ListContainers(ctx context.Context, _ models.ContainerListOptions) ([]models.ContainerSummary, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type blockingResolverLogDocker struct {
	*fakeLogDocker
	started chan struct{}
	release chan struct{}
}

func (d *blockingResolverLogDocker) ListContainers(_ context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	select {
	case <-d.started:
	default:
		close(d.started)
	}
	<-d.release
	return d.fakeLogDocker.ListContainers(context.Background(), opts)
}

type contextBlockingLogDocker struct {
	fakeLogDocker *fakeLogDocker
}

func (d *contextBlockingLogDocker) ContainerLogs(ctx context.Context, _ string, _ dockercore.LogOptions) (io.ReadCloser, error) {
	return &contextBlockingReadCloser{ctx: ctx}, nil
}

func (d *contextBlockingLogDocker) ListContainers(ctx context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	return d.fakeLogDocker.ListContainers(ctx, opts)
}

func (d *contextBlockingLogDocker) GetContainer(ctx context.Context, id string) (*models.ContainerDetail, error) {
	return d.fakeLogDocker.GetContainer(ctx, id)
}

type contextBlockingReadCloser struct {
	ctx context.Context
}

func (r *contextBlockingReadCloser) Read(_ []byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r *contextBlockingReadCloser) Close() error {
	return nil
}
