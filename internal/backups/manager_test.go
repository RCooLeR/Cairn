package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestPlanBackupVolumeWarnsForRunningContainers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, _, _ := newTestManager(t)
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{
		Summary: models.VolumeSummary{
			Name:      "app-db",
			Labels:    map[string]string{"com.docker.compose.project": "app"},
			SizeBytes: 1024,
		},
		Containers: []models.ContainerSummary{{ID: "c1", Name: "db-1", State: "running"}},
	}

	plan, err := mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: t.TempDir()})
	if err != nil {
		t.Fatalf("PlanBackupVolume() error = %v", err)
	}
	if plan.Risk != models.RiskSafe {
		t.Fatalf("risk = %q, want safe", plan.Risk)
	}
	if len(plan.Commands) != 1 || !strings.Contains(plan.Commands[0].Command, "tar czf") {
		t.Fatalf("commands = %#v", plan.Commands)
	}
	if !slices.ContainsFunc(plan.Effects, func(effect string) bool {
		return strings.Contains(effect, "running containers") && strings.Contains(effect, "db-1")
	}) {
		t.Fatalf("effects missing running-container warning: %#v", plan.Effects)
	}
}

func TestBackupSidecarAndFilenameCollision(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts := time.Date(2026, 6, 13, 16, 0, 0, 0, time.UTC)
	archive, archivePath, metadataPath := backupPaths(dir, "app/db", ts)
	if archive != "app-db-20260613T160000Z.tar.gz" {
		t.Fatalf("archive = %q", archive)
	}
	if err := os.WriteFile(archivePath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := os.WriteFile(metadataPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	archive, _, _ = backupPaths(dir, "app/db", ts)
	if archive != "app-db-20260613T160000Z-2.tar.gz" {
		t.Fatalf("collision archive = %q", archive)
	}

	payload := []byte("backup-data")
	path := filepath.Join(dir, archive)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	sum := sha256.Sum256(payload)
	sidecar := BackupSidecar{FormatVersion: formatVersion, Volume: "app-db", SHA256: hex.EncodeToString(sum[:])}
	sidecarPath := metadataPathForArchive(path)
	if err := writeSidecar(sidecarPath, sidecar); err != nil {
		t.Fatalf("writeSidecar() error = %v", err)
	}
	read, err := readSidecar(sidecarPath)
	if err != nil {
		t.Fatalf("readSidecar() error = %v", err)
	}
	if read.Volume != "app-db" {
		t.Fatalf("sidecar = %#v", read)
	}
	if err := verifyArchiveChecksum(path, read.SHA256); err != nil {
		t.Fatalf("verifyArchiveChecksum() error = %v", err)
	}
	if err := verifyArchiveChecksum(path, "bad"); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("checksum mismatch error = %v", err)
	}
}

func TestApplyBackupWritesMetadataAndRepositoryRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, events, provider := newTestManager(t)
	dest := t.TempDir()
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: dest})
	if err != nil {
		t.Fatalf("PlanBackupVolume() error = %v", err)
	}
	jobID, err := mgr.ApplyBackup(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("ApplyBackup() error = %v", err)
	}
	waitJobDone(t, done, jobID)

	backups, err := mgr.ListBackups(ctx, models.BackupFilter{VolumeName: "app-db"})
	if err != nil {
		t.Fatalf("ListBackups() error = %v", err)
	}
	if len(backups) != 1 || backups[0].Result != "success" {
		t.Fatalf("backups = %#v", backups)
	}
	if _, err := os.Stat(backups[0].Path); err != nil {
		t.Fatalf("backup archive missing: %v", err)
	}
	if _, err := os.Stat(backups[0].MetadataPath); err != nil {
		t.Fatalf("backup metadata missing: %v", err)
	}
	if !provider.hasRunArg("tar") {
		t.Fatalf("provider calls = %#v", provider.calls)
	}
}

func TestRestoreOverwriteRequiresTypedNameAndRunsHelper(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, events, provider := newTestManager(t)
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "app-db.tar.gz")
	payload := []byte("backup-data")
	if err := os.WriteFile(archivePath, payload, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	sum := sha256.Sum256(payload)
	if err := writeSidecar(metadataPathForArchive(archivePath), BackupSidecar{
		FormatVersion: formatVersion,
		Volume:        "app-db",
		Project:       "app",
		SHA256:        hex.EncodeToString(sum[:]),
	}); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db",
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	if plan.Risk != models.RiskDangerous || plan.RequiresTypedName != "app-db" {
		t.Fatalf("plan = %#v", plan)
	}
	if _, err := mgr.ApplyRestore(ctx, plan.PlanID, "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("ApplyRestore(wrong) error = %v", err)
	}
	jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "app-db")
	if err != nil {
		t.Fatalf("ApplyRestore() error = %v", err)
	}
	waitJobDone(t, done, jobID)
	if !provider.hasRunArg("tar xzf") {
		t.Fatalf("provider calls = %#v", provider.calls)
	}
}

func newTestManager(t *testing.T) (*Manager, *bus.MemoryBus, *fakeBackupProvider) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	provider := &fakeBackupProvider{}
	eventBus := bus.New()
	t.Cleanup(eventBus.Close)
	mgr := NewManager(
		fakeProviderResolver{provider: provider},
		&fakeBackupDocker{volumes: map[string]*models.VolumeDetail{}},
		db.Settings(),
		db.Backups(),
		db.Audit(),
		eventBus,
		"test",
	)
	mgr.Now = func() time.Time { return time.Date(2026, 6, 13, 16, 0, 0, 0, time.UTC) }
	mgr.NewID = func() string { return "id" }
	mgr.AvailableBytes = func(string) (uint64, bool) { return 1 << 40, true }
	return mgr, eventBus, provider
}

type fakeProviderResolver struct {
	provider providers.PlatformProvider
}

func (r fakeProviderResolver) ActiveProvider(context.Context) (providers.PlatformProvider, error) {
	return r.provider, nil
}

type fakeBackupProvider struct {
	calls [][]string
}

func (p *fakeBackupProvider) ID() string          { return "linux_native" }
func (p *fakeBackupProvider) DisplayName() string { return "Linux Native" }
func (p *fakeBackupProvider) Type() string        { return providers.TypeLinuxNative }
func (p *fakeBackupProvider) Platform() string    { return providers.PlatformLinux }
func (p *fakeBackupProvider) Detect(context.Context) (*models.ProviderStatus, error) {
	return &models.ProviderStatus{Healthy: true}, nil
}
func (p *fakeBackupProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return nil, nil
}
func (p *fakeBackupProvider) ExecuteInstallStep(context.Context, string, int, chan<- providers.InstallProgress) error {
	return nil
}
func (p *fakeBackupProvider) Start(context.Context) error   { return nil }
func (p *fakeBackupProvider) Stop(context.Context) error    { return nil }
func (p *fakeBackupProvider) Restart(context.Context) error { return nil }
func (p *fakeBackupProvider) DockerHost(context.Context) (string, error) {
	return "unix:///var/run/docker.sock", nil
}
func (p *fakeBackupProvider) DockerContext(context.Context) (string, error) { return "default", nil }
func (p *fakeBackupProvider) RunDocker(_ context.Context, args ...string) (*providers.CommandResult, error) {
	p.calls = append(p.calls, append([]string(nil), args...))
	if slices.Contains(args, "czf") {
		archive := ""
		backupDir := ""
		for i, arg := range args {
			if arg == "-v" && i+1 < len(args) && strings.HasSuffix(args[i+1], ":/backup") {
				backupDir = strings.TrimSuffix(args[i+1], ":/backup")
			}
			if arg == "czf" && i+1 < len(args) {
				archive = strings.TrimPrefix(args[i+1], "/backup/")
			}
		}
		if archive != "" && backupDir != "" {
			if err := os.WriteFile(filepath.Join(backupDir, archive), []byte("backup-data"), 0o600); err != nil {
				return &providers.CommandResult{ExitCode: 1, Stderr: err.Error()}, err
			}
		}
	}
	return &providers.CommandResult{ExitCode: 0}, nil
}
func (p *fakeBackupProvider) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	return nil, nil
}
func (p *fakeBackupProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeBackupProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeBackupProvider) MapPathToBackend(path string) (string, error) { return path, nil }
func (p *fakeBackupProvider) MapPathToHost(path string) (string, error)    { return path, nil }
func (p *fakeBackupProvider) hasRunArg(value string) bool {
	for _, call := range p.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, value) {
			return true
		}
	}
	return false
}

type fakeBackupDocker struct {
	volumes map[string]*models.VolumeDetail
}

func (d *fakeBackupDocker) ProviderID() string { return "linux_native" }
func (d *fakeBackupDocker) GetVolume(_ context.Context, name string) (*models.VolumeDetail, error) {
	volume, ok := d.volumes[name]
	if !ok {
		return nil, apperror.New(apperror.NotFound, "Volume was not found")
	}
	return volume, nil
}
func (d *fakeBackupDocker) CreateVolume(_ context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	summary := &models.VolumeSummary{Name: req.Name, Driver: firstNonEmpty(req.Driver, "local")}
	d.volumes[req.Name] = &models.VolumeDetail{Summary: *summary}
	return summary, nil
}

func waitJobDone(t *testing.T, events <-chan bus.Event, jobID string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			payload, ok := event.Payload.(jobDonePayload)
			if ok && payload.JobID == jobID {
				if payload.Error != "" {
					t.Fatalf("job %s failed: %s", jobID, payload.Error)
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for job %s", jobID)
		}
	}
}
