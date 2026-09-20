package store

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestContainerSnapshotKeysIncludeHealthWithoutTimestampChurn(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	scope := mustRuntimeScope(t, "windows_wsl_ubuntu", "wsl:Ubuntu")
	record := ContainerCacheRecord{Summary: models.ContainerSummary{
		ID: "container-health", Name: "web", State: "running", Status: "running",
	}}
	seen := time.Now().UTC()
	previous := ""
	for _, health := range []models.HealthStatus{
		models.HealthStatusStarting, models.HealthStatusHealthy,
		models.HealthStatusUnhealthy, models.HealthStatusUnknown,
	} {
		record.Summary.Health = health
		if err := db.Objects().SaveContainersScoped(ctx, scope, []ContainerCacheRecord{record}, seen); err != nil {
			t.Fatal(err)
		}
		snapshot, err := db.Objects().SnapshotKeysScoped(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		key := snapshot.Containers[record.Summary.ID]
		if key == "" || key == previous {
			t.Fatalf("health %q did not change running-container snapshot identity", health)
		}
		seen = seen.Add(time.Minute)
		if err := db.Objects().SaveContainersScoped(ctx, scope, []ContainerCacheRecord{record}, seen); err != nil {
			t.Fatal(err)
		}
		repeated, err := db.Objects().SnapshotKeysScoped(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if repeated.Containers[record.Summary.ID] != key {
			t.Fatalf("unchanged health %q changed snapshot identity when only last-seen advanced", health)
		}
		previous = key
	}
}

func TestContainerCacheChangesAreCommittedAndSerialized(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := db.Objects()
	scope := mustRuntimeScope(t, "windows_wsl_ubuntu", "wsl:Ubuntu")
	record := ContainerCacheRecord{Summary: models.ContainerSummary{
		ID: "retained", Name: "web", State: "running", Status: "running", Health: models.HealthStatusStarting,
	}}
	if err := repo.SaveContainersScoped(ctx, scope, []ContainerCacheRecord{record}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.writer.ExecContext(ctx, `CREATE TEMP TRIGGER reject_container_insert BEFORE INSERT ON containers_cache
		WHEN NEW.id = 'reject' BEGIN SELECT RAISE(FAIL, 'injected write failure'); END`); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.SaveContainersWithChangesScoped(ctx, scope, []ContainerCacheRecord{
		{Summary: models.ContainerSummary{ID: "new", State: "running"}},
		{Summary: models.ContainerSummary{ID: "reject", State: "running"}},
	}, time.Now(), true)
	if err == nil || len(changed) != 0 {
		t.Fatalf("failed transaction returned changes=%v err=%v", changed, err)
	}
	retained, err := repo.ListContainersScoped(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) != 1 || retained[0].Summary.ID != record.Summary.ID {
		t.Fatalf("rollback lost original snapshot: %#v", retained)
	}

	record.Summary.Health = models.HealthStatusHealthy
	type result struct {
		ids []string
		err error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			ids, err := repo.SaveContainersWithChangesScoped(ctx, scope, []ContainerCacheRecord{record}, time.Now(), false)
			results <- result{ids: ids, err: err}
		})
	}
	workers.Wait()
	close(results)
	var reported []string
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		reported = append(reported, result.ids...)
	}
	if !slices.Equal(reported, []string{record.Summary.ID}) {
		t.Fatalf("concurrent writers reported duplicate/lost changes: %v", reported)
	}
	removed, err := repo.SaveContainersWithChangesScoped(ctx, scope, nil, time.Now(), true)
	if err != nil || !slices.Equal(removed, []string{record.Summary.ID}) {
		t.Fatalf("empty replacement changes=%v err=%v", removed, err)
	}
}
