package store

import (
	"context"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestUpdateRepositoryIgnoreOverlayAndBadges(t *testing.T) {
	ctx := context.Background()
	db := openMigratedStore(t, ctx)
	defer closeStore(t, db)
	repo := db.Updates()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	id, err := repo.InsertCheck(ctx, UpdateCheckRecord{
		ProviderID:        "linux_native",
		ProjectID:         "linux_native/app",
		ServiceID:         "linux_native/app/web",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "nginx:1.25",
		LocalDigest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RemoteDigest:      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
		CheckedAt:         now,
	})
	if err != nil {
		t.Fatalf("InsertCheck() error = %v", err)
	}
	if _, err := repo.InsertCheck(ctx, UpdateCheckRecord{
		ProviderID:        "linux_native",
		ProjectID:         "linux_native/app",
		ServiceID:         "linux_native/app/api",
		Kind:              models.UpdateKindBaseImage,
		ImageRef:          "app:local",
		BaseImageRef:      "alpine:3.20",
		Confidence:        models.ConfidenceHigh,
		RecommendedAction: models.RecommendedActionRebuildRedeploy,
		Status:            models.UpdateStatusRebuildRequired,
		CheckedAt:         now,
	}); err != nil {
		t.Fatalf("InsertCheck(base) error = %v", err)
	}

	badges, err := repo.Badges(ctx, "linux_native/app")
	if err != nil {
		t.Fatalf("Badges() error = %v", err)
	}
	if badges.ImageUpdates != 1 || badges.RebuildNeeded != 1 {
		t.Fatalf("badges before ignore = %#v", badges)
	}

	if err := repo.IgnoreCheck(ctx, id, "wait for maintenance", now.Add(time.Minute)); err != nil {
		t.Fatalf("IgnoreCheck() error = %v", err)
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
