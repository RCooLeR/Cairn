//go:build windows && wslintegration

package backups

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestManagerRealWSLDockerBackupRestoreRoundTrip(t *testing.T) {
	if os.Getenv("CAIRN_REAL_WSL_DOCKER_BACKUPS") != "1" {
		t.Skip("set CAIRN_REAL_WSL_DOCKER_BACKUPS=1 to run against the local cairn-dev WSL distro")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	provider := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: "cairn-dev"})
	status, err := provider.Detect(ctx)
	if err != nil {
		t.Fatalf("provider Detect() error = %v", err)
	}
	if !status.Healthy {
		t.Fatalf("cairn-dev WSL provider is not healthy: %#v", status.Problems)
	}

	suffix := time.Now().UnixNano()
	sourceVolume := fmt.Sprintf("cairn-wsl-backup-src-%d", suffix)
	restoredVolume := fmt.Sprintf("cairn-wsl-backup-restored-%d", suffix)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = runProviderDocker(cleanupCtx, provider, "volume", "rm", "-f", sourceVolume, restoredVolume)
	})

	runWSLDockerCommand(t, ctx, provider, "volume", "create", sourceVolume)
	writeProviderVolumeData(t, ctx, provider, sourceVolume, "alpha", "beta")

	eventBus := bus.New()
	defer eventBus.Close()
	client := dockercore.New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Docker Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

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

	manager := NewManager(
		fakeProviderResolver{provider: provider},
		client,
		db.Settings(),
		db.Backups(),
		db.Audit(),
		eventBus,
		"test",
	)
	backupDir := t.TempDir()
	done := eventBus.Subscribe(ctx, bus.TopicJobDone, 8)

	backupPlan, err := manager.PlanBackupVolume(ctx, models.BackupVolumeRequest{
		VolumeName: sourceVolume,
		DestPath:   backupDir,
		ProjectID:  "windows_wsl_ubuntu/app-db",
	})
	if err != nil {
		t.Fatalf("PlanBackupVolume() error = %v", err)
	}
	backupJob, err := manager.ApplyBackup(ctx, backupPlan.PlanID)
	if err != nil {
		t.Fatalf("ApplyBackup() error = %v", err)
	}
	waitJobDone(t, done, backupJob)

	records, err := manager.ListBackups(ctx, models.BackupFilter{VolumeName: sourceVolume})
	if err != nil {
		t.Fatalf("ListBackups() error = %v", err)
	}
	if len(records) != 1 || records[0].Result != "success" {
		t.Fatalf("backup records = %#v", records)
	}

	restorePlan, err := manager.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		BackupID:   records[0].ID,
		VolumeName: restoredVolume,
		Overwrite:  false,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume(new) error = %v", err)
	}
	restoreJob, err := manager.ApplyRestore(ctx, restorePlan.PlanID, "")
	if err != nil {
		t.Fatalf("ApplyRestore(new) error = %v", err)
	}
	waitJobDone(t, done, restoreJob)
	if got := readProviderVolumeData(t, ctx, provider, restoredVolume); got != "alpha:beta" {
		t.Fatalf("restored data = %q, want alpha:beta", got)
	}

	writeProviderVolumeData(t, ctx, provider, sourceVolume, "stale", "data")
	overwritePlan, err := manager.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		BackupID:   records[0].ID,
		VolumeName: sourceVolume,
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume(overwrite) error = %v", err)
	}
	if _, err := manager.ApplyRestore(ctx, overwritePlan.PlanID, "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("ApplyRestore(wrong typed name) error = %v", err)
	}
	overwriteJob, err := manager.ApplyRestore(ctx, overwritePlan.PlanID, sourceVolume)
	if err != nil {
		t.Fatalf("ApplyRestore(overwrite) error = %v", err)
	}
	waitJobDone(t, done, overwriteJob)
	if got := readProviderVolumeData(t, ctx, provider, sourceVolume); got != "alpha:beta" {
		t.Fatalf("overwritten data = %q, want alpha:beta", got)
	}
}

func writeProviderVolumeData(t *testing.T, ctx context.Context, provider providers.PlatformProvider, volumeName string, value string, nested string) {
	t.Helper()
	script := fmt.Sprintf(
		"printf %%s %s > /data/value.txt && mkdir -p /data/nested && printf %%s %s > /data/nested/check.txt",
		shellQuote(value),
		shellQuote(nested),
	)
	runWSLDockerCommand(t, ctx, provider, "run", "--rm", "-v", volumeName+":/data", "alpine:3", "sh", "-c", script)
}

func readProviderVolumeData(t *testing.T, ctx context.Context, provider providers.PlatformProvider, volumeName string) string {
	t.Helper()
	return runWSLDockerCommand(t, ctx, provider, "run", "--rm", "-v", volumeName+":/data:ro", "alpine:3", "sh", "-c", "cat /data/value.txt; printf :; cat /data/nested/check.txt")
}

func runWSLDockerCommand(t *testing.T, ctx context.Context, provider providers.PlatformProvider, args ...string) string {
	t.Helper()
	result, err := provider.RunDocker(ctx, args...)
	if err != nil || result == nil || result.ExitCode != 0 {
		stderr := ""
		if result != nil {
			stderr = strings.TrimSpace(result.Stderr)
		}
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(result.Stdout)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
