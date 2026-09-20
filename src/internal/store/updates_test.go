package store

import (
	"context"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestUpdateRepositoryIgnoreOverlayAndBadges(t *testing.T) {
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "default")
	projectID := "linux_native/app"
	seedGenerationProject(t, ctx, db, scope, projectID, projectID+"/web", projectID+"/api")
	published := publishProjectGeneration(t, ctx, repo, scope, projectID, []UpdateCheckRecord{{
		ProviderID:        "linux_native",
		ContextName:       scope.ContextName(),
		ProjectID:         projectID,
		ServiceID:         projectID + "/web",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "nginx:1.25",
		LocalDigest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RemoteDigest:      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
		CheckedAt:         now,
	}, {
		ProviderID:        "linux_native",
		ContextName:       scope.ContextName(),
		ProjectID:         projectID,
		ServiceID:         projectID + "/api",
		Kind:              models.UpdateKindBaseImage,
		ImageRef:          "app:local",
		BaseImageRef:      "alpine:3.20",
		Confidence:        models.ConfidenceHigh,
		RecommendedAction: models.RecommendedActionRebuildRedeploy,
		Status:            models.UpdateStatusRebuildRequired,
		CheckedAt:         now,
	}}, now)
	id := published[0].ID

	badges, err := repo.Badges(ctx, projectID)
	if err != nil {
		t.Fatalf("Badges() error = %v", err)
	}
	if badges.ImageUpdates != 1 || badges.RebuildNeeded != 1 {
		t.Fatalf("badges before ignore = %#v", badges)
	}

	if err := repo.IgnoreCheck(ctx, id, "wait for maintenance", now.Add(time.Minute)); err != nil {
		t.Fatalf("IgnoreCheck() error = %v", err)
	}
	if err := repo.IgnoreCheck(ctx, id, "same rule updated", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("IgnoreCheck(update existing) error = %v", err)
	}
	var ignoredCount int
	var ignoredReason string
	if err := db.writer.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MAX(reason), '')
		FROM ignored_updates
		WHERE provider_id = ? AND image_ref = ? AND update_kind = ?
	`, "linux_native", "nginx:1.25", string(models.UpdateKindServiceImage)).Scan(&ignoredCount, &ignoredReason); err != nil {
		t.Fatalf("ignored update count query: %v", err)
	}
	if ignoredCount != 1 || ignoredReason != "same rule updated" {
		t.Fatalf("ignored rule count/reason = %d/%q, want one updated rule", ignoredCount, ignoredReason)
	}
	current, err := repo.ListCurrent(ctx, models.UpdateFilter{ProjectID: "linux_native/app"})
	if err != nil {
		t.Fatalf("ListCurrent() error = %v", err)
	}
	if len(current) != 1 || current[0].Status != models.UpdateStatusRebuildRequired {
		t.Fatalf("current after ignore = %#v", current)
	}
	ignored, err := repo.ListCurrent(ctx, models.UpdateFilter{
		ProjectID: "linux_native/app",
		Status:    []models.UpdateStatus{models.UpdateStatusIgnored},
	})
	if err != nil {
		t.Fatalf("ListCurrent(ignored) error = %v", err)
	}
	if len(ignored) != 1 || ignored[0].Status != models.UpdateStatusIgnored {
		t.Fatalf("ignored = %#v, want ignored status overlay", ignored)
	}
	badges, err = repo.Badges(ctx, "linux_native/app")
	if err != nil {
		t.Fatalf("Badges(after ignore) error = %v", err)
	}
	if badges.ImageUpdates != 0 || badges.RebuildNeeded != 1 {
		t.Fatalf("badges after ignore = %#v", badges)
	}

	if err := repo.Unignore(ctx, ignored[0].ID); err != nil {
		t.Fatalf("Unignore() error = %v", err)
	}
	badges, err = repo.Badges(ctx, "linux_native/app")
	if err != nil {
		t.Fatalf("Badges(after unignore) error = %v", err)
	}
	if badges.ImageUpdates != 1 || badges.RebuildNeeded != 1 {
		t.Fatalf("badges after unignore = %#v", badges)
	}
}

func TestUpdateRepositoryBadgesByProjectIDs(t *testing.T) {
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	now := time.Date(2026, 6, 13, 13, 0, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "default")
	webProjectID := "linux_native/web"
	cacheProjectID := "linux_native/cache"
	seedGenerationProject(t, ctx, db, scope, webProjectID, webProjectID+"/api", webProjectID+"/worker")
	seedGenerationProject(t, ctx, db, scope, cacheProjectID, cacheProjectID+"/redis")
	publishedWeb := publishProjectGeneration(t, ctx, repo, scope, webProjectID, []UpdateCheckRecord{{
		ProviderID:        "linux_native",
		ContextName:       scope.ContextName(),
		ProjectID:         webProjectID,
		ServiceID:         webProjectID + "/api",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "nginx:1.25",
		LocalDigest:       "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		RemoteDigest:      "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
		CheckedAt:         now,
	}, {
		ProviderID:        "linux_native",
		ContextName:       scope.ContextName(),
		ProjectID:         webProjectID,
		ServiceID:         webProjectID + "/worker",
		Kind:              models.UpdateKindBaseImage,
		ImageRef:          "web-worker:local",
		BaseImageRef:      "alpine:3.20",
		Confidence:        models.ConfidenceHigh,
		RecommendedAction: models.RecommendedActionRebuildRedeploy,
		Status:            models.UpdateStatusRebuildRequired,
		CheckedAt:         now,
	}}, now)
	publishProjectGeneration(t, ctx, repo, scope, cacheProjectID, []UpdateCheckRecord{{
		ProviderID:        "linux_native",
		ContextName:       scope.ContextName(),
		ProjectID:         cacheProjectID,
		ServiceID:         cacheProjectID + "/redis",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "redis:7",
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusPinnedDigest,
		CheckedAt:         now,
	}}, now)
	ignoredID := publishedWeb[0].ID
	if err := repo.IgnoreCheck(ctx, ignoredID, "keep pinned for test", now.Add(time.Minute)); err != nil {
		t.Fatalf("IgnoreCheck() error = %v", err)
	}

	badgesByProject, err := repo.BadgesByProjectIDs(ctx, []string{
		"linux_native/web",
		"linux_native/cache",
		"linux_native/empty",
		"linux_native/web",
	})
	if err != nil {
		t.Fatalf("BadgesByProjectIDs() error = %v", err)
	}
	if badgesByProject["linux_native/web"].ImageUpdates != 0 || badgesByProject["linux_native/web"].RebuildNeeded != 1 {
		t.Fatalf("web badges = %#v", badgesByProject["linux_native/web"])
	}
	if badgesByProject["linux_native/cache"].Pinned != 1 {
		t.Fatalf("cache badges = %#v", badgesByProject["linux_native/cache"])
	}
	if _, ok := badgesByProject["linux_native/empty"]; !ok {
		t.Fatal("empty project should be present with zero badges")
	}
}

func TestUpdateRepositoryBadgesByProjectIDsInScopeRejectsForeignRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Updates()
	now := time.Date(2026, 7, 16, 13, 0, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "unix:///var/run/docker.sock")
	projectID := "linux_native/app"
	if err := db.Projects().SaveSnapshot(ctx, scope, []ProjectRecord{{
		ID:          projectID,
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		Name:        "app",
		LastSeenAt:  now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	publishProjectGeneration(t, ctx, repo, scope, projectID, []UpdateCheckRecord{{
		ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), ProjectID: projectID, ServiceID: projectID + "/web",
		Kind: models.UpdateKindServiceImage, ImageRef: "nginx:1.25",
		Status: models.UpdateStatusServiceImageUpdateAvailable, CheckedAt: now,
	}}, now)
	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO image_update_checks (
			generation_id, is_current, provider_id, context_name, project_id, service_id,
			kind, image_ref, base_image_ref, status, checked_at
		) VALUES (999, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "existing_context:foreign", "foreign", projectID, projectID+"/api",
		string(models.UpdateKindBaseImage), "app:foreign", "alpine:3.20",
		string(models.UpdateStatusRebuildRequired), formatTime(now.Add(time.Minute))); err != nil {
		t.Fatalf("insert corrupt foreign check: %v", err)
	}
	badges, err := repo.BadgesByProjectIDsInScope(ctx, scope, []string{projectID})
	if err != nil {
		t.Fatalf("BadgesByProjectIDsInScope() error = %v", err)
	}
	if got := badges[projectID]; got.ImageUpdates != 1 || got.RebuildNeeded != 0 {
		t.Fatalf("scoped badges = %#v", got)
	}
	wrongScope := runtimescope.Must(scope.ProviderID(), "unix:///run/user/1000/docker.sock")
	badges, err = repo.BadgesByProjectIDsInScope(ctx, wrongScope, []string{projectID})
	if err != nil {
		t.Fatalf("BadgesByProjectIDsInScope(wrong scope) error = %v", err)
	}
	if got := badges[projectID]; got != (models.UpdateBadges{}) {
		t.Fatalf("wrong-scope badges = %#v", got)
	}
}

func TestUpdateRecordsKeepServiceNameSuffixAfterProjectPrefix(t *testing.T) {
	t.Parallel()
	check := UpdateCheckRecord{
		ProjectID: "linux_native/demo",
		ServiceID: "linux_native/demo/api/v1",
	}
	if got := check.ToModel().Service; got != "api/v1" {
		t.Fatalf("check service = %q, want api/v1", got)
	}
	history := UpdateHistoryRecord{
		ProjectID: "linux_native/demo",
		ServiceID: "linux_native/demo/api/v1",
	}
	if got := history.ToModel().Service; got != "api/v1" {
		t.Fatalf("history service = %q, want api/v1", got)
	}
}
