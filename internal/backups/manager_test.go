package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	archive, archivePath, metadataPath, err := backupPaths(dir, "app/db", ts)
	if err != nil {
		t.Fatalf("backupPaths() error = %v", err)
	}
	if archive != "app-db-20260613T160000Z.tar.gz" {
		t.Fatalf("archive = %q", archive)
	}
	if err := os.WriteFile(archivePath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := os.WriteFile(metadataPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	archive, _, _, err = backupPaths(dir, "app/db", ts)
	if err != nil {
		t.Fatalf("backupPaths(collision) error = %v", err)
	}
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

func TestBackupPathsReturnStatErrorsAndCapCollisions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts := time.Date(2026, 6, 13, 16, 0, 0, 0, time.UTC)
	_, _, _, err := backupPathsWithStat(dir, "app/db", ts, func(string) (os.FileInfo, error) {
		return nil, os.ErrPermission
	}, 3)
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("stat error = %v", err)
	}

	_, _, _, err = backupPathsWithStat(dir, "app/db", ts, func(string) (os.FileInfo, error) {
		return nil, nil
	}, 3)
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("collision cap error = %v", err)
	}
}

func TestCheckFreeSpaceIgnoresUnknownOrNegativeEstimates(t *testing.T) {
	t.Parallel()
	calls := 0
	mgr := &Manager{
		AvailableBytes: func(string) (uint64, bool) {
			calls++
			return 0, true
		},
	}

	if err := mgr.checkFreeSpace(t.TempDir(), -1); err != nil {
		t.Fatalf("checkFreeSpace(negative) error = %v, want nil", err)
	}
	if err := mgr.checkFreeSpace(t.TempDir(), 0); err != nil {
		t.Fatalf("checkFreeSpace(zero) error = %v, want nil", err)
	}
	if calls != 0 {
		t.Fatalf("AvailableBytes calls = %d, want 0 for unknown estimates", calls)
	}
	if err := mgr.checkFreeSpace(t.TempDir(), 1); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("checkFreeSpace(positive) error = %v, want conflict", err)
	}
}

func TestRemoveBackupArtifactsRemovesArchiveAndMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.tar.gz")
	metadataPath := filepath.Join(dir, "archive.tar.gz.json")
	if err := os.WriteFile(archivePath, []byte("archive"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := os.WriteFile(metadataPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	if err := removeBackupArtifacts(archivePath, metadataPath); err != nil {
		t.Fatalf("removeBackupArtifacts() error = %v", err)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("archive exists after cleanup: %v", err)
	}
	if _, err := os.Stat(metadataPath); !os.IsNotExist(err) {
		t.Fatalf("metadata exists after cleanup: %v", err)
	}
}

func TestRemoveBackupFilesJoinsRemoveErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive-dir")
	metadataPath := filepath.Join(dir, "metadata-dir")
	for _, path := range []string{archivePath, metadataPath} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write child: %v", err)
		}
	}

	err := removeBackupFiles(store.BackupRecord{
		BackupPath:   archivePath,
		MetadataPath: metadataPath,
	})
	if err == nil {
		t.Fatalf("removeBackupFiles() error = nil, want joined remove errors")
	}
	message := err.Error()
	if !strings.Contains(message, "archive-dir") || !strings.Contains(message, "metadata-dir") {
		t.Fatalf("joined error = %q, want both failed paths", message)
	}
}

func TestPlanDeleteBackupRequiresConfirmationAndRemovesRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, _, _ := newTestManager(t)
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.tar.gz")
	metadataPath := filepath.Join(dir, "archive.json")
	for _, item := range []struct {
		path string
		body string
	}{
		{archivePath, "archive"},
		{metadataPath, "{}"},
	} {
		if err := os.WriteFile(item.path, []byte(item.body), 0o600); err != nil {
			t.Fatalf("write %s: %v", item.path, err)
		}
	}
	record := store.BackupRecord{
		ID:                  "backup-delete",
		ProviderID:          "linux_native",
		ProjectID:           "linux_native/app",
		VolumeName:          "app-data",
		BackupPath:          archivePath,
		MetadataPath:        metadataPath,
		CompressedSizeBytes: 7,
		Result:              backupResultOK,
		CreatedAt:           time.Now().UTC(),
	}
	if err := mgr.Backups.Insert(ctx, record); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	if err := mgr.DeleteBackup(ctx, record.ID); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("DeleteBackup() error = %v, want confirmation required", err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive removed by rejected delete: %v", err)
	}
	if _, err := mgr.Backups.Get(ctx, record.ID); err != nil {
		t.Fatalf("record removed by rejected delete: %v", err)
	}

	plan, err := mgr.PlanDeleteBackup(ctx, record.ID)
	if err != nil {
		t.Fatalf("PlanDeleteBackup() error = %v", err)
	}
	if plan == nil || plan.Risk != models.RiskNeedsConfirmation {
		t.Fatalf("PlanDeleteBackup() plan = %#v", plan)
	}
	if err := mgr.ApplyDeleteBackup(ctx, plan.PlanID); err != nil {
		t.Fatalf("ApplyDeleteBackup() error = %v", err)
	}
	if _, err := mgr.Backups.Get(ctx, record.ID); err == nil {
		t.Fatal("backup record still exists after delete")
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("archive exists after delete: %v", err)
	}
	if _, err := os.Stat(metadataPath); !os.IsNotExist(err) {
		t.Fatalf("metadata exists after delete: %v", err)
	}
}

func TestRestoreHelperUsesPositionalArchiveAndRollbackStash(t *testing.T) {
	t.Parallel()
	archiveName := "app-db.tar.gz; touch /restore/pwned #"
	args := dockerRunRestoreArgs("app-db", "/tmp/backups", archiveName)
	if got, want := args[len(args)-2], "cairn-restore"; got != want {
		t.Fatalf("restore argv script name = %q, want %q", got, want)
	}
	if got, want := args[len(args)-1], "/backup/"+archiveName; got != want {
		t.Fatalf("restore archive argv = %q, want %q", got, want)
	}
	script := args[len(args)-3]
	if strings.Contains(script, archiveName) {
		t.Fatalf("archive name was interpolated into shell script: %q", script)
	}
	for _, want := range []string{`tar xzf "$archive"`, "stash_name", "rmdir \"$stash\""} {
		if !strings.Contains(script, want) {
			t.Fatalf("restore script missing %q: %q", want, script)
		}
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

func TestApplyBackupStopsWithManager(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, events, provider := newTestManager(t)
	rootCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	mgr.Start(rootCtx)
	provider.blockRun = make(chan struct{})
	provider.runStarted = make(chan struct{}, 1)
	provider.runCanceled = make(chan error, 1)
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
	select {
	case <-provider.runStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for backup helper to start")
	}
	mgr.StopAll()
	select {
	case err := <-provider.runCanceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("provider context error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for provider context cancellation")
	}
	payload := waitJobDonePayload(t, done, jobID)
	if payload.Error == "" {
		t.Fatalf("job error is empty, want cancellation failure")
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

func TestRestoreIntoNewVolumeRequiresConfirmationOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, _, _ := newTestManager(t)
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

	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db-restored",
		Overwrite:  false,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	if plan.Risk != models.RiskNeedsConfirmation || plan.RequiresTypedName != "" {
		t.Fatalf("plan = %#v, want confirmation without typed name", plan)
	}
}

func TestRestoreReverifiesChecksumBeforeRunningHelper(t *testing.T) {
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
	if err := os.WriteFile(archivePath, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper archive: %v", err)
	}
	jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "app-db")
	if err != nil {
		t.Fatalf("ApplyRestore() error = %v", err)
	}
	payloadDone := waitJobDonePayload(t, done, jobID)
	if payloadDone.Error == "" {
		t.Fatalf("restore succeeded after checksum mismatch")
	}
	if provider.hasRunArg("tar xzf") {
		t.Fatalf("restore helper ran despite checksum mismatch: %#v", provider.calls)
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
	mu          sync.Mutex
	calls       [][]string
	blockRun    chan struct{}
	runStarted  chan struct{}
	runCanceled chan error
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
func (p *fakeBackupProvider) RunDocker(ctx context.Context, args ...string) (*providers.CommandResult, error) {
	p.mu.Lock()
	p.calls = append(p.calls, append([]string(nil), args...))
	blockRun := p.blockRun
	runStarted := p.runStarted
	runCanceled := p.runCanceled
	p.mu.Unlock()
	if runStarted != nil {
		select {
		case runStarted <- struct{}{}:
		default:
		}
	}
	if blockRun != nil {
		select {
		case <-blockRun:
		case <-ctx.Done():
			if runCanceled != nil {
				runCanceled <- ctx.Err()
			}
			return &providers.CommandResult{ExitCode: 1, Stderr: ctx.Err().Error()}, ctx.Err()
		}
	}
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
	p.mu.Lock()
	defer p.mu.Unlock()
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
	payload := waitJobDonePayload(t, events, jobID)
	if payload.Error != "" {
		t.Fatalf("job %s failed: %s", jobID, payload.Error)
	}
}

func waitJobDonePayload(t *testing.T, events <-chan bus.Event, jobID string) jobDonePayload {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			payload, ok := event.Payload.(jobDonePayload)
			if ok && payload.JobID == jobID {
				return payload
			}
		case <-deadline:
			t.Fatalf("timed out waiting for job %s", jobID)
		}
	}
}
