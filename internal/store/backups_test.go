package store

import (
	"context"
	"testing"
	"time"
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
