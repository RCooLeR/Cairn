package store

import (
	"context"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestBackupRepositoryDeleteReportsMissingRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedStore(t, ctx)
	defer closeStore(t, db)

	repo := db.Backups()
	record := BackupRecord{
		ID:         "backup-1",
		ProviderID: "linux_native",
		VolumeName: "data",
		BackupPath: "/tmp/data.tar.gz",
		Result:     "success",
		CreatedAt:  time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
	}
	if err := repo.Insert(ctx, record); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := repo.Delete(ctx, record.ID); err != nil {
		t.Fatalf("Delete(existing) error = %v", err)
	}
	if err := repo.Delete(ctx, record.ID); !IsStoreNotFound(err) {
		t.Fatalf("Delete(missing) error = %v, want store not found", err)
	}
}

func TestBackupRepositoryRoundTripAndFilters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedStore(t, ctx)
	defer closeStore(t, db)

	repo := db.Backups()
	base := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	records := []BackupRecord{
		{
			ID:                  "backup-data-app",
			ProviderID:          "linux_native",
			ProjectID:           "linux_native/app",
			VolumeName:          "data",
			BackupPath:          "/tmp/data.tar.gz",
			MetadataPath:        "/tmp/data.json",
			CompressedSizeBytes: 42,
			Result:              "success",
			CreatedAt:           base.Add(time.Minute),
		},
		{
			ID:         "backup-cache-app",
			ProviderID: "linux_native",
			ProjectID:  "linux_native/app",
			VolumeName: "cache",
			BackupPath: "/tmp/cache.tar.gz",
			Result:     "failed",
			CreatedAt:  base.Add(2 * time.Minute),
			Error:      "disk full",
		},
		{
			ID:         "backup-data-other",
			ProviderID: "linux_native",
			ProjectID:  "linux_native/other",
			VolumeName: "data",
			BackupPath: "/tmp/other-data.tar.gz",
			Result:     "success",
			CreatedAt:  base.Add(3 * time.Minute),
		},
	}
	for _, record := range records {
		if err := repo.Insert(ctx, record); err != nil {
			t.Fatalf("Insert(%s) error = %v", record.ID, err)
		}
	}

	got, err := repo.Get(ctx, "backup-data-app")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.MetadataPath != "/tmp/data.json" || got.CompressedSizeBytes != 42 || !got.CreatedAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("Get() = %#v", got)
	}

	byVolume, err := repo.List(ctx, models.BackupFilter{VolumeName: "data"})
	if err != nil {
		t.Fatalf("List(volume) error = %v", err)
	}
	if len(byVolume) != 2 || byVolume[0].ID != "backup-data-other" || byVolume[1].ID != "backup-data-app" {
		t.Fatalf("List(volume) = %#v", byVolume)
	}

	byProject, err := repo.List(ctx, models.BackupFilter{ProjectID: "linux_native/app"})
	if err != nil {
		t.Fatalf("List(project) error = %v", err)
	}
	if len(byProject) != 2 || byProject[0].ID != "backup-cache-app" || byProject[1].ID != "backup-data-app" {
		t.Fatalf("List(project) = %#v", byProject)
	}
}
