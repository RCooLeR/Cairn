package store

import (
	"context"
	"database/sql"
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

func TestProjectRepositorySnapshotNilServicesPreservesExistingServices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	project := ProjectRecord{
		ID:         "linux_native/partial",
		ProviderID: "linux_native",
		Name:       "partial",
		LastSeenAt: now,
	}
	if err := repo.SaveSnapshot(ctx, "linux_native", []ProjectRecord{project}, []ServiceRecord{{
		ID:         "linux_native/partial/api",
		ProjectID:  "linux_native/partial",
		Name:       "api",
		ImageRef:   "nginx:alpine",
		LastSeenAt: now,
	}}, now, time.Time{}); err != nil {
		t.Fatalf("seed SaveSnapshot() error = %v", err)
	}

	project.LastSeenAt = now.Add(time.Minute)
	if err := repo.SaveSnapshot(ctx, "linux_native", []ProjectRecord{project}, nil, project.LastSeenAt, time.Time{}); err != nil {
		t.Fatalf("partial SaveSnapshot() error = %v", err)
	}
	services, err := repo.ListServices(ctx, "linux_native/partial")
	if err != nil {
		t.Fatalf("ListServices() after partial snapshot error = %v", err)
	}
	if len(services) != 1 || services[0].Name != "api" {
		t.Fatalf("services after partial snapshot = %#v", services)
	}

	if err := repo.SaveSnapshot(ctx, "linux_native", []ProjectRecord{project}, []ServiceRecord{}, project.LastSeenAt, time.Time{}); err != nil {
		t.Fatalf("empty service SaveSnapshot() error = %v", err)
	}
	services, err = repo.ListServices(ctx, "linux_native/partial")
	if err != nil {
		t.Fatalf("ListServices() after empty service snapshot error = %v", err)
	}
	if len(services) != 0 {
		t.Fatalf("services after empty service snapshot = %#v, want empty", services)
	}
}

func TestProjectRepositoryPartialServiceSnapshotOnlyReplacesCoveredProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 15, 12, 30, 0, 0, time.UTC)

	projects := []ProjectRecord{
		{ID: "linux_native/web", ProviderID: "linux_native", Name: "web", LastSeenAt: now},
		{ID: "linux_native/worker", ProviderID: "linux_native", Name: "worker", LastSeenAt: now},
	}
	if err := repo.SaveSnapshot(ctx, "linux_native", projects, []ServiceRecord{
		{ID: "linux_native/web/api", ProjectID: "linux_native/web", Name: "api", ImageRef: "nginx", LastSeenAt: now},
		{ID: "linux_native/worker/job", ProjectID: "linux_native/worker", Name: "job", ImageRef: "busybox", LastSeenAt: now},
	}, now, time.Time{}); err != nil {
		t.Fatalf("seed SaveSnapshot() error = %v", err)
	}

	next := now.Add(time.Minute)
	projects[0].LastSeenAt = next
	projects[1].LastSeenAt = next
	if err := repo.SaveSnapshot(ctx, "linux_native", projects, []ServiceRecord{
		{ID: "linux_native/web/api-v2", ProjectID: "linux_native/web", Name: "api-v2", ImageRef: "nginx:alpine", LastSeenAt: next},
	}, next, time.Time{}); err != nil {
		t.Fatalf("partial service SaveSnapshot() error = %v", err)
	}

	webServices, err := repo.ListServices(ctx, "linux_native/web")
	if err != nil {
		t.Fatalf("ListServices(web) error = %v", err)
	}
	if len(webServices) != 1 || webServices[0].Name != "api-v2" {
		t.Fatalf("web services = %#v, want only replacement service", webServices)
	}
	workerServices, err := repo.ListServices(ctx, "linux_native/worker")
	if err != nil {
		t.Fatalf("ListServices(worker) error = %v", err)
	}
	if len(workerServices) != 1 || workerServices[0].Name != "job" {
		t.Fatalf("worker services = %#v, want original service preserved", workerServices)
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

func TestProjectRepositoryListByProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)

	if err := repo.SaveSnapshot(ctx, "windows_wsl_ubuntu", []ProjectRecord{{
		ID:          "windows_wsl_ubuntu/ubuntu-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:Ubuntu",
		Name:        "ubuntu-app",
		LastSeenAt:  now,
	}, {
		ID:          "windows_wsl_ubuntu/cairn-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
		Name:        "cairn-app",
		Source:      ProjectSourceImported,
		LastSeenAt:  now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot windows error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, "linux_native", []ProjectRecord{{
		ID:         "linux_native/app",
		ProviderID: "linux_native",
		Name:       "app",
		LastSeenAt: now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot linux error = %v", err)
	}

	projects, err := repo.ListByProvider(ctx, "windows_wsl_ubuntu")
	if err != nil {
		t.Fatalf("ListByProvider() error = %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("provider projects = %#v", projects)
	}
	projects, err = repo.ListByProviderContext(ctx, "windows_wsl_ubuntu", "wsl:cairn-dev")
	if err != nil {
		t.Fatalf("ListByProviderContext() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "windows_wsl_ubuntu/cairn-app" {
		t.Fatalf("context projects = %#v", projects)
	}
	imported, err := repo.ListImportedByProviderContext(ctx, "windows_wsl_ubuntu", "wsl:Ubuntu")
	if err != nil {
		t.Fatalf("ListImportedByProviderContext(ubuntu) error = %v", err)
	}
	if len(imported) != 0 {
		t.Fatalf("ubuntu imported projects = %#v", imported)
	}
	imported, err = repo.ListImportedByProviderContext(ctx, "windows_wsl_ubuntu", "wsl:cairn-dev")
	if err != nil {
		t.Fatalf("ListImportedByProviderContext(cairn) error = %v", err)
	}
	if len(imported) != 1 || imported[0].ID != "windows_wsl_ubuntu/cairn-app" {
		t.Fatalf("cairn imported projects = %#v", imported)
	}
}

func TestProjectRepositoryListServicesByProjectIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

	if err := repo.SaveSnapshot(ctx, "linux_native", []ProjectRecord{
		{ID: "linux_native/web", ProviderID: "linux_native", Name: "web", LastSeenAt: now},
		{ID: "linux_native/worker", ProviderID: "linux_native", Name: "worker", LastSeenAt: now},
	}, []ServiceRecord{
		{ID: "linux_native/web/api", ProjectID: "linux_native/web", Name: "api", ImageRef: "nginx", LastSeenAt: now},
		{ID: "linux_native/web/cache", ProjectID: "linux_native/web", Name: "cache", ImageRef: "redis", LastSeenAt: now},
		{ID: "linux_native/worker/job", ProjectID: "linux_native/worker", Name: "job", ImageRef: "busybox", LastSeenAt: now},
	}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}

	servicesByProject, err := repo.ListServicesByProjectIDs(ctx, []string{
		"linux_native/web",
		"linux_native/worker",
		"linux_native/missing",
		"linux_native/web",
		"",
	})
	if err != nil {
		t.Fatalf("ListServicesByProjectIDs() error = %v", err)
	}
	if got := len(servicesByProject["linux_native/web"]); got != 2 {
		t.Fatalf("web service count = %d, want 2: %#v", got, servicesByProject["linux_native/web"])
	}
	if got := len(servicesByProject["linux_native/worker"]); got != 1 {
		t.Fatalf("worker service count = %d, want 1: %#v", got, servicesByProject["linux_native/worker"])
	}
	if _, ok := servicesByProject["linux_native/missing"]; !ok {
		t.Fatal("missing project should be present with an empty service list")
	}
}

func TestProjectRepositoryDeleteRemovesProjectAndServices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 15, 11, 0, 0, 0, time.UTC)

	if err := repo.SaveSnapshot(ctx, "linux_native", []ProjectRecord{{
		ID:         "linux_native/delete-me",
		ProviderID: "linux_native",
		Name:       "delete-me",
		LastSeenAt: now,
	}}, []ServiceRecord{{
		ID:         "linux_native/delete-me/web",
		ProjectID:  "linux_native/delete-me",
		Name:       "web",
		ImageRef:   "nginx",
		LastSeenAt: now,
	}}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}

	if err := repo.Delete(ctx, "linux_native/delete-me"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repo.Get(ctx, "linux_native/delete-me"); err == nil {
		t.Fatal("Get() after Delete succeeded, want missing project")
	}
	services, err := repo.ListServices(ctx, "linux_native/delete-me")
	if err != nil {
		t.Fatalf("ListServices() after Delete error = %v", err)
	}
	if len(services) != 0 {
		t.Fatalf("services after Delete = %#v", services)
	}
	if err := repo.Delete(ctx, "linux_native/delete-me"); err != sql.ErrNoRows {
		t.Fatalf("Delete(missing) error = %v, want sql.ErrNoRows", err)
	}
}

func TestProjectRepositoryForgetSkipsDetectedSnapshotsUntilImported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 15, 11, 10, 0, 0, time.UTC)
	project := ProjectRecord{
		ID:          "windows_wsl_ubuntu/cairn-test-web",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
		Name:        "cairn-test-web",
		Source:      ProjectSourceLabels,
		LastSeenAt:  now,
	}
	service := ServiceRecord{
		ID:         "windows_wsl_ubuntu/cairn-test-web/web",
		ProjectID:  project.ID,
		Name:       "web",
		ImageRef:   "nginx",
		LastSeenAt: now,
	}

	if err := repo.SaveSnapshot(ctx, project.ProviderID, []ProjectRecord{project}, []ServiceRecord{service}, now, time.Time{}); err != nil {
		t.Fatalf("seed SaveSnapshot() error = %v", err)
	}
	if err := repo.Forget(ctx, project, now.Add(time.Minute)); err != nil {
		t.Fatalf("Forget() error = %v", err)
	}
	if err := repo.Delete(ctx, project.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, project.ProviderID, []ProjectRecord{project}, []ServiceRecord{service}, now.Add(2*time.Minute), time.Time{}); err != nil {
		t.Fatalf("forgotten SaveSnapshot() error = %v", err)
	}
	if _, err := repo.Get(ctx, project.ID); err == nil {
		t.Fatal("forgotten detected project was saved again")
	}

	project.Source = ProjectSourceImported
	if err := repo.Unforget(ctx, project.ProviderID, project.ContextName, project.Name, project.ID); err != nil {
		t.Fatalf("Unforget() error = %v", err)
	}
	if err := repo.UpsertImported(ctx, project); err != nil {
		t.Fatalf("UpsertImported() error = %v", err)
	}
	if _, err := repo.Get(ctx, project.ID); err != nil {
		t.Fatalf("imported project was not restored: %v", err)
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
		t.Fatalf("seed linux provider: %v", err)
	}
	if err := db.Providers().Upsert(ctx, ProviderRecord{
		ID:          "windows_wsl_ubuntu",
		Type:        "windows_wsl_ubuntu",
		Platform:    "windows",
		DisplayName: "Windows WSL Ubuntu",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed windows provider: %v", err)
	}
	return db
}
