package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
)

func NewManager(docker DockerClient, repo *store.MetricsRepository, projects *store.ProjectRepository, audit *store.AuditRepository, events bus.Bus, opts Options) *Manager {
	manager := &Manager{
		Docker:     docker,
		Repository: repo,
		Projects:   projects,
		Audit:      audit,
		Events:     events,
	}
	manager.applyOptions(opts)
	return manager
}

func (m *Manager) Start(ctx context.Context) {
	m.ensureReady()
	if m.Docker == nil {
		return
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.started = true
	m.mu.Unlock()

	go m.reconcileLoop()
	go m.persistLoop()
}

func (m *Manager) StopAll() {
	m.ensureReady()
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	sessions := make([]*streamSession, 0, len(m.sessions))
	for id, session := range m.sessions {
		delete(m.sessions, id)
		sessions = append(sessions, session)
	}
	watchers := make([]*containerWatcher, 0, len(m.watchers))
	for id, watcher := range m.watchers {
		delete(m.watchers, id)
		watchers = append(watchers, watcher)
	}
	m.started = false
	m.mu.Unlock()
	for _, session := range sessions {
		session.cancel()
		<-session.done
	}
	for _, watcher := range watchers {
		watcher.cancel()
	}
	_ = m.flush(context.Background())
}

func (m *Manager) StartStatsStream(ctx context.Context, scope models.StatsScope) (string, error) {
	m.ensureReady()
	if err := m.requireDocker(); err != nil {
		return "", err
	}
	scope = normalizeScope(scope)
	if err := validateScope(scope); err != nil {
		return "", err
	}
	m.Start(context.Background())
	streamID := uuid.NewString()
	session := newStreamSession(m, streamID, scope)

	m.mu.Lock()
	m.sessions[streamID] = session
	m.mu.Unlock()

	go session.run()
	go func() {
		_ = m.reconcileOnce(ctx)
	}()
	return streamID, nil
}

func (m *Manager) StopStream(streamID string) error {
	m.ensureReady()
	m.mu.Lock()
	session := m.sessions[streamID]
	if session != nil {
		delete(m.sessions, streamID)
	}
	m.mu.Unlock()
	if session == nil {
		return apperror.New(apperror.NotFound, "Stats stream was not found")
	}
	session.cancel()
	<-session.done
	return nil
}

func (m *Manager) GetDashboardMetrics(ctx context.Context) (*models.DashboardMetrics, error) {
	m.ensureReady()
	if err := m.requireDocker(); err != nil {
		return nil, err
	}
	containers, err := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	images, err := m.Docker.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	volumes, err := m.Docker.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	usage, err := m.Docker.DiskUsage(ctx)
	if err != nil {
		return nil, err
	}
	if usage == nil {
		usage = &models.DiskUsage{}
	}
	out := &models.DashboardMetrics{
		Containers: len(containers),
		Images:     len(images),
		Volumes:    len(volumes),
		DiskUsage:  *usage,
		Top:        m.topContainers(),
	}
	if m.Projects != nil {
		projects, err := m.Projects.List(ctx)
		if err != nil {
			return nil, err
		}
		out.Projects = len(projects)
	}
	if m.Audit != nil {
		recent, err := m.Audit.List(ctx, models.AuditFilter{Limit: 10})
		if err != nil {
			return nil, err
		}
		out.RecentEvents = recent
	}
	return out, nil
}

func (m *Manager) GetProjectMetrics(ctx context.Context, projectID string, r models.TimeRange) (*models.SeriesBundle, error) {
	if m.Repository == nil {
		return nil, notReady()
	}
	return m.Repository.QuerySeries(ctx, store.MetricsSeriesFilter{
		ProviderID: m.providerID(),
		ProjectID:  strings.TrimSpace(projectID),
		Resolution: store.ResolutionForRange(r.From, r.To),
		From:       r.From,
		To:         r.To,
	})
}

func (m *Manager) GetContainerMetrics(ctx context.Context, containerID string, r models.TimeRange) (*models.SeriesBundle, error) {
	if m.Repository == nil {
		return nil, notReady()
	}
	return m.Repository.QuerySeries(ctx, store.MetricsSeriesFilter{
		ProviderID:  m.providerID(),
		ContainerID: strings.TrimSpace(containerID),
		Resolution:  store.ResolutionForRange(r.From, r.To),
		From:        r.From,
		To:          r.To,
	})
}

func (m *Manager) reconcileLoop() {
	ticker := time.NewTicker(m.backgroundInterval)
	defer ticker.Stop()
	_ = m.reconcileOnce(m.ctx)
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			_ = m.reconcileOnce(m.ctx)
		}
	}
}

func (m *Manager) reconcileOnce(ctx context.Context) error {
	if err := m.requireDocker(); err != nil {
		return err
	}
	m.refreshDockerInfo(ctx)
	containers, err := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: false})
	if err != nil {
		return err
	}
	current := map[string]models.ContainerSummary{}
	for _, item := range containers {
		if item.ID == "" {
			continue
		}
		current[item.ID] = item
	}

	m.mu.Lock()
	for id, summary := range current {
		m.containers[id] = summary
		if m.watchers[id] == nil && m.ctx != nil {
			watchCtx, cancel := context.WithCancel(m.ctx)
			m.watchers[id] = &containerWatcher{id: id, cancel: cancel}
			go m.watchContainer(watchCtx, id)
		}
	}
	for id, watcher := range m.watchers {
		if _, ok := current[id]; ok {
			continue
		}
		watcher.cancel()
		delete(m.watchers, id)
		delete(m.containers, id)
		delete(m.latest, id)
		delete(m.previous, id)
		delete(m.lastAccepted, id)
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) watchContainer(ctx context.Context, containerID string) {
	failures := 0
	for ctx.Err() == nil {
		if failures < 3 {
			err := m.streamContainer(ctx, containerID)
			if err == nil || errors.Is(err, context.Canceled) {
				return
			}
			failures++
			sleepContext(ctx, time.Duration(failures)*time.Second)
			continue
		}
		err := m.sampleOneShot(ctx, containerID)
		if err == nil {
			failures = 0
		}
		sleepContext(ctx, m.sampleInterval(containerID))
	}
}

func (m *Manager) streamContainer(ctx context.Context, containerID string) error {
	reader, err := m.Docker.ContainerStats(ctx, containerID, dockercore.StatsOptions{Stream: true})
	if err != nil {
		return err
	}
	defer func() {
		if reader != nil && reader.Body != nil {
			_ = reader.Body.Close()
		}
	}()

	decoder := json.NewDecoder(reader.Body)
	for ctx.Err() == nil {
		var raw container.StatsResponse
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return err
			}
			return err
		}
		m.ingest(containerID, raw)
	}
	return ctx.Err()
}

func (m *Manager) sampleOneShot(ctx context.Context, containerID string) error {
	reader, err := m.Docker.ContainerStats(ctx, containerID, dockercore.StatsOptions{OneShot: true})
	if err != nil {
		return err
	}
	defer func() {
		if reader != nil && reader.Body != nil {
			_ = reader.Body.Close()
		}
	}()
	var raw container.StatsResponse
	if err := json.NewDecoder(reader.Body).Decode(&raw); err != nil {
		return err
	}
	m.ingest(containerID, raw)
	return nil
}

func (m *Manager) ingest(containerID string, raw container.StatsResponse) {
	sample, ok := m.buildSample(containerID, raw)
	if !ok {
		return
	}

	m.mu.Lock()
	interval := m.sampleIntervalLocked(containerID)
	last := m.lastAccepted[containerID]
	if !last.IsZero() && sample.SampledAt.Sub(last) < interval {
		m.mu.Unlock()
		return
	}
	m.lastAccepted[containerID] = sample.SampledAt
	m.latest[containerID] = sample
	if m.Repository != nil {
		m.pending = append(m.pending, recordFromSample(sample))
	}
	m.mu.Unlock()
}

func (m *Manager) buildSample(containerID string, raw container.StatsResponse) (Sample, bool) {
	if raw.Read.IsZero() {
		raw.Read = m.now()
	}
	if containerID == "" {
		containerID = raw.ID
	}
	if containerID == "" {
		return Sample{}, false
	}

	rx, txBytes := networkBytes(raw.Networks)
	blockRead, blockWrite := blockBytes(raw)

	m.mu.Lock()
	previous, hasPrevious := m.previous[containerID]
	summary := m.containers[containerID]
	onlineCPUs := m.onlineCPUs
	m.previous[containerID] = raw
	m.mu.Unlock()

	cpuPrevious := raw.PreCPUStats
	if hasPrevious {
		cpuPrevious = previous.CPUStats
	}
	cpu := CPUPercentWithFallback(cpuPrevious, raw.CPUStats, onlineCPUs)
	var netRXRate, netTXRate, blockReadRate, blockWriteRate float64
	if hasPrevious {
		previousRX, previousTX := networkBytes(previous.Networks)
		previousBlockRead, previousBlockWrite := blockBytes(previous)
		elapsed := raw.Read.Sub(previous.Read)
		netRXRate = CounterRate(previousRX, rx, elapsed)
		netTXRate = CounterRate(previousTX, txBytes, elapsed)
		blockReadRate = CounterRate(previousBlockRead, blockRead, elapsed)
		blockWriteRate = CounterRate(previousBlockWrite, blockWrite, elapsed)
	}

	if summary.ID == "" {
		summary.ID = containerID
	}
	if summary.Name == "" && raw.Name != "" {
		summary.Name = strings.TrimPrefix(raw.Name, "/")
	}
	serviceID := summary.Service
	if summary.ProjectID != "" && summary.Service != "" {
		serviceID = summary.ProjectID + "::" + summary.Service
	}
	uptime := int64(0)
	if !summary.CreatedAt.IsZero() {
		uptime = int64(raw.Read.Sub(summary.CreatedAt).Seconds())
		if uptime < 0 {
			uptime = 0
		}
	}

	return Sample{
		ProviderID:       m.providerID(),
		ProjectID:        summary.ProjectID,
		ServiceID:        serviceID,
		ContainerID:      containerID,
		ContainerName:    summary.Name,
		Health:           summary.Health,
		RestartCount:     summary.Restarts,
		UptimeSeconds:    uptime,
		CPUPercent:       cpu,
		MemoryBytes:      memoryUsageBytes(raw.MemoryStats),
		MemoryLimitBytes: memoryLimitBytes(raw.MemoryStats),
		NetworkRXBytes:   uintToInt64(rx),
		NetworkTXBytes:   uintToInt64(txBytes),
		NetworkRXRate:    netRXRate,
		NetworkTXRate:    netTXRate,
		BlockReadBytes:   uintToInt64(blockRead),
		BlockWriteBytes:  uintToInt64(blockWrite),
		BlockReadRate:    blockReadRate,
		BlockWriteRate:   blockWriteRate,
		PIDs:             pids(raw),
		SampledAt:        raw.Read.UTC(),
	}, true
}

func (m *Manager) persistLoop() {
	ticker := time.NewTicker(m.persistInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			_ = m.flush(context.Background())
			return
		case <-ticker.C:
			_ = m.flush(m.ctx)
			m.maybeRetain(m.ctx)
		}
	}
}

func (m *Manager) flush(ctx context.Context) error {
	if m.Repository == nil {
		return nil
	}
	m.mu.Lock()
	pending := append([]store.MetricsSampleRecord(nil), m.pending...)
	m.pending = nil
	m.mu.Unlock()
	return m.Repository.InsertBatch(ctx, pending)
}

func (m *Manager) maybeRetain(ctx context.Context) {
	if m.Repository == nil {
		return
	}
	now := m.now()
	m.mu.Lock()
	last := m.lastRetain
	if !last.IsZero() && now.Sub(last) < m.retainInterval {
		m.mu.Unlock()
		return
	}
	m.lastRetain = now
	m.mu.Unlock()
	_ = m.Repository.RetainAndDownsample(ctx, now)
}

func (m *Manager) refreshDockerInfo(ctx context.Context) {
	m.mu.Lock()
	hasCPUCount := m.onlineCPUs > 0
	m.mu.Unlock()
	if hasCPUCount || m.Docker == nil {
		return
	}
	info, err := m.Docker.Info(ctx)
	if err != nil || info == nil || info.CPUs <= 0 {
		return
	}
	m.mu.Lock()
	if m.onlineCPUs == 0 {
		m.onlineCPUs = uint32(info.CPUs)
	}
	m.mu.Unlock()
}

func newStreamSession(manager *Manager, streamID string, scope models.StatsScope) *streamSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &streamSession{
		id:      streamID,
		scope:   scope,
		ctx:     ctx,
		cancel:  cancel,
		manager: manager,
		done:    make(chan struct{}),
	}
}

func (s *streamSession) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.manager.publishInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			samples := s.manager.latestForScope(s.scope)
			if len(samples) == 0 {
				continue
			}
			s.manager.publish(bus.TopicStatsSample, SamplePayload{StreamID: s.id, Samples: samples})
		}
	}
}

func (m *Manager) latestForScope(scope models.StatsScope) []Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	samples := make([]Sample, 0, len(m.latest))
	for _, sample := range m.latest {
		if scopeMatchesSample(scope, sample) {
			samples = append(samples, sample)
		}
	}
	sort.Slice(samples, func(i int, j int) bool {
		return samples[i].ContainerName < samples[j].ContainerName
	})
	return samples
}

func (m *Manager) topContainers() []models.MetricRankItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]models.MetricRankItem, 0, len(m.latest))
	for _, sample := range m.latest {
		name := sample.ContainerName
		if name == "" {
			name = sample.ContainerID
		}
		items = append(items, models.MetricRankItem{
			ID:          sample.ContainerID,
			Name:        name,
			Kind:        ScopeContainer,
			CPUPercent:  sample.CPUPercent,
			MemoryBytes: sample.MemoryBytes,
		})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].CPUPercent == items[j].CPUPercent {
			return items[i].MemoryBytes > items[j].MemoryBytes
		}
		return items[i].CPUPercent > items[j].CPUPercent
	})
	if len(items) > m.topN {
		items = items[:m.topN]
	}
	return items
}

func (m *Manager) sampleInterval(containerID string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sampleIntervalLocked(containerID)
}

func (m *Manager) sampleIntervalLocked(containerID string) time.Duration {
	summary := m.containers[containerID]
	for _, session := range m.sessions {
		if scopeMatchesContainer(session.scope, summary) {
			return m.visibleInterval
		}
	}
	return m.backgroundInterval
}

func (m *Manager) publish(topic bus.Topic, payload any) {
	if m.Events == nil {
		return
	}
	m.Events.Publish(bus.Event{Topic: topic, Payload: payload})
}

func (m *Manager) ensureReady() {
	if m.watchers == nil {
		m.watchers = map[string]*containerWatcher{}
	}
	if m.sessions == nil {
		m.sessions = map[string]*streamSession{}
	}
	if m.containers == nil {
		m.containers = map[string]models.ContainerSummary{}
	}
	if m.latest == nil {
		m.latest = map[string]Sample{}
	}
	if m.previous == nil {
		m.previous = map[string]container.StatsResponse{}
	}
	if m.lastAccepted == nil {
		m.lastAccepted = map[string]time.Time{}
	}
	if m.visibleInterval <= 0 {
		m.visibleInterval = defaultVisibleInterval
	}
	if m.backgroundInterval <= 0 {
		m.backgroundInterval = defaultBackgroundInterval
	}
	if m.publishInterval <= 0 {
		m.publishInterval = defaultPublishInterval
	}
	if m.persistInterval <= 0 {
		m.persistInterval = defaultPersistInterval
	}
	if m.retainInterval <= 0 {
		m.retainInterval = defaultRetainInterval
	}
	if m.topN <= 0 {
		m.topN = defaultTopN
	}
	if m.now == nil {
		m.now = func() time.Time { return time.Now().UTC() }
	}
}

func (m *Manager) applyOptions(opts Options) {
	m.visibleInterval = opts.VisibleInterval
	m.backgroundInterval = opts.BackgroundInterval
	m.publishInterval = opts.PublishInterval
	m.persistInterval = opts.PersistInterval
	m.retainInterval = opts.RetainInterval
	m.topN = opts.TopN
	m.now = opts.Now
	m.ensureReady()
}

func (m *Manager) requireDocker() error {
	if m.Docker == nil {
		return notReady()
	}
	return nil
}

func (m *Manager) providerID() string {
	if m.Docker == nil {
		return ""
	}
	return m.Docker.ProviderID()
}

func notReady() error {
	return apperror.New(apperror.ProviderNotReady, "Docker metrics are not ready")
}

func recordFromSample(sample Sample) store.MetricsSampleRecord {
	return store.MetricsSampleRecord{
		ProviderID:       sample.ProviderID,
		ProjectID:        sample.ProjectID,
		ServiceID:        sample.ServiceID,
		ContainerID:      sample.ContainerID,
		CPUPercent:       sample.CPUPercent,
		MemoryBytes:      sample.MemoryBytes,
		MemoryLimitBytes: sample.MemoryLimitBytes,
		NetworkRXBytes:   sample.NetworkRXBytes,
		NetworkTXBytes:   sample.NetworkTXBytes,
		BlockReadBytes:   sample.BlockReadBytes,
		BlockWriteBytes:  sample.BlockWriteBytes,
		PIDs:             sample.PIDs,
		Resolution:       store.MetricsResolutionRaw,
		SampledAt:        sample.SampledAt,
	}
}

func normalizeScope(scope models.StatsScope) models.StatsScope {
	scope.Kind = strings.TrimSpace(scope.Kind)
	if scope.Kind == "" {
		scope.Kind = ScopeAll
	}
	scope.Kind = strings.ToLower(scope.Kind)
	for i := range scope.IDs {
		scope.IDs[i] = strings.TrimSpace(scope.IDs[i])
	}
	return scope
}

func validateScope(scope models.StatsScope) error {
	switch scope.Kind {
	case ScopeAll, ScopeProject, ScopeService, ScopeContainer:
		return nil
	default:
		return apperror.New(apperror.NotFound, "Unsupported stats scope", apperror.WithDetail(scope.Kind))
	}
}

func scopeMatchesContainer(scope models.StatsScope, container models.ContainerSummary) bool {
	if container.ID == "" && container.Name == "" {
		return false
	}
	return scopeMatchesSample(scope, Sample{
		ProjectID:     container.ProjectID,
		ServiceID:     serviceID(container.ProjectID, container.Service),
		ContainerID:   container.ID,
		ContainerName: container.Name,
	})
}

func scopeMatchesSample(scope models.StatsScope, sample Sample) bool {
	switch scope.Kind {
	case ScopeAll:
		return true
	case ScopeProject:
		return contains(scope.IDs, sample.ProjectID)
	case ScopeService:
		return contains(scope.IDs, sample.ServiceID) || contains(scope.IDs, serviceNameOnly(sample.ServiceID))
	case ScopeContainer:
		return contains(scope.IDs, sample.ContainerID) || contains(scope.IDs, sample.ContainerName)
	default:
		return false
	}
}

func serviceID(projectID string, service string) string {
	if projectID == "" {
		return service
	}
	if service == "" {
		return ""
	}
	return projectID + "::" + service
}

func serviceNameOnly(value string) string {
	if _, service, ok := strings.Cut(value, "::"); ok {
		return service
	}
	return value
}

func contains(values []string, target string) bool {
	if target == "" {
		return false
	}
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sleepContext(ctx context.Context, duration time.Duration) {
	if duration <= 0 {
		return
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
