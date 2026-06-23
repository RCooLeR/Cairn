//go:build linux

package backups

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestManagerRealDockerBackupRestoreRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real Docker backup integration runs only on Linux")
	}
	if os.Getenv("CAIRN_REAL_DOCKER_BACKUPS") != "1" {
		t.Skip("set CAIRN_REAL_DOCKER_BACKUPS=1 to run real backup integration")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	suffix := time.Now().UnixNano()
	sourceVolume := fmt.Sprintf("cairn-backup-src-%d", suffix)
	restoredVolume := fmt.Sprintf("cairn-backup-restored-%d", suffix)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = exec.CommandContext(cleanupCtx, "docker", "volume", "rm", "-f", sourceVolume, restoredVolume).Run()
	})

	runDockerCommand(t, ctx, "volume", "create", sourceVolume)
	writeVolumeData(t, ctx, sourceVolume, "alpha", "beta")

	eventBus := bus.New()
	defer eventBus.Close()
	provider := providers.NewLinuxNative(providers.LinuxNativeOptions{})
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
		ProjectID:  "linux_native/app-db",
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
	if _, err := os.Stat(records[0].Path); err != nil {
		t.Fatalf("backup archive missing: %v", err)
	}
	if _, err := os.Stat(records[0].MetadataPath); err != nil {
		t.Fatalf("backup metadata missing: %v", err)
	}

	restorePlan, err := manager.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		BackupID:   records[0].ID,
		VolumeName: restoredVolume,
		Overwrite:  false,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume(new) error = %v", err)
	}
	if restorePlan.Risk != models.RiskNeedsConfirmation || restorePlan.RequiresTypedName != "" {
		t.Fatalf("new restore plan = %#v, want confirmation without typed name", restorePlan)
	}
	restoreJob, err := manager.ApplyRestore(ctx, restorePlan.PlanID, "")
	if err != nil {
		t.Fatalf("ApplyRestore(new) error = %v", err)
	}
	waitJobDone(t, done, restoreJob)
	if got := readVolumeData(t, ctx, restoredVolume); got != "alpha:beta" {
		t.Fatalf("restored data = %q, want alpha:beta", got)
	}

	writeVolumeData(t, ctx, sourceVolume, "stale", "data")
	overwritePlan, err := manager.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		BackupID:   records[0].ID,
		VolumeName: sourceVolume,
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume(overwrite) error = %v", err)
	}
	if overwritePlan.RequiresTypedName != sourceVolume {
		t.Fatalf("RequiresTypedName = %q, want %q", overwritePlan.RequiresTypedName, sourceVolume)
	}
	if _, err := manager.ApplyRestore(ctx, overwritePlan.PlanID, "wrong"); err == nil {
		t.Fatalf("ApplyRestore(wrong typed name) succeeded")
	}
	overwriteJob, err := manager.ApplyRestore(ctx, overwritePlan.PlanID, sourceVolume)
	if err != nil {
		t.Fatalf("ApplyRestore(overwrite) error = %v", err)
	}
	waitJobDone(t, done, overwriteJob)
	if got := readVolumeData(t, ctx, sourceVolume); got != "alpha:beta" {
		t.Fatalf("overwritten data = %q, want alpha:beta", got)
	}
}

func writeVolumeData(t *testing.T, ctx context.Context, volumeName string, value string, nested string) {
	t.Helper()
	script := fmt.Sprintf(
		"printf %%s %s > /data/value.txt && mkdir -p /data/nested && printf %%s %s > /data/nested/check.txt",
		shellQuote(value),
		shellQuote(nested),
	)
	runDockerCommand(t, ctx, "run", "--rm", "-v", volumeName+":/data", "alpine:3", "sh", "-c", script)
}

func readVolumeData(t *testing.T, ctx context.Context, volumeName string) string {
	t.Helper()
	return runDockerCommand(t, ctx, "run", "--rm", "-v", volumeName+":/data:ro", "alpine:3", "sh", "-c", "cat /data/value.txt; printf :; cat /data/nested/check.txt")
}

func runDockerCommand(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
