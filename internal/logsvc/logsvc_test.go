package logsvc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
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

func TestReadDockerLogStreamStreamsMaximumFrameThroughBoundedScratch(t *testing.T) {
	header := make([]byte, 8)
	header[0] = 1
	binary.BigEndian.PutUint32(header[4:], maxDockerLogFrame)
	payload := &boundedFillReader{remaining: maxDockerLogFrame, value: 'x'}
	reader := io.MultiReader(bytes.NewReader(header), payload)
	var lines []models.LogLine

	err := ReadDockerLogStream(context.Background(), reader, sourceInfo{}, time.Now, func(line models.LogLine) bool {
		lines = append(lines, line)
		return true
	})
	if err != nil {
		t.Fatalf("ReadDockerLogStream() error = %v", err)
	}
	if payload.maxRequest > dockerLogReadChunkBytes {
		t.Fatalf("largest frame-body read = %d, scratch limit = %d", payload.maxRequest, dockerLogReadChunkBytes)
	}
	if len(lines) != 1 || !lines[0].Truncated {
		t.Fatalf("maximum-frame lines = %#v", lines)
	}
}

func TestReadDockerLogStreamShortFrameUsesOnlyBytesActuallyRead(t *testing.T) {
	header := make([]byte, 8)
	header[0] = 1
	binary.BigEndian.PutUint32(header[4:], 1024)
	var lines []models.LogLine

	err := ReadDockerLogStream(context.Background(), io.MultiReader(bytes.NewReader(header), strings.NewReader("partial")), sourceInfo{}, time.Now, func(line models.LogLine) bool {
		lines = append(lines, line)
		return true
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadDockerLogStream() error = %v, want io.ErrUnexpectedEOF", err)
	}
	if len(lines) != 1 || lines[0].Text != "partial" || strings.ContainsRune(lines[0].Text, '\x00') {
		t.Fatalf("short-frame lines = %#v", lines)
	}
}

func TestReadDockerLogStreamRejectsPartialHeaderAfterValidFrame(t *testing.T) {
	input := bytes.NewBuffer(dockerLogFrame(1, "2026-06-13T09:00:00Z INFO complete\n"))
	input.Write([]byte{1, 0, 0})
	var lines []models.LogLine
	err := ReadDockerLogStream(context.Background(), input, sourceInfo{}, time.Now, func(line models.LogLine) bool {
		lines = append(lines, line)
		return true
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadDockerLogStream() error = %v, want io.ErrUnexpectedEOF", err)
	}
	if len(lines) != 1 || lines[0].Text != "INFO complete" {
		t.Fatalf("lines before partial header = %#v", lines)
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

func TestRingEnforcesRetainedByteBudget(t *testing.T) {
	line := models.LogLine{ContainerID: "container", Stream: "stdout", Text: strings.Repeat("x", 128)}
	lineBytes := retainedLogLineBytes(line)
	ring := newRingBuffer(10, lineBytes*2)

	ring.add(models.LogLine{ContainerID: "container", Stream: "stdout", Text: strings.Repeat("a", 128)})
	ring.add(models.LogLine{ContainerID: "container", Stream: "stdout", Text: strings.Repeat("b", 128)})
	ring.add(models.LogLine{ContainerID: "container", Stream: "stdout", Text: strings.Repeat("c", 128)})

	lines := ring.snapshot()
	if ring.retainedBytes > ring.byteLimit {
		t.Fatalf("retained bytes = %d, limit = %d", ring.retainedBytes, ring.byteLimit)
	}
	if ring.dropped != 1 || len(lines) != 2 || lines[0].Text != strings.Repeat("b", 128) {
		t.Fatalf("byte-bounded ring dropped=%d lines=%#v", ring.dropped, lines)
	}

	oversized := ring.add(models.LogLine{Text: strings.Repeat("z", int(ring.byteLimit)+1)})
	if oversized.Sequence != 4 {
		t.Fatalf("oversized line sequence = %d, want 4", oversized.Sequence)
	}
	if got := ring.snapshot(); len(got) != 2 {
		t.Fatalf("oversized line was retained: %#v", got)
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

func TestManagerCapsConcurrentLogStreams(t *testing.T) {
	ctx := context.Background()
	docker := newFakeLogDocker()
	manager := NewManager(docker, nil, Options{
		MaxStreams:         3,
		MaxScopeStreams:    3,
		ReaderRetryInitial: time.Minute,
		ReaderRetryMaximum: time.Minute,
	})

	const attempts = 24
	results := make(chan struct {
		id  string
		err error
	}, attempts)
	var starts sync.WaitGroup
	for range attempts {
		starts.Add(1)
		go func() {
			defer starts.Done()
			id, err := manager.StartLogStream(ctx, models.LogStreamRequest{
				Scope:  ScopeContainer,
				IDs:    []string{"container-1"},
				Follow: true,
			})
			results <- struct {
				id  string
				err error
			}{id: id, err: err}
		}()
	}
	starts.Wait()
	close(results)

	var admitted []string
	for result := range results {
		if result.err == nil {
			admitted = append(admitted, result.id)
			continue
		}
		if !apperror.IsCode(result.err, apperror.Conflict) {
			t.Fatalf("rejected StartLogStream() error = %v, want conflict", result.err)
		}
	}
	if len(admitted) != 3 {
		t.Fatalf("admitted streams = %d, want 3", len(admitted))
	}
	manager.mu.Lock()
	streams := len(manager.sessions)
	readers := manager.reservedReaders
	manager.mu.Unlock()
	if streams != 3 || readers != 3 {
		t.Fatalf("capacity state streams=%d readers=%d, want 3/3", streams, readers)
	}
	manager.StopAll()
	manager.mu.Lock()
	readers = manager.reservedReaders
	manager.mu.Unlock()
	if readers != 0 {
		t.Fatalf("reserved readers after StopAll = %d, want 0", readers)
	}
}

func TestManagerReservesPendingAdmissionAndStopCancelsResolution(t *testing.T) {
	docker := newBlockingResolveLogDocker()
	manager := NewManager(docker, nil, Options{
		MaxStreams:      3,
		MaxScopeStreams: 3,
		StopTimeout:     200 * time.Millisecond,
	})
	const attempts = 24
	results := make(chan error, attempts)
	var callers sync.WaitGroup
	for range attempts {
		callers.Add(1)
		go func() {
			defer callers.Done()
			_, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{Scope: ScopeAll, Follow: true})
			results <- err
		}()
	}
	docker.waitForCalls(t, 3)
	time.Sleep(20 * time.Millisecond)
	if calls := docker.callCount(); calls != 3 {
		t.Fatalf("Docker resolutions = %d, want exactly three admitted pending starts", calls)
	}
	manager.StopAll()
	callers.Wait()
	close(results)
	for err := range results {
		if err == nil {
			t.Fatal("pending start survived StopAll")
		}
	}
	if got := manager.Diagnostics(); got.ActiveStreams != 0 || got.PendingStreams != 0 || got.DrainingStreams != 0 {
		t.Fatalf("diagnostics after pending shutdown = %#v", got)
	}
	if _, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{Scope: ScopeAll}); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("StartLogStream(after StopAll) error = %v, want provider-not-ready", err)
	}
}

func TestTimedOutStopKeepsSessionDrainingAndCharged(t *testing.T) {
	reader := newStubbornLogReader()
	docker := newFakeLogDocker()
	docker.readers["container-1"] = reader
	manager := NewManager(docker, nil, Options{
		MaxStreams:      1,
		MaxScopeStreams: 1,
		StopTimeout:     20 * time.Millisecond,
	})
	streamID, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	reader.waitStarted(t)
	if err := manager.StopStream(streamID); !apperror.IsCode(err, apperror.Timeout) {
		t.Fatalf("StopStream() error = %v, want timeout", err)
	}
	if got := manager.Diagnostics(); got.ActiveStreams != 0 || got.DrainingStreams != 1 || got.ReservedReaders != 1 {
		t.Fatalf("diagnostics while reader is stuck = %#v", got)
	}
	if _, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("StartLogStream(while draining) error = %v, want conflict", err)
	}
	reader.release()
	waitForLogDiagnostics(t, manager, func(value models.LogRuntimeDiagnostics) bool {
		return value.DrainingStreams == 0 && value.ReservedReaders == 0 && value.ActiveProducers == 0
	})
}

func TestReaderCloseMustFinishBeforeSessionReleasesCapacity(t *testing.T) {
	reader := newBlockingCloseLogReader()
	docker := newFakeLogDocker()
	docker.readers["container-1"] = reader
	manager := NewManager(docker, nil, Options{
		MaxStreams:      1,
		MaxScopeStreams: 1,
		StopTimeout:     20 * time.Millisecond,
	})
	streamID, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: false,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	reader.waitCloseStarted(t)
	if got := manager.Diagnostics(); got.ActiveStreams != 1 || got.ReservedReaders != 1 || got.ActiveProducers != 1 {
		t.Fatalf("diagnostics while Close is blocked = %#v", got)
	}
	if _, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: false,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("StartLogStream(while Close blocked) error = %v, want conflict", err)
	}
	if err := manager.StopStream(streamID); !apperror.IsCode(err, apperror.Timeout) {
		t.Fatalf("StopStream() error = %v, want timeout", err)
	}
	if got := manager.Diagnostics(); got.DrainingStreams != 1 || got.ReservedReaders != 1 {
		t.Fatalf("diagnostics while blocked Close is draining = %#v", got)
	}
	reader.releaseClose()
	waitForLogDiagnostics(t, manager, func(value models.LogRuntimeDiagnostics) bool {
		return value.DrainingStreams == 0 && value.ReservedReaders == 0 && value.ActiveProducers == 0
	})
}

func TestStopAllUsesOneSharedDeadlineForBlockedSessions(t *testing.T) {
	docker := newFakeLogDocker()
	containers := make([]models.ContainerSummary, 0, 4)
	readers := make([]*stubbornLogReader, 0, 4)
	for index := range 4 {
		id := fmt.Sprintf("container-%d", index+1)
		containers = append(containers, models.ContainerSummary{ID: id, Name: id, State: "running"})
		reader := newStubbornLogReader()
		readers = append(readers, reader)
		docker.readers[id] = reader
	}
	docker.setContainers(containers)
	manager := NewManager(docker, nil, Options{
		MaxStreams:      4,
		MaxScopeStreams: 4,
		StopTimeout:     30 * time.Millisecond,
	})
	for _, container := range containers {
		if _, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{
			Scope: ScopeContainer, IDs: []string{container.ID}, Follow: true,
		}); err != nil {
			t.Fatalf("StartLogStream(%s) error = %v", container.ID, err)
		}
	}
	for _, reader := range readers {
		reader.waitStarted(t)
	}
	started := time.Now()
	manager.StopAll()
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("StopAll elapsed = %s, want one shared deadline", elapsed)
	}
	if got := manager.Diagnostics(); got.DrainingStreams != 4 || got.ReservedReaders != 4 {
		t.Fatalf("diagnostics after timed-out StopAll = %#v", got)
	}
	for _, reader := range readers {
		reader.release()
	}
	waitForLogDiagnostics(t, manager, func(value models.LogRuntimeDiagnostics) bool {
		return value.DrainingStreams == 0 && value.ReservedReaders == 0
	})
}

func TestProjectFollowFinishesWhenWatcherCannotSubscribe(t *testing.T) {
	docker := newFakeLogDocker()
	container := docker.containers[0]
	container.State = "exited"
	container.Status = "exited"
	docker.setContainers([]models.ContainerSummary{container})
	manager := NewManager(docker, nil, Options{BatchWindow: time.Millisecond})
	if _, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Follow: true,
	}); err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	waitForLogDiagnostics(t, manager, func(value models.LogRuntimeDiagnostics) bool {
		return value.ActiveStreams == 0 && value.ActiveProducers == 0
	})
}

func TestLogRequestIdentifierLimitRejectsBeforeDockerCalls(t *testing.T) {
	docker := newBlockingResolveLogDocker()
	manager := NewManager(docker, nil, Options{MaxReadersPerStream: 4})
	ids := make([]string, 100)
	for index := range ids {
		ids[index] = fmt.Sprintf("unknown-%d", index)
	}
	if _, err := manager.StartLogStream(context.Background(), models.LogStreamRequest{
		Scope: ScopeContainer, IDs: ids,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("StartLogStream(too many IDs) error = %v, want conflict", err)
	}
	if calls := docker.callCount(); calls != 0 {
		t.Fatalf("Docker calls before ID rejection = %d, want zero", calls)
	}
}

func TestManagerCapsStreamsPerScope(t *testing.T) {
	ctx := context.Background()
	docker := newFakeLogDocker()
	manager := NewManager(docker, nil, Options{
		MaxStreams:         4,
		MaxScopeStreams:    1,
		ReaderRetryInitial: time.Minute,
		ReaderRetryMaximum: time.Minute,
	})

	first, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream(first) error = %v", err)
	}
	if _, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("same-scope StartLogStream() error = %v, want conflict", err)
	}
	project, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Follow: true,
	})
	if err != nil {
		t.Fatalf("different-scope StartLogStream() error = %v", err)
	}
	if err := manager.StopStream(first); err != nil {
		t.Fatalf("StopStream(first) error = %v", err)
	}
	if err := manager.StopStream(project); err != nil {
		t.Fatalf("StopStream(project) error = %v", err)
	}
}

func TestManagerRejectsReaderCapacityBeforeRegisteringStream(t *testing.T) {
	ctx := context.Background()
	second := models.ContainerSummary{
		ID: "container-2", Name: "app-2", State: "running", Status: "running", ProjectID: "linux_native/app",
	}
	docker := newFakeLogDocker()
	docker.setContainers(append(docker.containers, second))
	manager := NewManager(docker, nil, Options{
		MaxReadersPerStream: 1,
		MaxReaders:          4,
	})
	_, err := manager.StartLogStream(ctx, models.LogStreamRequest{Scope: ScopeAll, Follow: true})
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("StartLogStream() error = %v, want conflict", err)
	}
	manager.mu.Lock()
	streams := len(manager.sessions)
	readers := manager.reservedReaders
	manager.mu.Unlock()
	if streams != 0 || readers != 0 {
		t.Fatalf("rejected capacity state streams=%d readers=%d", streams, readers)
	}
}

func TestManagerCapsReadersAcrossStreams(t *testing.T) {
	ctx := context.Background()
	docker := newFakeLogDocker()
	manager := NewManager(docker, nil, Options{
		MaxStreams:          2,
		MaxScopeStreams:     2,
		MaxReadersPerStream: 1,
		MaxReaders:          1,
		ReaderRetryInitial:  time.Minute,
		ReaderRetryMaximum:  time.Minute,
	})
	first, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream(first) error = %v", err)
	}
	if _, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Follow: true,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("StartLogStream(over global reader cap) error = %v, want conflict", err)
	}
	manager.mu.Lock()
	readers := manager.reservedReaders
	manager.mu.Unlock()
	if readers != 1 {
		t.Fatalf("reserved readers = %d, want 1", readers)
	}
	if err := manager.StopStream(first); err != nil {
		t.Fatalf("StopStream(first) error = %v", err)
	}
	manager.mu.Lock()
	readers = manager.reservedReaders
	manager.mu.Unlock()
	if readers != 0 {
		t.Fatalf("reserved readers after stop = %d, want 0", readers)
	}
}

func TestFollowReaderRetriesTransientFailureAndResumes(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	linesCh := eventBus.Subscribe(ctx, bus.TopicLogsLines, 8)
	errorCh := eventBus.Subscribe(ctx, bus.TopicLogsError, 8)
	blocking := newBlockingLogReader()
	reader := &composedLogReader{
		Reader: io.MultiReader(
			bytes.NewReader(dockerLogFrame(1, "2026-06-13T09:00:00Z INFO resumed\n")),
			blocking,
		),
		Closer: blocking,
	}
	docker := newRetryLogDocker("running", 1, reader)
	manager := NewManager(docker, eventBus, Options{
		BatchWindow:         time.Millisecond,
		ReaderRetryAttempts: 3,
		ReaderRetryInitial:  time.Millisecond,
		ReaderRetryMaximum:  2 * time.Millisecond,
	})

	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:  ScopeContainer,
		IDs:    []string{"container-1"},
		Follow: true,
		Tail:   10,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	lines := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	if lines.StreamID != streamID || len(lines.Lines) != 1 || lines.Lines[0].Text != "INFO resumed" {
		t.Fatalf("resumed lines = %#v", lines)
	}
	if calls := docker.callCount(); calls != 2 {
		t.Fatalf("ContainerLogs calls = %d, want 2", calls)
	}
	select {
	case event := <-errorCh:
		t.Fatalf("successful retry published terminal error: %#v", event.Payload)
	default:
	}
	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if calls := docker.callCount(); calls != 2 {
		t.Fatalf("ContainerLogs calls after stop = %d, want 2", calls)
	}
}

func TestFollowReaderResumesAfterPartialReadWithoutReplayingTail(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	linesCh := eventBus.Subscribe(ctx, bus.TopicLogsLines, 8)
	blocking := newBlockingLogReader()
	one := "2026-06-13T09:00:00Z INFO one\n"
	two := "2026-06-13T09:00:01Z INFO two\n"
	docker := newScriptedLogDocker(
		logReaderStep{reader: readerEndingWith(one, errors.New("connection reset"))},
		logReaderStep{reader: &composedLogReader{Reader: io.MultiReader(strings.NewReader(one+two), blocking), Closer: blocking}},
	)
	manager := NewManager(docker, eventBus, Options{
		BatchMaxLines:       1,
		BatchWindow:         time.Millisecond,
		ReaderRetryAttempts: 3,
		ReaderRetryInitial:  time.Millisecond,
		ReaderRetryMaximum:  time.Millisecond,
	})
	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true, Tail: 500,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	first := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	second := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	if len(first.Lines) != 1 || first.Lines[0].Text != "INFO one" || len(second.Lines) != 1 || second.Lines[0].Text != "INFO two" {
		t.Fatalf("resumed payloads = %#v / %#v", first, second)
	}
	if first.StreamID != streamID || second.StreamID != streamID {
		t.Fatalf("stream IDs = %q/%q, want %q", first.StreamID, second.StreamID, streamID)
	}
	docker.waitForCalls(t, 2)
	requests := docker.requestsSnapshot()
	if requests[1].Tail != -1 || requests[1].Since != "2026-06-13T09:00:00Z" {
		t.Fatalf("reconnect options = %#v", requests[1])
	}
	select {
	case event := <-linesCh:
		t.Fatalf("replayed duplicate payload = %#v", event.Payload)
	case <-time.After(20 * time.Millisecond):
	}
	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
}

func TestFollowReaderBoundaryMismatchReplaysInsteadOfLosingRecord(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	linesCh := eventBus.Subscribe(ctx, bus.TopicLogsLines, 8)
	errorCh := eventBus.Subscribe(ctx, bus.TopicLogsError, 8)
	blocking := newBlockingLogReader()
	timestamp := "2026-06-13T09:00:00Z "
	docker := newScriptedLogDocker(
		logReaderStep{reader: readerEndingWith(timestamp+"INFO old-boundary\n", errors.New("connection reset"))},
		logReaderStep{reader: &composedLogReader{
			Reader: io.MultiReader(strings.NewReader(timestamp+"INFO new-boundary\n"), blocking),
			Closer: blocking,
		}},
	)
	manager := NewManager(docker, eventBus, Options{
		BatchMaxLines:       1,
		BatchWindow:         time.Millisecond,
		ReaderRetryAttempts: 3,
		ReaderRetryInitial:  time.Millisecond,
		ReaderRetryMaximum:  time.Millisecond,
	})
	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true, Tail: 500,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	first := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	second := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	if len(first.Lines) != 1 || first.Lines[0].Text != "INFO old-boundary" || len(second.Lines) != 1 || second.Lines[0].Text != "INFO new-boundary" {
		t.Fatalf("boundary payloads = %#v / %#v", first, second)
	}
	warning := receiveLogEvent[ErrorPayload](t, errorCh, time.Second)
	if warning.StreamID != streamID || !strings.Contains(warning.Error, "replayed to avoid loss") {
		t.Fatalf("boundary warning = %#v", warning)
	}
	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
}

func TestFollowReaderTailZeroCapturesLinesProducedDuringBackoff(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	linesCh := eventBus.Subscribe(ctx, bus.TopicLogsLines, 8)
	blocking := newBlockingLogReader()
	docker := newScriptedLogDocker(
		logReaderStep{err: errors.New("dial failed")},
		logReaderStep{reader: &composedLogReader{
			Reader: io.MultiReader(strings.NewReader("2026-06-13T09:00:01Z INFO during backoff\n"), blocking),
			Closer: blocking,
		}},
	)
	watermark := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	manager := NewManager(docker, eventBus, Options{
		BatchMaxLines:       1,
		BatchWindow:         time.Millisecond,
		ReaderRetryAttempts: 3,
		ReaderRetryInitial:  time.Millisecond,
		ReaderRetryMaximum:  time.Millisecond,
		Now:                 func() time.Time { return watermark },
	})
	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true, Tail: 0,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	payload := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	if len(payload.Lines) != 1 || payload.Lines[0].Text != "INFO during backoff" {
		t.Fatalf("backoff payload = %#v", payload)
	}
	requests := docker.requestsSnapshot()
	if len(requests) < 2 || requests[1].Tail != -1 || requests[1].Since != watermark.Format(time.RFC3339Nano) {
		t.Fatalf("tail-zero reconnect options = %#v", requests)
	}
	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
}

func TestFollowReaderHealthyReconnectsResetFailureBudget(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	linesCh := eventBus.Subscribe(ctx, bus.TopicLogsLines, 32)
	steps := make([]logReaderStep, 0, 11)
	lineAt := func(index int) string {
		return fmt.Sprintf("2026-06-13T09:00:%02dZ INFO line-%d\n", index, index)
	}
	for index := range 10 {
		data := lineAt(index)
		if index > 0 {
			data = lineAt(index-1) + data
		}
		steps = append(steps, logReaderStep{reader: readerEndingWith(data, errors.New("intermittent disconnect"))})
	}
	blocking := newBlockingLogReader()
	steps = append(steps, logReaderStep{reader: &composedLogReader{
		Reader: io.MultiReader(strings.NewReader(lineAt(9)), blocking),
		Closer: blocking,
	}})
	docker := newScriptedLogDocker(steps...)
	manager := NewManager(docker, eventBus, Options{
		BatchMaxLines:       1,
		BatchWindow:         time.Millisecond,
		ReaderRetryAttempts: 3,
		ReaderRetryInitial:  time.Millisecond,
		ReaderRetryMaximum:  time.Millisecond,
	})
	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true, Tail: 10,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	for index := range 10 {
		payload := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
		want := fmt.Sprintf("INFO line-%d", index)
		if len(payload.Lines) != 1 || payload.Lines[0].Text != want {
			t.Fatalf("payload %d = %#v, want %q", index, payload, want)
		}
	}
	docker.waitForCalls(t, 11)
	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
}

func TestFollowReaderDoesNotRetryPermanentFailure(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	errorCh := eventBus.Subscribe(ctx, bus.TopicLogsError, 8)
	eofCh := eventBus.Subscribe(ctx, bus.TopicLogsEOF, 8)
	docker := newScriptedLogDocker(logReaderStep{err: apperror.New(apperror.NotFound, "container disappeared")})
	manager := NewManager(docker, eventBus, Options{
		BatchWindow:         time.Millisecond,
		ReaderRetryAttempts: 8,
		ReaderRetryInitial:  time.Millisecond,
		ReaderRetryMaximum:  time.Millisecond,
	})
	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	if payload := receiveLogEvent[ErrorPayload](t, errorCh, time.Second); payload.StreamID != streamID {
		t.Fatalf("error payload = %#v", payload)
	}
	if payload := receiveLogEvent[EOFPayload](t, eofCh, time.Second); payload.StreamID != streamID {
		t.Fatalf("EOF payload = %#v", payload)
	}
	if calls := docker.callCount(); calls != 1 {
		t.Fatalf("ContainerLogs calls = %d, want one permanent attempt", calls)
	}
}

func TestFollowReaderRetryChainIsBounded(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	errorCh := eventBus.Subscribe(ctx, bus.TopicLogsError, 8)
	eofCh := eventBus.Subscribe(ctx, bus.TopicLogsEOF, 8)
	docker := newRetryLogDocker("running", 100, nil)
	manager := NewManager(docker, eventBus, Options{
		BatchWindow:         time.Millisecond,
		ReaderRetryAttempts: 3,
		ReaderRetryInitial:  10 * time.Millisecond,
		ReaderRetryMaximum:  20 * time.Millisecond,
	})

	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:  ScopeContainer,
		IDs:    []string{"container-1"},
		Follow: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	manager.mu.Lock()
	session := manager.sessions[streamID]
	manager.mu.Unlock()
	if session == nil {
		t.Fatal("session completed before retry chain could be observed")
	}
	terminal := receiveLogEvent[ErrorPayload](t, errorCh, time.Second)
	if terminal.StreamID != streamID || !strings.Contains(terminal.Error, "stopped after 3 attempts") {
		t.Fatalf("terminal error = %#v", terminal)
	}
	if eof := receiveLogEvent[EOFPayload](t, eofCh, time.Second); eof.StreamID != streamID {
		t.Fatalf("EOF = %#v", eof)
	}
	if calls := docker.callCount(); calls != 3 {
		t.Fatalf("ContainerLogs calls = %d, want 3", calls)
	}
	session.mu.Lock()
	attachments := len(session.attached)
	session.mu.Unlock()
	if attachments != 0 {
		t.Fatalf("attached markers after producer exit = %d, want 0", attachments)
	}
}

func TestFollowReaderDoesNotRetryStoppedContainerEOF(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	errorCh := eventBus.Subscribe(ctx, bus.TopicLogsError, 8)
	eofCh := eventBus.Subscribe(ctx, bus.TopicLogsEOF, 8)
	docker := newRetryLogDocker("exited", 0, io.NopCloser(strings.NewReader("")))
	manager := NewManager(docker, eventBus, Options{
		BatchWindow:         time.Millisecond,
		ReaderRetryAttempts: 3,
		ReaderRetryInitial:  time.Millisecond,
		ReaderRetryMaximum:  2 * time.Millisecond,
	})

	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:  ScopeContainer,
		IDs:    []string{"container-1"},
		Follow: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	if eof := receiveLogEvent[EOFPayload](t, eofCh, time.Second); eof.StreamID != streamID {
		t.Fatalf("EOF = %#v", eof)
	}
	if calls := docker.callCount(); calls != 1 {
		t.Fatalf("ContainerLogs calls = %d, want 1", calls)
	}
	select {
	case event := <-errorCh:
		t.Fatalf("stopped-container EOF published error: %#v", event.Payload)
	default:
	}
}

func TestStopCancelsFollowReaderRetryBackoff(t *testing.T) {
	ctx := context.Background()
	docker := newRetryLogDocker("running", 100, nil)
	manager := NewManager(docker, nil, Options{
		BatchWindow:         time.Millisecond,
		ReaderRetryAttempts: 3,
		ReaderRetryInitial:  100 * time.Millisecond,
		ReaderRetryMaximum:  100 * time.Millisecond,
	})

	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:  ScopeContainer,
		IDs:    []string{"container-1"},
		Follow: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	docker.waitForCalls(t, 1)
	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	if calls := docker.callCount(); calls != 1 {
		t.Fatalf("ContainerLogs calls after canceled backoff = %d, want 1", calls)
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

func TestProjectFollowCoalescesDynamicCapacityRejection(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	errorCh := eventBus.Subscribe(ctx, bus.TopicLogsError, 16)
	docker := newFakeLogDocker()
	reader := newBlockingLogReader()
	docker.readers["container-1"] = reader
	manager := NewManager(docker, eventBus, Options{
		BatchWindow:         time.Millisecond,
		MaxReadersPerStream: 1,
		MaxReaders:          2,
	})
	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeProject, IDs: []string{"linux_native/app"}, Follow: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	reader.waitStarted(t)
	containers := append([]models.ContainerSummary(nil), docker.containers...)
	for index := 2; index <= 1000; index++ {
		containers = append(containers, models.ContainerSummary{
			ID: fmt.Sprintf("container-%d", index), ProjectID: "linux_native/app", State: "running",
		})
	}
	docker.setContainers(containers)
	eventBus.Publish(bus.Event{Topic: bus.TopicObjectsChanged})
	payload := receiveLogEvent[ErrorPayload](t, errorCh, time.Second)
	if payload.StreamID != streamID || !strings.Contains(payload.Error, "candidate containers were not attached") {
		t.Fatalf("coalesced capacity error = %#v", payload)
	}
	select {
	case event := <-errorCh:
		t.Fatalf("capacity rejection fanned out: %#v", event.Payload)
	case <-time.After(30 * time.Millisecond):
	}
	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
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

func TestSessionEnqueueEnforcesByteBudgetBeforeChannelCapacity(t *testing.T) {
	line := models.LogLine{Text: strings.Repeat("x", 256)}
	manager := NewManager(nil, nil, Options{
		InputBuffer: 10,
		InputBytes:  retainedLogLineBytes(line),
	})
	s := newSession(manager, "stream-byte-budget", models.LogStreamRequest{})

	if !s.enqueue(line) {
		t.Fatal("first enqueue returned false")
	}
	if !s.enqueue(line) {
		t.Fatal("overflow enqueue returned false")
	}
	if queued := len(s.input); queued != 1 {
		t.Fatalf("queued lines = %d, want 1", queued)
	}
	if bytes := s.queuedBytes.Load(); bytes != retainedLogLineBytes(line) {
		t.Fatalf("queued bytes = %d, want %d", bytes, retainedLogLineBytes(line))
	}
	if dropped := s.dropped.Load(); dropped != 1 {
		t.Fatalf("dropped lines = %d, want 1", dropped)
	}
	s.cancel()
}

func TestManagerFlushesLiveBatchesAtByteBudget(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	linesCh := eventBus.Subscribe(ctx, bus.TopicLogsLines, 8)
	eofCh := eventBus.Subscribe(ctx, bus.TopicLogsEOF, 8)
	docker := newFakeLogDocker()
	firstText := "INFO " + strings.Repeat("a", 700)
	secondText := "INFO " + strings.Repeat("b", 700)
	docker.logs["container-1"] = "2026-06-13T09:00:00Z " + firstText + "\n2026-06-13T09:00:01Z " + secondText + "\n"
	manager := NewManager(docker, eventBus, Options{
		BatchMaxLines: 100,
		BatchWindow:   time.Hour,
		BatchBytes:    minimumBatchBytes,
	})

	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: false,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	first := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	second := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	if first.StreamID != streamID || len(first.Lines) != 1 || first.Lines[0].Text != firstText {
		t.Fatalf("first byte-bounded batch = %#v", first)
	}
	if second.StreamID != streamID || len(second.Lines) != 1 || second.Lines[0].Text != secondText {
		t.Fatalf("second byte-bounded batch = %#v", second)
	}
	if eof := receiveLogEvent[EOFPayload](t, eofCh, time.Second); eof.StreamID != streamID {
		t.Fatalf("EOF = %#v", eof)
	}
}

func TestManagerTruncatesSerializedLiveEventAtHardByteBudget(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	linesCh := eventBus.Subscribe(ctx, bus.TopicLogsLines, 8)
	docker := newFakeLogDocker()
	docker.logs["container-1"] = "2026-06-13T09:00:00Z " + strings.Repeat("\x01", 700) + "\n"
	manager := NewManager(docker, eventBus, Options{
		BatchMaxLines: 100,
		BatchWindow:   time.Millisecond,
		BatchBytes:    minimumBatchBytes,
	})

	if _, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope: ScopeContainer, IDs: []string{"container-1"}, Follow: false,
	}); err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	payload := receiveLogEvent[LinesPayload](t, linesCh, time.Second)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal(lines payload) error = %v", err)
	}
	if len(raw) > int(minimumBatchBytes) {
		t.Fatalf("serialized payload bytes = %d, limit = %d", len(raw), minimumBatchBytes)
	}
	if len(payload.Lines) != 1 || !payload.Lines[0].Truncated {
		t.Fatalf("bounded payload = %#v", payload)
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

type boundedFillReader struct {
	remaining  int
	value      byte
	maxRequest int
}

func (r *boundedFillReader) Read(buffer []byte) (int, error) {
	if len(buffer) > r.maxRequest {
		r.maxRequest = len(buffer)
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if len(buffer) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	for index := range buffer {
		buffer[index] = r.value
	}
	r.remaining -= len(buffer)
	return len(buffer), nil
}

type composedLogReader struct {
	io.Reader
	io.Closer
}

type retryLogDocker struct {
	mu           sync.Mutex
	container    models.ContainerSummary
	failures     int
	reader       io.ReadCloser
	calls        int
	callsChanged chan struct{}
}

type logReaderStep struct {
	reader io.ReadCloser
	err    error
}

type scriptedLogDocker struct {
	mu           sync.Mutex
	container    models.ContainerSummary
	steps        []logReaderStep
	requests     []dockercore.LogOptions
	callsChanged chan struct{}
}

func newScriptedLogDocker(steps ...logReaderStep) *scriptedLogDocker {
	return &scriptedLogDocker{
		container:    models.ContainerSummary{ID: "container-1", Name: "app-1", State: "running", Status: "running"},
		steps:        append([]logReaderStep(nil), steps...),
		callsChanged: make(chan struct{}, 32),
	}
}

func (f *scriptedLogDocker) ContainerLogs(_ context.Context, _ string, options dockercore.LogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	call := len(f.requests)
	f.requests = append(f.requests, options)
	var step logReaderStep
	if call < len(f.steps) {
		step = f.steps[call]
	} else {
		step.err = errors.New("unexpected scripted log call")
	}
	f.mu.Unlock()
	select {
	case f.callsChanged <- struct{}{}:
	default:
	}
	return step.reader, step.err
}

func (f *scriptedLogDocker) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []models.ContainerSummary{f.container}, nil
}

func (f *scriptedLogDocker) GetContainer(context.Context, string) (*models.ContainerDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &models.ContainerDetail{Summary: f.container}, nil
}

func (f *scriptedLogDocker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *scriptedLogDocker) requestsSnapshot() []dockercore.LogOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dockercore.LogOptions(nil), f.requests...)
}

func (f *scriptedLogDocker) waitForCalls(t *testing.T, want int) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for f.callCount() < want {
		select {
		case <-f.callsChanged:
		case <-timer.C:
			t.Fatalf("ContainerLogs calls = %d, want at least %d", f.callCount(), want)
		}
	}
}

type terminalErrorReader struct{ err error }

func (r terminalErrorReader) Read([]byte) (int, error) { return 0, r.err }

func readerEndingWith(data string, err error) io.ReadCloser {
	return io.NopCloser(io.MultiReader(strings.NewReader(data), terminalErrorReader{err: err}))
}

type blockingResolveLogDocker struct {
	mu           sync.Mutex
	calls        int
	callsChanged chan struct{}
}

func newBlockingResolveLogDocker() *blockingResolveLogDocker {
	return &blockingResolveLogDocker{callsChanged: make(chan struct{}, 32)}
}

func (f *blockingResolveLogDocker) ContainerLogs(context.Context, string, dockercore.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *blockingResolveLogDocker) ListContainers(ctx context.Context, _ models.ContainerListOptions) ([]models.ContainerSummary, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	select {
	case f.callsChanged <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *blockingResolveLogDocker) GetContainer(ctx context.Context, _ string) (*models.ContainerDetail, error) {
	_, err := f.ListContainers(ctx, models.ContainerListOptions{})
	return nil, err
}

func (f *blockingResolveLogDocker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *blockingResolveLogDocker) waitForCalls(t *testing.T, want int) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for f.callCount() < want {
		select {
		case <-f.callsChanged:
		case <-timer.C:
			t.Fatalf("Docker resolution calls = %d, want at least %d", f.callCount(), want)
		}
	}
}

type stubbornLogReader struct {
	started     chan struct{}
	releaseRead chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

type blockingCloseLogReader struct {
	closeStarted chan struct{}
	release      chan struct{}
	startOnce    sync.Once
	releaseOnce  sync.Once
}

func newBlockingCloseLogReader() *blockingCloseLogReader {
	return &blockingCloseLogReader{closeStarted: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingCloseLogReader) Read([]byte) (int, error) { return 0, io.EOF }

func (r *blockingCloseLogReader) Close() error {
	r.startOnce.Do(func() { close(r.closeStarted) })
	<-r.release
	return nil
}

func (r *blockingCloseLogReader) waitCloseStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("reader Close was not called")
	}
}

func (r *blockingCloseLogReader) releaseClose() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func newStubbornLogReader() *stubbornLogReader {
	return &stubbornLogReader{started: make(chan struct{}), releaseRead: make(chan struct{})}
}

func (r *stubbornLogReader) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.releaseRead
	return 0, io.EOF
}

func (r *stubbornLogReader) Close() error { return nil }

func (r *stubbornLogReader) release() {
	r.releaseOnce.Do(func() { close(r.releaseRead) })
}

func (r *stubbornLogReader) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("stubborn log reader was not consumed")
	}
}

func waitForLogDiagnostics(t *testing.T, manager *Manager, predicate func(models.LogRuntimeDiagnostics) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if value := manager.Diagnostics(); predicate(value) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("diagnostics did not settle: %#v", manager.Diagnostics())
}

func newRetryLogDocker(state string, failures int, reader io.ReadCloser) *retryLogDocker {
	return &retryLogDocker{
		container: models.ContainerSummary{
			ID:     "container-1",
			Name:   "app-1",
			State:  state,
			Status: state,
		},
		failures:     failures,
		reader:       reader,
		callsChanged: make(chan struct{}, 16),
	}
}

func (f *retryLogDocker) ContainerLogs(_ context.Context, _ string, _ dockercore.LogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	reader := f.reader
	f.mu.Unlock()
	select {
	case f.callsChanged <- struct{}{}:
	default:
	}
	if call <= f.failures {
		return nil, errors.New("transient log reader failure")
	}
	return reader, nil
}

func (f *retryLogDocker) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []models.ContainerSummary{f.container}, nil
}

func (f *retryLogDocker) GetContainer(context.Context, string) (*models.ContainerDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &models.ContainerDetail{Summary: f.container}, nil
}

func (f *retryLogDocker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *retryLogDocker) waitForCalls(t *testing.T, want int) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for f.callCount() < want {
		select {
		case <-f.callsChanged:
		case <-deadline.C:
			t.Fatalf("ContainerLogs calls = %d, want at least %d", f.callCount(), want)
		}
	}
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

func (r *blockingLogReader) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("blocking log reader was not consumed")
	}
}
