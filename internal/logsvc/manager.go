package logsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/google/uuid"
)

func NewManager(docker DockerClient, events bus.Bus, opts Options) *Manager {
	manager := &Manager{
		Docker:   docker,
		Events:   events,
		sessions: map[string]*session{},
	}
	manager.applyOptions(opts)
	return manager
}

func (m *Manager) StartLogStream(ctx context.Context, req models.LogStreamRequest) (string, error) {
	m.ensureReady()
	containers, err := m.resolveContainers(ctx, req)
	if err != nil {
		return "", err
	}
	streamID := uuid.NewString()
	s := newSession(m, streamID, req)

	m.mu.Lock()
	m.sessions[streamID] = s
	m.mu.Unlock()

	s.start(containers)
	return streamID, nil
}

func (m *Manager) StopStream(streamID string) error {
	m.ensureReady()
	m.mu.Lock()
	s := m.sessions[streamID]
	if s != nil {
		delete(m.sessions, streamID)
	}
	m.mu.Unlock()
	if s == nil {
		return apperror.New(apperror.NotFound, "Log stream was not found")
	}
	s.stop()
	return nil
}

func (m *Manager) StopAll() {
	m.ensureReady()
	m.mu.Lock()
	sessions := make([]*session, 0, len(m.sessions))
	for id, s := range m.sessions {
		delete(m.sessions, id)
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		s.stop()
	}
}

func (m *Manager) removeSession(streamID string, s *session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[streamID] == s {
		delete(m.sessions, streamID)
	}
}

func (m *Manager) FetchLogPage(ctx context.Context, req models.LogPageRequest) (*models.LogPage, error) {
	m.ensureReady()
	if err := m.requireDocker(); err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}
	lines, err := m.collectLogs(ctx, models.LogStreamRequest{
		Scope:      req.Scope,
		IDs:        req.IDs,
		Follow:     false,
		Tail:       defaultFetchTail,
		Timestamps: true,
	})
	if err != nil {
		return nil, err
	}
	page := pageLines(lines, req.Cursor, limit)
	return &page, nil
}

func (m *Manager) ExportLogs(ctx context.Context, req models.ExportLogsRequest) (*models.ExportResult, error) {
	m.ensureReady()
	if err := m.requireDocker(); err != nil {
		return nil, err
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return nil, apperror.New(apperror.Internal, "Export path is required")
	}
	lines, err := m.collectLogs(ctx, models.LogStreamRequest{
		Scope:      req.Scope,
		IDs:        req.IDs,
		Follow:     false,
		Tail:       -1,
		Timestamps: true,
	})
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Create log export directory failed", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Create log export failed", err)
	}
	defer func() {
		_ = file.Close()
	}()
	var bytesWritten int64
	for _, line := range lines {
		var text string
		if strings.EqualFold(filepath.Ext(path), ".jsonl") {
			raw, err := json.Marshal(line)
			if err != nil {
				return nil, apperror.Wrap(apperror.Internal, "Encode log export failed", err)
			}
			text = string(raw) + "\n"
		} else {
			text = fmt.Sprintf("%s %s %s\n", line.TS.Format(time.RFC3339Nano), line.Stream, line.Text)
		}
		n, err := io.WriteString(file, text)
		bytesWritten += int64(n)
		if err != nil {
			return nil, apperror.Wrap(apperror.Internal, "Write log export failed", err)
		}
	}
	return &models.ExportResult{Path: path, Bytes: bytesWritten, LineCount: len(lines)}, nil
}

func (m *Manager) collectLogs(ctx context.Context, req models.LogStreamRequest) ([]models.LogLine, error) {
	containers, err := m.resolveContainers(ctx, req)
	if err != nil {
		return nil, err
	}
	var lines []models.LogLine
	for _, container := range containers {
		source := sourceFromContainer(container)
		reader, err := m.Docker.ContainerLogs(ctx, container.ID, dockercore.LogOptions{
			Follow:     false,
			Tail:       req.Tail,
			Since:      req.Since,
			Timestamps: true,
		})
		if err != nil {
			return nil, err
		}
		err = ReadDockerLogStream(ctx, reader, source, m.now, func(line models.LogLine) bool {
			lines = append(lines, line)
			return true
		})
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, apperror.Wrap(apperror.Internal, "Close log stream failed", closeErr)
		}
	}
	SortLines(lines)
	return lines, nil
}

func (m *Manager) resolveContainers(ctx context.Context, req models.LogStreamRequest) ([]models.ContainerSummary, error) {
	if err := m.requireDocker(); err != nil {
		return nil, err
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = ScopeContainer
	}
	var containers []models.ContainerSummary
	switch scope {
	case ScopeContainer:
		if len(req.IDs) == 0 {
			return nil, apperror.New(apperror.NotFound, "No containers were selected")
		}
		known := map[string]models.ContainerSummary{}
		listed, _ := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true})
		for _, container := range listed {
			known[container.ID] = container
			known[container.Name] = container
		}
		for _, id := range req.IDs {
			if container, ok := known[id]; ok {
				containers = append(containers, container)
				continue
			}
			detail, err := m.Docker.GetContainer(ctx, id)
			if err != nil {
				return nil, err
			}
			if detail != nil {
				containers = append(containers, detail.Summary)
			}
		}
	case ScopeProject:
		for _, projectID := range req.IDs {
			listed, err := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true, ProjectID: projectID})
			if err != nil {
				return nil, err
			}
			containers = append(containers, listed...)
		}
	case ScopeService:
		for _, serviceID := range req.IDs {
			projectID, serviceName := splitServiceID(serviceID)
			listed, err := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true, ProjectID: projectID, Service: serviceName})
			if err != nil {
				return nil, err
			}
			containers = append(containers, listed...)
		}
	case ScopeAll:
		listed, err := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true})
		if err != nil {
			return nil, err
		}
		containers = append(containers, listed...)
	default:
		return nil, apperror.New(apperror.NotFound, "Unsupported log scope", apperror.WithDetail(scope))
	}
	containers = uniqueContainers(containers)
	if len(containers) == 0 {
		return nil, apperror.New(apperror.NotFound, "No log containers matched the request")
	}
	return containers, nil
}

func splitServiceID(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	projectID, service, ok := strings.Cut(value, "::")
	if ok {
		return projectID, service
	}
	index := strings.LastIndex(value, "/")
	if index <= 0 {
		return "", value
	}
	return value[:index], value[index+1:]
}

func uniqueContainers(containers []models.ContainerSummary) []models.ContainerSummary {
	seen := map[string]struct{}{}
	unique := make([]models.ContainerSummary, 0, len(containers))
	for _, container := range containers {
		key := container.ID
		if key == "" {
			key = container.Name
		}
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, container)
	}
	return unique
}

func sourceFromContainer(container models.ContainerSummary) sourceInfo {
	return sourceInfo{
		ContainerID:   container.ID,
		ContainerName: container.Name,
		Service:       container.Service,
	}
}

func (m *Manager) requireDocker() error {
	if m.Docker == nil {
		return apperror.New(apperror.ProviderNotReady, "Docker client is not ready")
	}
	return nil
}

func (m *Manager) ensureReady() {
	if m.sessions == nil {
		m.sessions = map[string]*session{}
	}
	if m.ringSize <= 0 {
		m.ringSize = defaultRingSize
	}
	if m.inputBuffer <= 0 {
		m.inputBuffer = defaultInputBuffer
	}
	if m.batchMaxLines <= 0 {
		m.batchMaxLines = defaultBatchMaxLines
	}
	if m.batchWindow <= 0 {
		m.batchWindow = defaultBatchWindow
	}
	if m.now == nil {
		m.now = func() time.Time { return time.Now().UTC() }
	}
}

func (m *Manager) applyOptions(opts Options) {
	m.ringSize = opts.RingSize
	m.inputBuffer = opts.InputBuffer
	m.batchMaxLines = opts.BatchMaxLines
	m.batchWindow = opts.BatchWindow
	m.now = opts.Now
	m.ensureReady()
}

type session struct {
	manager  *Manager
	streamID string
	req      models.LogStreamRequest
	ctx      context.Context
	cancel   context.CancelFunc

	input     chan models.LogLine
	ring      *ringBuffer
	attached  map[string]struct{}
	dropped   atomic.Int64
	producers sync.WaitGroup
	done      chan struct{}
	mu        sync.Mutex
}

func newSession(manager *Manager, streamID string, req models.LogStreamRequest) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		manager:  manager,
		streamID: streamID,
		req:      req,
		ctx:      ctx,
		cancel:   cancel,
		input:    make(chan models.LogLine, manager.inputBuffer),
		ring:     newRingBuffer(manager.ringSize),
		attached: map[string]struct{}{},
		done:     make(chan struct{}),
	}
}

func (s *session) start(containers []models.ContainerSummary) {
	go s.batchLoop()
	for _, container := range containers {
		s.attach(container)
	}
	if s.req.Follow && s.req.Scope != ScopeContainer {
		go s.watchObjects()
		return
	}
	go func() {
		s.producers.Wait()
		close(s.input)
	}()
}

func (s *session) attach(container models.ContainerSummary) {
	key := container.ID
	if key == "" {
		key = container.Name
	}
	if key == "" {
		return
	}
	s.mu.Lock()
	if _, exists := s.attached[key]; exists {
		s.mu.Unlock()
		return
	}
	s.attached[key] = struct{}{}
	s.producers.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.producers.Done()
		source := sourceFromContainer(container)
		reader, err := s.manager.Docker.ContainerLogs(s.ctx, container.ID, dockercore.LogOptions{
			Follow:     s.req.Follow,
			Tail:       s.req.Tail,
			Since:      s.req.Since,
			Timestamps: true,
		})
		if err != nil {
			s.publishError(err)
			return
		}
		defer func() {
			_ = reader.Close()
		}()
		err = ReadDockerLogStream(s.ctx, reader, source, s.manager.now, s.enqueue)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.publishError(err)
		}
	}()
}

func (s *session) watchObjects() {
	if s.manager.Events == nil {
		return
	}
	events := s.manager.Events.Subscribe(s.ctx, bus.TopicObjectsChanged, 16)
	for {
		select {
		case <-s.ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			containers, err := s.manager.resolveContainers(s.ctx, s.req)
			if err != nil {
				s.publishError(err)
				continue
			}
			for _, container := range containers {
				s.attach(container)
			}
		}
	}
}

func (s *session) enqueue(line models.LogLine) bool {
	select {
	case <-s.ctx.Done():
		return false
	case s.input <- line:
		return true
	}
}

func (s *session) batchLoop() {
	defer close(s.done)
	ticker := time.NewTicker(s.manager.batchWindow)
	defer ticker.Stop()

	batch := make([]models.LogLine, 0, s.manager.batchMaxLines)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.manager.publish(bus.TopicLogsLines, LinesPayload{StreamID: s.streamID, Lines: append([]models.LogLine(nil), batch...)})
		batch = batch[:0]
	}
	appendLine := func(line models.LogLine) {
		s.ring.add(line)
		batch = append(batch, line)
		if len(batch) >= s.manager.batchMaxLines {
			flush()
		}
	}
	appendSkipped := func() {
		skipped := s.dropped.Swap(0)
		if skipped == 0 {
			return
		}
		appendLine(models.LogLine{
			TS:     s.manager.now(),
			Stream: "system",
			Level:  "warn",
			Text:   fmt.Sprintf("%d lines skipped", skipped),
		})
	}

	for {
		select {
		case line, ok := <-s.input:
			if !ok {
				appendSkipped()
				flush()
				s.manager.removeSession(s.streamID, s)
				s.manager.publish(bus.TopicLogsEOF, EOFPayload{StreamID: s.streamID})
				return
			}
			appendSkipped()
			appendLine(line)
		case <-ticker.C:
			appendSkipped()
			flush()
		case <-s.ctx.Done():
			appendSkipped()
			flush()
			s.manager.removeSession(s.streamID, s)
			s.manager.publish(bus.TopicLogsEOF, EOFPayload{StreamID: s.streamID})
			return
		}
	}
}

func (s *session) publishError(err error) {
	if err == nil {
		return
	}
	s.manager.publish(bus.TopicLogsError, ErrorPayload{StreamID: s.streamID, Error: err.Error()})
}

func (s *session) stop() {
	s.cancel()
	<-s.done
}

func (m *Manager) publish(topic bus.Topic, payload any) {
	if m.Events == nil {
		return
	}
	m.Events.Publish(bus.Event{Topic: topic, Payload: payload})
}
