package providers

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
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
	runner    CommandRunner

	mu           sync.RWMutex
	activeID     string
	installPlans map[string]installPlanRecord
}

type installPlanRecord struct {
	providerID string
	steps      int
	command    string
	risk       models.Risk
}

type distroConfigurable interface {
	SetDistro(string)
}

type colimaConfigurable interface {
	SetColimaConfig(profile string, cpu, memoryGB, diskGB int)
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
		repo:         repo,
		settings:     settings,
		providers:    providersByID,
		order:        order,
		now:          func() time.Time { return time.Now().UTC() },
		runner:       ExecRunner{},
		installPlans: map[string]installPlanRecord{},
	}
}

func NewDefaultManager(repo *store.ProviderRepository, settings *store.SettingsRepository, linuxSocketPath string) *Manager {
	var providerSet []PlatformProvider
	switch runtime.GOOS {
	case "linux":
		providerSet = append(providerSet, NewLinuxNative(LinuxNativeOptions{SocketPath: linuxSocketPath}))
	case "darwin":
		providerSet = append(providerSet, NewMacOSColima(MacOSColimaOptions{}))
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
	m.applyProviderSettings(ctx, provider)
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
	for _, id := range m.order {
		m.applyProviderSettings(ctx, m.providers[id])
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
	if strings.HasPrefix(providerID, existingContextIDPrefix) {
		contextName := strings.TrimPrefix(providerID, existingContextIDPrefix)
		if err := m.ensureExistingContextProvider(ctx, contextName); err != nil {
			return err
		}
	}
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

func (m *Manager) ActiveProvider(ctx context.Context) (PlatformProvider, error) {
	providerID := m.ActiveProviderID(ctx)
	if providerID == "" {
		return nil, apperror.New(apperror.ProviderNotReady, "No active Docker provider")
	}
	if strings.HasPrefix(providerID, existingContextIDPrefix) {
		contextName := strings.TrimPrefix(providerID, existingContextIDPrefix)
		if err := m.ensureExistingContextProvider(ctx, contextName); err != nil {
			return nil, err
		}
	}
	m.mu.RLock()
	provider, ok := m.providers[providerID]
	m.mu.RUnlock()
	if !ok {
		return nil, apperror.New(apperror.NotFound, "Active provider was not found")
	}
	m.applyProviderSettings(ctx, provider)
	return provider, nil
}

func (m *Manager) PlanInstall(ctx context.Context, providerID string, opts models.InstallOptions) (*models.CommandPlan, error) {
	provider, ok := m.providers[providerID]
	if !ok {
		return nil, apperror.New(apperror.NotFound, "Provider was not found")
	}
	m.applyProviderSettings(ctx, provider)
	plan, err := provider.PlanInstall(ctx, opts)
	if err != nil {
		return nil, err
	}
	if plan != nil {
		m.mu.Lock()
		m.installPlans[plan.PlanID] = installPlanRecord{
			providerID: providerID,
			steps:      len(plan.Commands),
			command:    plannedCommandText(plan),
			risk:       plan.Risk,
		}
		m.mu.Unlock()
	}
	return plan, nil
}

func (m *Manager) InstallPlanAuditContext(planID string) (string, string, models.Risk) {
	m.mu.RLock()
	record, ok := m.installPlans[planID]
	m.mu.RUnlock()
	if !ok {
		return "", "", ""
	}
	return record.providerID, record.command, record.risk
}

func (m *Manager) ApplyInstall(ctx context.Context, planID string, progress chan<- InstallProgress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	record, ok := m.installPlans[planID]
	if ok {
		delete(m.installPlans, planID)
	}
	m.mu.Unlock()
	if !ok {
		return apperror.New(apperror.PlanExpired, "Install plan expired or was not found")
	}
	provider, ok := m.providers[record.providerID]
	if !ok {
		return apperror.New(apperror.NotFound, "Provider was not found")
	}
	m.applyProviderSettings(ctx, provider)
	for step := range record.steps {
		if err := provider.ExecuteInstallStep(ctx, planID, step, progress); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Start(ctx context.Context, providerID string) error {
	provider, ok := m.providers[providerID]
	if !ok {
		return apperror.New(apperror.NotFound, "Provider was not found")
	}
	m.applyProviderSettings(ctx, provider)
	return provider.Start(ctx)
}

func (m *Manager) Stop(ctx context.Context, providerID string) error {
	provider, ok := m.providers[providerID]
	if !ok {
		return apperror.New(apperror.NotFound, "Provider was not found")
	}
	m.applyProviderSettings(ctx, provider)
	return provider.Stop(ctx)
}

func (m *Manager) Restart(ctx context.Context, providerID string) error {
	provider, ok := m.providers[providerID]
	if !ok {
		return apperror.New(apperror.NotFound, "Provider was not found")
	}
	m.applyProviderSettings(ctx, provider)
	return provider.Restart(ctx)
}

func (m *Manager) ListDockerContexts(ctx context.Context) ([]models.DockerContextInfo, error) {
	contexts, ok := listDockerContexts(ctx, m.contextRunner())
	if !ok {
		return nil, apperror.New(apperror.ProviderNotReady, "Docker contexts are not available")
	}
	return contexts, nil
}

func (m *Manager) SetDockerContext(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return apperror.New(apperror.Conflict, "Docker context name is required")
	}
	if err := m.ensureExistingContextProvider(ctx, name); err != nil {
		return err
	}
	providerID := ExistingContextProviderID(name)
	status, err := m.Detect(ctx, providerID)
	if err != nil {
		return err
	}
	if status == nil || !status.Healthy {
		return apperror.New(apperror.ProviderNotReady, "Docker context is not reachable")
	}
	return m.SetActiveProvider(ctx, providerID)
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

func (m *Manager) ensureExistingContextProvider(ctx context.Context, contextName string) error {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return apperror.New(apperror.Conflict, "Docker context name is required")
	}
	contexts, ok := listDockerContexts(ctx, m.contextRunner())
	if !ok {
		return apperror.New(apperror.ProviderNotReady, "Docker contexts are not available")
	}
	found := false
	for _, dockerContext := range contexts {
		if dockerContext.Name == contextName {
			found = true
			break
		}
	}
	if !found {
		return apperror.New(apperror.NotFound, "Docker context was not found")
	}
	provider := NewExistingContext(ExistingContextOptions{ContextName: contextName, Runner: m.contextRunner()})
	m.mu.Lock()
	if _, exists := m.providers[provider.ID()]; !exists {
		m.providers[provider.ID()] = provider
		m.order = append(m.order, provider.ID())
	}
	m.mu.Unlock()
	return m.ensureProviderRecord(ctx, provider)
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

func (m *Manager) applyProviderSettings(ctx context.Context, provider PlatformProvider) {
	if m.settings == nil || provider == nil {
		return
	}
	if configurable, ok := provider.(distroConfigurable); ok && provider.Type() == TypeWindowsWSL {
		if distro, err := m.settings.GetString(ctx, "windows.wsl_distro"); err == nil && strings.TrimSpace(distro) != "" {
			configurable.SetDistro(distro)
		}
	}
	if configurable, ok := provider.(colimaConfigurable); ok && provider.Type() == TypeMacOSColima {
		profile := ""
		cpu := 0
		memoryGB := 0
		diskGB := 0
		if value, err := m.settings.GetString(ctx, "macos.colima_profile"); err == nil {
			profile = value
		}
		if value, err := m.settings.GetInt(ctx, "macos.colima_cpu"); err == nil {
			cpu = value
		}
		if value, err := m.settings.GetInt(ctx, "macos.colima_memory_gb"); err == nil {
			memoryGB = value
		}
		if value, err := m.settings.GetInt(ctx, "macos.colima_disk_gb"); err == nil {
			diskGB = value
		}
		configurable.SetColimaConfig(profile, cpu, memoryGB, diskGB)
	}
}

func (m *Manager) contextRunner() CommandRunner {
	if m.runner != nil {
		return m.runner
	}
	return ExecRunner{}
}

func plannedCommandText(plan *models.CommandPlan) string {
	if plan == nil {
		return ""
	}
	commands := make([]string, 0, len(plan.Commands))
	for _, command := range plan.Commands {
		commands = append(commands, command.Command)
	}
	return strings.Join(commands, "\n")
}
