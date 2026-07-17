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

func TestReadDockerLogStreamBoundsFragmentedFramedRecords(t *testing.T) {
	input := bytes.NewBuffer(nil)
	exact := strings.Repeat("a", maxDockerLogRecordBytes)
	input.Write(dockerLogFrame(1, exact+"\n"))
	input.Write(dockerLogFrame(2, strings.Repeat("b", maxDockerLogRecordBytes/2)))
	input.Write(dockerLogFrame(2, strings.Repeat("b", maxDockerLogRecordBytes/2+128)))
	input.Write(dockerLogFrame(2, "\nstderr-next\n"))
	input.Write(dockerLogFrame(1, strings.Repeat("c", maxDockerLogRecordBytes/2)))
	input.Write(dockerLogFrame(1, strings.Repeat("c", maxDockerLogRecordBytes)))
	var lines []models.LogLine

	err := ReadDockerLogStream(context.Background(), input, sourceInfo{}, time.Now, func(line models.LogLine) bool {
		lines = append(lines, line)
		return true
	})
	if err != nil {
		t.Fatalf("ReadDockerLogStream() error = %v", err)
	}
	if len(lines) != 4 {
		t.Fatalf("lines count = %d, want 4: %#v", len(lines), lines)
	}
	if lines[0].Text != exact || lines[0].Truncated {
		t.Fatalf("exact-limit line = len %d truncated %v", len(lines[0].Text), lines[0].Truncated)
	}
	for _, index := range []int{1, 3} {
		line := lines[index]
		if !line.Truncated || len(line.Text) != maxDockerLogRecordBytes+len(truncatedRecordSuffix) || !strings.HasSuffix(line.Text, truncatedRecordSuffix) {
			t.Fatalf("truncated line %d = len %d truncated %v suffix %v", index, len(line.Text), line.Truncated, strings.HasSuffix(line.Text, truncatedRecordSuffix))
		}
	}
	if lines[1].Stream != "stderr" || lines[2].Text != "stderr-next" || lines[2].Truncated {
		t.Fatalf("stderr recovery lines = %#v", lines[1:3])
	}
}

func TestReadDockerLogStreamPlainRecordsUseTheSameBoundedSemantics(t *testing.T) {
	exact := strings.Repeat("x", maxDockerLogRecordBytes)
	oversized := strings.Repeat("y", maxDockerLogRecordBytes+4096)
	input := strings.NewReader(exact + "\n" + oversized + "\nnext\n")
	var lines []models.LogLine

	err := ReadDockerLogStream(context.Background(), input, sourceInfo{}, time.Now, func(line models.LogLine) bool {
		lines = append(lines, line)
		return true
	})
	if err != nil {
		t.Fatalf("ReadDockerLogStream() error = %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("lines count = %d, want 3", len(lines))
	}
	if lines[0].Text != exact || lines[0].Truncated {
		t.Fatalf("exact-limit plain line = len %d truncated %v", len(lines[0].Text), lines[0].Truncated)
	}
	if !lines[1].Truncated || !strings.HasSuffix(lines[1].Text, truncatedRecordSuffix) {
		t.Fatalf("oversized plain line = len %d truncated %v", len(lines[1].Text), lines[1].Truncated)
	}
	if lines[2].Text != "next" || lines[2].Truncated {
		t.Fatalf("plain recovery line = %#v", lines[2])
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

func TestRingCursorPaginatesByteIdenticalLinesExactlyOnce(t *testing.T) {
	ring := newRingBuffer(10)
	timestamp := time.Date(2026, 6, 13, 9, 0, 0, 123, time.UTC)
	for range 7 {
		ring.add(models.LogLine{TS: timestamp, ContainerID: "same", Stream: "stdout", Text: "duplicate"})
	}
	lines := ring.snapshot()
	SortLines(lines)

	var sequences []uint64
	cursor := ""
	for {
		page := pageLines(lines, cursor, 2)
		for _, line := range page.Lines {
			sequences = append(sequences, line.Sequence)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	if len(sequences) != 7 {
		t.Fatalf("paginated sequences = %v, want seven records", sequences)
	}
	for index, sequence := range sequences {
		if sequence != uint64(index+1) {
			t.Fatalf("paginated sequences = %v, want [1 2 3 4 5 6 7]", sequences)
		}
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

func TestStopStreamClosesBlockingLogReader(t *testing.T) {
	ctx := context.Background()
	docker := newFakeLogDocker()
	reader := newBlockingLogReader()
	docker.readers["container-1"] = reader
	manager := NewManager(docker, nil, Options{BatchWindow: time.Millisecond})

	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:      ScopeContainer,
		IDs:        []string{"container-1"},
		Follow:     true,
		Tail:       10,
		Timestamps: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("log reader was not consumed")
	}
	if got := manager.Diagnostics(); got.ActiveStreams != 1 || got.ActiveProducers != 1 {
		t.Fatalf("diagnostics while streaming = %#v", got)
	}
	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
	select {
	case <-reader.closed:
	default:
		t.Fatal("StopStream did not close active log reader")
	}
	if got := manager.Diagnostics(); got.ActiveStreams != 0 || got.ActiveProducers != 0 {
		t.Fatalf("diagnostics after stop = %#v", got)
	}
}

func TestProjectFollowIgnoresEmptyContainerSetOnObjectChange(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	errorCh := eventBus.Subscribe(ctx, bus.TopicLogsError, 8)
	docker := newFakeLogDocker()
	reader := newBlockingLogReader()
	docker.readers["container-1"] = reader
	manager := NewManager(docker, eventBus, Options{BatchWindow: time.Millisecond})

	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:  ScopeProject,
		IDs:    []string{"linux_native/app"},
		Follow: true,
		Tail:   10,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.StopStream(streamID) })
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("log reader was not consumed")
	}

	docker.setContainers(nil)
	eventBus.Publish(bus.Event{Topic: bus.TopicObjectsChanged})

	select {
	case event := <-errorCh:
		t.Fatalf("unexpected logs:error after empty rescan: %#v", event.Payload)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStopStreamClosesReaderReturnedAfterStop(t *testing.T) {
	ctx := context.Background()
	docker := newFakeLogDocker()
	reader := newBlockingLogReader()
	docker.readers["container-1"] = reader
	blockLogs := make(chan struct{})
	logsCalled := make(chan struct{})
	docker.blockLogs = blockLogs
	docker.logsCalled = logsCalled
	manager := NewManager(docker, nil, Options{BatchWindow: time.Millisecond})

	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:  ScopeContainer,
		IDs:    []string{"container-1"},
		Follow: true,
		Tail:   10,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	select {
	case <-logsCalled:
	case <-time.After(time.Second):
		t.Fatal("ContainerLogs was not called")
	}

	stopped := make(chan error, 1)
	go func() {
		stopped <- manager.StopStream(streamID)
	}()
	close(blockLogs)
	select {
	case <-reader.closed:
	case <-time.After(time.Second):
		t.Fatal("reader returned after stop was not closed")
	}
	if err := <-stopped; err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
}

func TestSessionEnqueueDropsAndReportsWhenInputFull(t *testing.T) {
	manager := NewManager(nil, nil, Options{InputBuffer: 1})
	s := newSession(manager, "stream-1", models.LogStreamRequest{})
	first := models.LogLine{Text: "one"}
	second := models.LogLine{Text: "two"}

	if !s.enqueue(first) {
		t.Fatal("first enqueue returned false")
	}
	if !s.enqueue(second) {
		t.Fatal("second enqueue returned false")
	}

	if got := <-s.input; got.Text != "one" {
		t.Fatalf("first line was dropped: %#v", got)
	}
	select {
	case got := <-s.input:
		t.Fatalf("unexpected queued overflow line: %#v", got)
	default:
	}
	if dropped := s.dropped.Load(); dropped != 1 {
		t.Fatalf("dropped count = %d, want 1", dropped)
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
	docker.mu.Lock()
	defaultExportTail := docker.requests[len(docker.requests)-1].Tail
	docker.mu.Unlock()
	if defaultExportTail != -1 {
		t.Fatalf("default export tail = %d, want -1", defaultExportTail)
	}

	tailPath := filepath.Join(t.TempDir(), "tail.log")
	if _, err := manager.ExportLogs(ctx, models.ExportLogsRequest{
		Scope: ScopeProject,
		IDs:   []string{"linux_native/app"},
		Path:  tailPath,
		Tail:  17,
	}); err != nil {
		t.Fatalf("ExportLogs(tail) error = %v", err)
	}
	docker.mu.Lock()
	tailExportTail := docker.requests[len(docker.requests)-1].Tail
	docker.mu.Unlock()
	if tailExportTail != 17 {
		t.Fatalf("tail export tail = %d, want 17", tailExportTail)
	}
}

func TestParseRawLogLineAllowsLeadingWhitespaceBeforeTimestamp(t *testing.T) {
	t.Parallel()
	fallback := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	line := ParseRawLogLine(
		"  \t2026-06-13T09:00:00Z  INFO indented",
		"stdout",
		sourceInfo{ContainerID: "c1", ContainerName: "web"},
		func() time.Time { return fallback },
	)

	if got, want := line.TS, time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("timestamp = %s, want %s", got, want)
	}
	if line.Text != " INFO indented" {
		t.Fatalf("text = %q, want preserved spacing after timestamp", line.Text)
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
	readers    map[string]io.ReadCloser
	requests   []dockercore.LogOptions
	blockLogs  <-chan struct{}
	logsCalled chan struct{}
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
		logs:    map[string]string{},
		readers: map[string]io.ReadCloser{},
	}
}

func (f *fakeLogDocker) ContainerLogs(_ context.Context, id string, opts dockercore.LogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	f.requests = append(f.requests, opts)
	logsCalled := f.logsCalled
	blockLogs := f.blockLogs
	f.mu.Unlock()
	if logsCalled != nil {
		select {
		case <-logsCalled:
		default:
			close(logsCalled)
		}
	}
	if blockLogs != nil {
		<-blockLogs
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if reader := f.readers[id]; reader != nil {
		return reader, nil
	}
	return io.NopCloser(strings.NewReader(f.logs[id])), nil
}

func (f *fakeLogDocker) ListContainers(_ context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeLogDocker) setContainers(containers []models.ContainerSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.containers = append([]models.ContainerSummary(nil), containers...)
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

type blockingLogReader struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newBlockingLogReader() *blockingLogReader {
	return &blockingLogReader{
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (r *blockingLogReader) Read([]byte) (int, error) {
	r.startOnce.Do(func() {
		close(r.started)
	})
	<-r.closed
	return 0, io.EOF
}

func (r *blockingLogReader) Close() error {
	r.closeOnce.Do(func() {
		close(r.closed)
	})
	return nil
}
