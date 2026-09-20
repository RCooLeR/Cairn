package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
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
	scope := mustRuntimeScope(t, "linux_native", "default")
	oldSeen := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	freshSeen := oldSeen.Add(25 * time.Hour)

	if err := repo.SaveContainersScoped(ctx, scope, []ContainerCacheRecord{{
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
	if err := repo.SaveContainersScoped(ctx, scope, []ContainerCacheRecord{{
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
	containers, err := repo.ListContainersScoped(ctx, scope)
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
	if err := repo.DeleteStaleScoped(ctx, scope, oldSeen.Add(24*time.Hour)); err != nil {
		t.Fatalf("DeleteStale() error = %v", err)
	}
	if got := countRows(t, ctx, db, "containers_cache"); got != 1 {
		t.Fatalf("containers after prune = %d, want 1", got)
	}
	if err := repo.SaveContainersSnapshotScoped(ctx, scope, nil, freshSeen.Add(time.Hour)); err != nil {
		t.Fatalf("SaveContainersSnapshot(empty) error = %v", err)
	}
	if got := countRows(t, ctx, db, "containers_cache"); got != 0 {
		t.Fatalf("containers after empty snapshot = %d, want 0", got)
	}
}

func TestObjectCacheRepositorySnapshotsPruneMissingObjects(t *testing.T) {
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
	scope := mustRuntimeScope(t, "linux_native", "default")
	seen := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if err := repo.SaveImagesScoped(ctx, scope, []ImageCacheRecord{
		{Summary: models.ImageSummary{ID: "image-a", RepoTags: []string{"app:a"}}},
		{Summary: models.ImageSummary{ID: "image-b", RepoTags: []string{"app:b"}}},
	}, seen); err != nil {
		t.Fatalf("SaveImages() error = %v", err)
	}
	if err := repo.SaveVolumesScoped(ctx, scope, []VolumeCacheRecord{
		{Summary: models.VolumeSummary{Name: "volume-a"}},
		{Summary: models.VolumeSummary{Name: "volume-b"}},
	}, seen); err != nil {
		t.Fatalf("SaveVolumes() error = %v", err)
	}
	if err := repo.SaveNetworksScoped(ctx, scope, []NetworkCacheRecord{
		{Summary: models.NetworkSummary{ID: "network-a", Name: "net-a"}},
		{Summary: models.NetworkSummary{ID: "network-b", Name: "net-b"}},
	}, seen); err != nil {
		t.Fatalf("SaveNetworks() error = %v", err)
	}

	if err := repo.SaveImagesSnapshotScoped(ctx, scope, []ImageCacheRecord{
		{Summary: models.ImageSummary{ID: "image-b", RepoTags: []string{"app:b"}}},
	}, seen.Add(time.Minute)); err != nil {
		t.Fatalf("SaveImagesSnapshot() error = %v", err)
	}
	if err := repo.SaveVolumesSnapshotScoped(ctx, scope, []VolumeCacheRecord{
		{Summary: models.VolumeSummary{Name: "volume-b"}},
	}, seen.Add(time.Minute)); err != nil {
		t.Fatalf("SaveVolumesSnapshot() error = %v", err)
	}
	if err := repo.SaveNetworksSnapshotScoped(ctx, scope, []NetworkCacheRecord{
		{Summary: models.NetworkSummary{ID: "network-b", Name: "net-b"}},
	}, seen.Add(time.Minute)); err != nil {
		t.Fatalf("SaveNetworksSnapshot() error = %v", err)
	}

	if got := countRows(t, ctx, db, "images_cache"); got != 1 {
		t.Fatalf("images after snapshot = %d, want 1", got)
	}
	if got := countRows(t, ctx, db, "volumes_cache"); got != 1 {
		t.Fatalf("volumes after snapshot = %d, want 1", got)
	}
	if got := countRows(t, ctx, db, "networks_cache"); got != 1 {
		t.Fatalf("networks after snapshot = %d, want 1", got)
	}
}

func TestObjectCacheRepositoryIsolatesCollidingNativeKeysByRuntimeScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo := db.Objects()
	left := mustRuntimeScope(t, "windows_wsl_ubuntu", "wsl:Ubuntu")
	right := mustRuntimeScope(t, "windows_wsl_ubuntu", "wsl:Work")
	seen := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)

	for _, fixture := range []struct {
		scope  runtimescope.Scope
		suffix string
	}{
		{scope: left, suffix: "left"},
		{scope: right, suffix: "right"},
	} {
		if err := repo.SaveContainersScoped(ctx, fixture.scope, []ContainerCacheRecord{{
			Summary: models.ContainerSummary{ID: "same-container", Name: fixture.suffix, Status: "Up " + fixture.suffix},
		}}, seen); err != nil {
			t.Fatalf("SaveContainersScoped(%s) error = %v", fixture.suffix, err)
		}
		if err := repo.SaveImagesScoped(ctx, fixture.scope, []ImageCacheRecord{{
			Summary: models.ImageSummary{ID: "same-image", RepoTags: []string{"app:" + fixture.suffix}},
		}}, seen); err != nil {
			t.Fatalf("SaveImagesScoped(%s) error = %v", fixture.suffix, err)
		}
		if err := repo.SaveVolumesScoped(ctx, fixture.scope, []VolumeCacheRecord{{
			Summary: models.VolumeSummary{Name: "same-volume", Driver: fixture.suffix},
		}}, seen); err != nil {
			t.Fatalf("SaveVolumesScoped(%s) error = %v", fixture.suffix, err)
		}
		if err := repo.SaveNetworksScoped(ctx, fixture.scope, []NetworkCacheRecord{{
			Summary: models.NetworkSummary{ID: "same-network", Name: fixture.suffix},
		}}, seen); err != nil {
			t.Fatalf("SaveNetworksScoped(%s) error = %v", fixture.suffix, err)
		}
	}

	for _, table := range []string{"containers_cache", "images_cache", "volumes_cache", "networks_cache"} {
		if got := countRows(t, ctx, db, table); got != 2 {
			t.Fatalf("%s rows = %d, want both colliding scopes", table, got)
		}
	}
	leftContainers, err := repo.ListContainersScoped(ctx, left)
	if err != nil {
		t.Fatalf("ListContainersScoped(left) error = %v", err)
	}
	rightContainers, err := repo.ListContainersScoped(ctx, right)
	if err != nil {
		t.Fatalf("ListContainersScoped(right) error = %v", err)
	}
	if len(leftContainers) != 1 || leftContainers[0].Summary.Name != "left" {
		t.Fatalf("left containers = %#v", leftContainers)
	}
	if len(rightContainers) != 1 || rightContainers[0].Summary.Name != "right" {
		t.Fatalf("right containers = %#v", rightContainers)
	}
	leftSnapshot, err := repo.SnapshotKeysScoped(ctx, left)
	if err != nil {
		t.Fatalf("SnapshotKeysScoped(left) error = %v", err)
	}
	rightSnapshot, err := repo.SnapshotKeysScoped(ctx, right)
	if err != nil {
		t.Fatalf("SnapshotKeysScoped(right) error = %v", err)
	}
	if leftSnapshot.Images["same-image"] == rightSnapshot.Images["same-image"] ||
		leftSnapshot.Volumes["same-volume"] == rightSnapshot.Volumes["same-volume"] ||
		leftSnapshot.Networks["same-network"] == rightSnapshot.Networks["same-network"] {
		t.Fatalf("scoped snapshots were overwritten: left=%#v right=%#v", leftSnapshot, rightSnapshot)
	}

	if err := repo.SaveContainersSnapshotScoped(ctx, left, nil, seen.Add(time.Minute)); err != nil {
		t.Fatalf("SaveContainersSnapshotScoped(left empty) error = %v", err)
	}
	if got, err := repo.ListContainersScoped(ctx, right); err != nil || len(got) != 1 || got[0].Summary.Name != "right" {
		t.Fatalf("right containers after left replacement = %#v, %v", got, err)
	}
	if err := repo.DeleteStaleScoped(ctx, left, seen.Add(time.Hour)); err != nil {
		t.Fatalf("DeleteStaleScoped(left) error = %v", err)
	}
	for _, table := range []string{"images_cache", "volumes_cache", "networks_cache"} {
		if got := countRows(t, ctx, db, table); got != 1 {
			t.Fatalf("%s rows after left stale cleanup = %d, want right scope preserved", table, got)
		}
	}
}

func TestObjectCacheRepositoryRejectsIncompleteScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo := db.Objects()
	if err := repo.SaveImagesScoped(ctx, runtimescope.Scope{}, nil, time.Now()); !errors.Is(err, errInvalidObjectCacheScope) {
		t.Fatalf("SaveImagesScoped(zero scope) error = %v, want invalid scope", err)
	}
}

func TestObjectCacheTablesUseRuntimeScopeCompositePrimaryKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	for table, want := range map[string][]string{
		"containers_cache": {"provider_id", "context_name", "id"},
		"images_cache":     {"provider_id", "context_name", "id"},
		"volumes_cache":    {"provider_id", "context_name", "name"},
		"networks_cache":   {"provider_id", "context_name", "id"},
	} {
		if got := primaryKeyColumns(t, ctx, db.writer, table); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s primary key = %v, want %v", table, got, want)
		}
	}
}

func primaryKeyColumns(t *testing.T, ctx context.Context, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	byPosition := map[int]string{}
	maxPosition := 0
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			position     int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &position); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if position > 0 {
			byPosition[position] = name
			if position > maxPosition {
				maxPosition = position
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	columns := make([]string, 0, maxPosition)
	for position := 1; position <= maxPosition; position++ {
		columns = append(columns, byPosition[position])
	}
	return columns
}

func mustRuntimeScope(t *testing.T, providerID string, contextName string) runtimescope.Scope {
	t.Helper()
	scope, ok := runtimescope.New(providerID, contextName)
	if !ok {
		t.Fatalf("runtimescope.New(%q, %q) rejected test scope", providerID, contextName)
	}
	return scope
}

func countRows(t *testing.T, ctx context.Context, db *Store, table string) int {
	t.Helper()
	var count int
	if err := db.writer.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
