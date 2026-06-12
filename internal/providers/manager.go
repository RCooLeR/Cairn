package providers

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

const detectBudget = 5 * time.Second

type Manager struct {
	repo      *store.ProviderRepository
	settings  *store.SettingsRepository
	providers map[string]PlatformProvider
	order     []string
	now       func() time.Time

	mu       sync.RWMutex
	activeID string
}

func NewManager(repo *store.ProviderRepository, settings *store.SettingsRepository, providerSet []PlatformProvider) *Manager {
	providersByID := make(map[string]PlatformProvider, len(providerSet))
	order := make([]string, 0, len(providerSet))
	for _, provider := range providerSet {
		if provider == nil {
			continue
		}
		providersByID[provider.ID()] = provider
		order = append(order, provider.ID())
	}
	return &Manager{
		repo:      repo,
		settings:  settings,
		providers: providersByID,
		order:     order,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func NewDefaultManager(repo *store.ProviderRepository, settings *store.SettingsRepository, linuxSocketPath string) *Manager {
	var providerSet []PlatformProvider
	if runtime.GOOS == "linux" {
		providerSet = append(providerSet, NewLinuxNative(LinuxNativeOptions{SocketPath: linuxSocketPath}))
	}
	return NewManager(repo, settings, providerSet)
}

func (m *Manager) Detect(ctx context.Context, providerID string) (*models.ProviderStatus, error) {
	provider, ok := m.providers[providerID]
	if !ok {
		return nil, apperror.New(apperror.NotFound, "Provider was not found")
	}
	if err := m.ensureProviderRecord(ctx, provider); err != nil {
		return nil, err
	}
	detectCtx, cancel := context.WithTimeout(ctx, detectBudget)
	defer cancel()
	status, err := provider.Detect(detectCtx)
	if err != nil {
		return nil, err
	}
	if err := m.repo.SaveStatus(ctx, providerID, status, m.now()); err != nil {
		return nil, err
	}
	m.updateActiveAfterDetect(ctx, map[string]*models.ProviderStatus{providerID: status})
	return status, nil
}

func (m *Manager) DetectAll(ctx context.Context) (map[string]*models.ProviderStatus, error) {
	if err := m.ensureProviderRecords(ctx); err != nil {
		return nil, err
	}

	type detectResult struct {
		id     string
		status *models.ProviderStatus
		err    error
	}
	results := make(chan detectResult, len(m.providers))
	for _, id := range m.order {
		provider := m.providers[id]
		go func() {
			detectCtx, cancel := context.WithTimeout(ctx, detectBudget)
			defer cancel()
			status, err := provider.Detect(detectCtx)
			results <- detectResult{id: provider.ID(), status: status, err: err}
		}()
	}

	statuses := make(map[string]*models.ProviderStatus, len(m.providers))
	var joined error
	for range m.providers {
		result := <-results
		if result.err != nil {
			joined = errors.Join(joined, result.err)
			continue
		}
		statuses[result.id] = result.status
		if err := m.repo.SaveStatus(ctx, result.id, result.status, m.now()); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	m.updateActiveAfterDetect(ctx, statuses)
	return statuses, joined
}

func (m *Manager) ListProviders(ctx context.Context) ([]models.ProviderSummary, error) {
	if err := m.ensureProviderRecords(ctx); err != nil {
		return nil, err
	}
	records, err := m.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	activeID := m.ActiveProviderID(ctx)
	summaries := make([]models.ProviderSummary, 0, len(records))
	for _, record := range records {
		status := models.ProviderStatus{}
		if record.LastStatusJSON != "" {
			if err := json.Unmarshal([]byte(record.LastStatusJSON), &status); err != nil {
				return nil, err
			}
		}
		summaries = append(summaries, models.ProviderSummary{
			ID:      record.ID,
			Name:    record.DisplayName,
			Kind:    record.Type,
			Active:  record.ID == activeID,
			Status:  status,
			Healthy: status.Healthy,
		})
	}
	return summaries, nil
}

func (m *Manager) GetProvider(ctx context.Context, providerID string) (*models.ProviderDetail, error) {
	summaries, err := m.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	for _, summary := range summaries {
		if summary.ID == providerID {
			return &models.ProviderDetail{
				Summary:  summary,
				Problems: summary.Status.Problems,
			}, nil
		}
	}
	return nil, apperror.New(apperror.NotFound, "Provider was not found")
}

func (m *Manager) SetActiveProvider(ctx context.Context, providerID string) error {
	if _, ok := m.providers[providerID]; !ok {
		return apperror.New(apperror.NotFound, "Provider was not found")
	}
	if err := m.settings.SetString(ctx, "provider.active_id", providerID); err != nil {
		return err
	}
	m.mu.Lock()
	m.activeID = providerID
	m.mu.Unlock()
	return nil
}

func (m *Manager) ActiveProviderID(ctx context.Context) string {
	m.mu.RLock()
	activeID := m.activeID
	m.mu.RUnlock()
	if activeID != "" {
		return activeID
	}
	if m.settings == nil {
		return ""
	}
	saved, err := m.settings.GetString(ctx, "provider.active_id")
	if err == nil {
		return saved
	}
	return ""
}

func (m *Manager) PlanInstall(ctx context.Context, providerID string, opts models.InstallOptions) (*models.CommandPlan, error) {
	provider, ok := m.providers[providerID]
	if !ok {
		return nil, apperror.New(apperror.NotFound, "Provider was not found")
	}
	return provider.PlanInstall(ctx, opts)
}

func (m *Manager) Start(ctx context.Context, providerID string) error {
	provider, ok := m.providers[providerID]
	if !ok {
		return apperror.New(apperror.NotFound, "Provider was not found")
	}
	return provider.Start(ctx)
}

func (m *Manager) Stop(ctx context.Context, providerID string) error {
	provider, ok := m.providers[providerID]
	if !ok {
		return apperror.New(apperror.NotFound, "Provider was not found")
	}
	return provider.Stop(ctx)
}

func (m *Manager) Restart(ctx context.Context, providerID string) error {
	provider, ok := m.providers[providerID]
	if !ok {
		return apperror.New(apperror.NotFound, "Provider was not found")
	}
	return provider.Restart(ctx)
}

func (m *Manager) ListDockerContexts(context.Context) ([]models.DockerContextInfo, error) {
	return []models.DockerContextInfo{}, nil
}

func (m *Manager) ensureProviderRecords(ctx context.Context) error {
	for _, id := range m.order {
		if err := m.ensureProviderRecord(ctx, m.providers[id]); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ensureProviderRecord(ctx context.Context, provider PlatformProvider) error {
	return m.repo.Upsert(ctx, store.ProviderRecord{
		ID:          provider.ID(),
		Type:        provider.Type(),
		Platform:    provider.Platform(),
		DisplayName: provider.DisplayName(),
		Enabled:     true,
	})
}

func (m *Manager) updateActiveAfterDetect(ctx context.Context, statuses map[string]*models.ProviderStatus) {
	if m.settings == nil {
		return
	}
	saved, _ := m.settings.GetString(ctx, "provider.active_id")
	if saved != "" {
		if status, ok := statuses[saved]; ok && status.Healthy {
			m.setActiveBestEffort(ctx, saved)
			return
		}
	}
	for _, id := range m.order {
		if status, ok := statuses[id]; ok && status.Healthy {
			m.setActiveBestEffort(ctx, id)
			return
		}
	}
}

func (m *Manager) setActiveBestEffort(ctx context.Context, providerID string) {
	_ = m.settings.SetString(ctx, "provider.active_id", providerID)
	m.mu.Lock()
	m.activeID = providerID
	m.mu.Unlock()
}
