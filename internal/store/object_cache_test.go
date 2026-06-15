package store

import (
	"context"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestObjectCacheRepositoryUpsertAndDeleteStale(t *testing.T) {
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

	repo := db.Objects()
	oldSeen := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	freshSeen := oldSeen.Add(25 * time.Hour)

	if err := repo.SaveContainers(ctx, "linux_native", []ContainerCacheRecord{{
		Summary: models.ContainerSummary{
			ID:        "container-1",
			Name:      "web",
			Image:     "example/web:latest",
			Status:    "Up 3 hours",
			State:     "running",
			Health:    models.HealthStatusHealthy,
			CreatedAt: oldSeen,
		},
		Labels: map[string]string{"com.docker.compose.project": "demo"},
	}}, oldSeen); err != nil {
		t.Fatalf("SaveContainers() old error = %v", err)
	}
	if err := repo.SaveContainers(ctx, "linux_native", []ContainerCacheRecord{{
		Summary: models.ContainerSummary{
			ID:        "container-2",
			Name:      "worker",
			Image:     "example/worker:latest",
			Status:    "Exited (0) 1 hour ago",
			State:     "exited",
			Health:    models.HealthStatusUnknown,
			CreatedAt: freshSeen,
		},
	}}, freshSeen); err != nil {
		t.Fatalf("SaveContainers() fresh error = %v", err)
	}

	if got := countRows(t, ctx, db, "containers_cache"); got != 2 {
		t.Fatalf("containers before prune = %d, want 2", got)
	}
	containers, err := repo.ListContainers(ctx, "linux_native")
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("containers = %#v, want 2 records", containers)
	}
	if containers[0].Summary.Name != "web" || containers[0].Summary.Status != "Up 3 hours" || containers[0].Summary.State != "running" {
		t.Fatalf("first cached container = %#v, want web with status and state preserved", containers[0].Summary)
	}
	if containers[1].Summary.Name != "worker" || containers[1].Summary.Status != "Exited (0) 1 hour ago" || containers[1].Summary.State != "exited" {
		t.Fatalf("second cached container = %#v, want worker with status and state preserved", containers[1].Summary)
	}
	if err := repo.DeleteStale(ctx, "linux_native", oldSeen.Add(24*time.Hour)); err != nil {
		t.Fatalf("DeleteStale() error = %v", err)
	}
	if got := countRows(t, ctx, db, "containers_cache"); got != 1 {
		t.Fatalf("containers after prune = %d, want 1", got)
	}
	if err := repo.SaveContainersSnapshot(ctx, "linux_native", nil, freshSeen.Add(time.Hour)); err != nil {
		t.Fatalf("SaveContainersSnapshot(empty) error = %v", err)
	}
	if got := countRows(t, ctx, db, "containers_cache"); got != 0 {
		t.Fatalf("containers after empty snapshot = %d, want 0", got)
	}
}

func countRows(t *testing.T, ctx context.Context, db *Store, table string) int {
	t.Helper()
	var count int
	if err := db.writer.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
