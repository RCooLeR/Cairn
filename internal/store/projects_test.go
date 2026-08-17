package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestProjectRepositorySnapshotPreservesPinnedAndReplacesServices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 13, 4, 0, 0, 0, time.UTC)

	if err := repo.UpsertImported(ctx, ProjectRecord{
		ID:          "linux_native/demo",
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "demo",
		Source:      ProjectSourceImported,
		Pinned:      true,
		LastSeenAt:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("UpsertImported() error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []ProjectRecord{{
		ID:          "linux_native/demo",
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "demo",
		Source:      ProjectSourceLabels,
		Status:      models.ProjectStatusRunning,
		Health:      models.HealthStatusHealthy,
		LastSeenAt:  now,
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

	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []ProjectRecord{{
		ID:          "linux_native/demo",
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "demo",
		Source:      ProjectSourceLabels,
		Status:      models.ProjectStatusStopped,
		LastSeenAt:  now.Add(time.Minute),
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
		ID:          "linux_native/partial",
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "partial",
		LastSeenAt:  now,
	}
	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []ProjectRecord{project}, []ServiceRecord{{
		ID:         "linux_native/partial/api",
		ProjectID:  "linux_native/partial",
		Name:       "api",
		ImageRef:   "nginx:alpine",
		LastSeenAt: now,
	}}, now, time.Time{}); err != nil {
		t.Fatalf("seed SaveSnapshot() error = %v", err)
	}

	project.LastSeenAt = now.Add(time.Minute)
	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []ProjectRecord{project}, nil, project.LastSeenAt, time.Time{}); err != nil {
		t.Fatalf("partial SaveSnapshot() error = %v", err)
	}
	services, err := repo.ListServices(ctx, "linux_native/partial")
	if err != nil {
		t.Fatalf("ListServices() after partial snapshot error = %v", err)
	}
	if len(services) != 1 || services[0].Name != "api" {
		t.Fatalf("services after partial snapshot = %#v", services)
	}

	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []ProjectRecord{project}, []ServiceRecord{}, project.LastSeenAt, time.Time{}); err != nil {
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
		{ID: "linux_native/web", ProviderID: "linux_native", ContextName: "default", Name: "web", LastSeenAt: now},
		{ID: "linux_native/worker", ProviderID: "linux_native", ContextName: "default", Name: "worker", LastSeenAt: now},
	}
	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), projects, []ServiceRecord{
		{ID: "linux_native/web/api", ProjectID: "linux_native/web", Name: "api", ImageRef: "nginx", LastSeenAt: now},
		{ID: "linux_native/worker/job", ProjectID: "linux_native/worker", Name: "job", ImageRef: "busybox", LastSeenAt: now},
	}, now, time.Time{}); err != nil {
		t.Fatalf("seed SaveSnapshot() error = %v", err)
	}

	next := now.Add(time.Minute)
	projects[0].LastSeenAt = next
	projects[1].LastSeenAt = next
	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), projects, []ServiceRecord{
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

func TestProjectRepositoryDetectedSnapshotClearsServicesForEveryIncomingProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	scope := runtimescope.Must("linux_native", "default")
	now := time.Date(2026, 7, 24, 11, 0, 0, 0, time.UTC)
	projects := []ProjectRecord{
		{ID: "linux_native/web", ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), Name: "web", LastSeenAt: now},
		{ID: "linux_native/worker", ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), Name: "worker", LastSeenAt: now},
	}
	seedServices := []ServiceRecord{
		{ID: "linux_native/web/api", ProjectID: "linux_native/web", Name: "api", ImageRef: "nginx:old", LastSeenAt: now},
		{ID: "linux_native/worker/job", ProjectID: "linux_native/worker", Name: "job", ImageRef: "busybox:old", LastSeenAt: now},
	}
	if err := repo.SaveSnapshot(ctx, scope, projects, seedServices, now, time.Time{}); err != nil {
		t.Fatalf("seed SaveSnapshot() error = %v", err)
	}

	next := now.Add(time.Minute)
	for i := range projects {
		projects[i].LastSeenAt = next
	}
	skipped, err := repo.SaveDetectedSnapshot(ctx, scope, projects, []ServiceRecord{{
		ID:         "linux_native/worker/job-v2",
		ProjectID:  "linux_native/worker",
		Name:       "job-v2",
		ImageRef:   "busybox:new",
		LastSeenAt: next,
	}}, next, time.Time{})
	if err != nil {
		t.Fatalf("SaveDetectedSnapshot(mixed services) error = %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("SaveDetectedSnapshot(mixed services) skipped = %#v, want none", skipped)
	}

	webServices, err := repo.ListServices(ctx, "linux_native/web")
	if err != nil {
		t.Fatalf("ListServices(web) error = %v", err)
	}
	if len(webServices) != 0 {
		t.Fatalf("web services = %#v, want empty full-snapshot result", webServices)
	}
	workerServices, err := repo.ListServices(ctx, "linux_native/worker")
	if err != nil {
		t.Fatalf("ListServices(worker) error = %v", err)
	}
	if len(workerServices) != 1 || workerServices[0].Name != "job-v2" {
		t.Fatalf("worker services = %#v, want only job-v2", workerServices)
	}

	reseedAt := next.Add(time.Minute)
	for i := range projects {
		projects[i].LastSeenAt = reseedAt
	}
	for i := range seedServices {
		seedServices[i].LastSeenAt = reseedAt
	}
	if err := repo.SaveSnapshot(ctx, scope, projects, seedServices, reseedAt, time.Time{}); err != nil {
		t.Fatalf("reseed SaveSnapshot() error = %v", err)
	}
	skipped, err = repo.SaveDetectedSnapshot(ctx, scope, projects, nil, reseedAt.Add(time.Minute), time.Time{})
	if err != nil {
		t.Fatalf("SaveDetectedSnapshot(no services) error = %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("SaveDetectedSnapshot(no services) skipped = %#v, want none", skipped)
	}
	for _, project := range projects {
		services, err := repo.ListServices(ctx, project.ID)
		if err != nil {
			t.Fatalf("ListServices(%s) after empty full snapshot error = %v", project.ID, err)
		}
		if len(services) != 0 {
			t.Fatalf("services for %s = %#v, want empty", project.ID, services)
		}
	}
}

func TestProjectRepositoryDeletesOnlyStaleDetectedProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 13, 5, 0, 0, 0, time.UTC)
	old := now.Add(-25 * time.Hour)

	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []ProjectRecord{
		{ID: "linux_native/old", ProviderID: "linux_native", ContextName: "default", Name: "old", Source: ProjectSourceLabels, LastSeenAt: old},
		{ID: "linux_native/imported", ProviderID: "linux_native", ContextName: "default", Name: "imported", Source: ProjectSourceImported, LastSeenAt: old},
	}, nil, old, time.Time{}); err != nil {
		t.Fatalf("seed SaveSnapshot() error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), nil, nil, now, now.Add(-24*time.Hour)); err != nil {
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

	if err := repo.SaveSnapshot(ctx, runtimescope.Must("windows_wsl_ubuntu", "wsl:Ubuntu"), []ProjectRecord{{
		ID:          "windows_wsl_ubuntu/ubuntu-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:Ubuntu",
		Name:        "ubuntu-app",
		LastSeenAt:  now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot windows Ubuntu error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, runtimescope.Must("windows_wsl_ubuntu", "wsl:cairn-dev"), []ProjectRecord{{
		ID:          "windows_wsl_ubuntu/cairn-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
		Name:        "cairn-app",
		Source:      ProjectSourceImported,
		LastSeenAt:  now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot windows error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []ProjectRecord{{
		ID:          "linux_native/app",
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "app",
		LastSeenAt:  now,
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

	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []ProjectRecord{
		{ID: "linux_native/web", ProviderID: "linux_native", ContextName: "default", Name: "web", LastSeenAt: now},
		{ID: "linux_native/worker", ProviderID: "linux_native", ContextName: "default", Name: "worker", LastSeenAt: now},
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

	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []ProjectRecord{{
		ID:          "linux_native/delete-me",
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "delete-me",
		LastSeenAt:  now,
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

func TestProjectRepositoryForgetAndDeletePersistsTombstone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 7, 24, 11, 45, 0, 0, time.UTC)
	project := ProjectRecord{
		ID:          "linux_native/forget-and-delete",
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "forget-and-delete",
		LastSeenAt:  now,
	}
	if err := repo.SaveSnapshot(ctx, runtimescope.Must(project.ProviderID, project.ContextName), []ProjectRecord{project}, []ServiceRecord{{
		ID:         project.ID + "/web",
		ProjectID:  project.ID,
		Name:       "web",
		ImageRef:   "nginx",
		LastSeenAt: now,
	}}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}

	forgottenAt := now.Add(time.Minute)
	if err := repo.ForgetAndDelete(ctx, project.ID, forgottenAt); err != nil {
		t.Fatalf("ForgetAndDelete() error = %v", err)
	}
	if _, err := repo.Get(ctx, project.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get() after ForgetAndDelete() error = %v, want sql.ErrNoRows", err)
	}
	if got := queryInt64(t, ctx, db, `SELECT COUNT(*) FROM services WHERE project_id = ?`, project.ID); got != 0 {
		t.Fatalf("services after ForgetAndDelete() = %d, want 0", got)
	}
	var storedProjectID string
	var storedAt string
	if err := db.writer.QueryRowContext(ctx, `
		SELECT project_id, forgotten_at
		FROM forgotten_projects
		WHERE provider_id = ? AND context_name = ? AND name = ?
	`, project.ProviderID, project.ContextName, project.Name).Scan(&storedProjectID, &storedAt); err != nil {
		t.Fatalf("read forgotten tombstone: %v", err)
	}
	if storedProjectID != project.ID || !parseStoreTime(storedAt).Equal(forgottenAt) {
		t.Fatalf("forgotten tombstone = project %q at %q, want %q at %s", storedProjectID, storedAt, project.ID, forgottenAt)
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

	if err := repo.SaveSnapshot(ctx, runtimescope.Must(project.ProviderID, project.ContextName), []ProjectRecord{project}, []ServiceRecord{service}, now, time.Time{}); err != nil {
		t.Fatalf("seed SaveSnapshot() error = %v", err)
	}
	scope := runtimescope.Must(project.ProviderID, project.ContextName)
	if err := repo.ForgetAndDeleteInScope(ctx, scope, project.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("ForgetAndDeleteInScope() error = %v", err)
	}
	skipped, err := repo.SaveDetectedSnapshot(ctx, scope, []ProjectRecord{project}, []ServiceRecord{service}, now.Add(2*time.Minute), time.Time{})
	if err != nil {
		t.Fatalf("forgotten SaveDetectedSnapshot() error = %v", err)
	}
	if len(skipped) != 1 || skipped[0] != project.ID {
		t.Fatalf("forgotten skipped IDs = %#v, want [%q]", skipped, project.ID)
	}
	if _, err := repo.Get(ctx, project.ID); err == nil {
		t.Fatal("forgotten detected project was saved again")
	}

	project.Source = ProjectSourceImported
	if err := repo.UpsertImported(ctx, project); err != nil {
		t.Fatalf("UpsertImported() error = %v", err)
	}
	if _, err := repo.Get(ctx, project.ID); err != nil {
		t.Fatalf("imported project was not restored: %v", err)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*)
		FROM forgotten_projects
		WHERE provider_id = ? AND context_name = ? AND name = ?
	`, project.ProviderID, project.ContextName, project.Name); got != 0 {
		t.Fatalf("forgotten tombstones after UpsertImported() = %d, want 0", got)
	}
}

func TestProjectRepositoryImportedSnapshotRollsBackUnforgetOnSaveFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "default")
	project := ProjectRecord{
		ID:          "linux_native/import-rollback",
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		Name:        "import-rollback",
		Source:      ProjectSourceImported,
		LastSeenAt:  now,
	}
	if err := repo.Forget(ctx, project, now.Add(-time.Minute)); err != nil {
		t.Fatalf("Forget() error = %v", err)
	}
	if _, err := db.writer.ExecContext(ctx, `
		CREATE TRIGGER fail_import_snapshot
		BEFORE INSERT ON projects
		WHEN NEW.id = 'linux_native/import-rollback'
		BEGIN
			SELECT RAISE(ABORT, 'forced import snapshot failure');
		END
	`); err != nil {
		t.Fatalf("create import failure trigger: %v", err)
	}

	err := repo.SaveImportedSnapshotInScope(ctx, scope, project, []ServiceRecord{{
		ID:         project.ID + "/web",
		ProjectID:  project.ID,
		Name:       "web",
		ImageRef:   "nginx",
		LastSeenAt: now,
	}}, now)
	if err == nil {
		t.Fatal("SaveImportedSnapshotInScope() succeeded, want forced failure")
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*)
		FROM forgotten_projects
		WHERE provider_id = ? AND context_name = ? AND name = ? AND project_id = ?
	`, project.ProviderID, project.ContextName, project.Name, project.ID); got != 1 {
		t.Fatalf("forgotten tombstones after failed import = %d, want 1", got)
	}
	if _, err := repo.Get(ctx, project.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get() after failed import error = %v, want sql.ErrNoRows", err)
	}
	if got := queryInt64(t, ctx, db, `SELECT COUNT(*) FROM services WHERE project_id = ?`, project.ID); got != 0 {
		t.Fatalf("services after failed import = %d, want 0", got)
	}
}

func TestProjectRepositoryForgetAndDeleteRollsBackTombstoneOnDeleteFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "default")
	project := ProjectRecord{
		ID:          "linux_native/remove-rollback",
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		Name:        "remove-rollback",
		LastSeenAt:  now,
	}
	service := ServiceRecord{
		ID:         project.ID + "/web",
		ProjectID:  project.ID,
		Name:       "web",
		ImageRef:   "nginx",
		LastSeenAt: now,
	}
	if err := repo.SaveSnapshot(ctx, scope, []ProjectRecord{project}, []ServiceRecord{service}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	if _, err := db.writer.ExecContext(ctx, `
		CREATE TRIGGER fail_project_delete
		BEFORE DELETE ON projects
		WHEN OLD.id = 'linux_native/remove-rollback'
		BEGIN
			SELECT RAISE(ABORT, 'forced project delete failure');
		END
	`); err != nil {
		t.Fatalf("create delete failure trigger: %v", err)
	}

	err := repo.ForgetAndDeleteInScope(ctx, scope, project.ID, now.Add(time.Minute))
	if err == nil {
		t.Fatal("ForgetAndDeleteInScope() succeeded, want forced failure")
	}
	if _, err := repo.GetInScope(ctx, scope, project.ID); err != nil {
		t.Fatalf("project was not restored after rollback: %v", err)
	}
	services, err := repo.ListServices(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListServices() after rollback error = %v", err)
	}
	if len(services) != 1 || services[0].ID != service.ID {
		t.Fatalf("services after rollback = %#v, want original service", services)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*)
		FROM forgotten_projects
		WHERE provider_id = ? AND context_name = ? AND name = ?
	`, project.ProviderID, project.ContextName, project.Name); got != 0 {
		t.Fatalf("forgotten tombstones after failed removal = %d, want 0", got)
	}

	wrongScope := runtimescope.Must(scope.ProviderID(), "socket:other")
	if err := repo.ForgetAndDeleteInScope(ctx, wrongScope, project.ID, now.Add(2*time.Minute)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ForgetAndDeleteInScope(wrong scope) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := repo.GetInScope(ctx, scope, project.ID); err != nil {
		t.Fatalf("wrong-scope removal mutated project: %v", err)
	}
}

func TestProjectRepositoryRemovalDoesNotReattachMutableStateOnImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	projects := db.Projects()
	scope := runtimescope.Must("linux_native", "default")
	now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	project := ProjectRecord{
		ID:          "linux_native/lifecycle",
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		Name:        "lifecycle",
		Source:      ProjectSourceImported,
		LastSeenAt:  now,
	}
	service := ServiceRecord{
		ID:         project.ID + "/web",
		ProjectID:  project.ID,
		Name:       "web",
		ImageRef:   "example/web:latest",
		LastSeenAt: now,
	}
	if err := projects.SaveSnapshot(ctx, scope, []ProjectRecord{project}, []ServiceRecord{service}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}

	if err := db.Lineage().ReplaceProjectInScope(ctx, scope, project.ID, []LineageRecord{{
		ProviderID:      scope.ProviderID(),
		ContextName:     scope.ContextName(),
		ProjectID:       project.ID,
		ServiceID:       service.ID,
		ServiceName:     service.Name,
		ServiceImageRef: service.ImageRef,
		DockerfilePath:  "Dockerfile",
		Source:          models.LineageSourceComposeDockerfile,
		Confidence:      models.ConfidenceMedium,
		DiscoveredAt:    now,
		UpdatedAt:       now,
		BaseRefs: []BaseImageRefRecord{{
			Name:             "alpine",
			Tag:              "3.20",
			ImageRef:         "alpine:3.20",
			IsFinalStageBase: true,
			Status:           models.UpdateStatusUnknown,
		}},
	}}); err != nil {
		t.Fatalf("ReplaceProjectInScope() error = %v", err)
	}
	lineage, err := db.Lineage().ListProjectInScope(ctx, scope, project.ID)
	if err != nil || len(lineage) != 1 || len(lineage[0].BaseRefs) != 1 {
		t.Fatalf("ListProjectInScope() = %#v, %v", lineage, err)
	}

	run, err := db.Updates().BeginServiceCheckRunInScope(ctx, scope, project.ID, service.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BeginServiceCheckRunInScope() error = %v", err)
	}
	checks, accepted, err := db.Updates().PublishCheckRunInScope(ctx, scope, run.ID, []UpdateCheckRecord{{
		ProviderID:     scope.ProviderID(),
		ContextName:    scope.ContextName(),
		ProjectID:      project.ID,
		ServiceID:      service.ID,
		Kind:           models.UpdateKindServiceImage,
		ImageRef:       service.ImageRef,
		LineageID:      lineage[0].ID,
		BaseImageRefID: lineage[0].BaseRefs[0].ID,
		Status:         models.UpdateStatusServiceImageUpdateAvailable,
	}}, now.Add(2*time.Minute))
	if err != nil || !accepted || len(checks) != 1 {
		t.Fatalf("PublishCheckRunInScope() = %#v, %v, %v", checks, accepted, err)
	}
	if err := db.Updates().IgnoreCheckInScope(ctx, scope, checks[0].ID, "old project rule", now.Add(3*time.Minute)); err != nil {
		t.Fatalf("IgnoreCheckInScope() error = %v", err)
	}
	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO ignored_updates (
			provider_id, context_name, image_ref, update_kind, reason, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, scope.ProviderID(), scope.ContextName(), "global/image:latest", string(models.UpdateKindServiceImage), "global rule", formatTime(now)); err != nil {
		t.Fatalf("insert global ignored rule: %v", err)
	}

	completedHistoryID, err := db.Updates().InsertHistoryInScope(ctx, scope, UpdateHistoryRecord{
		ProviderID:     scope.ProviderID(),
		ContextName:    scope.ContextName(),
		ProjectID:      project.ID,
		ServiceID:      service.ID,
		UpdateKind:     models.UpdateKindServiceImage,
		ImageRef:       service.ImageRef,
		Result:         "success",
		RollbackStatus: "available",
		StartedAt:      now.Add(-2 * time.Minute),
		FinishedAt:     now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertHistoryInScope(completed) error = %v", err)
	}
	activeHistoryID, err := db.Updates().InsertHistoryInScope(ctx, scope, UpdateHistoryRecord{
		ProviderID:     scope.ProviderID(),
		ContextName:    scope.ContextName(),
		ProjectID:      project.ID,
		ServiceID:      service.ID,
		UpdateKind:     models.UpdateKindServiceImage,
		ImageRef:       service.ImageRef,
		Result:         "started",
		RollbackStatus: "available",
		StartedAt:      now,
	})
	if err != nil {
		t.Fatalf("InsertHistoryInScope(active) error = %v", err)
	}
	if err := db.Metrics().InsertBatch(ctx, []MetricsSampleRecord{{
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		ProjectID:   project.ID,
		ServiceID:   service.ID,
		ContainerID: "container-lifecycle",
		CPUPercent:  5,
		Resolution:  MetricsResolutionRaw,
		SampledAt:   now,
	}}); err != nil {
		t.Fatalf("InsertBatch(metrics) error = %v", err)
	}
	if err := db.Backups().Insert(ctx, BackupRecord{
		ID:         "backup-lifecycle",
		ProviderID: scope.ProviderID(),
		ProjectID:  project.ID,
		VolumeName: "lifecycle-data",
		BackupPath: "lifecycle.tar",
		Result:     "success",
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("Insert(backup) error = %v", err)
	}
	auditID, err := db.Audit().Insert(ctx, AuditRecord{
		Action:     "project.lifecycle",
		TargetType: "project",
		TargetID:   project.ID,
		ProviderID: scope.ProviderID(),
		ProjectID:  project.ID,
		Status:     "success",
		CreatedAt:  now,
	})
	if err != nil {
		t.Fatalf("Insert(audit) error = %v", err)
	}

	if err := projects.ForgetAndDeleteInScope(ctx, scope, project.ID, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("ForgetAndDeleteInScope() error = %v", err)
	}
	for _, table := range []string{
		"services",
		"image_lineage",
		"base_image_refs",
		"image_update_checks",
		"image_update_check_runs",
		"image_update_check_heads",
	} {
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
		if got := queryInt64(t, ctx, db, query); got != 0 {
			t.Fatalf("%s rows after removal = %d, want 0", table, got)
		}
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM ignored_updates
		WHERE provider_id = ? AND context_name = ? AND project_id = ?
	`, scope.ProviderID(), scope.ContextName(), project.ID); got != 0 {
		t.Fatalf("project ignored rules after removal = %d, want 0", got)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM ignored_updates
		WHERE provider_id = ? AND context_name = ? AND project_id IS NULL
	`, scope.ProviderID(), scope.ContextName()); got != 1 {
		t.Fatalf("global ignored rules after removal = %d, want 1", got)
	}
	completedHistory, err := db.Updates().GetHistoryInScope(ctx, scope, completedHistoryID)
	if err != nil {
		t.Fatalf("GetHistoryInScope(completed) error = %v", err)
	}
	if completedHistory.ProjectID != project.ID || completedHistory.RollbackStatus != "unavailable" {
		t.Fatalf("completed history after removal = %#v", completedHistory)
	}
	activeHistory, err := db.Updates().GetHistoryInScope(ctx, scope, activeHistoryID)
	if err != nil {
		t.Fatalf("GetHistoryInScope(active) error = %v", err)
	}
	if activeHistory.ProjectID != "" || activeHistory.ServiceID != "" {
		t.Fatalf("active history after removal = %#v, want detached target", activeHistory)
	}
	if activeHistory.RollbackStatus != "unavailable" {
		t.Fatalf("active history rollback status after removal = %q, want unavailable", activeHistory.RollbackStatus)
	}
	if err := db.Updates().FinishHistoryInScope(ctx, scope, activeHistoryID, UpdateHistoryRecord{
		Result:         "success",
		RollbackStatus: "available",
		FinishedAt:     now.Add(5 * time.Minute),
	}); err != nil {
		t.Fatalf("FinishHistoryInScope(late finisher) error = %v", err)
	}
	activeHistory, err = db.Updates().GetHistoryInScope(ctx, scope, activeHistoryID)
	if err != nil {
		t.Fatalf("GetHistoryInScope(late-finished active) error = %v", err)
	}
	if activeHistory.ProjectID != "" || activeHistory.ServiceID != "" || activeHistory.RollbackStatus != "unavailable" {
		t.Fatalf("late-finished history after removal = %#v, want detached and unavailable", activeHistory)
	}
	if _, err := db.Updates().InsertHistoryInScope(ctx, scope, UpdateHistoryRecord{
		ProviderID:     scope.ProviderID(),
		ContextName:    scope.ContextName(),
		ProjectID:      project.ID,
		ServiceID:      service.ID,
		UpdateKind:     models.UpdateKindServiceImage,
		ImageRef:       service.ImageRef,
		Result:         "started",
		RollbackStatus: "available",
		StartedAt:      now.Add(5 * time.Minute),
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("InsertHistoryInScope(after removal) error = %v, want sql.ErrNoRows", err)
	}
	if got := queryInt64(t, ctx, db, `SELECT COUNT(*) FROM metrics_samples WHERE project_id = ?`, project.ID); got != 1 {
		t.Fatalf("metrics history after removal = %d, want 1", got)
	}
	if got := queryInt64(t, ctx, db, `SELECT COUNT(*) FROM backups WHERE id = ?`, "backup-lifecycle"); got != 1 {
		t.Fatalf("backup history after removal = %d, want 1", got)
	}
	if got := queryInt64(t, ctx, db, `SELECT COUNT(*) FROM audit_log WHERE id = ?`, auditID); got != 1 {
		t.Fatalf("audit history after removal = %d, want 1", got)
	}

	project.LastSeenAt = now.Add(5 * time.Minute)
	service.LastSeenAt = project.LastSeenAt
	if err := projects.SaveImportedSnapshotInScope(ctx, scope, project, []ServiceRecord{service}, project.LastSeenAt); err != nil {
		t.Fatalf("SaveImportedSnapshotInScope() error = %v", err)
	}
	reimportedLineage, err := db.Lineage().ListProjectInScope(ctx, scope, project.ID)
	if err != nil {
		t.Fatalf("ListProjectInScope(reimported) error = %v", err)
	}
	if len(reimportedLineage) != 0 {
		t.Fatalf("reimported lineage = %#v, want no stale rows", reimportedLineage)
	}
	history, err := db.Updates().ListHistoryInScope(ctx, scope, models.UpdateHistoryFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListHistoryInScope(reimported) error = %v", err)
	}
	if len(history) != 1 || history[0].ID != completedHistoryID || history[0].RollbackStatus != "unavailable" {
		t.Fatalf("reimported intentional history = %#v", history)
	}

	newRun, err := db.Updates().BeginServiceCheckRunInScope(ctx, scope, project.ID, service.ID, now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("BeginServiceCheckRunInScope(reimported) error = %v", err)
	}
	newChecks, accepted, err := db.Updates().PublishCheckRunInScope(ctx, scope, newRun.ID, []UpdateCheckRecord{{
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		ProjectID:   project.ID,
		ServiceID:   service.ID,
		Kind:        models.UpdateKindServiceImage,
		ImageRef:    service.ImageRef,
		Status:      models.UpdateStatusServiceImageUpdateAvailable,
	}}, now.Add(7*time.Minute))
	if err != nil || !accepted || len(newChecks) != 1 {
		t.Fatalf("PublishCheckRunInScope(reimported) = %#v, %v, %v", newChecks, accepted, err)
	}
	current, err := db.Updates().ListCurrentInScope(ctx, scope, models.UpdateFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListCurrentInScope(reimported) error = %v", err)
	}
	if len(current) != 1 || current[0].Status != models.UpdateStatusServiceImageUpdateAvailable {
		t.Fatalf("reimported current updates = %#v, want unignored fresh update", current)
	}
}

func TestProjectRepositoryStaleCleanupDoesNotReattachMutableStateOnRedetection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	projects := db.Projects()
	scope := runtimescope.Must("linux_native", "default")
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	project := ProjectRecord{
		ID:          "linux_native/stale-lifecycle",
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		Name:        "stale-lifecycle",
		LastSeenAt:  old,
	}
	service := ServiceRecord{
		ID:         project.ID + "/web",
		ProjectID:  project.ID,
		Name:       "web",
		ImageRef:   "example/stale:latest",
		LastSeenAt: old,
	}
	if _, err := projects.SaveDetectedSnapshot(ctx, scope, []ProjectRecord{project}, []ServiceRecord{service}, old, time.Time{}); err != nil {
		t.Fatalf("SaveDetectedSnapshot(seed) error = %v", err)
	}
	if err := db.Lineage().ReplaceProjectInScope(ctx, scope, project.ID, []LineageRecord{{
		ProviderID:      scope.ProviderID(),
		ContextName:     scope.ContextName(),
		ProjectID:       project.ID,
		ServiceID:       service.ID,
		ServiceName:     service.Name,
		ServiceImageRef: service.ImageRef,
		Source:          models.LineageSourceComposeDockerfile,
		Confidence:      models.ConfidenceMedium,
		DiscoveredAt:    old,
		UpdatedAt:       old,
	}}); err != nil {
		t.Fatalf("ReplaceProjectInScope() error = %v", err)
	}
	lineage, err := db.Lineage().ListProjectInScope(ctx, scope, project.ID)
	if err != nil || len(lineage) != 1 {
		t.Fatalf("ListProjectInScope() = %#v, %v", lineage, err)
	}
	run, err := db.Updates().BeginServiceCheckRunInScope(ctx, scope, project.ID, service.ID, old.Add(time.Minute))
	if err != nil {
		t.Fatalf("BeginServiceCheckRunInScope() error = %v", err)
	}
	checks, accepted, err := db.Updates().PublishCheckRunInScope(ctx, scope, run.ID, []UpdateCheckRecord{{
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		ProjectID:   project.ID,
		ServiceID:   service.ID,
		Kind:        models.UpdateKindServiceImage,
		ImageRef:    service.ImageRef,
		LineageID:   lineage[0].ID,
		Status:      models.UpdateStatusServiceImageUpdateAvailable,
	}}, old.Add(2*time.Minute))
	if err != nil || !accepted || len(checks) != 1 {
		t.Fatalf("PublishCheckRunInScope() = %#v, %v, %v", checks, accepted, err)
	}
	if err := db.Updates().IgnoreCheckInScope(ctx, scope, checks[0].ID, "stale project rule", old.Add(3*time.Minute)); err != nil {
		t.Fatalf("IgnoreCheckInScope() error = %v", err)
	}

	if _, err := projects.SaveDetectedSnapshot(ctx, scope, nil, nil, now, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("SaveDetectedSnapshot(prune) error = %v", err)
	}
	if _, err := projects.GetInScope(ctx, scope, project.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetInScope() after stale prune error = %v, want sql.ErrNoRows", err)
	}
	for _, table := range []string{
		"services",
		"image_lineage",
		"image_update_checks",
		"image_update_check_runs",
		"image_update_check_heads",
		"ignored_updates",
	} {
		if got := queryInt64(t, ctx, db, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)); got != 0 {
			t.Fatalf("%s rows after stale prune = %d, want 0", table, got)
		}
	}

	project.LastSeenAt = now
	service.LastSeenAt = now
	if _, err := projects.SaveDetectedSnapshot(ctx, scope, []ProjectRecord{project}, []ServiceRecord{service}, now, time.Time{}); err != nil {
		t.Fatalf("SaveDetectedSnapshot(redetect) error = %v", err)
	}
	redetectedLineage, err := db.Lineage().ListProjectInScope(ctx, scope, project.ID)
	if err != nil {
		t.Fatalf("ListProjectInScope(redetected) error = %v", err)
	}
	if len(redetectedLineage) != 0 {
		t.Fatalf("redetected lineage = %#v, want no stale rows", redetectedLineage)
	}
	current, err := db.Updates().ListCurrentInScope(ctx, scope, models.UpdateFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListCurrentInScope(redetected) error = %v", err)
	}
	if len(current) != 0 {
		t.Fatalf("redetected current updates = %#v, want none", current)
	}

	newRun, err := db.Updates().BeginServiceCheckRunInScope(ctx, scope, project.ID, service.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BeginServiceCheckRunInScope(redetected) error = %v", err)
	}
	newChecks, accepted, err := db.Updates().PublishCheckRunInScope(ctx, scope, newRun.ID, []UpdateCheckRecord{{
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		ProjectID:   project.ID,
		ServiceID:   service.ID,
		Kind:        models.UpdateKindServiceImage,
		ImageRef:    service.ImageRef,
		Status:      models.UpdateStatusServiceImageUpdateAvailable,
	}}, now.Add(2*time.Minute))
	if err != nil || !accepted || len(newChecks) != 1 {
		t.Fatalf("PublishCheckRunInScope(redetected) = %#v, %v, %v", newChecks, accepted, err)
	}
	if newChecks[0].Status != models.UpdateStatusServiceImageUpdateAvailable {
		t.Fatalf("redetected update status = %q, want fresh unignored update", newChecks[0].Status)
	}
}

func TestProjectRepositoryStaleCleanupIsExactScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 16, 1, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	scopeA := runtimescope.Must("linux_native", "socket:a")
	scopeB := runtimescope.Must("linux_native", "socket:b")
	projectA := ProjectRecord{ID: "linux_native/a-old", ProviderID: "linux_native", ContextName: "socket:a", Name: "a-old", LastSeenAt: old}
	projectB := ProjectRecord{ID: "linux_native/b-old", ProviderID: "linux_native", ContextName: "socket:b", Name: "b-old", LastSeenAt: old}
	if err := repo.SaveSnapshot(ctx, scopeA, []ProjectRecord{projectA}, nil, old, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(A) error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, scopeB, []ProjectRecord{projectB}, nil, old, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(B) error = %v", err)
	}
	if err := repo.SaveSnapshot(ctx, scopeA, nil, nil, now, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("prune A error = %v", err)
	}
	if _, err := repo.Get(ctx, projectA.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("A project after prune error = %v, want missing", err)
	}
	if got, err := repo.GetInScope(ctx, scopeB, projectB.ID); err != nil || got.ID != projectB.ID {
		t.Fatalf("B project was pruned by A: %#v, %v", got, err)
	}
}

func TestProjectRepositoryRejectsLegacyGlobalIDCollisionAcrossScopes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 16, 2, 0, 0, 0, time.UTC)
	scopeA := runtimescope.Must("linux_native", "socket:a")
	scopeB := runtimescope.Must("linux_native", "socket:b")
	projectID := "linux_native/shared-name"
	projectA := ProjectRecord{ID: projectID, ProviderID: "linux_native", ContextName: "socket:a", Name: "shared-name", LastSeenAt: now}
	if err := repo.SaveSnapshot(ctx, scopeA, []ProjectRecord{projectA}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(A) error = %v", err)
	}
	projectB := projectA
	projectB.ContextName = "socket:b"
	projectB.WorkingDir = "/foreign"
	if err := repo.SaveSnapshot(ctx, scopeB, []ProjectRecord{projectB}, nil, now.Add(time.Minute), time.Time{}); !IsProjectScopeConflict(err) {
		t.Fatalf("SaveSnapshot(B collision) error = %v, want scope conflict", err)
	}
	got, err := repo.GetInScope(ctx, scopeA, projectID)
	if err != nil {
		t.Fatalf("GetInScope(A) error = %v", err)
	}
	if got.ContextName != "socket:a" || got.WorkingDir != "" {
		t.Fatalf("A project changed after collision = %#v", got)
	}
}

func TestProjectRepositoryRejectsForeignServiceReplacementWithoutDeletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC)
	scopeB := runtimescope.Must("linux_native", "socket:b")
	projectB := ProjectRecord{ID: "linux_native/b", ProviderID: "linux_native", ContextName: "socket:b", Name: "b", LastSeenAt: now}
	serviceB := ServiceRecord{ID: projectB.ID + "/web", ProjectID: projectB.ID, Name: "web", ImageRef: "nginx:old", LastSeenAt: now}
	if err := repo.SaveSnapshot(ctx, scopeB, []ProjectRecord{projectB}, []ServiceRecord{serviceB}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(B) error = %v", err)
	}
	foreignReplacement := serviceB
	foreignReplacement.ImageRef = "nginx:new"
	if err := repo.SaveSnapshot(ctx, runtimescope.Must("linux_native", "socket:a"), nil, []ServiceRecord{foreignReplacement}, now.Add(time.Minute), time.Time{}); !IsProjectScopeConflict(err) {
		t.Fatalf("foreign service replacement error = %v, want scope conflict", err)
	}
	services, err := repo.ListServices(ctx, projectB.ID)
	if err != nil {
		t.Fatalf("ListServices(B) error = %v", err)
	}
	if len(services) != 1 || services[0].ImageRef != "nginx:old" {
		t.Fatalf("foreign service replacement mutated B = %#v", services)
	}
}

func TestProjectRepositoryQuarantinesLegacyBlankContextRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 16, 4, 0, 0, 0, time.UTC)
	legacyID := "linux_native/legacy"
	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO projects (id, provider_id, context_name, name, status, health, source, pinned, last_seen_at, compose_files_json, metadata_json)
		VALUES (?, ?, '', ?, ?, ?, ?, 0, ?, '[]', '{}')
	`, legacyID, "linux_native", "legacy", string(models.ProjectStatusUnknown), string(models.HealthStatusUnknown), ProjectSourceLabels, formatTime(now)); err != nil {
		t.Fatalf("seed legacy blank-context row: %v", err)
	}
	scope := runtimescope.Must("linux_native", "unix:///var/run/docker.sock")
	if rows, err := repo.ListByScope(ctx, scope); err != nil || len(rows) != 0 {
		t.Fatalf("ListByScope() exposed blank-context row = %#v, %v", rows, err)
	}
	incoming := []ProjectRecord{
		{ID: legacyID, ProviderID: "linux_native", ContextName: scope.ContextName(), Name: "legacy", LastSeenAt: now},
		{ID: "linux_native/unrelated", ProviderID: "linux_native", ContextName: scope.ContextName(), Name: "unrelated", LastSeenAt: now},
	}
	if err := repo.SaveSnapshot(ctx, scope, incoming, nil, now, time.Time{}); !IsProjectScopeConflict(err) {
		t.Fatalf("SaveSnapshot() error = %v, want legacy scope conflict", err)
	}
	if _, err := repo.Get(ctx, "linux_native/unrelated"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("conflicting batch partially committed unrelated project: %v", err)
	}
	legacy, err := repo.Get(ctx, legacyID)
	if err != nil {
		t.Fatalf("Get(legacy) error = %v", err)
	}
	if legacy.ContextName != "" {
		t.Fatalf("legacy row was auto-claimed: %#v", legacy)
	}
}

func TestProjectRepositoryDetectedSnapshotSkipsLegacyCollisionAndCommitsUnrelatedProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Projects()
	now := time.Date(2026, 6, 16, 5, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	scope := runtimescope.Must("linux_native", "unix:///var/run/docker.sock")
	foreignScope := runtimescope.Must("linux_native", "socket:foreign")
	legacyID := "linux_native/legacy"
	unrelatedID := "linux_native/unrelated"
	staleID := "linux_native/stale"
	foreignStaleID := "linux_native/foreign-stale"

	if err := repo.SaveSnapshot(ctx, scope, []ProjectRecord{{
		ID: staleID, ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), Name: "stale", LastSeenAt: old,
	}}, nil, old, time.Time{}); err != nil {
		t.Fatalf("seed active-scope stale project: %v", err)
	}
	if err := repo.SaveSnapshot(ctx, foreignScope, []ProjectRecord{{
		ID: foreignStaleID, ProviderID: foreignScope.ProviderID(), ContextName: foreignScope.ContextName(), Name: "foreign-stale", LastSeenAt: old,
	}}, nil, old, time.Time{}); err != nil {
		t.Fatalf("seed foreign-scope stale project: %v", err)
	}
	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO projects (id, provider_id, context_name, name, working_dir, status, health, source, pinned, last_seen_at, compose_files_json, metadata_json)
		VALUES (?, ?, '', ?, ?, ?, ?, ?, 0, ?, '[]', '{}')
	`, legacyID, scope.ProviderID(), "legacy", "/legacy", string(models.ProjectStatusStopped), string(models.HealthStatusUnknown), ProjectSourceLabels, formatTime(old)); err != nil {
		t.Fatalf("seed legacy blank-context project: %v", err)
	}
	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO services (id, project_id, name, image_ref, status, health, replicas_running, replicas_total, metadata_json, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, 1, '{}', ?)
	`, legacyID+"/web", legacyID, "web", "nginx:legacy", string(models.ProjectStatusStopped), string(models.HealthStatusUnknown), formatTime(old)); err != nil {
		t.Fatalf("seed legacy service: %v", err)
	}

	projects := []ProjectRecord{
		{ID: legacyID, ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), Name: "legacy", WorkingDir: "/detected", LastSeenAt: now},
		{ID: unrelatedID, ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), Name: "unrelated", WorkingDir: "/unrelated", LastSeenAt: now},
	}
	services := []ServiceRecord{
		{ID: legacyID + "/web", ProjectID: legacyID, Name: "web", ImageRef: "nginx:detected", LastSeenAt: now},
		{ID: unrelatedID + "/api", ProjectID: unrelatedID, Name: "api", ImageRef: "cairn/api:latest", LastSeenAt: now},
	}
	skipped, err := repo.SaveDetectedSnapshot(ctx, scope, projects, services, now, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("SaveDetectedSnapshot() error = %v", err)
	}
	if len(skipped) != 1 || skipped[0] != legacyID {
		t.Fatalf("skipped IDs = %#v, want [%q]", skipped, legacyID)
	}

	legacy, err := repo.Get(ctx, legacyID)
	if err != nil {
		t.Fatalf("Get(legacy) error = %v", err)
	}
	if legacy.ContextName != "" || legacy.WorkingDir != "/legacy" || !legacy.LastSeenAt.Equal(old) {
		t.Fatalf("legacy project was mutated or claimed: %#v", legacy)
	}
	legacyServices, err := repo.ListServices(ctx, legacyID)
	if err != nil {
		t.Fatalf("ListServices(legacy) error = %v", err)
	}
	if len(legacyServices) != 1 || legacyServices[0].ImageRef != "nginx:legacy" {
		t.Fatalf("legacy services were overwritten: %#v", legacyServices)
	}

	unrelated, err := repo.GetInScope(ctx, scope, unrelatedID)
	if err != nil {
		t.Fatalf("GetInScope(unrelated) error = %v", err)
	}
	if unrelated.WorkingDir != "/unrelated" {
		t.Fatalf("unrelated project = %#v", unrelated)
	}
	unrelatedServices, err := repo.ListServices(ctx, unrelatedID)
	if err != nil {
		t.Fatalf("ListServices(unrelated) error = %v", err)
	}
	if len(unrelatedServices) != 1 || unrelatedServices[0].ImageRef != "cairn/api:latest" {
		t.Fatalf("unrelated services = %#v", unrelatedServices)
	}
	if _, err := repo.Get(ctx, staleID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("active-scope stale project error = %v, want missing", err)
	}
	if _, err := repo.GetInScope(ctx, foreignScope, foreignStaleID); err != nil {
		t.Fatalf("foreign-scope stale project was deleted: %v", err)
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
