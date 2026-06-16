package providers

import (
	"context"
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

	colimaProfile  string
	colimaCPU      int
	colimaMemoryGB int
	colimaDiskGB   int
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
	return nil, nil
}
func (p *fakeProvider) ExecuteInstallStep(context.Context, string, int, chan<- InstallProgress) error {
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
