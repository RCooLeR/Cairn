package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

type failingBackupEntropyReader struct{}

func (failingBackupEntropyReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestPlanBackupEntropyFailureLeavesNoPlanOrFilesystemMutation(t *testing.T) {
	ctx := context.Background()
	mgr, _, provider := newTestManager(t)
	mgr.IDs = security.NewIDSource(failingBackupEntropyReader{})
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{
		Summary: models.VolumeSummary{Name: "app-db", SizeBytes: 1024},
	}
	dest := filepath.Join(t.TempDir(), "not-created")

	plan, err := mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: dest})
	if plan != nil {
		t.Fatalf("PlanBackupVolume() plan = %#v, want nil", plan)
	}
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("PlanBackupVolume() error = %v, want %s", err, apperror.Internal)
	}
	mgr.mu.Lock()
	planCount := len(mgr.plans)
	mgr.mu.Unlock()
	if planCount != 0 {
		t.Fatalf("saved plans = %d, want 0", planCount)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("backup directory was created despite ID failure: %v", statErr)
	}
	provider.mu.Lock()
	providerCalls := len(provider.calls)
	provider.mu.Unlock()
	if providerCalls != 0 {
		t.Fatalf("provider commands = %d, want 0", providerCalls)
	}
}

func TestPendingBackupPlanCapClosesRejectedRestoreHandles(t *testing.T) {
	now := time.Date(2026, 6, 13, 16, 0, 0, 0, time.UTC)
	mgr := &Manager{
		Now:   func() time.Time { return now },
		plans: map[string]planRecord{},
		jobs:  map[string]context.CancelFunc{},
	}
	t.Cleanup(mgr.StopAll)
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, []byte("backup-data"), 0o600); err != nil {
		t.Fatalf("write restore source: %v", err)
	}
	accepted := make([]*os.File, 0, maxPendingBackupPlans*2)
	for i := 0; i < maxPendingBackupPlans; i++ {
		archive, err := os.Open(path)
		if err != nil {
			t.Fatalf("open archive handle %d: %v", i, err)
		}
		metadata, err := os.Open(path)
		if err != nil {
			_ = archive.Close()
			t.Fatalf("open metadata handle %d: %v", i, err)
		}
		identity, err := archive.Stat()
		if err != nil {
			_ = archive.Close()
			_ = metadata.Close()
			t.Fatalf("stat restore source %d: %v", i, err)
		}
		err = mgr.savePlan(planRecord{
			Plan: models.CommandPlan{
				PlanID:    fmt.Sprintf("plan-%03d", i),
				ExpiresAt: now.Add(time.Hour),
			},
			Operation:        "restore",
			ArchiveHandle:    archive,
			ArchiveIdentity:  identity,
			MetadataHandle:   metadata,
			MetadataIdentity: identity,
		})
		if err != nil {
			_ = archive.Close()
			_ = metadata.Close()
			t.Fatalf("savePlan(%d) error = %v", i, err)
		}
		accepted = append(accepted, archive, metadata)
	}

	rejectedArchive, err := os.Open(path)
	if err != nil {
		t.Fatalf("open rejected archive handle: %v", err)
	}
	rejectedMetadata, err := os.Open(path)
	if err != nil {
		_ = rejectedArchive.Close()
		t.Fatalf("open rejected metadata handle: %v", err)
	}
	identity, err := rejectedArchive.Stat()
	if err != nil {
		t.Fatalf("stat rejected restore source: %v", err)
	}
	err = mgr.savePlan(planRecord{
		Plan: models.CommandPlan{
			PlanID:    "plan-over-cap",
			ExpiresAt: now.Add(time.Hour),
		},
		Operation:        "restore",
		ArchiveHandle:    rejectedArchive,
		ArchiveIdentity:  identity,
		MetadataHandle:   rejectedMetadata,
		MetadataIdentity: identity,
	})
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("savePlan(over cap) error = %v, want Conflict", err)
	}
	assertFileHandleClosed(t, rejectedArchive, "rejected archive")
	assertFileHandleClosed(t, rejectedMetadata, "rejected metadata")
	mgr.mu.Lock()
	pending := len(mgr.plans)
	mgr.mu.Unlock()
	if pending != maxPendingBackupPlans {
		t.Fatalf("pending plans = %d, want cap %d", pending, maxPendingBackupPlans)
	}

	mgr.StopAll()
	for i, handle := range accepted {
		assertFileHandleClosed(t, handle, fmt.Sprintf("accepted handle %d after StopAll", i))
	}
}

func TestRestorePlanHandlesCloseOnExpiryAndStopAll(t *testing.T) {
	for _, action := range []string{"expiry", "stop"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			mgr, _, _ := newTestManager(t)
			archivePath := writeTestRestoreArchive(t, "app-db")
			plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
				SourcePath: archivePath,
				VolumeName: "app-db-restored",
			})
			if err != nil {
				t.Fatalf("PlanRestoreVolume() error = %v", err)
			}
			record := savedPlanRecord(t, mgr, plan.PlanID)
			if action == "expiry" {
				mgr.expirePlan(plan.PlanID, record.generation)
			} else {
				mgr.StopAll()
			}
			assertFileHandleClosed(t, record.ArchiveHandle, "archive after "+action)
			assertFileHandleClosed(t, record.MetadataHandle, "metadata after "+action)
		})
	}
}

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
	reservation, err := reserveBackupPaths(dir, "app/db", ts, "plan-one")
	if err != nil {
		t.Fatalf("reserveBackupPaths() error = %v", err)
	}
	if reservation.ArchiveName != "app-db-20260613T160000Z.tar.gz" {
		t.Fatalf("archive = %q", reservation.ArchiveName)
	}
	if err := releaseBackupReservationRecord(reservation); err != nil {
		t.Fatalf("releaseBackupReservationRecord() error = %v", err)
	}
	if err := os.WriteFile(reservation.ArchivePath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := os.WriteFile(reservation.MetadataPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	reservation, err = reserveBackupPaths(dir, "app/db", ts, "plan-two")
	if err != nil {
		t.Fatalf("reserveBackupPaths(collision) error = %v", err)
	}
	if reservation.ArchiveName != "app-db-20260613T160000Z-2.tar.gz" {
		t.Fatalf("collision archive = %q", reservation.ArchiveName)
	}
	if err := releaseBackupReservationRecord(reservation); err != nil {
		t.Fatalf("releaseBackupReservationRecord(collision) error = %v", err)
	}

	payload := []byte("backup-data")
	path := filepath.Join(dir, "sidecar-test.tar.gz")
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

func TestReadSidecarRejectsOversizedTrailingAndInvalidFields(t *testing.T) {
	t.Parallel()
	valid := BackupSidecar{
		FormatVersion: formatVersion,
		Volume:        "app-db",
		SHA256:        strings.Repeat("a", sha256.Size*2),
	}
	marshal := func(sidecar BackupSidecar) []byte {
		t.Helper()
		payload, err := json.Marshal(sidecar)
		if err != nil {
			t.Fatalf("marshal sidecar: %v", err)
		}
		return payload
	}
	validPayload := marshal(valid)
	missingVolume := valid
	missingVolume.Volume = ""
	oversizedVolume := valid
	oversizedVolume.Volume = strings.Repeat("v", maxSidecarNameBytes+1)
	invalidVolume := valid
	invalidVolume.Volume = "app-db\nforged"
	missingSHA := valid
	missingSHA.SHA256 = ""
	invalidSHA := valid
	invalidSHA.SHA256 = strings.Repeat("z", sha256.Size*2)
	oversizedProject := valid
	oversizedProject.Project = strings.Repeat("p", maxSidecarFieldBytes+1)
	tooManyContainers := valid
	tooManyContainers.UsingContainers = make([]string, maxSidecarContainers+1)
	oversizedContainer := valid
	oversizedContainer.UsingContainers = []string{strings.Repeat("c", maxSidecarNameBytes+1)}
	negativeSize := valid
	negativeSize.CompressedSizeBytes = -1

	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "oversized file", payload: append(append([]byte(nil), validPayload...), []byte(strings.Repeat(" ", maxSidecarBytes-len(validPayload)+1))...)},
		{name: "second JSON value", payload: append(append([]byte(nil), validPayload...), []byte("\n{}")...)},
		{name: "trailing garbage", payload: append(append([]byte(nil), validPayload...), []byte(" trailing")...)},
		{name: "missing volume", payload: marshal(missingVolume)},
		{name: "oversized volume", payload: marshal(oversizedVolume)},
		{name: "invalid volume", payload: marshal(invalidVolume)},
		{name: "missing sha256", payload: marshal(missingSHA)},
		{name: "malformed sha256", payload: marshal(invalidSHA)},
		{name: "oversized project", payload: marshal(oversizedProject)},
		{name: "too many containers", payload: marshal(tooManyContainers)},
		{name: "oversized container name", payload: marshal(oversizedContainer)},
		{name: "negative compressed size", payload: marshal(negativeSize)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "backup.json")
			if err := os.WriteFile(path, tt.payload, 0o600); err != nil {
				t.Fatalf("write sidecar: %v", err)
			}
			if _, err := readSidecar(path); !apperror.IsCode(err, apperror.Conflict) {
				t.Fatalf("readSidecar() error = %v, want conflict", err)
			}
		})
	}
}

func TestReadSidecarRejectsNonRegularAndReplacedMetadata(t *testing.T) {
	t.Parallel()
	validPayload, err := json.Marshal(BackupSidecar{
		FormatVersion: formatVersion,
		Volume:        "app-db",
		SHA256:        strings.Repeat("a", sha256.Size*2),
	})
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}

	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "backup.json")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("mkdir sidecar path: %v", err)
		}
		if _, err := readSidecar(path); !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("readSidecar(directory) error = %v, want conflict", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		path := filepath.Join(dir, "backup.json")
		if err := os.WriteFile(target, validPayload, 0o600); err != nil {
			t.Fatalf("write symlink target: %v", err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := readSidecar(path); !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("readSidecar(symlink) error = %v, want conflict", err)
		}
	})

	t.Run("replacement during open", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "backup.json")
		displaced := filepath.Join(dir, "original.json")
		if err := os.WriteFile(path, validPayload, 0o600); err != nil {
			t.Fatalf("write sidecar: %v", err)
		}
		_, err := readSidecarWithOpen(path, func(candidate string) (*os.File, error) {
			if err := os.Rename(candidate, displaced); err != nil {
				return nil, err
			}
			if err := os.WriteFile(candidate, validPayload, 0o600); err != nil {
				return nil, err
			}
			return os.Open(candidate)
		})
		if !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("readSidecarWithOpen(replacement) error = %v, want conflict", err)
		}
		if got, readErr := os.ReadFile(path); readErr != nil || string(got) != string(validPayload) {
			t.Fatalf("replacement changed: content=%q error=%v", got, readErr)
		}
	})
}

func TestConcurrentBackupPlansAndAppliesKeepArtifactsIndependent(t *testing.T) {
	ctx := context.Background()
	mgr, events, _ := newTestManager(t)
	mgr.Backups = nil
	mgr.Audit = nil
	dest := t.TempDir()
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}
	var nextID atomic.Uint64
	mgr.NewID = func() string {
		return fmt.Sprintf("id-%d", nextID.Add(1))
	}

	const count = 32
	plans := make([]*models.CommandPlan, count)
	planErrs := make([]error, count)
	startPlanning := make(chan struct{})
	var planning sync.WaitGroup
	planning.Add(count)
	for i := 0; i < count; i++ {
		go func(index int) {
			defer planning.Done()
			<-startPlanning
			plans[index], planErrs[index] = mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: dest})
		}(i)
	}
	close(startPlanning)
	planning.Wait()

	records := make([]planRecord, count)
	archivePaths := make(map[string]struct{}, count)
	metadataPaths := make(map[string]struct{}, count)
	commands := make(map[string]struct{}, count)
	for i := range plans {
		if planErrs[i] != nil {
			t.Fatalf("PlanBackupVolume(%d) error = %v", i, planErrs[i])
		}
		if plans[i] == nil {
			t.Fatalf("PlanBackupVolume(%d) plan = nil", i)
		}
		records[i] = savedPlanRecord(t, mgr, plans[i].PlanID)
		if _, duplicate := archivePaths[records[i].ArchivePath]; duplicate {
			t.Fatalf("duplicate archive path reserved: %s", records[i].ArchivePath)
		}
		if _, duplicate := metadataPaths[records[i].MetadataPath]; duplicate {
			t.Fatalf("duplicate metadata path reserved: %s", records[i].MetadataPath)
		}
		if _, duplicate := commands[plans[i].Commands[0].Command]; duplicate {
			t.Fatalf("duplicate private staging command planned: %s", plans[i].Commands[0].Command)
		}
		archivePaths[records[i].ArchivePath] = struct{}{}
		metadataPaths[records[i].MetadataPath] = struct{}{}
		commands[plans[i].Commands[0].Command] = struct{}{}
		if err := verifyReservationOwner(records[i].ReservationPath, records[i].ReservationOwner); err != nil {
			t.Fatalf("reservation %d is not owned: %v", i, err)
		}
	}

	done := events.Subscribe(ctx, bus.TopicJobDone, count*2)
	jobIDs := make([]string, count)
	applyErrs := make([]error, count)
	startApply := make(chan struct{})
	var applying sync.WaitGroup
	applying.Add(count)
	for i := range plans {
		go func(index int) {
			defer applying.Done()
			<-startApply
			jobIDs[index], applyErrs[index] = mgr.ApplyBackup(ctx, plans[index].PlanID)
		}(i)
	}
	close(startApply)
	applying.Wait()
	wantJobs := make(map[string]struct{}, count)
	for i, err := range applyErrs {
		if err != nil {
			t.Fatalf("ApplyBackup(%d) error = %v", i, err)
		}
		if _, duplicate := wantJobs[jobIDs[i]]; duplicate {
			t.Fatalf("duplicate job ID: %s", jobIDs[i])
		}
		wantJobs[jobIDs[i]] = struct{}{}
	}
	waitBackupJobs(t, done, wantJobs)

	var previousArchive os.FileInfo
	for i, record := range records {
		archiveInfo, err := os.Stat(record.ArchivePath)
		if err != nil {
			t.Fatalf("archive %d missing: %v", i, err)
		}
		if previousArchive != nil && os.SameFile(previousArchive, archiveInfo) {
			t.Fatalf("archive %d aliases another backup", i)
		}
		previousArchive = archiveInfo
		sidecar, err := readSidecar(record.MetadataPath)
		if err != nil {
			t.Fatalf("metadata %d invalid: %v", i, err)
		}
		if err := verifyArchiveChecksum(record.ArchivePath, sidecar.SHA256); err != nil {
			t.Fatalf("archive %d checksum error: %v", i, err)
		}
		assertPathMissing(t, record.ReservationPath)
		assertPathMissing(t, record.StagingDirHost)
	}
}

func TestExpiredBackupPlanReleasesOnlyOwnedReservationFiles(t *testing.T) {
	ctx := context.Background()
	mgr, _, _ := newTestManager(t)
	dest := t.TempDir()
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}
	now := time.Date(2026, 6, 13, 16, 0, 0, 0, time.UTC)
	mgr.Now = func() time.Time { return now }

	plan, err := mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: dest})
	if err != nil {
		t.Fatalf("PlanBackupVolume() error = %v", err)
	}
	record := savedPlanRecord(t, mgr, plan.PlanID)
	foreignArchive := []byte("created by another operation")
	if err := os.WriteFile(record.ArchivePath, foreignArchive, 0o600); err != nil {
		t.Fatalf("write foreign archive: %v", err)
	}
	now = plan.ExpiresAt.Add(time.Nanosecond)

	if _, err := mgr.ApplyBackup(ctx, plan.PlanID); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("ApplyBackup(expired) error = %v, want %s", err, apperror.PlanExpired)
	}
	got, err := os.ReadFile(record.ArchivePath)
	if err != nil {
		t.Fatalf("foreign archive was removed: %v", err)
	}
	if string(got) != string(foreignArchive) {
		t.Fatalf("foreign archive = %q, want %q", got, foreignArchive)
	}
	assertPathMissing(t, record.ReservationPath)
	assertPathMissing(t, record.StagingDirHost)
}

func TestStopAllReleasesUnappliedBackupReservation(t *testing.T) {
	ctx := context.Background()
	mgr, _, _ := newTestManager(t)
	dest := t.TempDir()
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}

	plan, err := mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: dest})
	if err != nil {
		t.Fatalf("PlanBackupVolume() error = %v", err)
	}
	record := savedPlanRecord(t, mgr, plan.PlanID)
	mgr.StopAll()

	assertPathMissing(t, record.ReservationPath)
	assertPathMissing(t, record.StagingDirHost)
	if _, err := mgr.ApplyBackup(ctx, plan.PlanID); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("ApplyBackup(after StopAll) error = %v, want %s", err, apperror.PlanExpired)
	}
}

func TestBackupMetadataPublishCollisionPreservesForeignMetadata(t *testing.T) {
	ctx := context.Background()
	mgr, events, provider := newTestManager(t)
	mgr.Backups = nil
	mgr.Audit = nil
	dest := t.TempDir()
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: dest})
	if err != nil {
		t.Fatalf("PlanBackupVolume() error = %v", err)
	}
	record := savedPlanRecord(t, mgr, plan.PlanID)
	foreignMetadata := []byte("foreign-metadata")
	provider.afterBackupWrite = func() error {
		return os.WriteFile(record.MetadataPath, foreignMetadata, 0o600)
	}
	jobID, err := mgr.ApplyBackup(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("ApplyBackup() error = %v", err)
	}
	payload := waitJobDonePayload(t, done, jobID)
	if payload.Error == "" {
		t.Fatal("backup succeeded despite metadata publication collision")
	}
	got, err := os.ReadFile(record.MetadataPath)
	if err != nil {
		t.Fatalf("foreign metadata was removed: %v", err)
	}
	if string(got) != string(foreignMetadata) {
		t.Fatalf("foreign metadata = %q, want %q", got, foreignMetadata)
	}
	assertPathMissing(t, record.ArchivePath)
	assertPathMissing(t, record.ReservationPath)
	assertPathMissing(t, record.StagingDirHost)
}

func TestBackupRepositoryFailureRemovesUntrackedPublishedPair(t *testing.T) {
	ctx := context.Background()
	mgr, events, _ := newTestManager(t)
	mgr.Audit = nil
	brokenDB, err := store.Open(ctx, filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("Open broken store: %v", err)
	}
	if err := brokenDB.Migrate(ctx); err != nil {
		t.Fatalf("Migrate broken store: %v", err)
	}
	mgr.Backups = brokenDB.Backups()
	if err := brokenDB.Close(); err != nil {
		t.Fatalf("Close broken store: %v", err)
	}
	dest := t.TempDir()
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: dest})
	if err != nil {
		t.Fatalf("PlanBackupVolume() error = %v", err)
	}
	record := savedPlanRecord(t, mgr, plan.PlanID)
	jobID, err := mgr.ApplyBackup(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("ApplyBackup() error = %v", err)
	}
	payload := waitJobDonePayload(t, done, jobID)
	if payload.Error == "" {
		t.Fatal("backup succeeded despite closed repository")
	}
	assertPathMissing(t, record.ArchivePath)
	assertPathMissing(t, record.MetadataPath)
	assertPathMissing(t, record.ReservationPath)
	assertPathMissing(t, record.StagingDirHost)
}

func TestBackupCleanupFailureReportsRecordedPartialSuccess(t *testing.T) {
	ctx := context.Background()
	mgr, _, provider := newTestManager(t)
	dest := t.TempDir()
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}

	plan, err := mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: dest})
	if err != nil {
		t.Fatalf("PlanBackupVolume() error = %v", err)
	}
	record, err := mgr.takePlan(ctx, plan.PlanID, "")
	if err != nil {
		t.Fatalf("takePlan() error = %v", err)
	}
	unexpectedPath := filepath.Join(record.StagingDirHost, "unexpected")
	provider.afterBackupWrite = func() error {
		return os.WriteFile(unexpectedPath, []byte("ownership unclear"), 0o600)
	}

	err = mgr.runBackup(ctx, "backup-partial", record)
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Partial == nil {
		t.Fatalf("runBackup() error = %#v, want structured partial resource", err)
	}
	if appErr.Partial.Type != "backup" || appErr.Partial.State != "created" || !appErr.Partial.CleanupRequired {
		t.Fatalf("partial resource = %#v", appErr.Partial)
	}
	backups, listErr := mgr.ListBackups(ctx, models.BackupFilter{VolumeName: "app-db"})
	if listErr != nil {
		t.Fatalf("ListBackups() error = %v", listErr)
	}
	if len(backups) != 1 || backups[0].Result != backupResultOK {
		t.Fatalf("recorded backups = %#v", backups)
	}
	if _, err := os.Stat(record.ArchivePath); err != nil {
		t.Fatalf("recorded archive missing: %v", err)
	}
	if _, err := os.Stat(record.MetadataPath); err != nil {
		t.Fatalf("recorded metadata missing: %v", err)
	}
	if _, err := os.Stat(record.ReservationPath); err != nil {
		t.Fatalf("reservation evidence missing: %v", err)
	}
	if _, err := os.Stat(unexpectedPath); err != nil {
		t.Fatalf("unexpected staging entry was removed: %v", err)
	}
}

func TestOwnedCleanupRefusesForeignArtifacts(t *testing.T) {
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "staged.tar.gz")
	destinationPath := filepath.Join(dir, "published.tar.gz")
	if err := os.WriteFile(stagingPath, []byte("ours"), 0o600); err != nil {
		t.Fatalf("write staged file: %v", err)
	}
	if err := os.WriteFile(destinationPath, []byte("foreign"), 0o600); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}
	if err := removePublishedBackupFile(stagingPath, destinationPath); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("removePublishedBackupFile() error = %v, want conflict", err)
	}
	got, err := os.ReadFile(destinationPath)
	if err != nil || string(got) != "foreign" {
		t.Fatalf("foreign destination changed: content=%q error=%v", got, err)
	}

	reservation, err := reserveBackupPaths(dir, "app-db", time.Now(), "plan-owner")
	if err != nil {
		t.Fatalf("reserveBackupPaths() error = %v", err)
	}
	if err := os.WriteFile(reservation.ReservationPath, []byte("another-owner\n"), 0o600); err != nil {
		t.Fatalf("replace reservation owner: %v", err)
	}
	if err := releaseBackupReservationRecord(reservation); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("releaseBackupReservationRecord() error = %v, want conflict", err)
	}
	if _, err := os.Stat(reservation.StagingDir); err != nil {
		t.Fatalf("foreign-owned staging directory was removed: %v", err)
	}
	if _, err := os.Stat(reservation.ReservationPath); err != nil {
		t.Fatalf("foreign-owned reservation marker was removed: %v", err)
	}
}

func TestReservationCleanupRefusesSamePathStagingDirectorySwap(t *testing.T) {
	dir := t.TempDir()
	reservation, err := reserveBackupPaths(dir, "app-db", time.Now(), "plan-owner")
	if err != nil {
		t.Fatalf("reserveBackupPaths() error = %v", err)
	}
	originalDir := reservation.StagingDir + ".original"
	if err := os.Rename(reservation.StagingDir, originalDir); err != nil {
		t.Fatalf("move owned staging directory: %v", err)
	}
	if err := os.Mkdir(reservation.StagingDir, 0o700); err != nil {
		t.Fatalf("create replacement staging directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reservation.StagingDir, stagingOwnerName), []byte(reservation.Owner+"\n"), 0o600); err != nil {
		t.Fatalf("write copied owner marker: %v", err)
	}
	foreignPath := filepath.Join(reservation.StagingDir, "foreign-data")
	if err := os.WriteFile(foreignPath, []byte("do not delete"), 0o600); err != nil {
		t.Fatalf("write foreign staging data: %v", err)
	}

	if err := releaseBackupReservationRecord(reservation); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("releaseBackupReservationRecord() error = %v, want conflict", err)
	}
	got, err := os.ReadFile(foreignPath)
	if err != nil || string(got) != "do not delete" {
		t.Fatalf("foreign staging data changed: content=%q error=%v", got, err)
	}
	if _, err := os.Stat(reservation.ReservationPath); err != nil {
		t.Fatalf("reservation evidence was removed after directory swap: %v", err)
	}
}

func TestReservationCleanupRefusesUnexpectedOwnedDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	reservation, err := reserveBackupPaths(dir, "app-db", time.Now(), "plan-owner")
	if err != nil {
		t.Fatalf("reserveBackupPaths() error = %v", err)
	}
	unexpectedPath := filepath.Join(reservation.StagingDir, "unexpected")
	if err := os.WriteFile(unexpectedPath, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write unexpected entry: %v", err)
	}

	if err := releaseBackupReservationRecord(reservation); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("releaseBackupReservationRecord() error = %v, want conflict", err)
	}
	got, err := os.ReadFile(unexpectedPath)
	if err != nil || string(got) != "keep" {
		t.Fatalf("unexpected entry changed: content=%q error=%v", got, err)
	}
	if _, err := os.Stat(reservation.ReservationPath); err != nil {
		t.Fatalf("reservation evidence was removed: %v", err)
	}
}

func TestUntrackedPublishCleanupFailurePreservesOwnershipEvidence(t *testing.T) {
	dir := t.TempDir()
	reservation, err := reserveBackupPaths(dir, "app-db", time.Now(), "plan-owner")
	if err != nil {
		t.Fatalf("reserveBackupPaths() error = %v", err)
	}
	if err := os.WriteFile(reservation.StagingArchivePath, []byte("archive"), 0o600); err != nil {
		t.Fatalf("write staged archive: %v", err)
	}
	if err := os.WriteFile(reservation.StagingMetadataPath, []byte("metadata"), 0o600); err != nil {
		t.Fatalf("write staged metadata: %v", err)
	}
	archiveIdentity, err := regularFileIdentity(reservation.StagingArchivePath)
	if err != nil {
		t.Fatalf("archive identity: %v", err)
	}
	metadataIdentity, err := regularFileIdentity(reservation.StagingMetadataPath)
	if err != nil {
		t.Fatalf("metadata identity: %v", err)
	}
	record := planRecord{
		Operation:               "backup",
		ArchivePath:             reservation.ArchivePath,
		MetadataPath:            reservation.MetadataPath,
		ReservationPath:         reservation.ReservationPath,
		ReservationOwner:        reservation.Owner,
		ReservationIdentity:     reservation.ReservationIdentity,
		StagingDirHost:          reservation.StagingDir,
		StagingDirIdentity:      reservation.StagingDirIdentity,
		StagingArchivePath:      reservation.StagingArchivePath,
		StagingArchiveIdentity:  archiveIdentity,
		StagingMetadataPath:     reservation.StagingMetadataPath,
		StagingMetadataIdentity: metadataIdentity,
		StagingOwnerPath:        reservation.StagingOwnerPath,
		StagingOwnerIdentity:    reservation.StagingOwnerIdentity,
	}
	if err := publishStagedBackupFile(record.StagingArchivePath, record.ArchivePath, record.StagingArchiveIdentity); err != nil {
		t.Fatalf("publish archive: %v", err)
	}
	if err := publishStagedBackupFile(record.StagingMetadataPath, record.MetadataPath, record.StagingMetadataIdentity); err != nil {
		t.Fatalf("publish metadata: %v", err)
	}
	if err := os.Remove(record.MetadataPath); err != nil {
		t.Fatalf("replace published metadata: %v", err)
	}
	if err := os.WriteFile(record.MetadataPath, []byte("foreign"), 0o600); err != nil {
		t.Fatalf("write foreign metadata: %v", err)
	}

	if err := cleanupUntrackedPublishedBackup(record); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("cleanupUntrackedPublishedBackup() error = %v, want conflict", err)
	}
	got, err := os.ReadFile(record.MetadataPath)
	if err != nil || string(got) != "foreign" {
		t.Fatalf("foreign metadata changed: content=%q error=%v", got, err)
	}
	assertPathMissing(t, record.ArchivePath)
	if _, err := os.Stat(record.ReservationPath); err != nil {
		t.Fatalf("reservation evidence was removed after cleanup failure: %v", err)
	}
	if _, err := os.Stat(record.StagingDirHost); err != nil {
		t.Fatalf("staging evidence was removed after cleanup failure: %v", err)
	}
}

func TestPublishStagedBackupFileRejectsArchiveAndMetadataSwaps(t *testing.T) {
	tests := []struct {
		name string
		link func(stagingPath string, destinationPath string) error
	}{
		{
			name: "archive staging swap after preflight",
			link: func(stagingPath string, destinationPath string) error {
				foreignPath := stagingPath + ".foreign"
				if err := os.WriteFile(foreignPath, []byte("foreign-archive"), 0o600); err != nil {
					return err
				}
				if err := os.Remove(stagingPath); err != nil {
					return err
				}
				if err := os.Rename(foreignPath, stagingPath); err != nil {
					return err
				}
				return os.Link(stagingPath, destinationPath)
			},
		},
		{
			name: "metadata destination swap after link",
			link: func(stagingPath string, destinationPath string) error {
				foreignPath := destinationPath + ".foreign"
				if err := os.WriteFile(foreignPath, []byte("foreign-metadata"), 0o600); err != nil {
					return err
				}
				if err := os.Link(stagingPath, destinationPath); err != nil {
					return err
				}
				if err := os.Remove(destinationPath); err != nil {
					return err
				}
				return os.Rename(foreignPath, destinationPath)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			stagingPath := filepath.Join(dir, "staged")
			destinationPath := filepath.Join(dir, "published")
			if err := os.WriteFile(stagingPath, []byte("owned"), 0o600); err != nil {
				t.Fatalf("write staged file: %v", err)
			}
			expected, err := regularFileIdentity(stagingPath)
			if err != nil {
				t.Fatalf("capture staged identity: %v", err)
			}

			err = publishStagedBackupFileWithLink(stagingPath, destinationPath, expected, tt.link)
			if !apperror.IsCode(err, apperror.Conflict) {
				t.Fatalf("publishStagedBackupFileWithLink() error = %v, want conflict", err)
			}
			if !errors.Is(err, errPreserveBackupReservation) {
				t.Fatalf("publish error = %v, want preserved ownership evidence", err)
			}
			got, readErr := os.ReadFile(destinationPath)
			if readErr != nil || !strings.HasPrefix(string(got), "foreign-") {
				t.Fatalf("foreign destination changed: content=%q error=%v", got, readErr)
			}
		})
	}
}

func TestWriteSidecarContentsChecksWriteFlushAndClose(t *testing.T) {
	t.Parallel()
	sidecar := BackupSidecar{FormatVersion: formatVersion, Volume: "app-db", SHA256: "abc"}
	tests := []struct {
		name      string
		file      *fakeSidecarFile
		wantError string
		wantSync  int
	}{
		{name: "success", file: &fakeSidecarFile{}, wantSync: 1},
		{name: "write", file: &fakeSidecarFile{writeErr: errors.New("disk full")}, wantError: "Write backup metadata failed"},
		{name: "flush", file: &fakeSidecarFile{syncErr: errors.New("flush failed")}, wantError: "Flush backup metadata failed", wantSync: 1},
		{name: "close", file: &fakeSidecarFile{closeErr: errors.New("close failed")}, wantError: "Close backup metadata failed", wantSync: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := writeSidecarContents(tt.file, sidecar)
			if tt.wantError == "" && err != nil {
				t.Fatalf("writeSidecarContents() error = %v", err)
			}
			if tt.wantError != "" && (err == nil || !strings.Contains(err.Error(), tt.wantError)) {
				t.Fatalf("writeSidecarContents() error = %v, want %q", err, tt.wantError)
			}
			if tt.file.syncCalls != tt.wantSync {
				t.Fatalf("Sync calls = %d, want %d", tt.file.syncCalls, tt.wantSync)
			}
			if tt.file.closeCalls != 1 {
				t.Fatalf("Close calls = %d, want 1", tt.file.closeCalls)
			}
			if tt.wantError == "" && !strings.Contains(tt.file.content.String(), `"format_version": 1`) {
				t.Fatalf("sidecar payload = %q", tt.file.content.String())
			}
		})
	}
}

type fakeSidecarFile struct {
	content    strings.Builder
	writeErr   error
	syncErr    error
	closeErr   error
	syncCalls  int
	closeCalls int
}

func (f *fakeSidecarFile) Write(payload []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.content.Write(payload)
}

func (f *fakeSidecarFile) Sync() error {
	f.syncCalls++
	return f.syncErr
}

func (f *fakeSidecarFile) Close() error {
	f.closeCalls++
	return f.closeErr
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
	planned := savedPlanRecord(t, mgr, plan.PlanID)
	if planned.ArchiveIdentity == nil || planned.MetadataIdentity == nil || planned.ArchiveHandle == nil || planned.MetadataHandle == nil {
		t.Fatalf("delete plan did not capture both artifact identities: %#v", planned)
	}
	if err := mgr.ApplyDeleteBackup(ctx, plan.PlanID); err != nil {
		t.Fatalf("ApplyDeleteBackup() error = %v", err)
	}
	assertFileHandleClosed(t, planned.ArchiveHandle, "deleted archive")
	assertFileHandleClosed(t, planned.MetadataHandle, "deleted metadata")
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

func TestPlanDeleteBackupRejectsNonRegularArtifactAndKeepsRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, _, _ := newTestManager(t)
	archivePath := filepath.Join(t.TempDir(), "archive-dir")
	if err := os.MkdirAll(filepath.Join(archivePath, "child"), 0o755); err != nil {
		t.Fatalf("mkdir archive dir: %v", err)
	}
	metadataPath := filepath.Join(t.TempDir(), "backup.json")
	if err := os.WriteFile(metadataPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	record := store.BackupRecord{
		ID:           "backup-fail-delete",
		ProviderID:   "linux_native",
		VolumeName:   "app-db",
		BackupPath:   archivePath,
		MetadataPath: metadataPath,
		Result:       "success",
	}
	if err := mgr.Backups.Insert(ctx, record); err != nil {
		t.Fatalf("insert backup: %v", err)
	}
	if _, err := mgr.PlanDeleteBackup(ctx, record.ID); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("PlanDeleteBackup() error = %v, want conflict", err)
	}
	if _, err := mgr.Backups.Get(ctx, record.ID); err != nil {
		t.Fatalf("backup record was removed after rejected plan: %v", err)
	}
	if _, err := os.Stat(metadataPath); err != nil {
		t.Fatalf("metadata was removed after rejected plan: %v", err)
	}
}

func TestApplyDeleteBackupRejectsReplacementArtifactsAndKeepsRecord(t *testing.T) {
	tests := []struct {
		name        string
		replaceKind string
	}{
		{name: "archive replaced", replaceKind: "archive"},
		{name: "metadata replaced", replaceKind: "metadata"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			mgr, _, _ := newTestManager(t)
			dir := t.TempDir()
			archivePath := filepath.Join(dir, "archive.tar.gz")
			metadataPath := filepath.Join(dir, "archive.json")
			if err := os.WriteFile(archivePath, []byte("planned-archive"), 0o600); err != nil {
				t.Fatalf("write archive: %v", err)
			}
			if err := os.WriteFile(metadataPath, []byte("planned-metadata"), 0o600); err != nil {
				t.Fatalf("write metadata: %v", err)
			}
			record := store.BackupRecord{
				ID:           "backup-replaced-" + tt.replaceKind,
				ProviderID:   "linux_native",
				VolumeName:   "app-db",
				BackupPath:   archivePath,
				MetadataPath: metadataPath,
				Result:       backupResultOK,
			}
			if err := mgr.Backups.Insert(ctx, record); err != nil {
				t.Fatalf("insert backup: %v", err)
			}
			plan, err := mgr.PlanDeleteBackup(ctx, record.ID)
			if err != nil {
				t.Fatalf("PlanDeleteBackup() error = %v", err)
			}
			planned := savedPlanRecord(t, mgr, plan.PlanID)
			if planned.ArchiveIdentity == nil || planned.MetadataIdentity == nil {
				t.Fatalf("delete plan identities = archive:%v metadata:%v, want both", planned.ArchiveIdentity, planned.MetadataIdentity)
			}

			replacedPath := archivePath
			otherPath := metadataPath
			otherPayload := "planned-metadata"
			if tt.replaceKind == "metadata" {
				replacedPath = metadataPath
				otherPath = archivePath
				otherPayload = "planned-archive"
			}
			displacedPath := replacedPath + ".displaced"
			if err := os.Rename(replacedPath, displacedPath); err != nil {
				t.Fatalf("displace planned %s: %v", tt.replaceKind, err)
			}
			replacementPayload := "replacement-" + tt.replaceKind
			if err := os.WriteFile(replacedPath, []byte(replacementPayload), 0o600); err != nil {
				t.Fatalf("write replacement %s: %v", tt.replaceKind, err)
			}

			if err := mgr.ApplyDeleteBackup(ctx, plan.PlanID); !apperror.IsCode(err, apperror.Conflict) {
				t.Fatalf("ApplyDeleteBackup() error = %v, want conflict", err)
			}
			if got, err := os.ReadFile(replacedPath); err != nil || string(got) != replacementPayload {
				t.Fatalf("replacement %s changed: content=%q error=%v", tt.replaceKind, got, err)
			}
			if got, err := os.ReadFile(otherPath); err != nil || string(got) != otherPayload {
				t.Fatalf("unreplaced artifact changed: content=%q error=%v", got, err)
			}
			if _, err := mgr.Backups.Get(ctx, record.ID); err != nil {
				t.Fatalf("backup record was removed after replacement conflict: %v", err)
			}
		})
	}
}

func TestApplyDeleteBackupRejectsSameContentUnlinkRecreate(t *testing.T) {
	for _, replaceKind := range []string{"archive", "metadata"} {
		t.Run(replaceKind, func(t *testing.T) {
			ctx := context.Background()
			mgr, _, _ := newTestManager(t)
			dir := t.TempDir()
			archivePath := filepath.Join(dir, "archive.tar.gz")
			metadataPath := filepath.Join(dir, "archive.json")
			if err := os.WriteFile(archivePath, []byte("planned-archive"), 0o600); err != nil {
				t.Fatalf("write archive: %v", err)
			}
			if err := os.WriteFile(metadataPath, []byte("planned-metadata"), 0o600); err != nil {
				t.Fatalf("write metadata: %v", err)
			}
			record := store.BackupRecord{
				ID:           "backup-same-content-" + replaceKind,
				ProviderID:   "linux_native",
				VolumeName:   "app-db",
				BackupPath:   archivePath,
				MetadataPath: metadataPath,
				Result:       backupResultOK,
			}
			if err := mgr.Backups.Insert(ctx, record); err != nil {
				t.Fatalf("insert backup: %v", err)
			}
			replacedPath := archivePath
			otherPath := metadataPath
			if replaceKind == "metadata" {
				replacedPath = metadataPath
				otherPath = archivePath
			}
			content, err := os.ReadFile(replacedPath)
			if err != nil {
				t.Fatalf("read planned %s: %v", replaceKind, err)
			}
			plan, err := mgr.PlanDeleteBackup(ctx, record.ID)
			if err != nil {
				t.Fatalf("PlanDeleteBackup() error = %v", err)
			}
			displacedPath := replacedPath + ".displaced"
			if err := os.Rename(replacedPath, displacedPath); err != nil {
				t.Fatalf("displace planned %s: %v", replaceKind, err)
			}
			if err := os.Remove(displacedPath); err != nil {
				t.Fatalf("unlink displaced planned %s: %v", replaceKind, err)
			}
			if err := os.WriteFile(replacedPath, content, 0o600); err != nil {
				t.Fatalf("recreate same-content %s: %v", replaceKind, err)
			}

			if err := mgr.ApplyDeleteBackup(ctx, plan.PlanID); !apperror.IsCode(err, apperror.Conflict) {
				t.Fatalf("ApplyDeleteBackup() error = %v, want conflict", err)
			}
			if got, err := os.ReadFile(replacedPath); err != nil || !slices.Equal(got, content) {
				t.Fatalf("replacement %s changed: content=%q error=%v", replaceKind, got, err)
			}
			if _, err := os.Stat(otherPath); err != nil {
				t.Fatalf("unreplaced artifact changed: %v", err)
			}
			if _, err := mgr.Backups.Get(ctx, record.ID); err != nil {
				t.Fatalf("backup record was removed after replacement conflict: %v", err)
			}
		})
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
	for _, want := range []string{`tar xzf "$archive"`, "stash_name", "trap cleanup EXIT HUP INT TERM", "restore_stash_dir"} {
		if !strings.Contains(script, want) {
			t.Fatalf("restore script missing %q: %q", want, script)
		}
	}
	backupArgs := dockerRunBackupArgs("app-db", "/tmp/backups", "app-db.tar.gz")
	if got, want := backupArgs[len(backupArgs)-2], "cairn-backup"; got != want {
		t.Fatalf("backup argv script name = %q, want %q", got, want)
	}
	if got, want := backupArgs[len(backupArgs)-1], "/backup/app-db.tar.gz"; got != want {
		t.Fatalf("backup archive argv = %q, want %q", got, want)
	}
	backupScript := backupArgs[len(backupArgs)-3]
	if strings.Contains(backupScript, "app-db.tar.gz") {
		t.Fatalf("archive name was interpolated into backup shell script: %q", backupScript)
	}
	for _, want := range []string{
		`tar czf "$archive" --exclude=.cairn-restore-old-* -C /source .`,
		`stat -c '%u:%g' /backup`,
		`chown "$owner" "$archive"`,
		`chmod 0600 "$archive"`,
	} {
		if !strings.Contains(backupScript, want) {
			t.Fatalf("backup script missing %q: %q", want, backupScript)
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
	record := savedPlanRecord(t, mgr, plan.PlanID)
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
	assertPathMissing(t, record.ArchivePath)
	assertPathMissing(t, record.MetadataPath)
	assertPathMissing(t, record.ReservationPath)
	assertPathMissing(t, record.StagingDirHost)
}

func TestStopAllJoinsCanceledBackupJobsAndRejectsNewWork(t *testing.T) {
	t.Parallel()
	mgr, _, _ := newTestManager(t)
	mgr.Start(context.Background())
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	workerDone := make(chan struct{})
	if !mgr.startJob("backup-lifecycle-test", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		close(workerDone)
	}) {
		t.Fatal("startJob() rejected work before shutdown")
	}
	<-started

	stopReturned := make(chan struct{})
	go func() {
		mgr.StopAll()
		close(stopReturned)
	}()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAll() did not cancel the backup worker")
	}
	select {
	case <-stopReturned:
		t.Fatal("StopAll() returned before the canceled backup worker exited")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("backup worker did not exit after release")
	}
	select {
	case <-stopReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAll() did not return after the backup worker exited")
	}
	if mgr.startJob("backup-late-test", func(context.Context) {}) {
		t.Fatal("startJob() admitted work after StopAll")
	}
}

func TestApplyBackupRejectedByCanceledRootReleasesReservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, _, _ := newTestManager(t)
	rootCtx, cancel := context.WithCancel(ctx)
	cancel()
	mgr.Start(rootCtx)
	dest := t.TempDir()
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}

	plan, err := mgr.PlanBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: "app-db", DestPath: dest})
	if err != nil {
		t.Fatalf("PlanBackupVolume() error = %v", err)
	}
	record := savedPlanRecord(t, mgr, plan.PlanID)
	if jobID, err := mgr.ApplyBackup(ctx, plan.PlanID); jobID != "" || !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("ApplyBackup() = %q, %v; want empty/%s", jobID, err, apperror.ProviderNotReady)
	}
	assertPathMissing(t, record.ReservationPath)
	assertPathMissing(t, record.StagingDirHost)
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
	record := savedPlanRecord(t, mgr, plan.PlanID)
	if _, err := mgr.ApplyRestore(ctx, plan.PlanID, "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("ApplyRestore(wrong) error = %v", err)
	}
	jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "app-db")
	if err != nil {
		t.Fatalf("ApplyRestore() error = %v", err)
	}
	waitJobDone(t, done, jobID)
	mgr.StopAll()
	assertFileHandleClosed(t, record.ArchiveHandle, "archive after restore worker")
	assertFileHandleClosed(t, record.MetadataHandle, "metadata after restore worker")
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

func TestRestoreIntoNewVolumeRechecksTargetAtApply(t *testing.T) {
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

	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db-restored",
		Overwrite:  false,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	mgr.Docker.(*fakeBackupDocker).volumes["app-db-restored"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db-restored"}}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "")
	if err != nil {
		t.Fatalf("ApplyRestore() error = %v", err)
	}
	payloadDone := waitJobDonePayload(t, done, jobID)
	if payloadDone.Error == "" {
		t.Fatalf("restore succeeded after target volume appeared")
	}
	if provider.hasRunArg("tar xzf") || provider.hasRunArg("volume create") {
		t.Fatalf("restore touched Docker after target appeared: %#v", provider.calls)
	}
}

func TestRestoreIntoNewVolumeRejectsForeignCreateRace(t *testing.T) {
	ctx := context.Background()
	mgr, events, provider := newTestManager(t)
	archivePath := writeTestRestoreArchive(t, "app-db")
	docker := mgr.Docker.(*fakeBackupDocker)
	docker.beforeCreate = func(req models.CreateVolumeRequest) {
		// Simulate another actor winning the gap between Cairn's absence check
		// and Docker's idempotent named-volume create.
		docker.volumes[req.Name] = &models.VolumeDetail{
			Summary: models.VolumeSummary{
				Name:   req.Name,
				Driver: "local",
				Labels: map[string]string{"owner": "foreign"},
			},
			CreatedAt: time.Date(2026, 6, 13, 15, 1, 0, 0, time.UTC),
		}
	}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db-restored",
		Overwrite:  false,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "")
	if err != nil {
		t.Fatalf("ApplyRestore() error = %v", err)
	}
	payload := waitJobDonePayload(t, done, jobID)
	if payload.Error == "" || !strings.Contains(payload.Error, "not created by this restore operation") {
		t.Fatalf("job error = %q, want foreign-volume ownership conflict", payload.Error)
	}
	if len(docker.createRequests) != 1 {
		t.Fatalf("create requests = %d, want 1", len(docker.createRequests))
	}
	if provider.hasRunArg("cairn-restore") || provider.hasRunArg("volume rm app-db-restored") {
		t.Fatalf("restore or cleanup touched the foreign volume: %#v", provider.callsSnapshot())
	}
}

func TestRestoreIntoNewVolumeRevalidatesSourceAfterCreate(t *testing.T) {
	ctx := context.Background()
	mgr, events, provider := newTestManager(t)
	archivePath := writeTestRestoreArchive(t, "app-db")
	docker := mgr.Docker.(*fakeBackupDocker)
	var mutateErr error
	docker.beforeCreate = func(models.CreateVolumeRequest) {
		mutateErr = os.WriteFile(archivePath, []byte("changed while target was being created"), 0o600)
	}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db-restored",
		Overwrite:  false,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "")
	if err != nil {
		t.Fatalf("ApplyRestore() error = %v", err)
	}
	payload := waitJobDonePayload(t, done, jobID)
	if mutateErr != nil {
		t.Fatalf("mutate restore source during create: %v", mutateErr)
	}
	if payload.Error == "" {
		t.Fatal("restore succeeded after its source changed during target creation")
	}
	if provider.hasRunArg("cairn-restore") {
		t.Fatalf("restore helper ran after source identity changed: %#v", provider.callsSnapshot())
	}
	if got := provider.countRunArg("volume rm app-db-restored"); got != 1 {
		t.Fatalf("owned target cleanup calls = %d, want 1; calls = %#v", got, provider.callsSnapshot())
	}
}

func TestRestoreIntoNewVolumeRemovesCreatedTargetWhenHelperFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, events, provider := newTestManager(t)
	archivePath := writeTestRestoreArchive(t, "app-db")
	provider.failCommands = map[string]error{
		"cairn-restore": errors.New("injected restore helper failure"),
	}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db-restored",
		Overwrite:  false,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "")
	if err != nil {
		t.Fatalf("ApplyRestore() error = %v", err)
	}
	payload := waitJobDonePayload(t, done, jobID)
	if payload.Error == "" {
		t.Fatal("restore succeeded after injected helper failure")
	}
	if got := len(mgr.Docker.(*fakeBackupDocker).createRequests); got != 1 {
		t.Fatalf("volume create calls = %d, want 1", got)
	}
	if got := provider.countRunArg("cairn-restore"); got != 1 {
		t.Fatalf("restore helper calls = %d, want 1; calls = %#v", got, provider.callsSnapshot())
	}
	if got := provider.countRunArg("volume rm app-db-restored"); got != 1 {
		t.Fatalf("volume cleanup calls = %d, want 1; calls = %#v", got, provider.callsSnapshot())
	}
	if !provider.callHadDeadline("volume rm app-db-restored") {
		t.Fatal("volume cleanup command did not have a bounded deadline")
	}
	if strings.Contains(payload.Error, "could not be removed") {
		t.Fatalf("job error reported cleanup failure after successful compensation: %q", payload.Error)
	}
}

func TestRestoreIntoNewVolumeReportsPartialResourceWhenCleanupFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, events, provider := newTestManager(t)
	archivePath := writeTestRestoreArchive(t, "app-db")
	provider.failCommands = map[string]error{
		"cairn-restore":             errors.New("injected restore helper failure"),
		"volume rm app-db-restored": errors.New("injected cleanup failure"),
	}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db-restored",
		Overwrite:  false,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "")
	if err != nil {
		t.Fatalf("ApplyRestore() error = %v", err)
	}
	payload := waitJobDonePayload(t, done, jobID)
	if !strings.Contains(payload.Error, `new target volume "app-db-restored" could not be removed`) {
		t.Fatalf("job error = %q, want explicit partial-resource cleanup failure", payload.Error)
	}
	if got := provider.countRunArg("volume rm app-db-restored"); got != 1 {
		t.Fatalf("volume cleanup calls = %d, want 1; calls = %#v", got, provider.callsSnapshot())
	}
	if !provider.callHadDeadline("volume rm app-db-restored") {
		t.Fatal("failed volume cleanup command did not have a bounded deadline")
	}
}

func TestFailedOverwriteRestoreNeverRemovesPreexistingTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, events, provider := newTestManager(t)
	archivePath := writeTestRestoreArchive(t, "app-db")
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{Summary: models.VolumeSummary{Name: "app-db"}}
	provider.failCommands = map[string]error{
		"cairn-restore": errors.New("injected restore helper failure"),
	}
	done := events.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db",
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "app-db")
	if err != nil {
		t.Fatalf("ApplyRestore() error = %v", err)
	}
	if payload := waitJobDonePayload(t, done, jobID); payload.Error == "" {
		t.Fatal("overwrite restore succeeded after injected helper failure")
	}
	if got := provider.countRunArg("volume rm"); got != 0 {
		t.Fatalf("pre-existing target cleanup calls = %d, want 0; calls = %#v", got, provider.callsSnapshot())
	}
}

func TestCreatedRestoreVolumeCleanupDetachesCancellationAndAddsDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mgr, _, provider := newTestManager(t)
	docker := mgr.Docker.(*fakeBackupDocker)
	record := planRecord{
		TargetVolumeName:        "app-db-restored",
		TargetVolumeFingerprint: "",
		RestoreOwnerToken:       "owner-token",
	}
	docker.volumes[record.TargetVolumeName] = &models.VolumeDetail{
		Summary: models.VolumeSummary{
			Name:   record.TargetVolumeName,
			Driver: "local",
			Labels: map[string]string{restoreOwnerLabel: record.RestoreOwnerToken},
		},
		CreatedAt: time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC),
	}
	var err error
	record.TargetVolumeFingerprint, err = security.VolumeIncarnationFingerprint(*docker.volumes[record.TargetVolumeName])
	if err != nil {
		t.Fatalf("VolumeIncarnationFingerprint() error = %v", err)
	}

	if err := mgr.cleanupCreatedRestoreVolume(ctx, provider, record); err != nil {
		t.Fatalf("cleanupCreatedRestoreVolume() error = %v", err)
	}
	if got := provider.countRunArg("volume rm app-db-restored"); got != 1 {
		t.Fatalf("volume cleanup calls = %d, want 1", got)
	}
	if !provider.callHadDeadline("volume rm app-db-restored") {
		t.Fatal("volume cleanup command did not have a bounded deadline")
	}
	if contextErr, ok := provider.callContextError("volume rm app-db-restored"); !ok || contextErr != nil {
		t.Fatalf("volume cleanup context error = %v, found = %t; want detached active context", contextErr, ok)
	}
}

func TestRestoreRejectsChangedArchiveBeforeRunningHelper(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, _, provider := newTestManager(t)
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
	if jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "app-db"); jobID != "" || !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyRestore() = %q, %v; want empty/%s", jobID, err, apperror.Conflict)
	}
	if provider.hasRunArg("tar xzf") {
		t.Fatalf("restore helper ran despite checksum mismatch: %#v", provider.calls)
	}
}

func TestRestoreRejectsReplacedSourceArtifactsEvenWhenContentMatches(t *testing.T) {
	for _, artifact := range []string{"archive", "metadata"} {
		t.Run(artifact, func(t *testing.T) {
			ctx := context.Background()
			mgr, _, provider := newTestManager(t)
			archivePath := writeTestRestoreArchive(t, "app-db")
			metadataPath := metadataPathForArchive(archivePath)
			mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{
				Summary:   models.VolumeSummary{Name: "app-db"},
				CreatedAt: time.Date(2026, 6, 13, 14, 0, 0, 0, time.UTC),
			}

			plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
				SourcePath: archivePath,
				VolumeName: "app-db",
				Overwrite:  true,
			})
			if err != nil {
				t.Fatalf("PlanRestoreVolume() error = %v", err)
			}
			path := archivePath
			if artifact == "metadata" {
				path = metadataPath
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read planned %s: %v", artifact, err)
			}
			if err := os.Remove(path); err != nil {
				if runtime.GOOS == "windows" {
					// Restore handles request delete sharing, but a filesystem or
					// filter driver may still reject this adversarial replacement.
					t.Logf("filesystem blocked planned %s replacement despite delete sharing: %v", artifact, err)
					return
				}
				t.Fatalf("remove planned %s: %v", artifact, err)
			}
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatalf("replace planned %s: %v", artifact, err)
			}

			if jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "app-db"); jobID != "" || !apperror.IsCode(err, apperror.Conflict) {
				t.Fatalf("ApplyRestore() = %q, %v; want empty/%s", jobID, err, apperror.Conflict)
			}
			if provider.hasRunArg("tar xzf") {
				t.Fatalf("restore helper ran after %s replacement: %#v", artifact, provider.calls)
			}
		})
	}
}

func TestRestoreRejectsRecreatedOverwriteTarget(t *testing.T) {
	ctx := context.Background()
	mgr, _, provider := newTestManager(t)
	archivePath := writeTestRestoreArchive(t, "app-db")
	docker := mgr.Docker.(*fakeBackupDocker)
	docker.volumes["app-db"] = &models.VolumeDetail{
		Summary:   models.VolumeSummary{Name: "app-db", Driver: "local"},
		CreatedAt: time.Date(2026, 6, 13, 14, 0, 0, 0, time.UTC),
	}
	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db",
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	docker.volumes["app-db"] = &models.VolumeDetail{
		Summary:   models.VolumeSummary{Name: "app-db", Driver: "local"},
		CreatedAt: time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC),
	}

	if jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "app-db"); jobID != "" || !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyRestore() = %q, %v; want empty/%s", jobID, err, apperror.Conflict)
	}
	if provider.hasRunArg("tar xzf") {
		t.Fatalf("restore helper ran for recreated target: %#v", provider.calls)
	}
}

func TestRestoreRejectsRuntimeScopeDrift(t *testing.T) {
	ctx := context.Background()
	mgr, _, provider := newTestManager(t)
	archivePath := writeTestRestoreArchive(t, "app-db")
	mgr.Docker.(*fakeBackupDocker).volumes["app-db"] = &models.VolumeDetail{
		Summary:   models.VolumeSummary{Name: "app-db"},
		CreatedAt: time.Date(2026, 6, 13, 14, 0, 0, 0, time.UTC),
	}
	plan, err := mgr.PlanRestoreVolume(ctx, models.RestoreVolumeRequest{
		SourcePath: archivePath,
		VolumeName: "app-db",
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("PlanRestoreVolume() error = %v", err)
	}
	mgr.Providers = fakeProviderResolver{provider: &fakeBackupProvider{contextName: "other"}}

	if jobID, err := mgr.ApplyRestore(ctx, plan.PlanID, "app-db"); jobID != "" || !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyRestore() = %q, %v; want empty/%s", jobID, err, apperror.Conflict)
	}
	if provider.hasRunArg("tar xzf") {
		t.Fatalf("planned runtime executed after scope drift: %#v", provider.calls)
	}
}

func writeTestRestoreArchive(t *testing.T, volumeName string) string {
	t.Helper()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, volumeName+".tar.gz")
	payload := []byte("backup-data")
	if err := os.WriteFile(archivePath, payload, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	sum := sha256.Sum256(payload)
	if err := writeSidecar(metadataPathForArchive(archivePath), BackupSidecar{
		FormatVersion: formatVersion,
		Volume:        volumeName,
		Project:       "app",
		SHA256:        hex.EncodeToString(sum[:]),
	}); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	return archivePath
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
	t.Cleanup(mgr.StopAll)
	return mgr, eventBus, provider
}

type fakeProviderResolver struct {
	provider providers.PlatformProvider
}

func (r fakeProviderResolver) ActiveProvider(context.Context) (providers.PlatformProvider, error) {
	return r.provider, nil
}

type fakeBackupProvider struct {
	mu               sync.Mutex
	calls            [][]string
	callDeadlines    []bool
	callContextErrs  []error
	failCommands     map[string]error
	blockRun         chan struct{}
	runStarted       chan struct{}
	runCanceled      chan error
	afterBackupWrite func() error
	contextName      string
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
func (p *fakeBackupProvider) DockerContext(context.Context) (string, error) {
	return firstNonEmpty(p.contextName, "default"), nil
}
func (p *fakeBackupProvider) RunDocker(ctx context.Context, args ...string) (*providers.CommandResult, error) {
	_, hasDeadline := ctx.Deadline()
	joined := strings.Join(args, " ")
	p.mu.Lock()
	p.calls = append(p.calls, append([]string(nil), args...))
	p.callDeadlines = append(p.callDeadlines, hasDeadline)
	p.callContextErrs = append(p.callContextErrs, ctx.Err())
	blockRun := p.blockRun
	runStarted := p.runStarted
	runCanceled := p.runCanceled
	afterBackupWrite := p.afterBackupWrite
	var commandErr error
	for needle, injectedErr := range p.failCommands {
		if strings.Contains(joined, needle) {
			commandErr = injectedErr
			break
		}
	}
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
	if commandErr != nil {
		return &providers.CommandResult{ExitCode: 1, Stderr: commandErr.Error()}, commandErr
	}
	if scriptIndex := slices.Index(args, "cairn-backup"); scriptIndex >= 0 && scriptIndex+1 < len(args) {
		archive := ""
		backupDir := ""
		for i, arg := range args {
			if arg == "-v" && i+1 < len(args) && strings.HasSuffix(args[i+1], ":/backup") {
				backupDir = strings.TrimSuffix(args[i+1], ":/backup")
			}
		}
		archive = strings.TrimPrefix(args[scriptIndex+1], "/backup/")
		if archive != "" && backupDir != "" {
			if err := os.WriteFile(filepath.Join(backupDir, archive), []byte("backup-data"), 0o600); err != nil {
				return &providers.CommandResult{ExitCode: 1, Stderr: err.Error()}, err
			}
			if afterBackupWrite != nil {
				if err := afterBackupWrite(); err != nil {
					return &providers.CommandResult{ExitCode: 1, Stderr: err.Error()}, err
				}
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
	return p.countRunArg(value) > 0
}

func (p *fakeBackupProvider) countRunArg(value string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, call := range p.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, value) {
			count++
		}
	}
	return count
}

func (p *fakeBackupProvider) callsSnapshot() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([][]string, len(p.calls))
	for index, call := range p.calls {
		result[index] = append([]string(nil), call...)
	}
	return result
}

func (p *fakeBackupProvider) callHadDeadline(value string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for index, call := range p.calls {
		if strings.Contains(strings.Join(call, " "), value) && index < len(p.callDeadlines) {
			return p.callDeadlines[index]
		}
	}
	return false
}

func (p *fakeBackupProvider) callContextError(value string) (error, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for index, call := range p.calls {
		if strings.Contains(strings.Join(call, " "), value) && index < len(p.callContextErrs) {
			return p.callContextErrs[index], true
		}
	}
	return nil, false
}

type fakeBackupDocker struct {
	volumes        map[string]*models.VolumeDetail
	createRequests []models.CreateVolumeRequest
	beforeCreate   func(models.CreateVolumeRequest)
}

func (d *fakeBackupDocker) ProviderID() string { return "linux_native" }
func (d *fakeBackupDocker) GetVolume(_ context.Context, name string) (*models.VolumeDetail, error) {
	volume, ok := d.volumes[name]
	if !ok {
		return nil, apperror.New(apperror.NotFound, "Volume was not found")
	}
	copy := *volume
	if copy.CreatedAt.IsZero() {
		copy.CreatedAt = time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC)
	}
	return &copy, nil
}
func (d *fakeBackupDocker) CreateVolume(_ context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	requestCopy := req
	requestCopy.Labels = cloneStringMap(req.Labels)
	d.createRequests = append(d.createRequests, requestCopy)
	if d.beforeCreate != nil {
		d.beforeCreate(requestCopy)
	}
	if existing := d.volumes[req.Name]; existing != nil {
		summary := existing.Summary
		summary.Labels = cloneStringMap(existing.Summary.Labels)
		return &summary, nil
	}
	summary := &models.VolumeSummary{
		Name:   req.Name,
		Driver: firstNonEmpty(req.Driver, "local"),
		Labels: cloneStringMap(req.Labels),
	}
	d.volumes[req.Name] = &models.VolumeDetail{
		Summary:   *summary,
		CreatedAt: time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC),
	}
	return summary, nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
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

func waitBackupJobs(t *testing.T, events <-chan bus.Event, remaining map[string]struct{}) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for len(remaining) > 0 {
		select {
		case event := <-events:
			payload, ok := event.Payload.(jobDonePayload)
			if !ok {
				continue
			}
			if _, wanted := remaining[payload.JobID]; !wanted {
				continue
			}
			if payload.Error != "" {
				t.Fatalf("job %s failed: %s", payload.JobID, payload.Error)
			}
			delete(remaining, payload.JobID)
		case <-deadline:
			t.Fatalf("timed out waiting for %d backup jobs", len(remaining))
		}
	}
}

func savedPlanRecord(t *testing.T, mgr *Manager, planID string) planRecord {
	t.Helper()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	record, ok := mgr.plans[planID]
	if !ok {
		t.Fatalf("plan %s is not saved", planID)
	}
	return record
}

func assertFileHandleClosed(t *testing.T, handle *os.File, label string) {
	t.Helper()
	if handle == nil {
		t.Fatalf("%s handle is nil", label)
	}
	if _, err := handle.Stat(); err == nil {
		t.Fatalf("%s handle remains open", label)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("path %s still exists: %v", path, err)
	}
}
