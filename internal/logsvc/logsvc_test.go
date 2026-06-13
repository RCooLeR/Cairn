package logsvc

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
)

func TestReadDockerLogStreamDemuxesAndDetectsLevels(t *testing.T) {
	now := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	input := bytes.NewBuffer(nil)
	input.Write(dockerLogFrame(1, "2026-06-13T09:00:00.000000001Z INFO booted\n"))
	input.Write(dockerLogFrame(2, "2026-06-13T09:00:00.000000002Z {\"level\":\"error\",\"msg\":\"failed\"}\n"))
	var lines []models.LogLine

	err := ReadDockerLogStream(context.Background(), input, sourceInfo{
		ContainerID:   "container-1",
		ContainerName: "web-1",
		Service:       "web",
	}, func() time.Time { return now }, func(line models.LogLine) bool {
		lines = append(lines, line)
		return true
	})
	if err != nil {
		t.Fatalf("ReadDockerLogStream() error = %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
	if lines[0].Stream != "stdout" || lines[0].Level != "info" || lines[0].Text != "INFO booted" {
		t.Fatalf("stdout line = %#v", lines[0])
	}
	if lines[1].Stream != "stderr" || lines[1].Level != "error" || !strings.Contains(lines[1].Text, "failed") {
		t.Fatalf("stderr line = %#v", lines[1])
	}
	if lines[0].ContainerName != "web-1" || lines[0].Service != "web" {
		t.Fatalf("source = %#v", lines[0])
	}
}

func TestReadDockerLogStreamPlainTTYUsesNowForUntimestampedLines(t *testing.T) {
	now := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	var lines []models.LogLine

	err := ReadDockerLogStream(context.Background(), strings.NewReader("WARN from tty\nplain\n"), sourceInfo{}, func() time.Time { return now }, func(line models.LogLine) bool {
		lines = append(lines, line)
		return true
	})
	if err != nil {
		t.Fatalf("ReadDockerLogStream() error = %v", err)
	}
	if len(lines) != 2 || lines[0].Level != "warn" || !lines[1].TS.Equal(now) {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestRingDropsOldestAndCursorPages(t *testing.T) {
	ring := newRingBuffer(2)
	base := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	ring.add(models.LogLine{TS: base, ContainerID: "a", Stream: "stdout", Text: "one"})
	ring.add(models.LogLine{TS: base.Add(time.Second), ContainerID: "a", Stream: "stdout", Text: "two"})
	ring.add(models.LogLine{TS: base.Add(2 * time.Second), ContainerID: "a", Stream: "stdout", Text: "three"})

	lines := ring.snapshot()
	if ring.dropped != 1 || len(lines) != 2 || lines[0].Text != "two" {
		t.Fatalf("ring dropped=%d lines=%#v", ring.dropped, lines)
	}
	page := pageLines(lines, "", 1)
	if len(page.Lines) != 1 || page.Lines[0].Text != "two" || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	next := pageLines(lines, page.NextCursor, 1)
	if len(next.Lines) != 1 || next.Lines[0].Text != "three" {
		t.Fatalf("next page = %#v", next)
	}
}

func TestManagerPublishesBatchesAndEOF(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	linesCh := eventBus.Subscribe(ctx, bus.TopicLogsLines, 8)
	eofCh := eventBus.Subscribe(ctx, bus.TopicLogsEOF, 8)
	docker := newFakeLogDocker()
	docker.logs["container-1"] = string(dockerLogFrame(1, "2026-06-13T09:00:00Z INFO one\n")) +
		string(dockerLogFrame(1, "2026-06-13T09:00:01Z WARN two\n"))
	manager := NewManager(docker, eventBus, Options{BatchWindow: time.Millisecond, BatchMaxLines: 2})

	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:      ScopeContainer,
		IDs:        []string{"container-1"},
		Tail:       10,
		Timestamps: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	lines := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	if lines.StreamID != streamID || len(lines.Lines) != 2 || lines.Lines[1].Level != "warn" {
		t.Fatalf("lines payload = %#v", lines)
	}
	eof := receiveLogEvent[EOFPayload](t, eofCh, time.Second)
	if eof.StreamID != streamID {
		t.Fatalf("eof = %#v", eof)
	}
	manager.mu.Lock()
	sessionCount := len(manager.sessions)
	manager.mu.Unlock()
	if sessionCount != 0 {
		t.Fatalf("sessions still registered = %d", sessionCount)
	}
}

func TestSessionEnqueueBackpressuresInsteadOfDropping(t *testing.T) {
	manager := NewManager(nil, nil, Options{InputBuffer: 1})
	s := newSession(manager, "stream-1", models.LogStreamRequest{})
	first := models.LogLine{Text: "one"}
	second := models.LogLine{Text: "two"}

	if !s.enqueue(first) {
		t.Fatal("first enqueue returned false")
	}
	done := make(chan bool, 1)
	go func() {
		done <- s.enqueue(second)
	}()

	select {
	case ok := <-done:
		t.Fatalf("second enqueue returned before capacity was available: %v", ok)
	case <-time.After(25 * time.Millisecond):
	}

	if got := <-s.input; got.Text != "one" {
		t.Fatalf("first line was dropped: %#v", got)
	}
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("second enqueue returned false")
		}
	case <-time.After(time.Second):
		t.Fatal("second enqueue did not resume after capacity was available")
	}
	if got := <-s.input; got.Text != "two" {
		t.Fatalf("second line was not queued: %#v", got)
	}
	if dropped := s.dropped.Load(); dropped != 0 {
		t.Fatalf("dropped count = %d", dropped)
	}
}

func TestManagerFetchPageAndExport(t *testing.T) {
	ctx := context.Background()
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "2026-06-13T09:00:00Z INFO one\n2026-06-13T09:00:01Z ERROR two\n"
	manager := NewManager(docker, nil, Options{})

	page, err := manager.FetchLogPage(ctx, models.LogPageRequest{
		Scope: ScopeProject,
		IDs:   []string{"linux_native/app"},
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("FetchLogPage() error = %v", err)
	}
	if len(page.Lines) != 1 || page.Lines[0].Text != "INFO one" || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	next, err := manager.FetchLogPage(ctx, models.LogPageRequest{
		Scope:  ScopeProject,
		IDs:    []string{"linux_native/app"},
		Cursor: page.NextCursor,
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("FetchLogPage(next) error = %v", err)
	}
	if len(next.Lines) != 1 || next.Lines[0].Level != "error" {
		t.Fatalf("next = %#v", next)
	}

	exportPath := filepath.Join(t.TempDir(), "logs.jsonl")
	result, err := manager.ExportLogs(ctx, models.ExportLogsRequest{
		Scope: ScopeProject,
		IDs:   []string{"linux_native/app"},
		Path:  exportPath,
	})
	if err != nil {
		t.Fatalf("ExportLogs() error = %v", err)
	}
	content, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if result.LineCount != 2 || !bytes.Contains(content, []byte(`"level":"error"`)) {
		t.Fatalf("result = %#v content = %s", result, content)
	}
}

func dockerLogFrame(stream byte, payload string) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}

type fakeLogDocker struct {
	mu         sync.Mutex
	containers []models.ContainerSummary
	logs       map[string]string
	requests   []dockercore.LogOptions
}

func newFakeLogDocker() *fakeLogDocker {
	return &fakeLogDocker{
		containers: []models.ContainerSummary{{
			ID:        "container-1",
			Name:      "app-1",
			Image:     "cairn/app:latest",
			Status:    "running",
			State:     "running",
			ProjectID: "linux_native/app",
			Service:   "app",
			CreatedAt: time.Date(2026, 6, 13, 8, 0, 0, 0, time.UTC),
		}},
		logs: map[string]string{},
	}
}

func (f *fakeLogDocker) ContainerLogs(_ context.Context, id string, opts dockercore.LogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, opts)
	return io.NopCloser(strings.NewReader(f.logs[id])), nil
}

func (f *fakeLogDocker) ListContainers(_ context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	containers := make([]models.ContainerSummary, 0, len(f.containers))
	for _, container := range f.containers {
		if opts.ProjectID != "" && container.ProjectID != opts.ProjectID {
			continue
		}
		if opts.Service != "" && container.Service != opts.Service {
			continue
		}
		containers = append(containers, container)
	}
	return containers, nil
}

func (f *fakeLogDocker) GetContainer(_ context.Context, id string) (*models.ContainerDetail, error) {
	for _, container := range f.containers {
		if container.ID == id || container.Name == id {
			return &models.ContainerDetail{Summary: container}, nil
		}
	}
	return nil, nil
}

func receiveLogEvent[T any](t *testing.T, events <-chan bus.Event, timeout time.Duration) T {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case event := <-events:
		payload, ok := event.Payload.(T)
		if !ok {
			t.Fatalf("payload = %#v", event.Payload)
		}
		return payload
	case <-timer.C:
		var zero T
		t.Fatalf("timed out waiting for event")
		return zero
	}
}
