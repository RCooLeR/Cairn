package updates

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/store"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	digestC = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	digestD = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func TestManagerServiceImageStatusMachine(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/app"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.25", ""),
		serviceRecord(projectID, "latest", "redis:latest", ""),
		serviceRecord(projectID, "pinned", "busybox@"+digestC, ""),
		serviceRecord(projectID, "private", "private.local/team/app:1", ""),
		serviceRecord(projectID, "rate", "ratelimited.local/team/app:1", ""),
		serviceRecord(projectID, "down", "down.local/team/app:1", ""),
		serviceRecord(projectID, "local", "local/app:dev", ""),
		serviceRecord(projectID, "postgres", "postgres:16", ""),
	})
	images := fakeImages{details: map[string]*models.ImageDetail{
		"nginx:1.25":                   imageDetail("sha256:web", "docker.io/library/nginx@"+digestA),
		"redis:latest":                 imageDetail("sha256:redis", "docker.io/library/redis@"+digestA),
		"private.local/team/app:1":     imageDetail("sha256:private", "private.local/team/app@"+digestA),
		"ratelimited.local/team/app:1": imageDetail("sha256:rate", "ratelimited.local/team/app@"+digestA),
		"down.local/team/app:1":        imageDetail("sha256:down", "down.local/team/app@"+digestA),
		"local/app:dev":                imageDetail("sha256:local", "docker.io/local/app@"+digestA),
		"postgres:16":                  imageDetail("sha256:postgres", "docker.io/library/postgres@"+digestA),
	}}
	registry := &fakeRegistry{
		digests: map[string]string{
			"nginx:1.25":   digestB,
			"redis:latest": digestD,
			"postgres:16":  digestA,
		},
		errs: map[string]error{
			"private.local/team/app:1":     apperror.New(apperror.RegistryAuth, "auth required"),
			"ratelimited.local/team/app:1": apperror.New(apperror.RegistryRateLimit, "rate limited"),
			"down.local/team/app:1":        apperror.New(apperror.RegistryUnreachable, "registry down"),
			"local/app:dev":                apperror.New(apperror.NotFound, "not found"),
		},
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), images, registry, db.Settings(), nil, nil)

	got, err := manager.CheckProjectUpdates(ctx, projectID)
	if err != nil {
		t.Fatalf("CheckProjectUpdates() error = %v", err)
	}
	byService := updatesByService(got)
	assertStatus(t, byService, "web", models.UpdateKindServiceImage, "", models.UpdateStatusServiceImageUpdateAvailable)
	assertStatus(t, byService, "latest", models.UpdateKindServiceImage, "", models.UpdateStatusServiceImageUpdateAvailable)
	assertStatus(t, byService, "pinned", models.UpdateKindServiceImage, "", models.UpdateStatusPinnedDigest)
	assertStatus(t, byService, "private", models.UpdateKindServiceImage, "", models.UpdateStatusAuthRequired)
	assertStatus(t, byService, "rate", models.UpdateKindServiceImage, "", models.UpdateStatusRateLimited)
	assertStatus(t, byService, "down", models.UpdateKindServiceImage, "", models.UpdateStatusError)
	assertStatus(t, byService, "local", models.UpdateKindServiceImage, "", models.UpdateStatusLocalOnlyImage)
	assertStatus(t, byService, "postgres", models.UpdateKindServiceImage, "", models.UpdateStatusUpToDate)
	if len(byService["postgres"]) != 1 {
		t.Fatalf("postgres updates = %#v, want service-image status only", byService["postgres"])
	}
	if !hasNoteContaining(byService["latest"][0], "Mutable tag 'latest'") {
		t.Fatalf("latest notes = %#v, want mutable-tag warning", byService["latest"][0].Notes)
	}
}

func TestManagerBaseImageStatusMachine(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/builds"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "api", "builds-api:local", "."),
		serviceRecord(projectID, "unknown", "builds-unknown:local", "."),
	})
	if err := db.Lineage().ReplaceProject(ctx, projectID, []store.LineageRecord{{
		ProviderID:      "linux_native",
		ProjectID:       projectID,
		ServiceID:       projectID + "/api",
		ServiceName:     "api",
		ServiceImageRef: "builds-api:local",
		Source:          models.LineageSourceComposeDockerfile,
		Confidence:      models.ConfidenceHigh,
		BaseRefs: []store.BaseImageRefRecord{
			{Name: "golang", Tag: "1.22", ImageRef: "golang:1.22", StageName: "builder", StageIndex: 0, BuildTimeDigest: digestA, Status: models.UpdateStatusUnknown},
			{Name: "alpine", Tag: "3.20", ImageRef: "alpine:3.20", StageName: "runtime", StageIndex: 1, IsFinalStageBase: true, BuildTimeDigest: digestA, Status: models.UpdateStatusUnknown},
			{Name: "busybox", ImageRef: "busybox@" + digestC, StageIndex: 2, BuildTimeDigest: digestC, Status: models.UpdateStatusPinnedDigest},
		},
	}}); err != nil {
		t.Fatalf("ReplaceProject(lineage) error = %v", err)
	}
	registry := &fakeRegistry{digests: map[string]string{
		"golang:1.22": digestB,
		"alpine:3.20": digestD,
	}}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, registry, db.Settings(), nil, nil)

	got, err := manager.CheckProjectUpdates(ctx, projectID)
	if err != nil {
		t.Fatalf("CheckProjectUpdates() error = %v", err)
	}
	byService := updatesByService(got)
	assertStatus(t, byService, "api", models.UpdateKindServiceImage, "", models.UpdateStatusBuiltLocally)
	assertStatus(t, byService, "api", models.UpdateKindBaseImage, "golang:1.22", models.UpdateStatusBaseImageUpdateAvailable)
	assertStatus(t, byService, "api", models.UpdateKindBaseImage, "alpine:3.20", models.UpdateStatusRebuildRequired)
	assertStatus(t, byService, "api", models.UpdateKindBaseImage, "busybox@"+digestC, models.UpdateStatusPinnedDigest)
	assertStatus(t, byService, "unknown", models.UpdateKindBaseImage, "", models.UpdateStatusUnknownBaseImage)

	records, err := db.Lineage().ListProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListProject(lineage) error = %v", err)
	}
	for _, record := range records {
		if record.ServiceName != "api" {
			continue
		}
		for _, ref := range record.BaseRefs {
			if ref.ImageRef == "alpine:3.20" && (ref.RemoteDigest != digestD || ref.Status != models.UpdateStatusRebuildRequired) {
				t.Fatalf("alpine ref after check = %#v", ref)
			}
		}
	}
}

func TestManagerIgnoreRoundTripExcludesBadges(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/app"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{serviceRecord(projectID, "web", "nginx:1.25", "")})
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{details: map[string]*models.ImageDetail{
		"nginx:1.25": imageDetail("sha256:web", "docker.io/library/nginx@"+digestA),
	}}, &fakeRegistry{digests: map[string]string{"nginx:1.25": digestB}}, db.Settings(), nil, nil)

	current, err := manager.CheckProjectUpdates(ctx, projectID)
	if err != nil {
		t.Fatalf("CheckProjectUpdates() error = %v", err)
	}
	if len(current) != 1 {
		t.Fatalf("current = %#v", current)
	}
	if err := manager.IgnoreUpdate(ctx, models.IgnoreUpdateRequest{ID: current[0].ID, Reason: "later"}); err != nil {
		t.Fatalf("IgnoreUpdate() error = %v", err)
	}
	defaultList, err := manager.ListCurrentUpdates(ctx, models.UpdateFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListCurrentUpdates() error = %v", err)
	}
	if len(defaultList) != 0 {
		t.Fatalf("default list after ignore = %#v", defaultList)
	}
	ignored, err := manager.ListCurrentUpdates(ctx, models.UpdateFilter{ProjectID: projectID, Status: []models.UpdateStatus{models.UpdateStatusIgnored}})
	if err != nil {
		t.Fatalf("ListCurrentUpdates(ignored) error = %v", err)
	}
	if len(ignored) != 1 || ignored[0].Status != models.UpdateStatusIgnored {
		t.Fatalf("ignored = %#v", ignored)
	}
	badges, err := db.Updates().Badges(ctx, projectID)
	if err != nil {
		t.Fatalf("Badges() error = %v", err)
	}
	if badges.ImageUpdates != 0 {
		t.Fatalf("badges after ignore = %#v", badges)
	}
	if err := manager.UnignoreUpdate(ctx, ignored[0].ID); err != nil {
		t.Fatalf("UnignoreUpdate() error = %v", err)
	}
	badges, err = db.Updates().Badges(ctx, projectID)
	if err != nil {
		t.Fatalf("Badges(after unignore) error = %v", err)
	}
	if badges.ImageUpdates != 1 {
		t.Fatalf("badges after unignore = %#v", badges)
	}
}

func TestManagerMixedProjectPlanOrdering(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/mixed"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "a", "nginx:1.25", ""),
		serviceRecord(projectID, "b", "redis:7", ""),
		serviceRecord(projectID, "c", "mixed-api:local", "."),
		serviceRecord(projectID, "d", "mixed-worker:local", "."),
		serviceRecord(projectID, "pinned", "busybox@"+digestC, ""),
		serviceRecord(projectID, "unknown", "mixed-unknown:local", "."),
	})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        "linux_native",
		ProjectID:         projectID,
		ServiceID:         projectID + "/a",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "nginx:1.25",
		LocalImageID:      "sha256:web-old",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        "linux_native",
		ProjectID:         projectID,
		ServiceID:         projectID + "/b",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "redis:7",
		LocalImageID:      "sha256:redis-old",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        "linux_native",
		ProjectID:         projectID,
		ServiceID:         projectID + "/c",
		Kind:              models.UpdateKindBaseImage,
		ImageRef:          "mixed-api:local",
		BaseImageRef:      "alpine:3.20",
		LocalDigest:       digestA,
		RemoteDigest:      digestD,
		Confidence:        models.ConfidenceHigh,
		RecommendedAction: models.RecommendedActionRebuildRedeploy,
		Status:            models.UpdateStatusRebuildRequired,
	})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        "linux_native",
		ProjectID:         projectID,
		ServiceID:         projectID + "/d",
		Kind:              models.UpdateKindBaseImage,
		ImageRef:          "mixed-worker:local",
		BaseImageRef:      "golang:1.22",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionRebuildRedeploy,
		Status:            models.UpdateStatusBaseImageUpdateAvailable,
	})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID: "linux_native",
		ProjectID:  projectID,
		ServiceID:  projectID + "/pinned",
		Kind:       models.UpdateKindServiceImage,
		ImageRef:   "busybox@" + digestC,
		Status:     models.UpdateStatusPinnedDigest,
	})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID: "linux_native",
		ProjectID:  projectID,
		ServiceID:  projectID + "/unknown",
		Kind:       models.UpdateKindBaseImage,
		ImageRef:   "mixed-unknown:local",
		Status:     models.UpdateStatusUnknownBaseImage,
	})
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)

	plan, err := manager.PlanProjectUpdate(ctx, projectID)
	if err != nil {
		t.Fatalf("PlanProjectUpdate() error = %v", err)
	}
	gotCommands := commandTexts(plan.Commands)
	wantCommands := []string{
		"docker compose -f compose.yaml pull a b",
		"docker compose -f compose.yaml build --pull c d",
		"docker compose -f compose.yaml up -d a b c d",
	}
	if strings.Join(gotCommands, "\n") != strings.Join(wantCommands, "\n") {
		t.Fatalf("commands = %#v, want %#v", gotCommands, wantCommands)
	}
	if len(plan.Items) != 4 {
		t.Fatalf("items = %#v, want 4 actionable updates", plan.Items)
	}
	if !warningsContain(plan.Warnings, "pinned") || !warningsContain(plan.Warnings, "base image is unknown") {
		t.Fatalf("warnings = %#v", plan.Warnings)
	}
}

func TestManagerApplyUpdateHealthFailureRollsBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openUpdatesStore(t)
	projectID := "linux_native/health"
	service := serviceRecord(projectID, "web", "nginx:1.25", "")
	service.Metadata = map[string]any{"hasHealthcheck": true}
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{service})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        "linux_native",
		ProjectID:         projectID,
		ServiceID:         projectID + "/web",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "nginx:1.25",
		LocalImageID:      "sha256:web-old",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	eventBus := bus.New()
	t.Cleanup(eventBus.Close)
	done := eventBus.Subscribe(ctx, bus.TopicJobDone, 4)
	compose := &fakeUpdateCompose{}
	docker := &fakeUpdateDocker{
		images: map[string]*models.ImageDetail{
			"sha256:web-old": imageDetail("sha256:web-old", "docker.io/library/nginx@"+digestA),
		},
		containers: []models.ContainerSummary{{
			ID:        "container-web",
			Name:      "web-1",
			Image:     "nginx:1.25",
			ImageID:   "sha256:web-new",
			ProjectID: projectID,
			Service:   "web",
			State:     "running",
			Health:    models.HealthStatusUnhealthy,
		}},
		logs: map[string]string{"container-web": "panic: update failed\n"},
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), eventBus, nil)
	manager.Compose = compose
	manager.HealthWindow = 20 * time.Millisecond
	manager.HealthPollInterval = time.Millisecond
	manager.NewID = func() string { return "job" }

	plan, err := manager.PlanProjectUpdate(ctx, projectID)
	if err != nil {
		t.Fatalf("PlanProjectUpdate() error = %v", err)
	}
	jobID, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID, WatchHealth: true, RollbackOnFailure: true})
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}
	if jobID != "updates-job" {
		t.Fatalf("jobID = %q", jobID)
	}
	waitUpdateDone(t, done, updateResultRolledBack)
	if !containsCall(compose.calls, "pull:web") || countCall(compose.calls, "up:web") != 2 {
		t.Fatalf("compose calls = %#v", compose.calls)
	}
	if len(docker.tags) != 1 || docker.tags[0] != "sha256:web-old->nginx:1.25" {
		t.Fatalf("tags = %#v", docker.tags)
	}
	history, err := manager.ListUpdateHistory(ctx, models.UpdateHistoryFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListUpdateHistory() error = %v", err)
	}
	if len(history) != 1 || history[0].Result != updateResultRolledBack || history[0].RollbackStatus != rollbackStatusRolledBack {
		t.Fatalf("history = %#v", history)
	}
}

func TestManagerApplyUpdateManualNeededWhenOldImagePruned(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openUpdatesStore(t)
	projectID := "linux_native/pruned"
	service := serviceRecord(projectID, "web", "nginx:1.25", "")
	service.Metadata = map[string]any{"hasHealthcheck": true}
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{service})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        "linux_native",
		ProjectID:         projectID,
		ServiceID:         projectID + "/web",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "nginx:1.25",
		LocalImageID:      "sha256:web-old",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	eventBus := bus.New()
	t.Cleanup(eventBus.Close)
	done := eventBus.Subscribe(ctx, bus.TopicJobDone, 4)
	docker := &fakeUpdateDocker{
		images: map[string]*models.ImageDetail{},
		containers: []models.ContainerSummary{{
			ID:        "container-web",
			Name:      "web-1",
			Image:     "nginx:1.25",
			ImageID:   "sha256:web-new",
			ProjectID: projectID,
			Service:   "web",
			State:     "running",
			Health:    models.HealthStatusUnhealthy,
		}},
		logs: map[string]string{"container-web": "Fatal: no database\n"},
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), eventBus, nil)
	manager.Compose = &fakeUpdateCompose{}
	manager.HealthWindow = 20 * time.Millisecond
	manager.HealthPollInterval = time.Millisecond

	plan, err := manager.PlanProjectUpdate(ctx, projectID)
	if err != nil {
		t.Fatalf("PlanProjectUpdate() error = %v", err)
	}
	_, err = manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID, WatchHealth: true, RollbackOnFailure: true})
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}
	waitUpdateDone(t, done, updateResultManualNeeded)
	history, err := manager.ListUpdateHistory(ctx, models.UpdateHistoryFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListUpdateHistory() error = %v", err)
	}
	if len(history) != 1 || history[0].Result != updateResultManualNeeded || history[0].RollbackStatus != rollbackStatusManualNeeded {
		t.Fatalf("history = %#v", history)
	}
}

func TestManagerApplyUpdateBackupFirstSuccessWarn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openUpdatesStore(t)
	projectID := "linux_native/backup"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{serviceRecord(projectID, "web", "nginx:1.25", "")})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        "linux_native",
		ProjectID:         projectID,
		ServiceID:         projectID + "/web",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "nginx:1.25",
		LocalImageID:      "sha256:web-old",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	eventBus := bus.New()
	t.Cleanup(eventBus.Close)
	done := eventBus.Subscribe(ctx, bus.TopicJobDone, 4)
	backups := &fakeUpdateBackups{}
	docker := &fakeUpdateDocker{
		images: map[string]*models.ImageDetail{
			"sha256:web-old": imageDetail("sha256:web-old", "docker.io/library/nginx@"+digestA),
			"sha256:web-new": imageDetail("sha256:web-new", "docker.io/library/nginx@"+digestB),
		},
		containers: []models.ContainerSummary{{
			ID:        "container-web",
			Name:      "web-1",
			Image:     "nginx:1.25",
			ImageID:   "sha256:web-new",
			ProjectID: projectID,
			Service:   "web",
			State:     "running",
			Health:    models.HealthStatusUnknown,
		}},
		details: map[string]*models.ContainerDetail{
			"container-web": {
				Summary: models.ContainerSummary{ID: "container-web"},
				Mounts:  []models.MountSpec{{Type: "volume", VolumeName: "web-data", Target: "/data"}},
			},
		},
		logs: map[string]string{"container-web": "server started\n"},
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), eventBus, nil)
	manager.Compose = &fakeUpdateCompose{}
	manager.Backups = backups

	plan, err := manager.PlanServiceUpdate(ctx, projectID, "web")
	if err != nil {
		t.Fatalf("PlanServiceUpdate() error = %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Service != "web" {
		t.Fatalf("service plan = %#v", plan)
	}
	_, err = manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID, BackupVolumesFirst: true, WatchHealth: true})
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}
	waitUpdateDone(t, done, updateResultSuccessWarn)
	if strings.Join(backups.volumes, ",") != "web-data" {
		t.Fatalf("backup volumes = %#v", backups.volumes)
	}
	history, err := manager.ListUpdateHistory(ctx, models.UpdateHistoryFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListUpdateHistory() error = %v", err)
	}
	if len(history) != 1 || history[0].Result != updateResultSuccessWarn || history[0].RollbackStatus != rollbackStatusAvailable {
		t.Fatalf("history = %#v", history)
	}
}

func TestManagerRollbackHistory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openUpdatesStore(t)
	projectID := "linux_native/rollback"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{serviceRecord(projectID, "web", "rollback-web:local", ".")})
	historyID, err := db.Updates().InsertHistory(ctx, store.UpdateHistoryRecord{
		ProviderID:     "linux_native",
		ProjectID:      projectID,
		ServiceID:      projectID + "/web",
		UpdateKind:     models.UpdateKindBaseImage,
		ImageRef:       "rollback-web:local",
		BaseImageRef:   "alpine:3.20",
		OldImageID:     "sha256:web-old",
		OldDigest:      digestA,
		OldBaseDigest:  digestA,
		Commands:       []models.PlannedCommand{{Order: 1, Command: "docker compose build --pull web", Risk: models.RiskNeedsConfirmation}},
		Result:         updateResultSuccess,
		RollbackStatus: rollbackStatusAvailable,
		StartedAt:      time.Now().UTC().Add(-time.Minute),
		FinishedAt:     time.Now().UTC().Add(-30 * time.Second),
	})
	if err != nil {
		t.Fatalf("InsertHistory() error = %v", err)
	}
	eventBus := bus.New()
	t.Cleanup(eventBus.Close)
	done := eventBus.Subscribe(ctx, bus.TopicJobDone, 4)
	compose := &fakeUpdateCompose{}
	docker := &fakeUpdateDocker{images: map[string]*models.ImageDetail{
		"sha256:web-old": imageDetail("sha256:web-old", "docker.io/library/rollback-web@"+digestA),
	}}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), eventBus, nil)
	manager.Compose = compose

	_, err = manager.Rollback(ctx, historyID)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	waitUpdateDone(t, done, updateResultRolledBack)
	if len(docker.tags) != 1 || docker.tags[0] != "sha256:web-old->rollback-web:local" {
		t.Fatalf("tags = %#v", docker.tags)
	}
	if !containsCall(compose.calls, "up-no-build:web") {
		t.Fatalf("compose calls = %#v", compose.calls)
	}
	history, err := db.Updates().GetHistory(ctx, historyID)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if history.Result != updateResultRolledBack || history.RollbackStatus != rollbackStatusRolledBack {
		t.Fatalf("history = %#v", history)
	}
}

func TestManagerPlanServiceWarningsAndExpiry(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/warnings"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.25", ""),
		serviceRecord(projectID, "api", "api:local", "."),
	})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID: "linux_native",
		ProjectID:  projectID,
		ServiceID:  projectID + "/api",
		Kind:       models.UpdateKindBaseImage,
		ImageRef:   "api:local",
		Status:     models.UpdateStatusUnknownBaseImage,
	})
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)

	plan, err := manager.PlanServiceUpdate(ctx, projectID, "api")
	if err != nil {
		t.Fatalf("PlanServiceUpdate() error = %v", err)
	}
	if len(plan.Commands) != 0 || !warningsContain(plan.Warnings, "base image is unknown") {
		t.Fatalf("warning-only plan = %#v", plan)
	}
	if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyUpdate(warning-only) error = %v, want conflict", err)
	}
	if _, err := manager.PlanServiceUpdate(ctx, projectID, "missing"); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("PlanServiceUpdate(missing) error = %v, want not found", err)
	}
	if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{}); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("ApplyUpdate(empty plan) error = %v, want confirmation required", err)
	}
}

func TestManagerAuditNotificationAndErrorHelpers(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)
	manager.Audit = db.Audit()
	manager.Notify = db.Notifications()
	now := time.Date(2026, 6, 13, 14, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }

	if err := manager.recordAudit(ctx, "update.apply", "project", "p1", "linux_native", "p1", "docker compose up -d", models.RiskNeedsConfirmation, "success", time.Second, nil); err != nil {
		t.Fatalf("recordAudit(success) error = %v", err)
	}
	actionErr := apperror.New(apperror.Conflict, "boom")
	if err := manager.recordAudit(ctx, "update.apply", "project", "p1", "linux_native", "p1", "docker compose up -d", models.RiskNeedsConfirmation, "failed", time.Second, actionErr); err != nil {
		t.Fatalf("recordAudit(failed) error = %v", err)
	}
	manager.insertNotification(ctx, updateResultSuccess, "demo", nil)
	manager.insertNotification(ctx, updateResultManualNeeded, "demo", actionErr)
	notifications, err := db.Notifications().List(ctx, false, 10)
	if err != nil {
		t.Fatalf("List notifications error = %v", err)
	}
	if len(notifications) != 2 || notifications[0].Level != "error" || notifications[1].Level != "info" {
		t.Fatalf("notifications = %#v", notifications)
	}
	if !apperror.IsCode(mapStoreError(sql.ErrNoRows, "missing"), apperror.NotFound) {
		t.Fatalf("mapStoreError(sql.ErrNoRows) did not map to not found")
	}
	if !apperror.IsCode(mapStoreError(errors.New("other"), "missing"), apperror.Internal) {
		t.Fatalf("mapStoreError(other) did not map to internal")
	}
	if !IsRateLimited(context.DeadlineExceeded) || !IsRateLimited(apperror.New(apperror.RegistryRateLimit, "rate")) || IsRateLimited(errors.New("other")) {
		t.Fatalf("IsRateLimited mapping failed")
	}
}

func TestExecutorHelperBranches(t *testing.T) {
	order := map[string]int{"b": 0, "a": 1}
	services := []string{"z", "a", "b"}
	sortServicesByOrder(services, order)
	if strings.Join(services, ",") != "b,a,z" {
		t.Fatalf("sorted services = %#v", services)
	}
	if got := serviceNameFromID("plain"); got != "plain" {
		t.Fatalf("serviceNameFromID plain = %q", got)
	}
	if got := metadataStringMap(map[string]any{"buildArgs": map[string]string{"A": "B"}}, "buildArgs"); got["A"] != "B" {
		t.Fatalf("metadataStringMap string map = %#v", got)
	}
	if got := metadataStringMap(map[string]any{"buildArgs": map[string]any{"A": "B", "N": 1}}, "buildArgs"); got["A"] != "B" || got["N"] != "" {
		t.Fatalf("metadataStringMap any map = %#v", got)
	}
	if metadataStringMap(map[string]any{"buildArgs": 1}, "buildArgs") != nil {
		t.Fatalf("metadataStringMap invalid should be nil")
	}
	if metadataBool(map[string]any{"hasHealthcheck": "true"}, "hasHealthcheck") != true {
		t.Fatalf("metadataBool string true failed")
	}
	if metadataBool(map[string]any{"hasHealthcheck": 1}, "hasHealthcheck") {
		t.Fatalf("metadataBool invalid should be false")
	}
	if !containerRunning(models.ContainerSummary{Status: "Up 2 minutes"}) {
		t.Fatalf("containerRunning status Up = false")
	}
	if containerRunning(models.ContainerSummary{State: "exited"}) {
		t.Fatalf("containerRunning exited = true")
	}
	if rollbackStatusForImage("") != rollbackStatusUnavailable || rollbackStatusForImage("sha256:old") != rollbackStatusAvailable {
		t.Fatalf("rollbackStatusForImage mapping failed")
	}
	if auditStatus(context.Canceled) != "cancelled" || auditStatus(nil) != "success" || auditStatus(errors.New("x")) != "failed" {
		t.Fatalf("auditStatus mapping failed")
	}
}

func TestFatalLogDetected(t *testing.T) {
	for _, input := range []string{"panic: boom", "Fatal error", "Exception in thread main", "exit-on-start"} {
		if !fatalLogDetected(input) {
			t.Fatalf("fatalLogDetected(%q) = false", input)
		}
	}
	if fatalLogDetected("server started normally") {
		t.Fatalf("fatalLogDetected(normal) = true")
	}
}

func TestManagerCheckAllPublishesProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openUpdatesStore(t)
	projectID := "linux_native/app"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{serviceRecord(projectID, "web", "nginx:1.25", "")})
	eventBus := bus.New()
	t.Cleanup(eventBus.Close)
	events := eventBus.Subscribe(ctx, bus.TopicUpdatesCheckProgress, 8)
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{details: map[string]*models.ImageDetail{
		"nginx:1.25": imageDetail("sha256:web", "docker.io/library/nginx@"+digestA),
	}}, &fakeRegistry{digests: map[string]string{"nginx:1.25": digestA}}, db.Settings(), eventBus, nil)
	manager.NewID = func() string { return "job" }

	jobID, err := manager.CheckAllUpdates(ctx)
	if err != nil {
		t.Fatalf("CheckAllUpdates() error = %v", err)
	}
	if jobID != "updates-job" {
		t.Fatalf("jobID = %q", jobID)
	}
	for {
		select {
		case event := <-events:
			payload, ok := event.Payload.(checkProgressPayload)
			if !ok {
				t.Fatalf("progress payload = %#v", event.Payload)
			}
			if payload.JobID == jobID && payload.Done == 1 && payload.Total == 1 {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for update progress")
		}
	}
}

func TestManagerCheckServiceUpdatePrimaryAndFilters(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/builds"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{serviceRecord(projectID, "api", "builds-api:local", ".")})
	if err := db.Lineage().ReplaceProject(ctx, projectID, []store.LineageRecord{{
		ProviderID:      "linux_native",
		ProjectID:       projectID,
		ServiceID:       projectID + "/api",
		ServiceName:     "api",
		ServiceImageRef: "builds-api:local",
		Source:          models.LineageSourceComposeDockerfile,
		Confidence:      models.ConfidenceHigh,
		BaseRefs: []store.BaseImageRefRecord{
			{Name: "golang", Tag: "1.22", ImageRef: "golang:1.22", StageIndex: 0, BuildTimeDigest: digestA},
			{Name: "alpine", Tag: "3.20", ImageRef: "alpine:3.20", StageIndex: 1, IsFinalStageBase: true, BuildTimeDigest: digestA},
		},
	}}); err != nil {
		t.Fatalf("ReplaceProject(lineage) error = %v", err)
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{digests: map[string]string{
		"golang:1.22": digestB,
		"alpine:3.20": digestD,
	}}, db.Settings(), nil, nil)

	primary, err := manager.CheckServiceUpdate(ctx, projectID, "api")
	if err != nil {
		t.Fatalf("CheckServiceUpdate() error = %v", err)
	}
	if primary.Status != models.UpdateStatusRebuildRequired || primary.BaseImage != "alpine:3.20" {
		t.Fatalf("primary = %#v, want final-stage rebuild", primary)
	}
	filtered, err := manager.ListCurrentUpdates(ctx, models.UpdateFilter{
		ProjectID: projectID,
		Kind:      []models.UpdateKind{models.UpdateKindBaseImage},
		Status:    []models.UpdateStatus{models.UpdateStatusBaseImageUpdateAvailable},
	})
	if err != nil {
		t.Fatalf("ListCurrentUpdates(filter) error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].BaseImage != "golang:1.22" {
		t.Fatalf("filtered = %#v", filtered)
	}
	if _, err := manager.CheckServiceUpdate(ctx, projectID, "missing"); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("missing service error = %v, want not found", err)
	}
}

func TestManagerValidationSchedulerAndHelperPaths(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Manager{}).CheckProjectUpdates(ctx, "p"); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("not-ready check error = %v", err)
	}
	if _, err := ((*Manager)(nil)).ListCurrentUpdates(ctx, models.UpdateFilter{}); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("nil list error = %v", err)
	}
	db := openUpdatesStore(t)
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{pingErr: errors.New("offline")}, &fakeRegistry{}, db.Settings(), nil, nil)
	if err := manager.IgnoreUpdate(ctx, models.IgnoreUpdateRequest{}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ignore validation error = %v", err)
	}
	if err := manager.UnignoreUpdate(ctx, 0); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("unignore validation error = %v", err)
	}
	if !manager.offline(ctx) {
		t.Fatalf("offline() = false, want true")
	}
	if err := db.Settings().SetInt(ctx, "updates.check_interval_hours", 0); err != nil {
		t.Fatalf("SetInt() error = %v", err)
	}
	if _, enabled := manager.schedulerInterval(ctx); enabled {
		t.Fatalf("scheduler enabled for manual-only interval")
	}
	if err := db.Settings().SetInt(ctx, "updates.check_interval_hours", 1); err != nil {
		t.Fatalf("SetInt(1) error = %v", err)
	}
	interval, enabled := manager.schedulerInterval(ctx)
	if !enabled || interval != time.Hour {
		t.Fatalf("scheduler interval = %v/%v, want 1h enabled", interval, enabled)
	}
	manager.JitterFor = func(max time.Duration) time.Duration { return max / 2 }
	if got := manager.jitter(time.Hour); got != 3*time.Minute {
		t.Fatalf("jitter = %v, want 3m", got)
	}

	if got := digestForImageRef([]string{"docker.io/library/nginx@" + digestA}, "nginx:1.25"); got != digestA {
		t.Fatalf("digestForImageRef match = %q", got)
	}
	if got := digestForImageRef([]string{"example.com/team/app@" + digestB}, "nginx:1.25"); got != digestB {
		t.Fatalf("digestForImageRef fallback = %q", got)
	}
	if got := normalizedRepoKey("nginx:1.25"); got != "docker.io/library/nginx" {
		t.Fatalf("normalizedRepoKey = %q", got)
	}
	if got := platformFromString("linux/arm64/v8"); got.OS != "linux" || got.Architecture != "arm64" || got.Variant != "v8" {
		t.Fatalf("platformFromString = %#v", got)
	}
	if !digestsEqual("SHA256:ABC", "sha256:abc") {
		t.Fatalf("digestsEqual should ignore case")
	}
	if got := firstNonEmpty("", " value "); got != "value" {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if got := uniqueNonEmpty("a", "", "a", "b"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("uniqueNonEmpty = %#v", got)
	}
	if primaryUpdate(nil).Status != models.UpdateStatusUnknown {
		t.Fatalf("primaryUpdate(nil) did not return unknown")
	}

	tests := []struct {
		err    error
		status models.UpdateStatus
	}{
		{apperror.New(apperror.RegistryAuth, "auth"), models.UpdateStatusAuthRequired},
		{apperror.New(apperror.RegistryRateLimit, "rate"), models.UpdateStatusRateLimited},
		{apperror.New(apperror.NotFound, "missing"), models.UpdateStatusLocalOnlyImage},
		{errors.New("boom"), models.UpdateStatusError},
	}
	for _, tt := range tests {
		status, action := statusForRegistryError(tt.err)
		if status != tt.status || action != models.RecommendedActionManual {
			t.Fatalf("statusForRegistryError(%v) = %s/%s", tt.err, status, action)
		}
	}
}

func TestManagerBaseImageLocalDigestAndErrorPaths(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/edge"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "local-ok", "edge-ok:local", "."),
		serviceRecord(projectID, "local-drift", "edge-drift:local", "."),
		serviceRecord(projectID, "auth-base", "edge-auth:local", "."),
		serviceRecord(projectID, "missing-base", "edge-missing:local", "."),
		serviceRecord(projectID, "invalid-base", "edge-invalid:local", "."),
		serviceRecord(projectID, "bad-image", "bad image ref", ""),
	})
	if err := db.Lineage().ReplaceProject(ctx, projectID, []store.LineageRecord{
		lineageRecord(projectID, "local-ok", "edge-ok:local", store.BaseImageRefRecord{
			Name: "debian", Tag: "bookworm", ImageRef: "debian:bookworm", IsFinalStageBase: true,
		}),
		lineageRecord(projectID, "local-drift", "edge-drift:local", store.BaseImageRefRecord{
			Name: "node", Tag: "20", ImageRef: "node:20", IsFinalStageBase: true,
		}),
		lineageRecord(projectID, "auth-base", "edge-auth:local", store.BaseImageRefRecord{
			Name: "private/base", Tag: "1", ImageRef: "private.local/base:1", IsFinalStageBase: true, BuildTimeDigest: digestA,
		}),
		lineageRecord(projectID, "missing-base", "edge-missing:local", store.BaseImageRefRecord{
			Name: "missing/base", Tag: "1", ImageRef: "missing.local/base:1", IsFinalStageBase: true, BuildTimeDigest: digestA,
		}),
		lineageRecord(projectID, "invalid-base", "edge-invalid:local", store.BaseImageRefRecord{
			Name: "unresolved", ImageRef: "${BASE}:latest", IsFinalStageBase: true, Status: models.UpdateStatusUnknownBaseImage, Error: "unresolved ARG BASE",
		}),
	}); err != nil {
		t.Fatalf("ReplaceProject(lineage) error = %v", err)
	}
	images := fakeImages{details: map[string]*models.ImageDetail{
		"debian:bookworm": imageDetail("sha256:debian", "docker.io/library/debian@"+digestA),
		"node:20":         imageDetail("sha256:node", "docker.io/library/node@"+digestA),
	}}
	registry := &fakeRegistry{
		digests: map[string]string{
			"debian:bookworm": digestA,
			"node:20":         digestB,
		},
		errs: map[string]error{
			"private.local/base:1": apperror.New(apperror.RegistryAuth, "auth required"),
			"missing.local/base:1": apperror.New(apperror.NotFound, "not found"),
		},
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), images, registry, db.Settings(), nil, nil)

	got, err := manager.CheckProjectUpdates(ctx, projectID)
	if err != nil {
		t.Fatalf("CheckProjectUpdates() error = %v", err)
	}
	byService := updatesByService(got)
	assertStatus(t, byService, "local-ok", models.UpdateKindBaseImage, "debian:bookworm", models.UpdateStatusUpToDate)
	assertStatus(t, byService, "local-drift", models.UpdateKindBaseImage, "node:20", models.UpdateStatusRebuildRequired)
	assertStatus(t, byService, "auth-base", models.UpdateKindBaseImage, "private.local/base:1", models.UpdateStatusAuthRequired)
	assertStatus(t, byService, "missing-base", models.UpdateKindBaseImage, "missing.local/base:1", models.UpdateStatusLocalOnlyImage)
	assertStatus(t, byService, "invalid-base", models.UpdateKindBaseImage, "${BASE}:latest", models.UpdateStatusUnknownBaseImage)
	assertStatus(t, byService, "bad-image", models.UpdateKindServiceImage, "", models.UpdateStatusLocalOnlyImage)

	manager.Discover = failingDiscoverer{}
	if _, err := manager.CheckProjectUpdates(ctx, projectID); err == nil || !strings.Contains(err.Error(), "discover failed") {
		t.Fatalf("discover failure error = %v", err)
	}
}

func TestManagerAdditionalBranchPaths(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/branches"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "nolocal", "httpd:2", ""),
		serviceRecord(projectID, "empty-remote", "empty.local/team/app:1", ""),
	})
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{
		digests:     map[string]string{"httpd:2": digestA},
		emptyResult: map[string]bool{"empty.local/team/app:1": true},
	}, db.Settings(), nil, nil)
	got, err := manager.CheckProjectUpdates(ctx, projectID)
	if err != nil {
		t.Fatalf("CheckProjectUpdates() error = %v", err)
	}
	byService := updatesByService(got)
	assertStatus(t, byService, "nolocal", models.UpdateKindServiceImage, "", models.UpdateStatusUnknown)
	assertStatus(t, byService, "empty-remote", models.UpdateKindServiceImage, "", models.UpdateStatusError)

	history, err := manager.ListUpdateHistory(ctx, models.UpdateHistoryFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListUpdateHistory() error = %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history = %#v, want empty", history)
	}

	offlineBus := bus.New()
	t.Cleanup(offlineBus.Close)
	offlineManager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{pingErr: errors.New("offline")}, &fakeRegistry{}, db.Settings(), offlineBus, nil)
	projects, err := db.Projects().List(ctx)
	if err != nil {
		t.Fatalf("List projects error = %v", err)
	}
	offlineManager.runAllChecks(ctx, "offline", projects)

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	offlineManager.Start(cancelCtx)
}

func TestManagerRemainingErrorAndDefaultBranches(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{details: map[string]*models.ImageDetail{
		"sha256:local": imageDetail("sha256:local", "docker.io/library/alpine@"+digestA),
	}}, &fakeRegistry{
		resultErrs: map[string]registryResultError{
			"partial.local/app:1": {digest: digestB, err: apperror.New(apperror.RegistryRateLimit, "retry later")},
		},
	}, nil, nil, nil)
	if err := manager.IgnoreUpdate(ctx, models.IgnoreUpdateRequest{ID: 404}); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("ignore missing error = %v, want not found", err)
	}
	digest, imageID := manager.localDigest(ctx, "alpine:3.20", "sha256:local")
	if digest != digestA || imageID != "sha256:local" {
		t.Fatalf("localDigest via imageID = %q/%q", digest, imageID)
	}
	remote, err := manager.remoteDigest(ctx, "partial.local/app:1", "linux/amd64")
	if remote != digestB || !apperror.IsCode(err, apperror.RegistryRateLimit) {
		t.Fatalf("remoteDigest partial = %q/%v", remote, err)
	}
	if err := manager.updateBaseRef(ctx, 0, "", "", models.UpdateStatusUnknown, time.Time{}, ""); err != nil {
		t.Fatalf("updateBaseRef zero id error = %v", err)
	}
	interval, enabled := manager.schedulerInterval(ctx)
	if !enabled || interval != 24*time.Hour {
		t.Fatalf("nil-settings scheduler interval = %v/%v", interval, enabled)
	}
	manager.Now = func() time.Time { return time.Unix(0, int64(5*time.Minute)).UTC() }
	if got := manager.jitter(time.Hour); got != 5*time.Minute {
		t.Fatalf("default jitter = %v, want 5m", got)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	manager.runScheduler(cancelCtx)
}

func openUpdatesStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
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

func seedUpdateProject(t *testing.T, ctx context.Context, db *store.Store, projectID string, services []store.ServiceRecord) {
	t.Helper()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if err := db.Projects().SaveSnapshot(ctx, "linux_native", []store.ProjectRecord{{
		ID:           projectID,
		ProviderID:   "linux_native",
		Name:         strings.TrimPrefix(projectID, "linux_native/"),
		WorkingDir:   t.TempDir(),
		ComposeFiles: []string{"compose.yaml"},
		Source:       store.ProjectSourceImported,
		LastSeenAt:   now,
	}}, services, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
}

func serviceRecord(projectID string, name string, image string, buildContext string) store.ServiceRecord {
	return store.ServiceRecord{
		ID:             projectID + "/" + name,
		ProjectID:      projectID,
		Name:           name,
		ImageRef:       image,
		BuildContext:   buildContext,
		DockerfilePath: "Dockerfile",
		LastSeenAt:     time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
	}
}

func lineageRecord(projectID string, service string, image string, base store.BaseImageRefRecord) store.LineageRecord {
	return store.LineageRecord{
		ProviderID:      "linux_native",
		ProjectID:       projectID,
		ServiceID:       projectID + "/" + service,
		ServiceName:     service,
		ServiceImageRef: image,
		Source:          models.LineageSourceComposeDockerfile,
		Confidence:      models.ConfidenceMedium,
		BaseRefs:        []store.BaseImageRefRecord{base},
	}
}

func imageDetail(id string, repoDigest string) *models.ImageDetail {
	return &models.ImageDetail{Summary: models.ImageSummary{ID: id, RepoDigests: []string{repoDigest}}}
}

func updatesByService(updates []models.ImageUpdate) map[string][]models.ImageUpdate {
	result := map[string][]models.ImageUpdate{}
	for _, update := range updates {
		result[update.Service] = append(result[update.Service], update)
	}
	return result
}

func assertStatus(t *testing.T, byService map[string][]models.ImageUpdate, service string, kind models.UpdateKind, base string, status models.UpdateStatus) {
	t.Helper()
	for _, update := range byService[service] {
		if update.Kind == kind && update.BaseImage == base && update.Status == status {
			return
		}
	}
	t.Fatalf("%s missing %s/%s status %s in %#v", service, kind, base, status, byService[service])
}

func hasNoteContaining(update models.ImageUpdate, want string) bool {
	for _, note := range update.Notes {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}

type fakeRegistry struct {
	digests     map[string]string
	errs        map[string]error
	emptyResult map[string]bool
	resultErrs  map[string]registryResultError
}

type registryResultError struct {
	digest string
	err    error
}

func (r *fakeRegistry) ResolveDigest(_ context.Context, image string, _ registrycore.ResolveOptions) (*registrycore.DigestResult, error) {
	if resultErr, ok := r.resultErrs[image]; ok {
		return &registrycore.DigestResult{ManifestDigest: resultErr.digest}, resultErr.err
	}
	if err := r.errs[image]; err != nil {
		return nil, err
	}
	if r.emptyResult[image] {
		return &registrycore.DigestResult{}, nil
	}
	if digest := r.digests[image]; digest != "" {
		return &registrycore.DigestResult{ManifestDigest: digest}, nil
	}
	return nil, apperror.New(apperror.NotFound, "not found")
}

type fakeImages struct {
	details map[string]*models.ImageDetail
	pingErr error
}

func (i fakeImages) GetImage(_ context.Context, id string) (*models.ImageDetail, error) {
	if detail := i.details[id]; detail != nil {
		return detail, nil
	}
	return nil, apperror.New(apperror.NotFound, "image not found")
}

func (i fakeImages) Ping(context.Context) error {
	if i.pingErr != nil {
		return i.pingErr
	}
	return nil
}

type fakeUpdateCompose struct {
	calls []string
	errs  map[string]error
}

func (f *fakeUpdateCompose) PullServices(_ context.Context, _ composecore.ProjectOptions, services []string) (*providers.CommandResult, error) {
	call := "pull:" + strings.Join(services, ",")
	f.calls = append(f.calls, call)
	return &providers.CommandResult{Stdout: call + "\n"}, f.errs[call]
}

func (f *fakeUpdateCompose) Build(_ context.Context, _ composecore.ProjectOptions, build composecore.BuildOptions) (*providers.CommandResult, error) {
	call := "build:" + strings.Join(build.Services, ",")
	f.calls = append(f.calls, call)
	return &providers.CommandResult{Stdout: call + "\n"}, f.errs[call]
}

func (f *fakeUpdateCompose) UpServices(_ context.Context, _ composecore.ProjectOptions, up composecore.UpOptions) (*providers.CommandResult, error) {
	prefix := "up:"
	if up.NoBuild {
		prefix = "up-no-build:"
	}
	call := prefix + strings.Join(up.Services, ",")
	f.calls = append(f.calls, call)
	return &providers.CommandResult{Stdout: call + "\n"}, f.errs[call]
}

func (f *fakeUpdateCompose) Config(context.Context, composecore.ProjectOptions) (*composecore.ConfigResult, error) {
	return &composecore.ConfigResult{Valid: true}, nil
}

type fakeUpdateDocker struct {
	images     map[string]*models.ImageDetail
	containers []models.ContainerSummary
	details    map[string]*models.ContainerDetail
	logs       map[string]string
	tags       []string
}

func (f *fakeUpdateDocker) ProviderID() string {
	return "linux_native"
}

func (f *fakeUpdateDocker) GetImage(_ context.Context, id string) (*models.ImageDetail, error) {
	if detail := f.images[id]; detail != nil {
		return detail, nil
	}
	return nil, apperror.New(apperror.NotFound, "image not found")
}

func (f *fakeUpdateDocker) ListContainers(_ context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	out := make([]models.ContainerSummary, 0, len(f.containers))
	for _, container := range f.containers {
		if opts.ProjectID != "" && container.ProjectID != opts.ProjectID {
			continue
		}
		if opts.Service != "" && container.Service != opts.Service {
			continue
		}
		out = append(out, container)
	}
	return out, nil
}

func (f *fakeUpdateDocker) GetContainer(_ context.Context, id string) (*models.ContainerDetail, error) {
	if detail := f.details[id]; detail != nil {
		return detail, nil
	}
	for _, container := range f.containers {
		if container.ID == id {
			return &models.ContainerDetail{Summary: container}, nil
		}
	}
	return nil, apperror.New(apperror.NotFound, "container not found")
}

func (f *fakeUpdateDocker) TagImage(_ context.Context, imageID string, ref string) error {
	if _, err := f.GetImage(context.Background(), imageID); err != nil {
		return err
	}
	f.tags = append(f.tags, imageID+"->"+ref)
	return nil
}

func (f *fakeUpdateDocker) ContainerLogs(_ context.Context, id string, _ dockercore.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.logs[id])), nil
}

type fakeUpdateBackups struct {
	volumes []string
	err     error
}

func (f *fakeUpdateBackups) RunBackupVolume(_ context.Context, req models.BackupVolumeRequest) error {
	f.volumes = append(f.volumes, req.VolumeName)
	return f.err
}

type failingDiscoverer struct{}

func (failingDiscoverer) DiscoverProjectLineage(context.Context, string) ([]models.ImageLineage, error) {
	return nil, errors.New("discover failed")
}

func insertCheck(t *testing.T, ctx context.Context, db *store.Store, record store.UpdateCheckRecord) int64 {
	t.Helper()
	id, err := db.Updates().InsertCheck(ctx, record)
	if err != nil {
		t.Fatalf("InsertCheck() error = %v", err)
	}
	return id
}

func commandTexts(commands []models.PlannedCommand) []string {
	result := make([]string, 0, len(commands))
	for _, command := range commands {
		result = append(result, command.Command)
	}
	return result
}

func warningsContain(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func countCall(calls []string, want string) int {
	count := 0
	for _, call := range calls {
		if call == want {
			count++
		}
	}
	return count
}

func waitUpdateDone(t *testing.T, events <-chan bus.Event, result string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			payload, ok := event.Payload.(jobDonePayload)
			if !ok {
				continue
			}
			if payload.Result != result {
				t.Fatalf("job result = %q, want %q (error %q)", payload.Result, result, payload.Error)
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for update job result %q", result)
		}
	}
}
