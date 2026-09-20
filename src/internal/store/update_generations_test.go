package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestCheckRunPublicationReplacesMutableIdentityAsCompleteGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	scope := runtimescope.Must("linux_native", "socket:updates")
	projectID := "linux_native/app"
	webID := projectID + "/web"
	workerID := projectID + "/worker"
	seedGenerationProject(t, ctx, db, scope, projectID, webID, workerID)
	firstAt := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)

	first := publishProjectGeneration(t, ctx, repo, scope, projectID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "container-old", "nginx:1.25"),
		{
			ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), ProjectID: projectID,
			ServiceID: webID, ContainerID: "container-old", Kind: models.UpdateKindBaseImage,
			ImageRef: "app:old", BaseImageRef: "alpine:3.19", Status: models.UpdateStatusRebuildRequired,
		},
		generationCheck(scope, projectID, workerID, "worker-old", "worker:1"),
	}, firstAt)
	second := publishProjectGeneration(t, ctx, repo, scope, projectID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "container-recreated", "nginx:1.26"),
	}, firstAt.Add(time.Hour))
	if len(first) != 3 || len(second) != 1 || second[0].GenerationID <= first[0].GenerationID {
		t.Fatalf("published generations first=%#v second=%#v", first, second)
	}

	current := currentGeneration(t, ctx, repo, scope, projectID)
	if len(current) != 1 || current[0].ContainerID != "container-recreated" || current[0].ImageRef != "nginx:1.26" {
		t.Fatalf("current checks = %#v, want only recreated web service", current)
	}
	var historical int
	queryGenerationCount(t, ctx, db, scope, projectID, `is_current = 0`, &historical)
	if historical != 3 {
		t.Fatalf("historical check count = %d, want 3", historical)
	}
}

func TestCheckRunPublicationReturnsPersistedNormalizedRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	scope := runtimescope.Must("linux_native", "socket:normalized")
	projectID := "linux_native/normalized"
	serviceID := projectID + "/web"
	seedGenerationProject(t, ctx, db, scope, projectID, serviceID)
	at := time.Date(2026, 7, 17, 8, 30, 0, 0, time.UTC)

	run, err := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, at)
	if err != nil {
		t.Fatalf("begin project generation: %v", err)
	}
	published, accepted, err := repo.PublishCheckRunInScope(ctx, scope, run.ID, []UpdateCheckRecord{{
		ProviderID: scope.ProviderID(), ContextName: scope.ContextName(),
		ProjectID: projectID, ServiceID: serviceID, ImageRef: "nginx:latest",
	}}, at)
	if err != nil || !accepted || len(published) != 1 {
		t.Fatalf("PublishCheckRunInScope() = %#v/%v/%v", published, accepted, err)
	}
	if published[0].Kind != models.UpdateKindServiceImage ||
		published[0].Status != models.UpdateStatusUnknown ||
		published[0].Confidence != models.ConfidenceUnknown ||
		published[0].RecommendedAction != models.RecommendedActionNone {
		t.Fatalf("returned defaults were not normalized: %#v", published[0])
	}
	persisted, err := repo.GetCheckInScope(ctx, scope, published[0].ID)
	if err != nil {
		t.Fatalf("GetCheckInScope() error = %v", err)
	}
	if !reflect.DeepEqual(published[0], persisted) {
		t.Fatalf("returned record differs from persisted row:\nreturned:  %#v\npersisted: %#v", published[0], persisted)
	}
}

func TestCheckRunPublicationRejectsLateOlderAndEqualRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	scope := runtimescope.Must("linux_native", "socket:ordering")
	projectID := "linux_native/ordering"
	webID := projectID + "/web"
	seedGenerationProject(t, ctx, db, scope, projectID, webID)
	base := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)

	older, err := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, base)
	if err != nil {
		t.Fatalf("begin older run: %v", err)
	}
	newer, err := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, base.Add(time.Second))
	if err != nil {
		t.Fatalf("begin newer run: %v", err)
	}
	newRows, accepted, err := repo.PublishCheckRunInScope(ctx, scope, newer.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "new", "web:new"),
	}, base.Add(2*time.Second))
	if err != nil || !accepted || len(newRows) != 1 {
		t.Fatalf("publish newer run = %#v/%v/%v", newRows, accepted, err)
	}
	if _, accepted, err := repo.PublishCheckRunInScope(ctx, scope, older.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "stale", "web:stale"),
	}, base.Add(3*time.Second)); err != nil || accepted {
		t.Fatalf("late older publication accepted/error = %v/%v", accepted, err)
	}
	if _, accepted, err := repo.PublishCheckRunInScope(ctx, scope, newer.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "equal", "web:equal"),
	}, base.Add(4*time.Second)); err != nil || accepted {
		t.Fatalf("equal run republication accepted/error = %v/%v", accepted, err)
	}
	current := currentGeneration(t, ctx, repo, scope, projectID)
	if len(current) != 1 || current[0].ImageRef != "web:new" {
		t.Fatalf("current after stale/equal publications = %#v", current)
	}
}

func TestCheckRunPublicationOrdersProjectServiceOverlapAndEmptyHeads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	scope := runtimescope.Must("linux_native", "socket:overlap")
	projectID := "linux_native/overlap"
	webID := projectID + "/web"
	workerID := projectID + "/worker"
	seedGenerationProject(t, ctx, db, scope, projectID, webID, workerID)
	base := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)

	oldProject, _ := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, base)
	newService, _ := repo.BeginServiceCheckRunInScope(ctx, scope, projectID, webID, base.Add(time.Second))
	if _, accepted, err := repo.PublishCheckRunInScope(ctx, scope, newService.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "web-new", "web:new"),
	}, base.Add(2*time.Second)); err != nil || !accepted {
		t.Fatalf("publish newer service run accepted/error = %v/%v", accepted, err)
	}
	if _, accepted, err := repo.PublishCheckRunInScope(ctx, scope, oldProject.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "web-old", "web:old"),
		generationCheck(scope, projectID, workerID, "worker-old", "worker:old"),
	}, base.Add(3*time.Second)); err != nil || accepted {
		t.Fatalf("older project overlapped newer service accepted/error = %v/%v", accepted, err)
	}
	current := currentGeneration(t, ctx, repo, scope, projectID)
	if len(current) != 1 || current[0].ImageRef != "web:new" {
		t.Fatalf("current after project/service overlap = %#v", current)
	}

	oldService, _ := repo.BeginServiceCheckRunInScope(ctx, scope, projectID, webID, base.Add(4*time.Second))
	newProject, _ := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, base.Add(5*time.Second))
	if _, accepted, err := repo.PublishCheckRunInScope(ctx, scope, newProject.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "project-web", "web:project"),
		generationCheck(scope, projectID, workerID, "project-worker", "worker:project"),
	}, base.Add(6*time.Second)); err != nil || !accepted {
		t.Fatalf("publish newer project run accepted/error = %v/%v", accepted, err)
	}
	if _, accepted, err := repo.PublishCheckRunInScope(ctx, scope, oldService.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "service-stale", "web:stale"),
	}, base.Add(7*time.Second)); err != nil || accepted {
		t.Fatalf("older service overlapped newer project accepted/error = %v/%v", accepted, err)
	}
	current = currentGeneration(t, ctx, repo, scope, projectID)
	if len(current) != 2 || current[0].GenerationID != current[1].GenerationID {
		t.Fatalf("current after service/project overlap = %#v", current)
	}

	oldNonEmpty, _ := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, base.Add(8*time.Second))
	newEmpty, _ := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, base.Add(9*time.Second))
	if rows, accepted, err := repo.PublishCheckRunInScope(ctx, scope, newEmpty.ID, nil, base.Add(10*time.Second)); err != nil || !accepted || len(rows) != 0 {
		t.Fatalf("publish empty generation = %#v/%v/%v", rows, accepted, err)
	}
	if _, accepted, err := repo.PublishCheckRunInScope(ctx, scope, oldNonEmpty.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "resurrected", "web:resurrected"),
	}, base.Add(11*time.Second)); err != nil || accepted {
		t.Fatalf("older run resurrected empty head accepted/error = %v/%v", accepted, err)
	}
	if current := currentGeneration(t, ctx, repo, scope, projectID); len(current) != 0 {
		t.Fatalf("current after empty generation = %#v", current)
	}
}

func TestCheckRunPublicationRollsBackPartialGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	scope := runtimescope.Must("linux_native", "socket:atomic")
	projectID := "linux_native/atomic"
	webID := projectID + "/web"
	workerID := projectID + "/worker"
	seedGenerationProject(t, ctx, db, scope, projectID, webID, workerID)
	now := time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC)
	prior := publishProjectGeneration(t, ctx, repo, scope, projectID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "prior", "nginx:prior"),
	}, now)
	run, err := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("begin failing generation: %v", err)
	}
	invalid := generationCheck(scope, projectID, workerID, "invalid", "worker:new")
	invalid.LineageID = 999999
	if _, _, err := repo.PublishCheckRunInScope(ctx, scope, run.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "new", "nginx:new"), invalid,
	}, now.Add(time.Hour)); err == nil {
		t.Fatal("partial generation error = nil, want foreign-key failure")
	}
	if err := repo.AbandonCheckRunInScope(ctx, scope, run.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("abandon failed generation: %v", err)
	}
	current := currentGeneration(t, ctx, repo, scope, projectID)
	if len(current) != 1 || current[0].ID != prior[0].ID || current[0].ImageRef != "nginx:prior" {
		t.Fatalf("current after failed publication = %#v", current)
	}
	var total int
	queryGenerationCount(t, ctx, db, scope, projectID, `1 = 1`, &total)
	if total != 1 {
		t.Fatalf("row count after failed publication = %d, want 1", total)
	}
}

func TestServiceGenerationPreservesSiblingsAndRepeatedWriterIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	scope := runtimescope.Must("linux_native", "socket:service")
	projectID := "linux_native/services"
	webID := projectID + "/web"
	workerID := projectID + "/worker"
	seedGenerationProject(t, ctx, db, scope, projectID, webID, workerID)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	publishProjectGeneration(t, ctx, repo, scope, projectID, []UpdateCheckRecord{
		generationCheck(scope, projectID, webID, "web-old", "web:old"),
		generationCheck(scope, projectID, workerID, "worker-current", "worker:current"),
	}, now)
	for i := 0; i < 2; i++ {
		run, err := repo.BeginServiceCheckRunInScope(ctx, scope, projectID, webID, now.Add(time.Duration(i+1)*time.Minute))
		if err != nil {
			t.Fatalf("begin repeated service writer %d: %v", i, err)
		}
		if _, accepted, err := repo.PublishCheckRunInScope(ctx, scope, run.ID, []UpdateCheckRecord{
			generationCheck(scope, projectID, webID, fmt.Sprintf("web-%d", i), fmt.Sprintf("web:%d", i)),
		}, now.Add(time.Duration(i+1)*time.Minute)); err != nil || !accepted {
			t.Fatalf("publish repeated service writer %d = %v/%v", i, accepted, err)
		}
	}
	current := currentGeneration(t, ctx, repo, scope, projectID)
	if len(current) != 2 {
		t.Fatalf("current checks = %#v, want one web and one worker", current)
	}
	byService := map[string]UpdateCheckRecord{}
	for _, check := range current {
		byService[check.ServiceID] = check
	}
	if byService[webID].ImageRef != "web:1" || byService[workerID].ImageRef != "worker:current" {
		t.Fatalf("current checks by service = %#v", byService)
	}
}

func TestCheckRunRetentionBoundsRowsAndRunMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	scope := runtimescope.Must("linux_native", "socket:retention")
	projectID := "linux_native/retention"
	webID := projectID + "/web"
	seedGenerationProject(t, ctx, db, scope, projectID, webID)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for generation := 0; generation < 1000; generation++ {
		at := base.Add(time.Duration(generation) * time.Minute)
		publishProjectGeneration(t, ctx, repo, scope, projectID, []UpdateCheckRecord{
			generationCheck(scope, projectID, webID, fmt.Sprintf("container-%d", generation), fmt.Sprintf("web:%d", generation)),
		}, at)
	}
	var total int
	var historicalGenerations int
	if err := db.writer.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT CASE WHEN is_current = 0 THEN generation_id END)
		FROM image_update_checks
		WHERE provider_id = ? AND context_name = ? AND project_id = ?
	`, scope.ProviderID(), scope.ContextName(), projectID).Scan(&total, &historicalGenerations); err != nil {
		t.Fatalf("count retained generations: %v", err)
	}
	if total != updateCheckHistoryKeepGenerations+1 || historicalGenerations != updateCheckHistoryKeepGenerations {
		t.Fatalf("retained rows/generations = %d/%d", total, historicalGenerations)
	}
	var runCount int
	if err := db.writer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM image_update_check_runs
		WHERE provider_id = ? AND context_name = ? AND project_id = ?
	`, scope.ProviderID(), scope.ContextName(), projectID).Scan(&runCount); err != nil {
		t.Fatalf("count retained run metadata: %v", err)
	}
	if runCount != total {
		t.Fatalf("retained run metadata = %d, want %d referenced generations", runCount, total)
	}

	ageProjectID := "linux_native/retention-age"
	ageWebID := ageProjectID + "/web"
	seedGenerationProject(t, ctx, db, scope, ageProjectID, ageWebID)
	oldAt := base.Add(-updateCheckHistoryRetention - time.Hour)
	publishProjectGeneration(t, ctx, repo, scope, ageProjectID, []UpdateCheckRecord{
		generationCheck(scope, ageProjectID, ageWebID, "old", "web:old"),
	}, oldAt)
	publishProjectGeneration(t, ctx, repo, scope, ageProjectID, []UpdateCheckRecord{
		generationCheck(scope, ageProjectID, ageWebID, "current", "web:current"),
	}, base)
	queryGenerationCount(t, ctx, db, scope, ageProjectID, `1 = 1`, &total)
	if total != 1 {
		t.Fatalf("age-retained row count = %d, want current row only", total)
	}
}

func TestGenerationMigrationDemotesTiedInterruptedLegacyRowsAndPurgesOrphans(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStore(t, ctx)
	defer closeStore(t, db)
	if err := ensureMigrationsTable(ctx, db.writer); err != nil {
		t.Fatalf("ensure migrations table: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil || len(migrations) < 10 {
		t.Fatalf("load migrations = %d/%v", len(migrations), err)
	}
	for _, migration := range migrations[:9] {
		if err := applyMigration(ctx, db.writer, migration); err != nil {
			t.Fatalf("apply pre-generation migration %s: %v", migration.name, err)
		}
	}
	if err := db.Providers().Upsert(ctx, ProviderRecord{ID: "linux_native", Type: "linux_native", Platform: "linux", DisplayName: "Linux", Enabled: true}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	projectID := "linux_native/legacy-generations"
	serviceID := projectID + "/web"
	seedGenerationProject(t, ctx, db, runtimescope.Must("linux_native", "default"), projectID, serviceID)
	tiedAt := time.Date(2026, 7, 16, 8, 0, 0, 500_000_000, time.UTC)
	for _, record := range []UpdateCheckRecord{
		{ProviderID: "linux_native", ContextName: "default", ProjectID: projectID, ServiceID: serviceID, ContainerID: "old", Kind: models.UpdateKindServiceImage, ImageRef: "nginx:old", Status: models.UpdateStatusServiceImageUpdateAvailable, CheckedAt: tiedAt.Add(-time.Hour)},
		{ProviderID: "linux_native", ContextName: "default", ProjectID: projectID, ServiceID: serviceID, ContainerID: "tied-a", Kind: models.UpdateKindServiceImage, ImageRef: "nginx:tied-a", Status: models.UpdateStatusServiceImageUpdateAvailable, CheckedAt: tiedAt},
		// This tied row could be the only committed row of an interrupted run.
		{ProviderID: "linux_native", ContextName: "default", ProjectID: projectID, ServiceID: serviceID, ContainerID: "tied-b", Kind: models.UpdateKindBaseImage, ImageRef: "app:tied", BaseImageRef: "alpine:3.20", Status: models.UpdateStatusRebuildRequired, CheckedAt: tiedAt},
		{ProviderID: "linux_native", ContextName: "default", ProjectID: "linux_native/deleted", ServiceID: "linux_native/deleted/web", Kind: models.UpdateKindServiceImage, ImageRef: "orphan", Status: models.UpdateStatusUnknown, CheckedAt: tiedAt},
	} {
		insertLegacyCheck(t, ctx, db, record)
	}
	largeProjectID := "linux_native/legacy-large"
	largeServiceID := largeProjectID + "/web"
	seedGenerationProject(t, ctx, db, runtimescope.Must("linux_native", "default"), largeProjectID, largeServiceID)
	legacyTx, err := db.writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin large legacy seed: %v", err)
	}
	for i := 0; i < updateCheckLegacyKeepRows+5; i++ {
		if _, err := legacyTx.ExecContext(ctx, `
			INSERT INTO image_update_checks (
				provider_id, context_name, project_id, service_id, kind, image_ref, status, checked_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, "linux_native", "default", largeProjectID, largeServiceID,
			string(models.UpdateKindServiceImage), fmt.Sprintf("legacy:%d", i),
			string(models.UpdateStatusUnknown), formatTime(tiedAt.Add(time.Duration(i)*time.Millisecond))); err != nil {
			_ = legacyTx.Rollback()
			t.Fatalf("insert large legacy row %d: %v", i, err)
		}
	}
	if err := legacyTx.Commit(); err != nil {
		t.Fatalf("commit large legacy seed: %v", err)
	}
	if err := applyMigration(ctx, db.writer, migrations[9]); err != nil {
		t.Fatalf("apply generation migration: %v", err)
	}
	if current, err := db.Updates().ListCurrent(ctx, models.UpdateFilter{ProjectID: projectID}); err != nil || len(current) != 0 {
		t.Fatalf("legacy current rows = %#v/%v, want none without a provably complete run", current, err)
	}
	var owned int
	var orphan int
	if err := db.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_update_checks WHERE project_id = ? AND is_current = 0`, projectID).Scan(&owned); err != nil {
		t.Fatalf("count demoted legacy rows: %v", err)
	}
	if err := db.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_update_checks WHERE project_id = 'linux_native/deleted'`).Scan(&orphan); err != nil {
		t.Fatalf("count orphan legacy rows: %v", err)
	}
	if owned != 3 || orphan != 0 {
		t.Fatalf("demoted/orphan legacy rows = %d/%d, want 3/0", owned, orphan)
	}
	var boundedLegacy int
	if err := db.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_update_checks WHERE project_id = ?`, largeProjectID).Scan(&boundedLegacy); err != nil {
		t.Fatalf("count bounded legacy rows: %v", err)
	}
	if boundedLegacy != updateCheckLegacyKeepRows {
		t.Fatalf("bounded legacy rows = %d, want %d", boundedLegacy, updateCheckLegacyKeepRows)
	}
}

func TestProjectDeletionAndStaleCleanupPurgeCheckLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	scope := runtimescope.Must("linux_native", "socket:deletion")
	base := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)

	for _, mode := range []string{"delete", "scoped", "forgotten"} {
		projectID := "linux_native/" + mode
		serviceID := projectID + "/web"
		seedGenerationProject(t, ctx, db, scope, projectID, serviceID)
		publishProjectGeneration(t, ctx, repo, scope, projectID, []UpdateCheckRecord{
			generationCheck(scope, projectID, serviceID, mode, "web:"+mode),
		}, base)
		if _, err := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, base.Add(time.Minute)); err != nil {
			t.Fatalf("begin abandoned %s run: %v", mode, err)
		}
		switch mode {
		case "delete":
			if err := db.Projects().Delete(ctx, projectID); err != nil {
				t.Fatalf("Delete(%s): %v", mode, err)
			}
		case "scoped":
			if err := db.Projects().DeleteInScope(ctx, scope, projectID); err != nil {
				t.Fatalf("DeleteInScope(%s): %v", mode, err)
			}
		case "forgotten":
			project, err := db.Projects().GetInScope(ctx, scope, projectID)
			if err != nil {
				t.Fatalf("GetInScope(%s): %v", mode, err)
			}
			if err := db.Projects().Forget(ctx, project, base); err != nil {
				t.Fatalf("Forget(%s): %v", mode, err)
			}
			if err := db.Projects().DeleteInScope(ctx, scope, projectID); err != nil {
				t.Fatalf("DeleteInScope forgotten %s: %v", mode, err)
			}
		}
		assertNoCheckLifecycle(t, ctx, db, scope, projectID)
		if _, err := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, base); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("begin deleted %s project error = %v, want sql.ErrNoRows", mode, err)
		}
	}

	staleID := "linux_native/stale-updates"
	staleServiceID := staleID + "/web"
	seedGenerationProject(t, ctx, db, scope, staleID, staleServiceID)
	if _, err := db.writer.ExecContext(ctx, `UPDATE projects SET source = ?, last_seen_at = ? WHERE id = ?`, ProjectSourceLabels, formatTime(base.Add(-2*time.Hour)), staleID); err != nil {
		t.Fatalf("age stale project: %v", err)
	}
	publishProjectGeneration(t, ctx, repo, scope, staleID, []UpdateCheckRecord{
		generationCheck(scope, staleID, staleServiceID, "stale", "web:stale"),
	}, base)
	if err := db.Projects().SaveSnapshot(ctx, scope, nil, nil, base, base.Add(-time.Hour)); err != nil {
		t.Fatalf("SaveSnapshot(stale cleanup): %v", err)
	}
	assertNoCheckLifecycle(t, ctx, db, scope, staleID)
}

func TestDeletedProjectInvalidatesReservedRunBeforePublication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	scope := runtimescope.Must("linux_native", "socket:deleted-run")
	projectID := "linux_native/deleted-run"
	serviceID := projectID + "/web"
	seedGenerationProject(t, ctx, db, scope, projectID, serviceID)
	now := time.Date(2026, 7, 17, 14, 0, 0, 0, time.UTC)
	run, err := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, now)
	if err != nil {
		t.Fatalf("begin run: %v", err)
	}
	if err := db.Projects().DeleteInScope(ctx, scope, projectID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, _, err := repo.PublishCheckRunInScope(ctx, scope, run.ID, []UpdateCheckRecord{
		generationCheck(scope, projectID, serviceID, "late", "web:late"),
	}, now.Add(time.Minute)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("publish deleted-project run error = %v, want sql.ErrNoRows", err)
	}
	assertNoCheckLifecycle(t, ctx, db, scope, projectID)
}

func seedGenerationProject(t *testing.T, ctx context.Context, db *Store, scope runtimescope.Scope, projectID string, serviceIDs ...string) {
	t.Helper()
	seenAt := time.Date(2026, 7, 17, 7, 0, 0, 0, time.UTC)
	services := make([]ServiceRecord, 0, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		name := strings.TrimPrefix(serviceID, projectID+"/")
		services = append(services, ServiceRecord{ID: serviceID, ProjectID: projectID, Name: name, ImageRef: name + ":latest", LastSeenAt: seenAt})
	}
	if err := db.Projects().SaveSnapshot(ctx, scope, []ProjectRecord{{
		ID: projectID, ProviderID: scope.ProviderID(), ContextName: scope.ContextName(),
		Name: strings.TrimPrefix(projectID, scope.ProviderID()+"/"), LastSeenAt: seenAt,
	}}, services, seenAt, time.Time{}); err != nil {
		t.Fatalf("seed generation project %s: %v", projectID, err)
	}
}

func publishProjectGeneration(t *testing.T, ctx context.Context, repo *UpdateRepository, scope runtimescope.Scope, projectID string, records []UpdateCheckRecord, at time.Time) []UpdateCheckRecord {
	t.Helper()
	run, err := repo.BeginProjectCheckRunInScope(ctx, scope, projectID, at)
	if err != nil {
		t.Fatalf("begin project generation: %v", err)
	}
	published, accepted, err := repo.PublishCheckRunInScope(ctx, scope, run.ID, records, at)
	if err != nil || !accepted {
		t.Fatalf("publish project generation = %#v/%v/%v", published, accepted, err)
	}
	return published
}

func currentGeneration(t *testing.T, ctx context.Context, repo *UpdateRepository, scope runtimescope.Scope, projectID string) []UpdateCheckRecord {
	t.Helper()
	current, err := repo.ListCurrentInScope(ctx, scope, models.UpdateFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListCurrentInScope(): %v", err)
	}
	return current
}

func queryGenerationCount(t *testing.T, ctx context.Context, db *Store, scope runtimescope.Scope, projectID string, condition string, count *int) {
	t.Helper()
	query := `SELECT COUNT(*) FROM image_update_checks WHERE provider_id = ? AND context_name = ? AND project_id = ? AND (` + condition + `)`
	if err := db.writer.QueryRowContext(ctx, query, scope.ProviderID(), scope.ContextName(), projectID).Scan(count); err != nil {
		t.Fatalf("count check rows: %v", err)
	}
}

func insertLegacyCheck(t *testing.T, ctx context.Context, db *Store, record UpdateCheckRecord) {
	t.Helper()
	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO image_update_checks (
			provider_id, context_name, project_id, service_id, container_id, kind,
			image_ref, base_image_ref, status, checked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)
	`, record.ProviderID, record.ContextName, record.ProjectID, record.ServiceID, record.ContainerID,
		string(record.Kind), record.ImageRef, record.BaseImageRef, string(record.Status), formatTime(record.CheckedAt)); err != nil {
		t.Fatalf("insert legacy check: %v", err)
	}
}

func assertNoCheckLifecycle(t *testing.T, ctx context.Context, db *Store, scope runtimescope.Scope, projectID string) {
	t.Helper()
	for _, table := range []string{"image_update_checks", "image_update_check_runs", "image_update_check_heads"} {
		var count int
		if err := db.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE provider_id = ? AND context_name = ? AND project_id = ?`, scope.ProviderID(), scope.ContextName(), projectID).Scan(&count); err != nil {
			t.Fatalf("count %s lifecycle rows: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s lifecycle rows = %d, want 0", table, count)
		}
	}
}

func generationCheck(scope runtimescope.Scope, projectID string, serviceID string, containerID string, imageRef string) UpdateCheckRecord {
	return UpdateCheckRecord{
		ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), ProjectID: projectID,
		ServiceID: serviceID, ContainerID: containerID, Kind: models.UpdateKindServiceImage,
		ImageRef: imageRef, Status: models.UpdateStatusServiceImageUpdateAvailable,
	}
}
