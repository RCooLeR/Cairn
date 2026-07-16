package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestHistoricalRepositoriesIsolateSequentialProjectIDReuse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	now := time.Date(2026, 7, 16, 16, 0, 0, 0, time.UTC)
	projectID := "linux_native/reused"
	scopeA := runtimescope.Must("linux_native", "socket:a")
	scopeB := runtimescope.Must("linux_native", "socket:b")

	saveHistoricalScopeProject(t, ctx, db, scopeA, projectID, now)
	if err := db.Lineage().ReplaceProjectInScope(ctx, scopeA, projectID, []LineageRecord{{
		ProviderID: scopeA.ProviderID(), ContextName: scopeA.ContextName(), ProjectID: projectID,
		ServiceID: projectID + "/web", ServiceName: "web", ContainerID: "shared-container",
		ServiceImageRef: "scope-a:latest", Source: models.LineageSourceUnknown,
		Confidence: models.ConfidenceUnknown, DiscoveredAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatalf("ReplaceProjectInScope(A): %v", err)
	}
	checkA, err := db.Updates().InsertCheckInScope(ctx, scopeA, UpdateCheckRecord{
		ProviderID: scopeA.ProviderID(), ContextName: scopeA.ContextName(), ProjectID: projectID,
		ServiceID: projectID + "/web", Kind: models.UpdateKindServiceImage,
		ImageRef: "nginx:latest", LocalDigest: "sha256:a", RemoteDigest: "sha256:b",
		Status: models.UpdateStatusServiceImageUpdateAvailable, CheckedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertCheckInScope(A): %v", err)
	}
	if err := db.Updates().IgnoreCheckInScope(ctx, scopeA, checkA, "scope A only", now); err != nil {
		t.Fatalf("IgnoreCheckInScope(A): %v", err)
	}
	ignoredA, err := db.Updates().ListCurrentInScope(ctx, scopeA, models.UpdateFilter{
		ProjectID: projectID, Status: []models.UpdateStatus{models.UpdateStatusIgnored},
	})
	if err != nil || len(ignoredA) != 1 {
		t.Fatalf("ListCurrentInScope(A ignored) = %#v, %v", ignoredA, err)
	}
	ignoreIDA := ignoredA[0].ID
	historyA, err := db.Updates().InsertHistoryInScope(ctx, scopeA, UpdateHistoryRecord{
		ProviderID: scopeA.ProviderID(), ContextName: scopeA.ContextName(), ProjectID: projectID,
		ServiceID: projectID + "/web", UpdateKind: models.UpdateKindServiceImage,
		ImageRef: "nginx:latest", OldImageID: "sha256:old-a", Result: "success",
		RollbackStatus: "available", StartedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertHistoryInScope(A): %v", err)
	}

	if err := db.Projects().DeleteInScope(ctx, scopeA, projectID); err != nil {
		t.Fatalf("DeleteInScope(A): %v", err)
	}
	saveHistoricalScopeProject(t, ctx, db, scopeB, projectID, now.Add(time.Hour))
	if err := db.Lineage().ReplaceProjectInScope(ctx, scopeB, projectID, []LineageRecord{{
		ProviderID: scopeB.ProviderID(), ContextName: scopeB.ContextName(), ProjectID: projectID,
		ServiceID: projectID + "/web", ServiceName: "web", ContainerID: "shared-container",
		ServiceImageRef: "scope-b:latest", Source: models.LineageSourceUnknown,
		Confidence: models.ConfidenceUnknown, DiscoveredAt: now.Add(time.Hour), UpdatedAt: now.Add(time.Hour),
	}}); err != nil {
		t.Fatalf("ReplaceProjectInScope(B): %v", err)
	}
	checkB, err := db.Updates().InsertCheckInScope(ctx, scopeB, UpdateCheckRecord{
		ProviderID: scopeB.ProviderID(), ContextName: scopeB.ContextName(), ProjectID: projectID,
		ServiceID: projectID + "/web", Kind: models.UpdateKindServiceImage,
		ImageRef: "nginx:latest", LocalDigest: "sha256:b", RemoteDigest: "sha256:c",
		Status: models.UpdateStatusServiceImageUpdateAvailable, CheckedAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("InsertCheckInScope(B): %v", err)
	}
	historyB, err := db.Updates().InsertHistoryInScope(ctx, scopeB, UpdateHistoryRecord{
		ProviderID: scopeB.ProviderID(), ContextName: scopeB.ContextName(), ProjectID: projectID,
		ServiceID: projectID + "/web", UpdateKind: models.UpdateKindServiceImage,
		ImageRef: "nginx:latest", OldImageID: "sha256:old-b", Result: "success",
		RollbackStatus: "available", StartedAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("InsertHistoryInScope(B): %v", err)
	}

	lineageB, err := db.Lineage().ListProjectInScope(ctx, scopeB, projectID)
	if err != nil || len(lineageB) != 1 || lineageB[0].ContextName != scopeB.ContextName() || lineageB[0].ServiceImageRef != "scope-b:latest" {
		t.Fatalf("ListProjectInScope(B) = %#v, %v", lineageB, err)
	}
	containerB, err := db.Lineage().GetContainerInScope(ctx, scopeB, "shared-container")
	if err != nil || containerB.ContextName != scopeB.ContextName() {
		t.Fatalf("GetContainerInScope(B) = %#v, %v", containerB, err)
	}
	currentB, err := db.Updates().ListCurrentInScope(ctx, scopeB, models.UpdateFilter{ProjectID: projectID})
	if err != nil || len(currentB) != 1 || currentB[0].ID != checkB || currentB[0].Status == models.UpdateStatusIgnored {
		t.Fatalf("ListCurrentInScope(B) = %#v, %v", currentB, err)
	}
	if _, err := db.Updates().GetCheckInScope(ctx, scopeB, checkA); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetCheckInScope(B, A check) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := db.Updates().GetIgnoredInScope(ctx, scopeB, ignoreIDA); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetIgnoredInScope(B, A rule) error = %v, want sql.ErrNoRows", err)
	}
	if err := db.Updates().UnignoreInScope(ctx, scopeB, ignoreIDA); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("UnignoreInScope(B, A rule) error = %v, want sql.ErrNoRows", err)
	}
	historyRowsB, err := db.Updates().ListHistoryInScope(ctx, scopeB, models.UpdateHistoryFilter{ProjectID: projectID})
	if err != nil || len(historyRowsB) != 1 || historyRowsB[0].ID != historyB {
		t.Fatalf("ListHistoryInScope(B) = %#v, %v", historyRowsB, err)
	}
	if _, err := db.Updates().GetHistoryInScope(ctx, scopeB, historyA); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetHistoryInScope(B, A history) error = %v, want sql.ErrNoRows", err)
	}
	if err := db.Updates().FinishHistoryInScope(ctx, scopeB, historyA, UpdateHistoryRecord{Result: "rolled_back"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("FinishHistoryInScope(B, A history) error = %v, want sql.ErrNoRows", err)
	}
}

func TestHistoricalScopeMigrationLeavesLegacyRowsUnclaimed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStore(t, ctx)
	defer closeStore(t, db)
	if err := ensureMigrationsTable(ctx, db.writer); err != nil {
		t.Fatalf("ensure migrations table: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) < 9 {
		t.Fatalf("migration count = %d, want at least 9", len(migrations))
	}
	for _, migration := range migrations[:8] {
		if err := applyMigration(ctx, db.writer, migration); err != nil {
			t.Fatalf("apply pre-scope migration %s: %v", migration.name, err)
		}
	}
	now := time.Date(2026, 7, 16, 15, 0, 0, 0, time.UTC)
	lineageResult, err := db.writer.ExecContext(ctx, `
		INSERT INTO image_lineage (
			provider_id, project_id, service_name, source, confidence, discovered_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "linux_native", "linux_native/legacy", "web", string(models.LineageSourceUnknown), string(models.ConfidenceUnknown), formatTime(now), formatTime(now))
	if err != nil {
		t.Fatalf("insert legacy lineage: %v", err)
	}
	legacyLineageID, _ := lineageResult.LastInsertId()
	checkResult, err := db.writer.ExecContext(ctx, `
		INSERT INTO image_update_checks (provider_id, project_id, service_id, kind, image_ref, status, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "linux_native", "linux_native/legacy", "linux_native/legacy/web", string(models.UpdateKindServiceImage), "nginx:latest", string(models.UpdateStatusServiceImageUpdateAvailable), formatTime(now))
	if err != nil {
		t.Fatalf("insert legacy check: %v", err)
	}
	legacyCheckID, _ := checkResult.LastInsertId()
	ignoreResult, err := db.writer.ExecContext(ctx, `
		INSERT INTO ignored_updates (provider_id, image_ref, update_kind, project_id, service_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "linux_native", "nginx:latest", string(models.UpdateKindServiceImage), "linux_native/legacy", "linux_native/legacy/web", formatTime(now))
	if err != nil {
		t.Fatalf("insert legacy ignored update: %v", err)
	}
	legacyIgnoreID, _ := ignoreResult.LastInsertId()
	historyResult, err := db.writer.ExecContext(ctx, `
		INSERT INTO update_history (provider_id, project_id, service_id, update_kind, image_ref, result, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "linux_native", "linux_native/legacy", "linux_native/legacy/web", string(models.UpdateKindServiceImage), "nginx:latest", "success", formatTime(now))
	if err != nil {
		t.Fatalf("insert legacy history: %v", err)
	}
	legacyHistoryID, _ := historyResult.LastInsertId()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate through historical scope: %v", err)
	}

	for _, table := range []string{"image_lineage", "image_update_checks", "ignored_updates", "update_history"} {
		var contextName string
		if err := db.writer.QueryRowContext(ctx, `SELECT context_name FROM `+table+` LIMIT 1`).Scan(&contextName); err != nil {
			t.Fatalf("read %s context_name: %v", table, err)
		}
		if contextName != "" {
			t.Fatalf("%s legacy context_name = %q, want unclaimed empty context", table, contextName)
		}
	}
	for _, index := range []string{"idx_lineage_project", "idx_lineage_service", "idx_lineage_container", "idx_checks_project", "idx_checks_kind", "idx_checks_latest", "idx_ignored_updates_unique", "idx_update_history_scope"} {
		var definition string
		if err := db.writer.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&definition); err != nil {
			t.Fatalf("read index %s: %v", index, err)
		}
		if !strings.Contains(strings.ToLower(definition), "context_name") {
			t.Fatalf("index %s does not include context_name: %s", index, definition)
		}
	}

	scope := runtimescope.Must("linux_native", "socket:new")
	if err := db.Providers().Upsert(ctx, ProviderRecord{
		ID: scope.ProviderID(), Type: "linux_native", Platform: "linux", DisplayName: "Linux native", Enabled: true,
	}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	saveHistoricalScopeProject(t, ctx, db, scope, "linux_native/legacy", now.Add(time.Hour))
	if got, err := db.Lineage().ListProjectInScope(ctx, scope, "linux_native/legacy"); err != nil || len(got) != 0 {
		t.Fatalf("legacy lineage claimed by scope = %#v, %v", got, err)
	}
	if _, err := db.Lineage().GetServiceInScope(ctx, scope, "linux_native/legacy", "web"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy service lineage error = %v, want sql.ErrNoRows", err)
	}
	if _, err := db.Updates().GetCheckInScope(ctx, scope, legacyCheckID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy check scope error = %v, want sql.ErrNoRows", err)
	}
	if got, err := db.Updates().ListCurrentInScope(ctx, scope, models.UpdateFilter{ProjectID: "linux_native/legacy"}); err != nil || len(got) != 0 {
		t.Fatalf("legacy checks claimed by scope = %#v, %v", got, err)
	}
	if _, err := db.Updates().GetIgnoredInScope(ctx, scope, legacyIgnoreID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy ignore scope error = %v, want sql.ErrNoRows", err)
	}
	if _, err := db.Updates().GetHistoryInScope(ctx, scope, legacyHistoryID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy history scope error = %v, want sql.ErrNoRows", err)
	}
	if got, err := db.Updates().ListHistoryInScope(ctx, scope, models.UpdateHistoryFilter{ProjectID: "linux_native/legacy"}); err != nil || len(got) != 0 {
		t.Fatalf("legacy history claimed by scope = %#v, %v", got, err)
	}
	if legacyLineageID <= 0 {
		t.Fatal("legacy lineage was not inserted")
	}
}

func saveHistoricalScopeProject(t *testing.T, ctx context.Context, db *Store, scope runtimescope.Scope, projectID string, seenAt time.Time) {
	t.Helper()
	project := ProjectRecord{
		ID: projectID, ProviderID: scope.ProviderID(), ContextName: scope.ContextName(),
		Name: "reused", WorkingDir: t.TempDir(), LastSeenAt: seenAt,
	}
	service := ServiceRecord{
		ID: projectID + "/web", ProjectID: projectID, Name: "web", ImageRef: "nginx:latest", LastSeenAt: seenAt,
	}
	if err := db.Projects().SaveSnapshot(ctx, scope, []ProjectRecord{project}, []ServiceRecord{service}, seenAt, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(%s): %v", scope.ContextName(), err)
	}
}
