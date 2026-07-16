package providers

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestManagerDetectAllPersistsAndSelectsSavedHealthyProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{
		&fakeProvider{id: "linux_native", kind: TypeLinuxNative, platform: PlatformLinux, healthy: true},
	})

	if err := db.Settings().SetString(ctx, "provider.active_id", "linux_native"); err != nil {
		t.Fatalf("SetString() error = %v", err)
	}

	statuses, err := manager.DetectAll(ctx)
	if err != nil {
		t.Fatalf("DetectAll() error = %v", err)
	}
	if !statuses["linux_native"].Healthy {
		t.Fatalf("linux_native status = %#v", statuses["linux_native"])
	}
	if activeID := manager.ActiveProviderID(ctx); activeID != "linux_native" {
		t.Fatalf("ActiveProviderID() = %q", activeID)
	}

	summaries, err := manager.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	if len(summaries) != 1 || !summaries[0].Active || !summaries[0].Healthy {
		t.Fatalf("summaries = %#v", summaries)
	}

	record, err := db.Providers().Get(ctx, "linux_native")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.LastStatusJSON == "" || record.LastCheckedAt.IsZero() {
		t.Fatalf("record not updated: %#v", record)
	}
}

func TestManagerRuntimeWarningOverlaysDetectionUntilCleared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	provider := &fakeProvider{id: "linux_native", kind: TypeLinuxNative, platform: PlatformLinux, healthy: true}
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{provider})
	if _, err := manager.Detect(ctx, provider.ID()); err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	manager.SetRuntimeWarning(provider.ID(), models.ProviderWarning{
		Code:    WarningDockerBridgeUnavailable,
		Message: "  Docker CLI bridge unavailable  ",
	})
	// A later detection result must not erase a live process-local warning.
	if _, err := manager.Detect(ctx, provider.ID()); err != nil {
		t.Fatalf("Detect(second) error = %v", err)
	}
	summaries, err := manager.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	if len(summaries) != 1 || len(summaries[0].Status.Warnings) != 1 {
		t.Fatalf("summaries with runtime warning = %#v", summaries)
	}
	warning := summaries[0].Status.Warnings[0]
	if warning.Code != WarningDockerBridgeUnavailable || warning.Message != "Docker CLI bridge unavailable" {
		t.Fatalf("runtime warning = %#v", warning)
	}

	record, err := db.Providers().Get(ctx, provider.ID())
	if err != nil {
		t.Fatalf("Get provider record: %v", err)
	}
	var persisted models.ProviderStatus
	if err := json.Unmarshal([]byte(record.LastStatusJSON), &persisted); err != nil {
		t.Fatalf("Unmarshal persisted status: %v", err)
	}
	if len(persisted.Warnings) != 0 {
		t.Fatalf("process-local warning was persisted: %#v", persisted.Warnings)
	}

	manager.ClearRuntimeWarning(provider.ID(), WarningDockerBridgeUnavailable)
	summaries, err = manager.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders(after clear) error = %v", err)
	}
	if len(summaries[0].Status.Warnings) != 0 {
		t.Fatalf("runtime warnings after clear = %#v", summaries[0].Status.Warnings)
	}
}

func TestManagerRuntimeWarningOverlayIsConcurrentSafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	provider := &fakeProvider{id: "linux_native", kind: TypeLinuxNative, platform: PlatformLinux, healthy: true}
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{provider})
	if _, err := manager.Detect(ctx, provider.ID()); err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			code := WarningDockerBridgeUnavailable
			manager.SetRuntimeWarning(provider.ID(), models.ProviderWarning{Code: code, Message: "degraded"})
			_, _ = manager.ListProviders(ctx)
			if index%2 == 0 {
				manager.ClearRuntimeWarning(provider.ID(), code)
			}
		}(i)
	}
	workers.Wait()
	if _, err := manager.ListProviders(ctx); err != nil {
		t.Fatalf("ListProviders() after concurrent overlays error = %v", err)
	}
}

func TestManagerSelectsBestDetectedWhenSavedProviderUnhealthy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{
		&fakeProvider{id: "linux_native", kind: TypeLinuxNative, platform: PlatformLinux, healthy: true},
		&fakeProvider{id: "ctx:remote", kind: TypeExistingContext, platform: PlatformAny, healthy: true},
	})
	if err := db.Settings().SetString(ctx, "provider.active_id", "ctx:missing"); err != nil {
		t.Fatalf("SetString() error = %v", err)
	}

	if _, err := manager.DetectAll(ctx); err != nil {
		t.Fatalf("DetectAll() error = %v", err)
	}
	if activeID := manager.ActiveProviderID(ctx); activeID != "linux_native" {
		t.Fatalf("ActiveProviderID() = %q, want linux_native", activeID)
	}
	saved, err := db.Settings().GetString(ctx, "provider.active_id")
	if err != nil {
		t.Fatalf("GetString(provider.active_id) error = %v", err)
	}
	if saved != "ctx:missing" {
		t.Fatalf("saved provider.active_id = %q, want original user intent", saved)
	}
}

func TestManagerApplyInstallKeepsPlanAfterStepFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	provider := &fakeProvider{id: "linux_native", kind: TypeLinuxNative, platform: PlatformLinux, installFailures: 1}
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{provider})

	plan, err := manager.PlanInstall(ctx, "linux_native", models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	if err := manager.ApplyInstall(ctx, plan.PlanID, nil); err == nil {
		t.Fatal("ApplyInstall() error = nil, want first step failure")
	}
	if _, _, risk := manager.InstallPlanAuditContext(plan.PlanID); risk == "" {
		t.Fatal("install plan was consumed after failed apply")
	}
	if err := manager.ApplyInstall(ctx, plan.PlanID, nil); err != nil {
		t.Fatalf("ApplyInstall() retry error = %v", err)
	}
	if _, _, risk := manager.InstallPlanAuditContext(plan.PlanID); risk != "" {
		t.Fatal("install plan remained after successful apply")
	}
}

func TestManagerAppliesWindowsWSLDistroSetting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	provider := &fakeProvider{id: "windows_wsl_ubuntu", kind: TypeWindowsWSL, platform: PlatformWindows, healthy: true}
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{provider})
	if err := db.Settings().SetString(ctx, "windows.wsl_distro", "cairn-dev"); err != nil {
		t.Fatalf("SetString() error = %v", err)
	}

	if _, err := manager.Detect(ctx, provider.ID()); err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if provider.distro != "cairn-dev" {
		t.Fatalf("provider distro = %q, want cairn-dev", provider.distro)
	}
}

func TestManagerApplySavedSettingsBeforeDetect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	provider := &fakeProvider{id: "windows_wsl_ubuntu", kind: TypeWindowsWSL, platform: PlatformWindows, healthy: true}
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{provider})
	if err := db.Settings().SetString(ctx, "windows.wsl_distro", "cairn-dev"); err != nil {
		t.Fatalf("SetString() error = %v", err)
	}

	manager.ApplySavedSettings(ctx)

	if provider.distro != "cairn-dev" {
		t.Fatalf("provider distro = %q, want cairn-dev", provider.distro)
	}
}

func TestManagerAppliesMacOSColimaSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	provider := &fakeProvider{id: "macos_colima", kind: TypeMacOSColima, platform: PlatformMacOS, healthy: true}
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{provider})
	if err := db.Settings().SetString(ctx, "macos.colima_profile", "dev"); err != nil {
		t.Fatalf("SetString profile error = %v", err)
	}
	if err := db.Settings().SetInt(ctx, "macos.colima_cpu", 6); err != nil {
		t.Fatalf("SetInt cpu error = %v", err)
	}
	if err := db.Settings().SetInt(ctx, "macos.colima_memory_gb", 12); err != nil {
		t.Fatalf("SetInt memory error = %v", err)
	}
	if err := db.Settings().SetInt(ctx, "macos.colima_disk_gb", 100); err != nil {
		t.Fatalf("SetInt disk error = %v", err)
	}

	if _, err := manager.Detect(ctx, provider.ID()); err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if provider.colimaProfile != "dev" || provider.colimaCPU != 6 || provider.colimaMemoryGB != 12 || provider.colimaDiskGB != 100 {
		t.Fatalf("colima settings = %q/%d/%d/%d", provider.colimaProfile, provider.colimaCPU, provider.colimaMemoryGB, provider.colimaDiskGB)
	}
}

func TestDetectBudgetForWindowsWSLAllowsColdStart(t *testing.T) {
	t.Parallel()
	if got := detectBudgetFor(&fakeProvider{id: "windows_wsl_ubuntu", kind: TypeWindowsWSL}); got != wslDetectBudget {
		t.Fatalf("Windows WSL detect budget = %s, want %s", got, wslDetectBudget)
	}
	if got := detectBudgetFor(&fakeProvider{id: "linux_native", kind: TypeLinuxNative}); got != detectBudget {
		t.Fatalf("Linux detect budget = %s, want %s", got, detectBudget)
	}
}

func openProviderTestStore(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()
	db, err := store.Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

type fakeProvider struct {
	id       string
	kind     string
	platform string
	healthy  bool
	distro   string

	colimaProfile   string
	colimaCPU       int
	colimaMemoryGB  int
	colimaDiskGB    int
	installFailures int
}

func (p *fakeProvider) ID() string          { return p.id }
func (p *fakeProvider) DisplayName() string { return p.id }
func (p *fakeProvider) Type() string        { return p.kind }
func (p *fakeProvider) Platform() string    { return p.platform }
func (p *fakeProvider) Detect(context.Context) (*models.ProviderStatus, error) {
	return &models.ProviderStatus{
		Installed:        p.healthy,
		Running:          p.healthy,
		Healthy:          p.healthy,
		DockerInstalled:  p.healthy,
		DockerRunning:    p.healthy,
		ComposeInstalled: p.healthy,
		BuildxInstalled:  p.healthy,
	}, nil
}
func (p *fakeProvider) SetDistro(distro string) {
	p.distro = distro
}
func (p *fakeProvider) SetColimaConfig(profile string, cpu, memoryGB, diskGB int) {
	p.colimaProfile = profile
	p.colimaCPU = cpu
	p.colimaMemoryGB = memoryGB
	p.colimaDiskGB = diskGB
}
func (p *fakeProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return &models.CommandPlan{
		PlanID: "plan-install-test",
		Risk:   models.RiskNeedsConfirmation,
		Commands: []models.PlannedCommand{{
			Order:   1,
			Command: "install",
			Risk:    models.RiskNeedsConfirmation,
		}},
	}, nil
}
func (p *fakeProvider) ExecuteInstallStep(context.Context, string, int, chan<- InstallProgress) error {
	if p.installFailures > 0 {
		p.installFailures--
		return errors.New("install failed")
	}
	return nil
}
func (p *fakeProvider) Start(context.Context) error { return nil }
func (p *fakeProvider) Stop(context.Context) error  { return nil }
func (p *fakeProvider) Restart(context.Context) error {
	return nil
}
func (p *fakeProvider) DockerHost(context.Context) (string, error)    { return "", nil }
func (p *fakeProvider) DockerContext(context.Context) (string, error) { return "", nil }
func (p *fakeProvider) RunDocker(context.Context, ...string) (*CommandResult, error) {
	return nil, nil
}
func (p *fakeProvider) RunCompose(context.Context, string, ...string) (*CommandResult, error) {
	return nil, nil
}
func (p *fakeProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeProvider) MapPathToBackend(path string) (string, error) { return path, nil }
func (p *fakeProvider) MapPathToHost(path string) (string, error)    { return path, nil }
