package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestProjectRepositorySnapshotPreservesPinnedAndReplacesServices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 13, 4, 0, 0, 0, time.UTC)

	if err := repo.UpsertImported(ctx, ProjectRecord{
		ID:         "linux_native/demo",
		ProviderID: "linux_native",
		Name:       "demo",
		Source:     ProjectSourceImported,
		Pinned:     true,
		LastSeenAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("UpsertImported() error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, "linux_native", []ProjectRecord{{
		ID:         "linux_native/demo",
		ProviderID: "linux_native",
		Name:       "demo",
		Source:     ProjectSourceLabels,
		Status:     models.ProjectStatusRunning,
		Health:     models.HealthStatusHealthy,
		LastSeenAt: now,
	}}, []ServiceRecord{{
		ID:              "linux_native/demo/web",
		ProjectID:       "linux_native/demo",
		Name:            "web",
		ImageRef:        "nginx:alpine",
		Status:          models.ProjectStatusRunning,
		Health:          models.HealthStatusHealthy,
		ReplicasRunning: 1,
		ReplicasTotal:   1,
		LastSeenAt:      now,
	}}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}

	project, err := repo.Get(ctx, "linux_native/demo")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !project.Pinned || project.Source != ProjectSourceLabels {
		t.Fatalf("project pinned/source = %v/%s", project.Pinned, project.Source)
	}
	services, err := repo.ListServices(ctx, "linux_native/demo")
	if err != nil {
		t.Fatalf("ListServices() error = %v", err)
	}
	if len(services) != 1 || services[0].ImageRef != "nginx:alpine" {
		t.Fatalf("services = %#v", services)
	}

	if err := repo.SaveSnapshot(ctx, "linux_native", []ProjectRecord{{
		ID:         "linux_native/demo",
		ProviderID: "linux_native",
		Name:       "demo",
		Source:     ProjectSourceLabels,
		Status:     models.ProjectStatusStopped,
		LastSeenAt: now.Add(time.Minute),
	}}, []ServiceRecord{{
		ID:         "linux_native/demo/worker",
		ProjectID:  "linux_native/demo",
		Name:       "worker",
		ImageRef:   "busybox:1.36",
		Status:     models.ProjectStatusStopped,
		LastSeenAt: now.Add(time.Minute),
	}}, now.Add(time.Minute), time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() replacement error = %v", err)
	}
	services, err = repo.ListServices(ctx, "linux_native/demo")
	if err != nil {
		t.Fatalf("ListServices() replacement error = %v", err)
	}
	if len(services) != 1 || services[0].Name != "worker" {
		t.Fatalf("replacement services = %#v", services)
	}
}

func TestProjectRepositoryDeletesOnlyStaleDetectedProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 13, 5, 0, 0, 0, time.UTC)
	old := now.Add(-25 * time.Hour)

	if err := repo.SaveSnapshot(ctx, "linux_native", []ProjectRecord{
		{ID: "linux_native/old", ProviderID: "linux_native", Name: "old", Source: ProjectSourceLabels, LastSeenAt: old},
		{ID: "linux_native/imported", ProviderID: "linux_native", Name: "imported", Source: ProjectSourceImported, LastSeenAt: old},
	}, nil, old, time.Time{}); err != nil {
		t.Fatalf("seed SaveSnapshot() error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, "linux_native", nil, nil, now, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("prune SaveSnapshot() error = %v", err)
	}

	projects, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "linux_native/imported" {
		t.Fatalf("projects after prune = %#v", projects)
	}
}

func openStoreForProjectTest(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := db.Providers().Upsert(ctx, ProviderRecord{
		ID:          "linux_native",
		Type:        "linux_native",
		Platform:    "linux",
		DisplayName: "Linux Native",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	return db
}
