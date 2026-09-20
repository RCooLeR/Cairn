package store

import (
	"context"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestProviderRepositoryRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo := db.Providers()
	if err := repo.Upsert(ctx, ProviderRecord{
		ID:          "linux_native",
		Type:        "linux_native",
		Platform:    "linux",
		DisplayName: "Linux Native",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	status := &models.ProviderStatus{Healthy: true, DockerVersion: "27.1.2"}
	if err := repo.SaveStatus(ctx, "linux_native", status, utcNowTimeForTest(t)); err != nil {
		t.Fatalf("SaveStatus() error = %v", err)
	}

	record, err := repo.Get(ctx, "linux_native")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !record.Enabled || record.LastStatusJSON == "" || record.LastCheckedAt.IsZero() {
		t.Fatalf("record = %#v", record)
	}
}

func utcNowTimeForTest(t *testing.T) time.Time {
	t.Helper()
	return time.Now().UTC()
}
