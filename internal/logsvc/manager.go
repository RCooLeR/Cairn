package logsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	rootCtx, rootCancel := context.WithCancel(context.Background())
	manager := &Manager{
		Docker:              docker,
		Events:              events,
		sessions:            map[string]*session{},
		draining:            map[string]*session{},
		pendingScopeStreams: map[string]int{},
		pageSnapshots:       map[string]*logPageSnapshot{},
		pageCursorKey:       newLogPageCursorKey(),
		rootCtx:             rootCtx,
		rootCancel:          rootCancel,
	}
	manager.applyOptions(opts)
	return manager
}

func (m *Manager) StartLogStream(ctx context.Context, req models.LogStreamRequest) (string, error) {
	m.ensureReady()
	var err error
	req, err = normalizeLogStreamRequest(req, m.maxReadersPerStream)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	if err := m.reservePendingStreamLocked(req.Scope); err != nil {
		m.mu.Unlock()
		return "", err
	}
	rootCtx := m.rootCtx
	m.mu.Unlock()
	pending := true
	defer func() {
		if pending {
			m.releasePendingStream(req.Scope)
		}
	}()
	resolveCtx, cancelResolve := context.WithCancel(ctx)
	stopRootCancel := context.AfterFunc(rootCtx, cancelResolve)
	defer func() {
		stopRootCancel()
		cancelResolve()
	}()
	containers, err := m.resolveContainers(resolveCtx, req, false)
	if err != nil {
		return "", err
	}
	if err := resolveCtx.Err(); err != nil {
		return "", apperror.Wrap(apperror.Cancelled, "Start log stream canceled", err)
	}
	if len(containers) > m.maxReadersPerStream {
		return "", apperror.New(
			apperror.Conflict,
			"Log reader capacity has been reached",
			apperror.WithDetail(fmt.Sprintf("A log stream can follow at most %d containers.", m.maxReadersPerStream)),
		)
	}
	streamID := uuid.NewString()
	s := newSession(m, streamID, req)
	for _, container := range containers {
		if key := containerKey(container); key != "" {
			s.attached[key] = struct{}{}
		}
	}

	m.mu.Lock()
	if m.closed || m.rootCtx == nil || m.rootCtx.Err() != nil {
		m.mu.Unlock()
		s.cancel()
		return "", apperror.New(apperror.ProviderNotReady, "Log runtime is stopping")
	}
	if m.reservedReaders+len(s.attached) > m.maxReaders {
		m.mu.Unlock()
		s.cancel()
		return "", logReaderCapacityError(m.maxReaders)
	}
	m.releasePendingStreamLocked(req.Scope)
	pending = false
	m.sessions[streamID] = s
	m.reservedReaders += len(s.attached)
	s.start(containers)
	m.mu.Unlock()
	m.pendingStarts.Done()
	return streamID, nil
}

func normalizeLogScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ScopeContainer
	}
	return scope
}

func normalizeLogStreamRequest(req models.LogStreamRequest, maxIDs int) (models.LogStreamRequest, error) {
	req.Scope = normalizeLogScope(req.Scope)
	switch req.Scope {
	case ScopeContainer, ScopeService, ScopeProject, ScopeAll:
	default:
		return models.LogStreamRequest{}, apperror.New(apperror.NotFound, "Unsupported log scope", apperror.WithDetail(req.Scope))
	}
	if maxIDs <= 0 {
		maxIDs = defaultMaxReadersPerStream
	}
	if len(req.IDs) > maxIDs {
		return models.LogStreamRequest{}, apperror.New(
			apperror.Conflict,
			"Log scope contains too many identifiers",
			apperror.WithDetail(fmt.Sprintf("At most %d identifiers can be requested at once.", maxIDs)),
		)
	}
	seen := make(map[string]struct{}, min(len(req.IDs), maxIDs+1))
	ids := make([]string, 0, min(len(req.IDs), maxIDs))
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if len(id) > 4096 {
			return models.LogStreamRequest{}, apperror.New(apperror.Conflict, "Log scope identifier is too large")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		if len(ids) == maxIDs {
			return models.LogStreamRequest{}, apperror.New(
				apperror.Conflict,
				"Log scope contains too many identifiers",
				apperror.WithDetail(fmt.Sprintf("At most %d identifiers can be requested at once.", maxIDs)),
			)
		}
		ids = append(ids, id)
	}
	req.IDs = ids
	if req.Scope == ScopeService {
		for _, serviceID := range req.IDs {
			if _, _, ok := splitServiceID(serviceID); !ok {
				return models.LogStreamRequest{}, apperror.New(
					apperror.Conflict,
					"Log service scope identifier is invalid",
					apperror.WithDetail("Choose a service from a specific project."),
				)
			}
		}
	}
	return req, nil
}

func (m *Manager) reservePendingStreamLocked(scope string) error {
	if m.closed || m.rootCtx == nil || m.rootCtx.Err() != nil {
		return apperror.New(apperror.ProviderNotReady, "Log runtime is stopping")
	}
	if len(m.sessions)+len(m.draining)+m.pendingStreams >= m.maxStreams {
		return apperror.New(
			apperror.Conflict,
			"Log stream capacity has been reached",
			apperror.WithDetail("Stop an existing log stream before starting another."),
		)
	}
	scopeStreams := 0
	for _, existing := range m.sessions {
		if normalizeLogScope(existing.req.Scope) == scope {
			scopeStreams++
		}
	}
	for _, existing := range m.draining {
		if normalizeLogScope(existing.req.Scope) == scope {
			scopeStreams++
		}
	}
	scopeStreams += m.pendingScopeStreams[scope]
	if scopeStreams >= m.maxScopeStreams {
		return apperror.New(
			apperror.Conflict,
			"Log scope capacity has been reached",
			apperror.WithDetail(fmt.Sprintf("At most %d concurrent %s log streams are allowed.", m.maxScopeStreams, scope)),
		)
	}
	m.pendingStreams++
	m.pendingScopeStreams[scope]++
	m.pendingStarts.Add(1)
	return nil
}

func (m *Manager) releasePendingStream(scope string) {
	m.mu.Lock()
	m.releasePendingStreamLocked(scope)
	m.mu.Unlock()
	m.pendingStarts.Done()
}

func (m *Manager) releasePendingStreamLocked(scope string) {
	if m.pendingStreams > 0 {
		m.pendingStreams--
	}
	if m.pendingScopeStreams[scope] <= 1 {
		delete(m.pendingScopeStreams, scope)
	} else {
		m.pendingScopeStreams[scope]--
	}
}

func logReaderCapacityError(limit int) error {
	return apperror.New(
		apperror.Conflict,
		"Log reader capacity has been reached",
		apperror.WithDetail(fmt.Sprintf("At most %d Docker log readers can be active at once.", limit)),
	)
}

func (m *Manager) StopStream(streamID string) error {
	m.ensureReady()
	m.mu.Lock()
	s := m.sessions[streamID]
	if s != nil {
		delete(m.sessions, streamID)
		m.draining[streamID] = s
	} else {
		s = m.draining[streamID]
	}
	m.mu.Unlock()
	if s == nil {
		return apperror.New(apperror.NotFound, "Log stream was not found")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), m.stopTimeout)
	defer cancel()
	if err := s.stop(stopCtx); err != nil {
		return apperror.Wrap(apperror.Timeout, "Log stream did not stop before the deadline", err)
	}
	return nil
}

func (m *Manager) StopAll() {
	m.ensureReady()
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		if m.rootCancel != nil {
			m.rootCancel()
		}
	}
	sessions := make([]*session, 0, len(m.sessions)+len(m.draining))
	for id, s := range m.sessions {
		delete(m.sessions, id)
		m.draining[id] = s
	}
	for _, s := range m.draining {
		sessions = append(sessions, s)
	}
	for id := range m.pageSnapshots {
		m.removePageSnapshotLocked(id)
	}
	m.pageSnapshots = map[string]*logPageSnapshot{}
	m.mu.Unlock()
	stopCtx, cancel := context.WithTimeout(context.Background(), m.stopTimeout)
	defer cancel()
	var stopping sync.WaitGroup
	for _, s := range sessions {
		stopping.Add(1)
		go func(s *session) {
			defer stopping.Done()
			if err := s.stop(stopCtx); err != nil {
				s.publishError(fmt.Errorf("log stream shutdown exceeded its deadline: %w", err))
			}
		}(s)
	}
	waitGroupUntil(stopCtx, &stopping)
	waitGroupUntil(stopCtx, &m.pendingStarts)
	waitGroupUntil(stopCtx, &m.operations)
}

func (m *Manager) removeSession(streamID string, s *session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[streamID] == s {
		delete(m.sessions, streamID)
	}
}

func (m *Manager) removeDraining(streamID string, s *session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.draining[streamID] == s {
		delete(m.draining, streamID)
	}
}

func waitGroupUntil(ctx context.Context, group *sync.WaitGroup) bool {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m *Manager) FetchLogPage(ctx context.Context, req models.LogPageRequest) (*models.LogPage, error) {
	return m.fetchLogPage(ctx, req)
}

func (m *Manager) ExportLogs(ctx context.Context, req models.ExportLogsRequest) (*models.ExportResult, error) {
	return m.exportLogs(ctx, req)
}

func (m *Manager) resolveContainers(ctx context.Context, req models.LogStreamRequest, allowEmpty bool) ([]models.ContainerSummary, error) {
	if err := m.requireDocker(); err != nil {
		return nil, err
	}
	var err error
	req, err = normalizeLogStreamRequest(req, m.maxReadersPerStream)
	if err != nil {
		return nil, err
	}
	scope := req.Scope
	collector := newContainerCollector(m.maxReadersPerStream + 1)
	switch scope {
	case ScopeContainer:
		if len(req.IDs) == 0 {
			return nil, apperror.New(apperror.NotFound, "No containers were selected")
		}
		wanted := make(map[string]struct{}, len(req.IDs))
		for _, id := range req.IDs {
			wanted[id] = struct{}{}
		}
		known := make(map[string]models.ContainerSummary, len(req.IDs)*2)
		listed, _ := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true})
		for _, container := range listed {
			if _, ok := wanted[container.ID]; ok {
				known[container.ID] = container
			}
			if _, ok := wanted[container.Name]; ok {
				known[container.Name] = container
			}
		}
		for _, id := range req.IDs {
			if container, ok := known[id]; ok {
				collector.add(container)
				continue
			}
			detail, err := m.Docker.GetContainer(ctx, id)
			if err != nil {
				return nil, err
			}
			if detail != nil {
				collector.add(detail.Summary)
			}
		}
	case ScopeProject:
		for _, projectID := range req.IDs {
			listed, err := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true, ProjectID: projectID})
			if err != nil {
				return nil, err
			}
			collector.addAll(listed)
			if collector.full() {
				break
			}
		}
	case ScopeService:
		for _, serviceID := range req.IDs {
			projectID, serviceName, ok := splitServiceID(serviceID)
			if !ok {
				return nil, apperror.New(apperror.Conflict, "Log service scope identifier is invalid")
			}
			listed, err := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true, ProjectID: projectID, Service: serviceName})
			if err != nil {
				return nil, err
			}
			collector.addAll(listed)
			if collector.full() {
				break
			}
		}
	case ScopeAll:
		listed, err := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true})
		if err != nil {
			return nil, err
		}
		collector.addAll(listed)
	default:
		return nil, apperror.New(apperror.NotFound, "Unsupported log scope", apperror.WithDetail(scope))
	}
	containers := collector.containers
	if len(containers) == 0 {
		if allowEmpty {
			return nil, nil
		}
		return nil, apperror.New(apperror.NotFound, "No log containers matched the request")
	}
	return containers, nil
}

type containerCollector struct {
	limit      int
	seen       map[string]struct{}
	containers []models.ContainerSummary
}

func newContainerCollector(limit int) *containerCollector {
	if limit <= 0 {
		limit = defaultMaxReadersPerStream + 1
	}
	return &containerCollector{
		limit:      limit,
		seen:       make(map[string]struct{}, limit),
		containers: make([]models.ContainerSummary, 0, limit),
	}
}

func (c *containerCollector) add(container models.ContainerSummary) {
	if c.full() {
		return
	}
	key := containerKey(container)
	if key == "" {
		return
	}
	if _, exists := c.seen[key]; exists {
		return
	}
	c.seen[key] = struct{}{}
	c.containers = append(c.containers, container)
}

func (c *containerCollector) addAll(containers []models.ContainerSummary) {
	for _, container := range containers {
		c.add(container)
		if c.full() {
			return
		}
	}
}

func (c *containerCollector) full() bool {
	return len(c.containers) >= c.limit
}

func splitServiceID(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	projectID, service, ok := strings.Cut(value, "::")
	if ok {
		projectID = strings.TrimSpace(projectID)
		service = strings.TrimSpace(service)
		return projectID, service, projectID != "" && service != "" && !strings.Contains(service, "::")
	}
	index := strings.LastIndex(value, "/")
	if index <= 0 || index == len(value)-1 {
		return "", "", false
	}
	projectID = strings.TrimSpace(value[:index])
	service = strings.TrimSpace(value[index+1:])
	return projectID, service, projectID != "" && service != ""
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
	if m.draining == nil {
		m.draining = map[string]*session{}
	}
	if m.pendingScopeStreams == nil {
		m.pendingScopeStreams = map[string]int{}
	}
	if m.pageSnapshots == nil {
		m.pageSnapshots = map[string]*logPageSnapshot{}
	}
	if m.pageCursorKey == ([32]byte{}) {
		m.pageCursorKey = newLogPageCursorKey()
	}
	if m.rootCtx == nil {
		m.rootCtx, m.rootCancel = context.WithCancel(context.Background())
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
	if m.retryAttempts <= 0 {
		m.retryAttempts = defaultRetryAttempts
	}
	if m.retryInitial <= 0 {
		m.retryInitial = defaultRetryInitial
	}
	if m.retryMaximum <= 0 {
		m.retryMaximum = defaultRetryMaximum
	}
	if m.retryMaximum < m.retryInitial {
		m.retryMaximum = m.retryInitial
	}
	if m.retryHealthy <= 0 {
		m.retryHealthy = defaultRetryHealthyAfter
	}
	if m.stopTimeout <= 0 {
		m.stopTimeout = defaultStopTimeout
	}
	if m.maxStreams <= 0 {
		m.maxStreams = defaultMaxStreams
	}
	if m.maxScopeStreams <= 0 {
		m.maxScopeStreams = defaultMaxScopeStreams
	}
	if m.maxScopeStreams > m.maxStreams {
		m.maxScopeStreams = m.maxStreams
	}
	if m.maxReadersPerStream <= 0 {
		m.maxReadersPerStream = defaultMaxReadersPerStream
	}
	if m.maxReaders <= 0 {
		m.maxReaders = defaultMaxReaders
	}
	if m.maxReadersPerStream > m.maxReaders {
		m.maxReadersPerStream = m.maxReaders
	}
	if m.ringBytes <= 0 {
		m.ringBytes = defaultRingBytes
	}
	if m.inputBytes <= 0 {
		m.inputBytes = defaultInputBytes
	}
	if m.batchBytes < minimumBatchBytes {
		if m.batchBytes > 0 {
			m.batchBytes = minimumBatchBytes
		} else {
			m.batchBytes = defaultBatchBytes
		}
	}
	if m.maxOperations <= 0 {
		m.maxOperations = defaultMaxOperations
	}
	if m.fetchTimeout <= 0 {
		m.fetchTimeout = defaultFetchTimeout
	}
	if m.pageSnapshotTTL <= 0 {
		m.pageSnapshotTTL = defaultPageSnapshotTTL
	}
	if m.maxPageSnapshots <= 0 {
		m.maxPageSnapshots = defaultMaxPageSnapshots
	}
	if m.pageSnapshotLines <= 0 {
		m.pageSnapshotLines = defaultPageSnapshotLines
	}
	if m.pageSnapshotBytes <= 0 {
		m.pageSnapshotBytes = defaultPageSnapshotBytes
	}
	if m.pageSnapshotsBytes <= 0 {
		m.pageSnapshotsBytes = defaultPageSnapshotsBytes
	}
	if m.pageSnapshotBytes > m.pageSnapshotsBytes {
		m.pageSnapshotBytes = m.pageSnapshotsBytes
	}
	if m.exportTimeout <= 0 {
		m.exportTimeout = defaultExportTimeout
	}
	if m.exportLines <= 0 {
		m.exportLines = defaultExportLines
	}
	if m.exportBytes <= 0 {
		m.exportBytes = defaultExportBytes
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
	m.retryAttempts = opts.ReaderRetryAttempts
	m.retryInitial = opts.ReaderRetryInitial
	m.retryMaximum = opts.ReaderRetryMaximum
	m.retryHealthy = opts.ReaderRetryHealthy
	m.stopTimeout = opts.StopTimeout
	m.maxStreams = opts.MaxStreams
	m.maxScopeStreams = opts.MaxScopeStreams
	m.maxReadersPerStream = opts.MaxReadersPerStream
	m.maxReaders = opts.MaxReaders
	m.ringBytes = opts.RingBytes
	m.inputBytes = opts.InputBytes
	m.batchBytes = opts.BatchBytes
	m.maxOperations = opts.MaxOperations
	m.fetchTimeout = opts.FetchTimeout
	m.pageSnapshotTTL = opts.PageSnapshotTTL
	m.maxPageSnapshots = opts.MaxPageSnapshots
	m.pageSnapshotLines = opts.PageSnapshotLines
	m.pageSnapshotBytes = opts.PageSnapshotBytes
	m.pageSnapshotsBytes = opts.PageSnapshotsBytes
	m.exportTimeout = opts.ExportTimeout
	m.exportLines = opts.ExportLines
	m.exportBytes = opts.ExportBytes
	m.exportDirectory = opts.ExportDirectory
	m.now = opts.Now
	m.ensureReady()
}

type session struct {
	manager  *Manager
	streamID string
	req      models.LogStreamRequest
	ctx      context.Context
	cancel   context.CancelFunc

	input            chan models.LogLine
	ring             *ringBuffer
	attached         map[string]struct{}
	activeReaders    map[string]*managedLogReader
	dropped          atomic.Int64
	queuedBytes      atomic.Int64
	retainedBytes    atomic.Int64
	activeProducers  atomic.Int64
	producers        sync.WaitGroup
	producerDone     chan struct{}
	producerDoneOnce sync.Once
	drainOnce        sync.Once
	watchDone        chan struct{}
	done             chan struct{}
	drainDone        chan struct{}
	closeInputOnce   sync.Once
	mu               sync.Mutex
}

func newSession(manager *Manager, streamID string, req models.LogStreamRequest) *session {
	ctx, cancel := context.WithCancel(manager.rootCtx)
	return &session{
		manager:       manager,
		streamID:      streamID,
		req:           req,
		ctx:           ctx,
		cancel:        cancel,
		input:         make(chan models.LogLine, manager.inputBuffer),
		ring:          newRingBuffer(manager.ringSize, manager.ringBytes),
		attached:      map[string]struct{}{},
		activeReaders: map[string]*managedLogReader{},
		producerDone:  make(chan struct{}),
		done:          make(chan struct{}),
		drainDone:     make(chan struct{}),
	}
}

func (s *session) start(containers []models.ContainerSummary) {
	go s.batchLoop()
	for _, container := range containers {
		key := containerKey(container)
		if key != "" {
			s.startProducer(container, key)
		}
	}
	if s.req.Follow && s.req.Scope != ScopeContainer {
		s.watchDone = make(chan struct{})
		go func() {
			defer close(s.watchDone)
			s.watchObjects()
			s.finishProducers(true)
		}()
		return
	}
	s.finishProducers(true)
}

func (s *session) attach(container models.ContainerSummary) error {
	key := containerKey(container)
	if key == "" {
		return nil
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return s.ctx.Err()
	}
	if _, exists := s.attached[key]; exists {
		s.mu.Unlock()
		return nil
	}
	if len(s.attached) >= s.manager.maxReadersPerStream {
		s.mu.Unlock()
		return fmt.Errorf("log stream reader limit reached: at most %d containers can be followed", s.manager.maxReadersPerStream)
	}
	s.manager.mu.Lock()
	if s.manager.closed || s.manager.sessions[s.streamID] != s {
		s.manager.mu.Unlock()
		s.mu.Unlock()
		return context.Canceled
	}
	if s.manager.reservedReaders >= s.manager.maxReaders {
		s.manager.mu.Unlock()
		s.mu.Unlock()
		return fmt.Errorf("global log reader limit reached: at most %d readers can be active", s.manager.maxReaders)
	}
	s.manager.reservedReaders++
	s.attached[key] = struct{}{}
	s.manager.mu.Unlock()
	s.mu.Unlock()
	s.startProducer(container, key)
	return nil
}

func (s *session) startProducer(container models.ContainerSummary, key string) {
	s.producers.Add(1)
	go func() {
		s.activeProducers.Add(1)
		defer s.producers.Done()
		defer s.activeProducers.Add(-1)
		defer s.removeAttachment(key)
		s.produce(container, key)
	}()
}

func (s *session) produce(container models.ContainerSummary, key string) {
	resume := logResumeState{}
	backoff := s.manager.retryInitial
	consecutiveFailures := 0
	var lastErr error
	for {
		attemptStarted := s.manager.now().UTC()
		result := s.readContainer(container, key, &resume)
		lastErr = result.err
		if !resume.valid && s.req.Tail == 0 {
			resume.setWatermark(attemptStarted)
		} else if !resume.valid && result.opened {
			resume.setWatermark(result.openedAt)
		}
		if s.ctx.Err() != nil || errors.Is(lastErr, context.Canceled) {
			return
		}
		if !s.req.Follow {
			if lastErr != nil {
				s.publishError(lastErr)
			}
			return
		}
		if lastErr != nil && !retryableLogError(lastErr) {
			s.publishError(lastErr)
			return
		}
		retry, stateErr := s.containerCanProduce(container)
		if stateErr != nil {
			if !retryableLogError(stateErr) {
				s.publishError(stateErr)
				return
			}
			if lastErr == nil {
				lastErr = stateErr
			}
		}
		if !retry {
			if lastErr != nil {
				s.publishError(lastErr)
			}
			return
		}
		if lastErr == nil {
			lastErr = io.ErrUnexpectedEOF
		}
		if result.delivered > 0 || result.connectedFor >= s.manager.retryHealthy {
			consecutiveFailures = 0
			backoff = s.manager.retryInitial
		}
		consecutiveFailures++
		if consecutiveFailures >= s.manager.retryAttempts {
			s.publishError(fmt.Errorf("log reader for %s stopped after %d attempts without a healthy recovery: %w", key, consecutiveFailures, lastErr))
			return
		}
		if !waitForRetry(s.ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > s.manager.retryMaximum {
			backoff = s.manager.retryMaximum
		}
	}
}

type logReadResult struct {
	err          error
	opened       bool
	openedAt     time.Time
	connectedFor time.Duration
	delivered    int
}

type logResumeState struct {
	valid        bool
	timestamp    time.Time
	fingerprints []uint64
	overflow     bool
}

func (r *logResumeState) setWatermark(timestamp time.Time) {
	if timestamp.IsZero() {
		return
	}
	r.valid = true
	r.timestamp = timestamp.UTC()
	r.fingerprints = r.fingerprints[:0]
	r.overflow = false
}

func (r *logResumeState) record(line models.LogLine) {
	if !r.valid || line.TS.After(r.timestamp) {
		r.setWatermark(line.TS)
	}
	if !line.TS.Equal(r.timestamp) {
		return
	}
	if len(r.fingerprints) < maxLogResumeBoundaryFingerprints {
		r.fingerprints = append(r.fingerprints, logLineFingerprint(line))
	} else {
		r.overflow = true
	}
}

type logResumeReplay struct {
	active                  bool
	timestamp               time.Time
	expectedFingerprints    []uint64
	nextFingerprint         int
	verificationUnavailable bool
	warned                  bool
}

func newLogResumeReplay(resume logResumeState) *logResumeReplay {
	return &logResumeReplay{
		active:                  resume.valid && !resume.overflow,
		timestamp:               resume.timestamp,
		expectedFingerprints:    resume.fingerprints,
		verificationUnavailable: resume.valid && resume.overflow,
	}
}

func (r *logResumeReplay) shouldSkip(line models.LogLine) (bool, bool) {
	if r.verificationUnavailable {
		r.verificationUnavailable = false
		return false, true
	}
	if !r.active {
		return false, false
	}
	if line.TS.Before(r.timestamp) {
		r.active = false
		return false, true
	}
	if line.TS.Equal(r.timestamp) && r.nextFingerprint < len(r.expectedFingerprints) {
		if logLineFingerprint(line) != r.expectedFingerprints[r.nextFingerprint] {
			r.active = false
			return false, true
		}
		r.nextFingerprint++
		if r.nextFingerprint == len(r.expectedFingerprints) {
			r.active = false
		}
		return true, false
	}
	if r.nextFingerprint < len(r.expectedFingerprints) {
		r.active = false
		return false, true
	}
	r.active = false
	return false, false
}

const (
	maxLogResumeBoundaryFingerprints        = 4096
	logBoundaryHashOffset            uint64 = 14695981039346656037
	logBoundaryHashPrime             uint64 = 1099511628211
)

func logLineFingerprint(line models.LogLine) uint64 {
	return extendLogBoundaryHash(logBoundaryHashOffset, line)
}

func extendLogBoundaryHash(value uint64, line models.LogLine) uint64 {
	for _, field := range []string{line.Stream, line.Text} {
		for index := 0; index < len(field); index++ {
			value ^= uint64(field[index])
			value *= logBoundaryHashPrime
		}
		value ^= 0xff
		value *= logBoundaryHashPrime
	}
	if line.Truncated {
		value ^= 1
		value *= logBoundaryHashPrime
	}
	return value
}

func (s *session) readContainer(container models.ContainerSummary, key string, resume *logResumeState) logReadResult {
	id := container.ID
	if id == "" {
		id = container.Name
	}
	options := dockercore.LogOptions{
		Follow:     s.req.Follow,
		Tail:       s.req.Tail,
		Since:      s.req.Since,
		Timestamps: true,
	}
	replay := newLogResumeReplay(*resume)
	if resume.valid {
		options.Tail = -1
		options.Since = resume.timestamp.Format(time.RFC3339Nano)
	}
	result := logReadResult{openedAt: s.manager.now().UTC()}
	reader, err := s.manager.Docker.ContainerLogs(s.ctx, id, options)
	if err != nil {
		result.err = err
		return result
	}
	result.opened = true
	if reader == nil {
		result.err = fmt.Errorf("Docker returned an empty log reader for %s", key)
		return result
	}
	connectedAt := time.Now()
	managedReader := s.addReader(key, reader)
	if s.ctx.Err() != nil {
		managedReader.requestClose()
		_ = managedReader.waitClose()
		s.removeReader(key)
		result.err = s.ctx.Err()
		return result
	}
	readErr := ReadDockerLogStream(s.ctx, reader, sourceFromContainer(container), s.manager.now, func(line models.LogLine) bool {
		skip, mismatch := replay.shouldSkip(line)
		if mismatch && !replay.warned {
			replay.warned = true
			resume.setWatermark(line.TS)
			s.publishError(fmt.Errorf("log resume boundary for %s changed or exceeded its verification window; records were replayed to avoid loss and may include duplicates", key))
		}
		if skip {
			return true
		}
		if !s.enqueue(line) {
			return false
		}
		result.delivered++
		resume.record(line)
		return true
	})
	result.connectedFor = time.Since(connectedAt)
	managedReader.requestClose()
	closeErr := managedReader.waitClose()
	s.removeReader(key)
	result.err = readErr
	if result.err == nil && closeErr != nil {
		result.err = closeErr
	}
	return result
}

func retryableLogError(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	for _, code := range []apperror.Code{
		apperror.NotFound,
		apperror.PermissionDenied,
		apperror.Conflict,
		apperror.Cancelled,
	} {
		if apperror.IsCode(err, code) {
			return false
		}
	}
	return true
}

func (s *session) containerCanProduce(container models.ContainerSummary) (bool, error) {
	id := container.ID
	if id == "" {
		id = container.Name
	}
	detail, err := s.manager.Docker.GetContainer(s.ctx, id)
	if err != nil {
		return true, err
	}
	if detail == nil {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(detail.Summary.State)) {
	case "dead", "exited", "removed", "removing", "stopped":
		return false, nil
	default:
		return true, nil
	}
}

func (s *session) removeAttachment(key string) {
	s.mu.Lock()
	_, existed := s.attached[key]
	if existed {
		delete(s.attached, key)
	}
	s.mu.Unlock()
	if existed {
		s.manager.mu.Lock()
		if s.manager.reservedReaders > 0 {
			s.manager.reservedReaders--
		}
		s.manager.mu.Unlock()
	}
}

func containerKey(container models.ContainerSummary) string {
	if container.ID != "" {
		return container.ID
	}
	return container.Name
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type managedLogReader struct {
	closer    io.Closer
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

func (r *managedLogReader) requestClose() {
	if r == nil || r.closer == nil {
		return
	}
	r.closeOnce.Do(func() {
		go func() {
			r.closeErr = r.closer.Close()
			close(r.closeDone)
		}()
	})
}

func (r *managedLogReader) waitClose() error {
	if r == nil {
		return nil
	}
	r.requestClose()
	<-r.closeDone
	return r.closeErr
}

func (s *session) addReader(key string, reader io.Closer) *managedLogReader {
	if reader == nil {
		return nil
	}
	managed := &managedLogReader{closer: reader, closeDone: make(chan struct{})}
	s.mu.Lock()
	s.activeReaders[key] = managed
	s.mu.Unlock()
	return managed
}

func (s *session) removeReader(key string) {
	s.mu.Lock()
	delete(s.activeReaders, key)
	s.mu.Unlock()
}

func (s *session) closeReaders() {
	s.mu.Lock()
	readers := make([]*managedLogReader, 0, len(s.activeReaders))
	for _, reader := range s.activeReaders {
		readers = append(readers, reader)
	}
	s.mu.Unlock()
	for _, reader := range readers {
		reader.requestClose()
	}
}

func (s *session) closeInput() {
	s.closeInputOnce.Do(func() {
		close(s.input)
	})
}

func (s *session) finishProducers(closeInput bool) {
	s.producerDoneOnce.Do(func() {
		go func() {
			s.producers.Wait()
			if closeInput {
				s.closeInput()
			}
			close(s.producerDone)
		}()
	})
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
			containers, err := s.manager.resolveContainers(s.ctx, s.req, true)
			if err != nil {
				s.publishError(err)
				continue
			}
			for index, container := range containers {
				if err := s.attach(container); err != nil {
					if s.ctx.Err() != nil || errors.Is(err, context.Canceled) {
						return
					}
					remaining := len(containers) - index
					s.publishError(fmt.Errorf("%w; %d candidate containers were not attached", err, remaining))
					break
				}
			}
		}
	}
}

func (s *session) enqueue(line models.LogLine) bool {
	size := retainedLogLineBytes(line)
	if !reserveBytes(&s.queuedBytes, size, s.manager.inputBytes) {
		s.dropped.Add(1)
		return true
	}
	select {
	case <-s.ctx.Done():
		s.queuedBytes.Add(-size)
		return false
	case s.input <- line:
		return true
	default:
		s.queuedBytes.Add(-size)
		s.dropped.Add(1)
		return true
	}
}

func reserveBytes(counter *atomic.Int64, size int64, limit int64) bool {
	if size < 0 || size > limit {
		return false
	}
	for {
		current := counter.Load()
		if current > limit-size {
			return false
		}
		if counter.CompareAndSwap(current, current+size) {
			return true
		}
	}
}

func (s *session) batchLoop() {
	defer close(s.done)
	ticker := time.NewTicker(s.manager.batchWindow)
	defer ticker.Stop()

	batch := make([]models.LogLine, 0, s.manager.batchMaxLines)
	var batchBytes int64
	payloadBaseBytes := serializedLinesPayloadBaseBytes(s.streamID)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.manager.publish(bus.TopicLogsLines, LinesPayload{StreamID: s.streamID, Lines: append([]models.LogLine(nil), batch...)})
		batch = batch[:0]
		batchBytes = 0
	}
	appendLine := func(line models.LogLine) {
		line = s.ring.add(line)
		s.retainedBytes.Store(s.ring.retainedBytes)
		var ok bool
		line, _, ok = fitSerializedLogLine(line, s.manager.batchBytes-payloadBaseBytes)
		if !ok {
			s.dropped.Add(1)
			return
		}
		size := serializedLogLineBytes(line)
		separatorBytes := int64(0)
		if len(batch) > 0 {
			separatorBytes = 1
		}
		if len(batch) > 0 && payloadBaseBytes+batchBytes+separatorBytes+size > s.manager.batchBytes {
			flush()
			separatorBytes = 0
		}
		batch = append(batch, line)
		batchBytes += separatorBytes + size
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
			s.queuedBytes.Add(-retainedLogLineBytes(line))
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

func serializedLinesPayloadBaseBytes(streamID string) int64 {
	raw, err := json.Marshal(LinesPayload{StreamID: streamID, Lines: []models.LogLine{}})
	if err != nil {
		return minimumBatchBytes
	}
	// Replacing [] with [line,...] adds encoded lines and separators while the
	// two array delimiters already remain represented by this base size.
	return int64(len(raw))
}

func serializedLogLineBytes(line models.LogLine) int64 {
	raw, err := json.Marshal(line)
	if err != nil {
		return 1<<63 - 1
	}
	return int64(len(raw))
}

func fitSerializedLogLine(line models.LogLine, limit int64) (models.LogLine, int64, bool) {
	if limit <= 0 {
		return models.LogLine{}, 0, false
	}
	size := serializedLogLineBytes(line)
	if size <= limit {
		return line, size, true
	}
	const suffix = " … [truncated: live log event byte limit]"
	original := line.Text
	line.Truncated = true
	line.Text = suffix
	minimum := serializedLogLineBytes(line)
	if minimum > limit {
		return models.LogLine{}, 0, false
	}
	low, high := 0, len(original)
	for low < high {
		middle := low + (high-low+1)/2
		line.Text = strings.ToValidUTF8(original[:middle], "") + suffix
		if serializedLogLineBytes(line) <= limit {
			low = middle
		} else {
			high = middle - 1
		}
	}
	line.Text = strings.ToValidUTF8(original[:low], "") + suffix
	return line, serializedLogLineBytes(line), true
}

func (s *session) publishError(err error) {
	if err == nil {
		return
	}
	s.manager.publish(bus.TopicLogsError, ErrorPayload{StreamID: s.streamID, Error: err.Error()})
}

func (s *session) beginDrain() {
	s.drainOnce.Do(func() {
		s.cancel()
		go func() {
			if s.watchDone != nil {
				<-s.watchDone
			}
			s.finishProducers(true)
			<-s.producerDone
			<-s.done
			s.manager.removeDraining(s.streamID, s)
			close(s.drainDone)
		}()
		s.closeReaders()
	})
}

func (s *session) stop(ctx context.Context) error {
	s.beginDrain()
	// Close is requested once per reader. Repeated stops only wait again while
	// the session remains addressable in the manager's draining set.
	s.closeReaders()
	select {
	case <-s.drainDone:
		return nil
	case <-ctx.Done():
		s.closeReaders()
		return ctx.Err()
	}
}

func (m *Manager) publish(topic bus.Topic, payload any) {
	if m.Events == nil {
		return
	}
	m.Events.Publish(bus.Event{Topic: topic, Payload: payload})
}

func (m *Manager) Diagnostics() models.LogRuntimeDiagnostics {
	m.ensureReady()
	m.mu.Lock()
	defer m.mu.Unlock()
	var producers int64
	retainedBytes := m.pageSnapshotBytesInUse
	for _, s := range m.sessions {
		producers += s.activeProducers.Load()
		retainedBytes += s.retainedBytes.Load()
	}
	for _, s := range m.draining {
		producers += s.activeProducers.Load()
		retainedBytes += s.retainedBytes.Load()
	}
	return models.LogRuntimeDiagnostics{
		ActiveStreams:    len(m.sessions),
		PendingStreams:   m.pendingStreams,
		DrainingStreams:  len(m.draining),
		ReservedReaders:  m.reservedReaders,
		RetainedBytes:    retainedBytes,
		ActiveProducers:  producers,
		ActiveOperations: m.activeOperations,
	}
}
