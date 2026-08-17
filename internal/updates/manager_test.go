package updates

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/store"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	digestC = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	digestD = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func TestManagerStopAllCancelsManagedJobs(t *testing.T) {
	t.Parallel()
	manager := NewManager(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	started := make(chan struct{})
	done := make(chan error, 1)
	if !manager.startJob("updates-test", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		done <- ctx.Err()
	}) {
		t.Fatal("startJob() rejected an active manager")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for managed job to start")
	}
	manager.StopAll()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("job context error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for managed job cancellation")
	}
}

func TestManagerCancelJobCancelsOnlyTheRequestedManagedJob(t *testing.T) {
	t.Parallel()
	manager := NewManager(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	firstStarted := make(chan struct{})
	firstDone := make(chan error, 1)
	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	if !manager.startJob("updates-first", func(ctx context.Context) {
		close(firstStarted)
		<-ctx.Done()
		firstDone <- ctx.Err()
	}) {
		t.Fatal("startJob(first) rejected an active manager")
	}
	if !manager.startJob("updates-second", func(ctx context.Context) {
		close(secondStarted)
		<-ctx.Done()
		secondDone <- ctx.Err()
	}) {
		t.Fatal("startJob(second) rejected an active manager")
	}
	t.Cleanup(manager.StopAll)

	for _, started := range []<-chan struct{}{firstStarted, secondStarted} {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for managed job to start")
		}
	}
	if err := manager.CancelJob("updates-first"); err != nil {
		t.Fatalf("CancelJob(first) error = %v", err)
	}
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first job context error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for requested job cancellation")
	}
	select {
	case err := <-secondDone:
		t.Fatalf("unrequested job was canceled: %v", err)
	default:
	}
	if err := manager.CancelJob("missing"); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("CancelJob(missing) error = %v, want NotFound", err)
	}
}

func TestManagerStopAllJoinsJobsAndRejectsNewAdmission(t *testing.T) {
	t.Parallel()
	manager := NewManager(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	started := make(chan struct{})
	returned := make(chan struct{})
	if !manager.startJob("updates-join", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(returned)
	}) {
		t.Fatal("startJob() rejected an active manager")
	}
	<-started

	manager.StopAll()
	select {
	case <-returned:
	default:
		t.Fatal("StopAll() returned before the canceled worker exited")
	}
	if manager.startJob("updates-late", func(context.Context) {}) {
		t.Fatal("startJob() admitted work after StopAll")
	}
}

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
	manager.Scope = runtimescope.Must("linux_native", "default")

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

func TestManagerCheckProjectReplacesChangedImageReferenceGeneration(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/reference-change"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.25", ""),
	})
	images := fakeImages{details: map[string]*models.ImageDetail{
		"nginx:1.25": imageDetail("sha256:old", "docker.io/library/nginx@"+digestA),
		"nginx:1.26": imageDetail("sha256:new", "docker.io/library/nginx@"+digestC),
	}}
	registry := &fakeRegistry{digests: map[string]string{
		"nginx:1.25": digestB,
		"nginx:1.26": digestD,
	}}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), images, registry, db.Settings(), nil, nil)
	manager.Scope = runtimescope.Must("linux_native", "default")
	manager.Now = func() time.Time { return time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC) }

	first, err := manager.CheckProjectUpdates(ctx, projectID)
	if err != nil {
		t.Fatalf("CheckProjectUpdates(first) error = %v", err)
	}
	if len(first) != 1 || first[0].CurrentImage != "nginx:1.25" {
		t.Fatalf("first current checks = %#v", first)
	}

	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.26", ""),
	})
	manager.Now = func() time.Time { return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC) }
	second, err := manager.CheckProjectUpdates(ctx, projectID)
	if err != nil {
		t.Fatalf("CheckProjectUpdates(second) error = %v", err)
	}
	if len(second) != 1 || second[0].CurrentImage != "nginx:1.26" {
		t.Fatalf("second current checks = %#v, want changed reference only", second)
	}
}

func TestManagerSupersededRunReturnsNewerCurrentState(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/overlapping-managers"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.25", ""),
	})
	images := fakeImages{details: map[string]*models.ImageDetail{
		"nginx:1.25": imageDetail("sha256:web", "docker.io/library/nginx@"+digestA),
	}}
	releaseOld := make(chan struct{})
	oldRegistry := &controlledRegistry{digest: digestB, calls: make(chan struct{}, 1), release: releaseOld}
	oldManager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), images, oldRegistry, db.Settings(), nil, nil)
	oldManager.Scope = runtimescope.Must("linux_native", "default")
	oldManager.Now = func() time.Time { return time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC) }
	newManager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), images, &fakeRegistry{digests: map[string]string{"nginx:1.25": digestD}}, db.Settings(), nil, nil)
	newManager.Scope = oldManager.Scope
	newManager.Now = func() time.Time { return time.Date(2026, 7, 17, 11, 1, 0, 0, time.UTC) }

	type checkResult struct {
		updates []models.ImageUpdate
		err     error
	}
	oldDone := make(chan checkResult, 1)
	go func() {
		updates, err := oldManager.CheckProjectUpdates(ctx, projectID)
		oldDone <- checkResult{updates: updates, err: err}
	}()
	select {
	case <-oldRegistry.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("older run did not reach controlled registry")
	}
	newer, err := newManager.CheckProjectUpdates(ctx, projectID)
	if err != nil || len(newer) != 1 || newer[0].RemoteDigest != digestD {
		t.Fatalf("newer manager result = %#v/%v", newer, err)
	}
	close(releaseOld)
	select {
	case result := <-oldDone:
		if result.err != nil || len(result.updates) != 1 || result.updates[0].RemoteDigest != digestD {
			t.Fatalf("superseded older caller returned stale computation = %#v/%v", result.updates, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("older run did not finish after release")
	}
}

func TestManagerSupersededServiceRunReturnsNewerProjectState(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/overlapping-service-project"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.25", ""),
	})
	images := fakeImages{details: map[string]*models.ImageDetail{
		"nginx:1.25": imageDetail("sha256:web", "docker.io/library/nginx@"+digestA),
	}}
	releaseOld := make(chan struct{})
	oldRegistry := &controlledRegistry{digest: digestB, calls: make(chan struct{}, 1), release: releaseOld}
	oldManager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), images, oldRegistry, db.Settings(), nil, nil)
	oldManager.Scope = runtimescope.Must("linux_native", "default")
	oldManager.Now = func() time.Time { return time.Date(2026, 7, 17, 11, 10, 0, 0, time.UTC) }
	newManager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), images, &fakeRegistry{digests: map[string]string{"nginx:1.25": digestD}}, db.Settings(), nil, nil)
	newManager.Scope = oldManager.Scope
	newManager.Now = func() time.Time { return time.Date(2026, 7, 17, 11, 11, 0, 0, time.UTC) }

	type serviceCheckResult struct {
		update *models.ImageUpdate
		err    error
	}
	oldDone := make(chan serviceCheckResult, 1)
	go func() {
		update, err := oldManager.CheckServiceUpdate(ctx, projectID, "web")
		oldDone <- serviceCheckResult{update: update, err: err}
	}()
	select {
	case <-oldRegistry.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("older service run did not reach controlled registry")
	}
	newer, err := newManager.CheckProjectUpdates(ctx, projectID)
	if err != nil || len(newer) != 1 || newer[0].RemoteDigest != digestD {
		t.Fatalf("newer project result = %#v/%v", newer, err)
	}
	close(releaseOld)
	select {
	case result := <-oldDone:
		if result.err != nil || result.update == nil || result.update.RemoteDigest != digestD {
			t.Fatalf("superseded service caller returned stale computation = %#v/%v", result.update, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("older service run did not finish after release")
	}
}

func TestManagerSerializesProjectAndServiceChecksPerProject(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/serialized-checks"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.25", ""),
	})
	images := fakeImages{details: map[string]*models.ImageDetail{
		"nginx:1.25": imageDetail("sha256:web", "docker.io/library/nginx@"+digestA),
	}}
	release := make(chan struct{})
	registry := &controlledRegistry{digest: digestB, calls: make(chan struct{}, 2), release: release}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), images, registry, db.Settings(), nil, nil)
	manager.Scope = runtimescope.Must("linux_native", "default")

	projectDone := make(chan error, 1)
	go func() {
		_, err := manager.CheckProjectUpdates(ctx, projectID)
		projectDone <- err
	}()
	select {
	case <-registry.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("project check did not reach controlled registry")
	}
	serviceDone := make(chan error, 1)
	go func() {
		_, err := manager.CheckServiceUpdate(ctx, projectID, "web")
		serviceDone <- err
	}()
	select {
	case <-registry.calls:
		t.Fatal("service check entered evaluation before the project check released its keyed lock")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-projectDone:
		if err != nil {
			t.Fatalf("project check error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project check did not finish")
	}
	select {
	case <-registry.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("service check did not start after project check released its keyed lock")
	}
	select {
	case err := <-serviceDone:
		if err != nil {
			t.Fatalf("service check error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("service check did not finish")
	}
}

func TestManagerServiceImageAcceptsRemoteIndexDigestMatch(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/app"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.25", ""),
	})
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{details: map[string]*models.ImageDetail{
		"nginx:1.25": imageDetail("sha256:web", "docker.io/library/nginx@"+digestA),
	}}, &fakeRegistry{
		digests:      map[string]string{"nginx:1.25": digestB},
		indexDigests: map[string]string{"nginx:1.25": digestA},
	}, db.Settings(), nil, nil)
	manager.Scope = runtimescope.Must("linux_native", "default")

	got, err := manager.CheckProjectUpdates(ctx, projectID)
	if err != nil {
		t.Fatalf("CheckProjectUpdates() error = %v", err)
	}
	byService := updatesByService(got)
	assertStatus(t, byService, "web", models.UpdateKindServiceImage, "", models.UpdateStatusUpToDate)
	if byService["web"][0].RemoteDigest != digestB {
		t.Fatalf("remote digest = %q, want platform manifest %q", byService["web"][0].RemoteDigest, digestB)
	}
}

func TestManagerServiceImageUsesDockerEnginePlatform(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "windows_wsl_ubuntu/app"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.25", ""),
	})
	images := fakeImages{
		details: map[string]*models.ImageDetail{
			"nginx:1.25": imageDetail("sha256:web", "docker.io/library/nginx@"+digestA),
		},
		info: &models.DockerInfo{
			OperatingSystem: "Ubuntu 24.04.2 LTS",
			Architecture:    "x86_64",
		},
	}
	registry := &fakeRegistry{digests: map[string]string{"nginx:1.25": digestB}}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), images, registry, db.Settings(), nil, nil)
	manager.Scope = runtimescope.Must("linux_native", "default")

	if _, err := manager.CheckProjectUpdates(ctx, projectID); err != nil {
		t.Fatalf("CheckProjectUpdates() error = %v", err)
	}

	got := registry.platforms["nginx:1.25"]
	if got.OS != "linux" || got.Architecture != "amd64" || got.Variant != "" {
		t.Fatalf("registry platform = %#v, want linux/amd64", got)
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
		ContextName:     "default",
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
	manager.Scope = runtimescope.Must("linux_native", "default")

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
	manager.Scope = runtimescope.Must("linux_native", "default")

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

func TestManagerIgnoreAndUnignoreRejectForeignRuntimeScope(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	now := time.Date(2026, 6, 13, 12, 15, 0, 0, time.UTC)
	foreignScope := runtimescope.Must("linux_native", "socket:foreign")
	foreignProjectID := "linux_native/foreign"
	if err := db.Projects().SaveSnapshot(ctx, foreignScope, []store.ProjectRecord{{
		ID: foreignProjectID, ProviderID: "linux_native", ContextName: foreignScope.ContextName(), Name: "foreign", WorkingDir: t.TempDir(), LastSeenAt: now,
	}}, []store.ServiceRecord{serviceRecord(foreignProjectID, "web", "nginx:1.25", "")}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(foreign) error = %v", err)
	}
	checkID := insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID: "linux_native", ProjectID: foreignProjectID, ServiceID: foreignProjectID + "/web",
		Kind: models.UpdateKindServiceImage, ImageRef: "nginx:1.25", Status: models.UpdateStatusServiceImageUpdateAvailable, CheckedAt: now,
	})
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)
	manager.Scope = runtimescope.Must("linux_native", "default")

	if err := manager.IgnoreUpdate(ctx, models.IgnoreUpdateRequest{ID: checkID, Reason: "foreign"}); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("IgnoreUpdate(foreign) error = %v, want not found", err)
	}
	if err := db.Updates().IgnoreCheck(ctx, checkID, "seed ignored", now); err != nil {
		t.Fatalf("IgnoreCheck(seed) error = %v", err)
	}
	ignored, err := db.Updates().ListCurrent(ctx, models.UpdateFilter{ProjectID: foreignProjectID, Status: []models.UpdateStatus{models.UpdateStatusIgnored}})
	if err != nil || len(ignored) != 1 {
		t.Fatalf("ListCurrent(foreign ignored) = %#v, %v", ignored, err)
	}
	ignoreID := ignored[0].ID
	if err := manager.UnignoreUpdate(ctx, ignoreID); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("UnignoreUpdate(foreign) error = %v, want not found", err)
	}
	if _, err := db.Updates().GetIgnored(ctx, ignoreID); err != nil {
		t.Fatalf("foreign ignore rule was deleted: %v", err)
	}
}

func TestManagerListCurrentUpdatesScopesToActiveBackendContext(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	providerID := "windows_wsl_ubuntu"
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
		ID:          providerID,
		Type:        providerID,
		Platform:    "windows",
		DisplayName: "Windows WSL Ubuntu",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed windows provider: %v", err)
	}
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	ubuntuProjectID := providerID + "/ubuntu-app"
	cairnProjectID := providerID + "/cairn-app"
	if err := db.Projects().SaveSnapshot(ctx, runtimescope.Must(providerID, "wsl:Ubuntu"), []store.ProjectRecord{
		{
			ID:           ubuntuProjectID,
			ProviderID:   providerID,
			ContextName:  "wsl:Ubuntu",
			Name:         "ubuntu-app",
			WorkingDir:   t.TempDir(),
			ComposeFiles: []string{"compose.yaml"},
			Source:       store.ProjectSourceImported,
			LastSeenAt:   now,
		},
	}, []store.ServiceRecord{
		serviceRecord(ubuntuProjectID, "web", "nginx:1.25", ""),
	}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(Ubuntu) error = %v", err)
	}
	if err := db.Projects().SaveSnapshot(ctx, runtimescope.Must(providerID, "wsl:cairn-dev"), []store.ProjectRecord{
		{
			ID:           cairnProjectID,
			ProviderID:   providerID,
			ContextName:  "wsl:cairn-dev",
			Name:         "cairn-app",
			WorkingDir:   t.TempDir(),
			ComposeFiles: []string{"compose.yaml"},
			Source:       store.ProjectSourceImported,
			LastSeenAt:   now,
		},
	}, []store.ServiceRecord{
		serviceRecord(cairnProjectID, "api", "redis:7", ""),
	}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        providerID,
		ProjectID:         ubuntuProjectID,
		ServiceID:         ubuntuProjectID + "/web",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "nginx:1.25",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
		CheckedAt:         now,
	})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        providerID,
		ProjectID:         cairnProjectID,
		ServiceID:         cairnProjectID + "/api",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "redis:7",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
		CheckedAt:         now,
	})
	if _, err := db.Updates().InsertHistory(ctx, store.UpdateHistoryRecord{
		ProviderID:   providerID,
		ContextName:  "wsl:Ubuntu",
		ProjectID:    ubuntuProjectID,
		ServiceID:    ubuntuProjectID + "/web",
		UpdateKind:   models.UpdateKindServiceImage,
		ImageRef:     "nginx:1.25",
		Result:       "success",
		StartedAt:    now,
		FinishedAt:   now.Add(time.Minute),
		OldDigest:    digestA,
		NewDigest:    digestB,
		OldImageID:   "sha256:ubuntu-old",
		NewImageID:   "sha256:ubuntu-new",
		HealthResult: "healthy",
	}); err != nil {
		t.Fatalf("InsertHistory(ubuntu) error = %v", err)
	}
	if _, err := db.Updates().InsertHistory(ctx, store.UpdateHistoryRecord{
		ProviderID:   providerID,
		ContextName:  "wsl:cairn-dev",
		ProjectID:    cairnProjectID,
		ServiceID:    cairnProjectID + "/api",
		UpdateKind:   models.UpdateKindServiceImage,
		ImageRef:     "redis:7",
		Result:       "success",
		StartedAt:    now,
		FinishedAt:   now.Add(time.Minute),
		OldDigest:    digestA,
		NewDigest:    digestB,
		OldImageID:   "sha256:cairn-old",
		NewImageID:   "sha256:cairn-new",
		HealthResult: "healthy",
	}); err != nil {
		t.Fatalf("InsertHistory(cairn-dev) error = %v", err)
	}
	// A newer foreign-context row must not consume the SQL LIMIT before scope
	// filtering; otherwise the active context incorrectly appears empty.
	if _, err := db.Updates().InsertHistory(ctx, store.UpdateHistoryRecord{
		ProviderID:   providerID,
		ContextName:  "wsl:Ubuntu",
		ProjectID:    ubuntuProjectID,
		ServiceID:    ubuntuProjectID + "/newer",
		UpdateKind:   models.UpdateKindServiceImage,
		ImageRef:     "busybox:latest",
		Result:       "success",
		StartedAt:    now.Add(2 * time.Hour),
		FinishedAt:   now.Add(2*time.Hour + time.Minute),
		HealthResult: "healthy",
	}); err != nil {
		t.Fatalf("InsertHistory(newer ubuntu) error = %v", err)
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), &fakeUpdateDocker{
		providerID: providerID,
		images:     map[string]*models.ImageDetail{},
		details:    map[string]*models.ContainerDetail{},
	}, &fakeRegistry{}, db.Settings(), nil, nil)
	manager.Scope = runtimescope.Must(providerID, "wsl:cairn-dev")

	current, err := manager.ListCurrentUpdates(ctx, models.UpdateFilter{})
	if err != nil {
		t.Fatalf("ListCurrentUpdates() error = %v", err)
	}
	if len(current) != 1 || current[0].ProjectID != cairnProjectID {
		t.Fatalf("current = %#v, want only %s", current, cairnProjectID)
	}
	stale, err := manager.ListCurrentUpdates(ctx, models.UpdateFilter{ProjectID: ubuntuProjectID})
	if !apperror.IsCode(err, apperror.NotFound) || stale != nil {
		t.Fatalf("ListCurrentUpdates(stale project) = %#v, %v; want not found", stale, err)
	}
	history, err := manager.ListUpdateHistory(ctx, models.UpdateHistoryFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListUpdateHistory() error = %v", err)
	}
	if len(history) != 1 || history[0].ProjectID != cairnProjectID {
		t.Fatalf("history = %#v, want only %s", history, cairnProjectID)
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
	manager.Scope = runtimescope.Must("linux_native", "default")

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

func TestManagerApplyUpdateRevalidatesPlanRuntimeScope(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/revalidate"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{serviceRecord(projectID, "web", "nginx:1.25", "")})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID: "linux_native", ProjectID: projectID, ServiceID: projectID + "/web",
		Kind: models.UpdateKindServiceImage, ImageRef: "nginx:1.25", LocalDigest: digestA, RemoteDigest: digestB,
		RecommendedAction: models.RecommendedActionPullRecreate, Status: models.UpdateStatusServiceImageUpdateAvailable,
	})
	compose := &fakeUpdateCompose{}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)
	manager.Scope = runtimescope.Must("linux_native", "default")
	manager.Compose = compose
	plan, err := manager.PlanProjectUpdate(ctx, projectID)
	if err != nil {
		t.Fatalf("PlanProjectUpdate() error = %v", err)
	}
	manager.Scope = runtimescope.Must("linux_native", "socket:other")
	if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID}); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("ApplyUpdate(after scope change) error = %v, want not found", err)
	}
	if len(compose.calls) != 0 {
		t.Fatalf("Compose ran after scope change: %#v", compose.calls)
	}
}

func TestManagerApplyUpdateRejectsPlanFromDeletedProjectIncarnation(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	scope := runtimescope.Must("linux_native", "default")
	projectID := "linux_native/reincarnated-plan"
	service := serviceRecord(projectID, "web", "nginx:1.25", "")
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{service})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        scope.ProviderID(),
		ContextName:       scope.ContextName(),
		ProjectID:         projectID,
		ServiceID:         service.ID,
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          service.ImageRef,
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	compose := &fakeUpdateCompose{}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)
	manager.Scope = scope
	manager.Compose = compose
	plan, err := manager.PlanProjectUpdate(ctx, projectID)
	if err != nil {
		t.Fatalf("PlanProjectUpdate() error = %v", err)
	}
	project, err := db.Projects().GetInScope(ctx, scope, projectID)
	if err != nil {
		t.Fatalf("GetInScope() error = %v", err)
	}
	if err := db.Projects().DeleteInScope(ctx, scope, projectID); err != nil {
		t.Fatalf("DeleteInScope() error = %v", err)
	}
	project.LastSeenAt = project.LastSeenAt.Add(time.Hour)
	service.LastSeenAt = project.LastSeenAt
	if err := db.Projects().SaveSnapshot(ctx, scope, []store.ProjectRecord{project}, []store.ServiceRecord{service}, project.LastSeenAt, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(reimport) error = %v", err)
	}
	if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID}); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("ApplyUpdate(old incarnation plan) error = %v, want PlanExpired", err)
	}
	if len(compose.calls) != 0 {
		t.Fatalf("Compose ran for old incarnation plan: %#v", compose.calls)
	}
}

func TestManagerProjectMutationAdmissionIsExclusiveAndInvalidatesPreexistingPlans(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openUpdatesStore(t)
	scope := runtimescope.Must("linux_native", "default")
	projectID := "linux_native/exclusive-update"
	service := serviceRecord(projectID, "web", "nginx:1.25", "")
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{service})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        scope.ProviderID(),
		ContextName:       scope.ContextName(),
		ProjectID:         projectID,
		ServiceID:         service.ID,
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          service.ImageRef,
		LocalImageID:      "sha256:old",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	historyID, err := db.Updates().InsertHistoryInScope(ctx, scope, store.UpdateHistoryRecord{
		ProviderID:     scope.ProviderID(),
		ContextName:    scope.ContextName(),
		ProjectID:      projectID,
		ServiceID:      service.ID,
		UpdateKind:     models.UpdateKindServiceImage,
		ImageRef:       service.ImageRef,
		OldImageID:     "sha256:rollback-old",
		OldDigest:      digestA,
		NewImageID:     "sha256:rollback-new",
		NewDigest:      digestB,
		Result:         updateResultSuccess,
		RollbackStatus: rollbackStatusAvailable,
		StartedAt:      time.Now().UTC().Add(-time.Minute),
		FinishedAt:     time.Now().UTC().Add(-30 * time.Second),
	})
	if err != nil {
		t.Fatalf("InsertHistoryInScope() error = %v", err)
	}

	eventBus := bus.New()
	t.Cleanup(eventBus.Close)
	doneEvents := eventBus.Subscribe(ctx, bus.TopicJobDone, 4)
	compose := newBlockingUpdateCompose()
	docker := &fakeUpdateDocker{images: map[string]*models.ImageDetail{
		"sha256:rollback-old": imageDetail("sha256:rollback-old", "docker.io/library/nginx@"+digestA),
	}}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), eventBus, nil)
	manager.Scope = scope
	manager.Compose = compose
	t.Cleanup(manager.StopAll)

	plans := make([]*models.UpdatePlan, 3)
	for i := range plans {
		plan, err := manager.PlanProjectUpdate(ctx, projectID)
		if err != nil {
			t.Fatalf("PlanProjectUpdate(%d) error = %v", i, err)
		}
		plans[i] = plan
	}
	rollbackPlan, err := manager.PlanRollback(ctx, historyID)
	if err != nil {
		t.Fatalf("PlanRollback() error = %v", err)
	}
	jobID, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plans[0].PlanID})
	if err != nil {
		t.Fatalf("ApplyUpdate(first) error = %v", err)
	}
	select {
	case <-compose.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first project mutation")
	}

	if _, err := manager.PlanProjectUpdate(ctx, projectID); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("PlanProjectUpdate(active mutation) error = %v, want Conflict", err)
	}
	if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plans[1].PlanID}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyUpdate(concurrent plan) error = %v, want Conflict", err)
	}
	if _, err := manager.ApplyRollback(ctx, rollbackPlan.PlanID); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyRollback(concurrent plan) error = %v, want Conflict", err)
	}
	if err := manager.CancelJob(jobID); err != nil {
		t.Fatalf("CancelJob(first) error = %v", err)
	}
	select {
	case <-compose.returned:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled mutation to return")
	}
	waitUpdateDone(t, doneEvents, updateResultFailed)

	if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plans[2].PlanID}); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("ApplyUpdate(preexisting plan after mutation) error = %v, want PlanExpired", err)
	}
	if got := compose.Calls(); strings.Join(got, ",") != "pull:web" {
		t.Fatalf("Compose calls = %#v, want one admitted mutation", got)
	}
}

func TestManagerApplyUpdateRejectsChangedConfirmedConfiguration(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*store.ProjectRecord, []store.ServiceRecord, *testing.T)
	}{
		{
			name: "compose target",
			mutate: func(project *store.ProjectRecord, _ []store.ServiceRecord, t *testing.T) {
				project.WorkingDir = t.TempDir()
				project.ComposeFiles = []string{"compose.changed.yaml"}
			},
		},
		{
			name: "service configuration",
			mutate: func(_ *store.ProjectRecord, services []store.ServiceRecord, _ *testing.T) {
				services[0].ImageRef = "nginx:1.26"
				services[0].BuildTarget = "changed"
			},
		},
		{
			name: "compose input closure",
			mutate: func(project *store.ProjectRecord, _ []store.ServiceRecord, t *testing.T) {
				path := filepath.Join(project.WorkingDir, "compose.yaml")
				if err := os.WriteFile(path, []byte("services:\n  web:\n    image: nginx:1.26\n"), 0o600); err != nil {
					t.Fatalf("mutate Compose input: %v", err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			db := openUpdatesStore(t)
			scope := runtimescope.Must("linux_native", "default")
			projectID := "linux_native/fingerprint-" + strings.ReplaceAll(testCase.name, " ", "-")
			service := serviceRecord(projectID, "web", "nginx:1.25", "")
			seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{service})
			insertCheck(t, ctx, db, store.UpdateCheckRecord{
				ProviderID:        scope.ProviderID(),
				ContextName:       scope.ContextName(),
				ProjectID:         projectID,
				ServiceID:         service.ID,
				Kind:              models.UpdateKindServiceImage,
				ImageRef:          service.ImageRef,
				LocalDigest:       digestA,
				RemoteDigest:      digestB,
				RecommendedAction: models.RecommendedActionPullRecreate,
				Status:            models.UpdateStatusServiceImageUpdateAvailable,
			})
			compose := &fakeUpdateCompose{}
			manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)
			manager.Scope = scope
			manager.Compose = compose

			plan, err := manager.PlanProjectUpdate(ctx, projectID)
			if err != nil {
				t.Fatalf("PlanProjectUpdate() error = %v", err)
			}
			project, services, err := manager.projectWithServices(ctx, projectID)
			if err != nil {
				t.Fatalf("projectWithServices() error = %v", err)
			}
			testCase.mutate(&project, services, t)
			project.LastSeenAt = project.LastSeenAt.Add(time.Minute)
			for i := range services {
				services[i].LastSeenAt = project.LastSeenAt
			}
			if err := db.Projects().SaveSnapshot(ctx, scope, []store.ProjectRecord{project}, services, project.LastSeenAt, time.Time{}); err != nil {
				t.Fatalf("SaveSnapshot(changed configuration) error = %v", err)
			}

			if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID}); !apperror.IsCode(err, apperror.PlanExpired) {
				t.Fatalf("ApplyUpdate(changed configuration) error = %v, want PlanExpired", err)
			}
			if len(compose.calls) != 0 {
				t.Fatalf("Compose ran after configuration changed: %#v", compose.calls)
			}
		})
	}
}

func TestManagerPlanUpdateClassifiesUnverifiableComposeInputs(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	scope := runtimescope.Must("linux_native", "default")
	projectID := "linux_native/unverifiable-compose"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{serviceRecord(projectID, "web", "nginx:1.25", "")})
	project, err := db.Projects().GetInScope(ctx, scope, projectID)
	if err != nil {
		t.Fatalf("GetInScope(project) error = %v", err)
	}
	if err := os.Remove(filepath.Join(project.WorkingDir, "compose.yaml")); err != nil {
		t.Fatalf("remove Compose fixture: %v", err)
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)
	manager.Scope = scope

	_, err = manager.PlanProjectUpdate(ctx, projectID)
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("PlanProjectUpdate(missing Compose input) error = %v, want Conflict", err)
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || len(appErr.RepairHints) == 0 {
		t.Fatalf("PlanProjectUpdate(missing Compose input) error = %#v, want repair hint", err)
	}
	if strings.Contains(err.Error(), project.WorkingDir) {
		t.Fatalf("public error leaked Compose path: %v", err)
	}
}

func TestManagerUpdateWorkerRevalidatesComposeInputsAfterBackup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openUpdatesStore(t)
	scope := runtimescope.Must("linux_native", "default")
	projectID := "linux_native/worker-fingerprint"
	service := serviceRecord(projectID, "web", "nginx:1.25", "")
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{service})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        scope.ProviderID(),
		ContextName:       scope.ContextName(),
		ProjectID:         projectID,
		ServiceID:         service.ID,
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          service.ImageRef,
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	project, err := db.Projects().GetInScope(ctx, scope, projectID)
	if err != nil {
		t.Fatalf("GetInScope(project) error = %v", err)
	}
	eventBus := bus.New()
	t.Cleanup(eventBus.Close)
	doneEvents := eventBus.Subscribe(ctx, bus.TopicJobDone, 2)
	backups := newBlockingUpdateBackups()
	compose := &fakeUpdateCompose{}
	docker := &fakeUpdateDocker{
		containers: []models.ContainerSummary{{
			ID:        "container-web",
			ProjectID: projectID,
			Service:   "web",
			State:     "running",
		}},
		details: map[string]*models.ContainerDetail{
			"container-web": {
				Summary: models.ContainerSummary{ID: "container-web"},
				Mounts:  []models.MountSpec{{Type: "volume", VolumeName: "web-data", Target: "/data"}},
			},
		},
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), eventBus, nil)
	manager.Scope = scope
	manager.Compose = compose
	manager.Backups = backups
	t.Cleanup(manager.StopAll)

	plan, err := manager.PlanProjectUpdate(ctx, projectID)
	if err != nil {
		t.Fatalf("PlanProjectUpdate() error = %v", err)
	}
	if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID, BackupVolumesFirst: true}); err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}
	select {
	case <-backups.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pre-update backup")
	}
	if err := os.WriteFile(filepath.Join(project.WorkingDir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx:1.26\n"), 0o600); err != nil {
		t.Fatalf("mutate Compose input while worker is blocked: %v", err)
	}
	close(backups.release)
	waitUpdateDone(t, doneEvents, updateResultFailed)
	if len(compose.calls) != 0 {
		t.Fatalf("Compose ran after the confirmed input changed while the worker was blocked: %#v", compose.calls)
	}
}

func TestManagerApplyUpdateAllowsVolatileSnapshotChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openUpdatesStore(t)
	scope := runtimescope.Must("linux_native", "default")
	projectID := "linux_native/fingerprint-volatile"
	service := serviceRecord(projectID, "web", "nginx:1.25", "")
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{service})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        scope.ProviderID(),
		ContextName:       scope.ContextName(),
		ProjectID:         projectID,
		ServiceID:         service.ID,
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          service.ImageRef,
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	eventBus := bus.New()
	t.Cleanup(eventBus.Close)
	doneEvents := eventBus.Subscribe(ctx, bus.TopicJobDone, 2)
	compose := &fakeUpdateCompose{}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), eventBus, nil)
	manager.Scope = scope
	manager.Compose = compose
	t.Cleanup(manager.StopAll)

	plan, err := manager.PlanProjectUpdate(ctx, projectID)
	if err != nil {
		t.Fatalf("PlanProjectUpdate() error = %v", err)
	}
	project, services, err := manager.projectWithServices(ctx, projectID)
	if err != nil {
		t.Fatalf("projectWithServices() error = %v", err)
	}
	project.Status = models.ProjectStatusRunning
	project.Health = models.HealthStatusHealthy
	project.LastSeenAt = project.LastSeenAt.Add(time.Minute)
	services[0].Status = models.ProjectStatusRunning
	services[0].Health = models.HealthStatusHealthy
	services[0].ReplicasRunning = 3
	services[0].ReplicasTotal = 3
	services[0].LastSeenAt = project.LastSeenAt
	if err := db.Projects().SaveSnapshot(ctx, scope, []store.ProjectRecord{project}, services, project.LastSeenAt, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(volatile changes) error = %v", err)
	}

	if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID}); err != nil {
		t.Fatalf("ApplyUpdate(volatile changes) error = %v", err)
	}
	waitUpdateDone(t, doneEvents, updateResultSuccess)
	if !containsCall(compose.calls, "pull:web") {
		t.Fatalf("Compose calls = %#v, want admitted update", compose.calls)
	}
}

func TestManagerApplyRollbackRejectsChangedConfirmedConfiguration(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	scope := runtimescope.Must("linux_native", "default")
	projectID := "linux_native/rollback-fingerprint"
	service := serviceRecord(projectID, "web", "rollback-web:local", ".")
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{service})
	historyID, err := db.Updates().InsertHistoryInScope(ctx, scope, store.UpdateHistoryRecord{
		ProviderID:     scope.ProviderID(),
		ContextName:    scope.ContextName(),
		ProjectID:      projectID,
		ServiceID:      service.ID,
		UpdateKind:     models.UpdateKindBaseImage,
		ImageRef:       service.ImageRef,
		OldImageID:     "sha256:web-old",
		OldDigest:      digestA,
		NewImageID:     "sha256:web-new",
		NewDigest:      digestB,
		Result:         updateResultSuccess,
		RollbackStatus: rollbackStatusAvailable,
		StartedAt:      time.Now().UTC().Add(-time.Minute),
		FinishedAt:     time.Now().UTC().Add(-30 * time.Second),
	})
	if err != nil {
		t.Fatalf("InsertHistoryInScope() error = %v", err)
	}
	compose := &fakeUpdateCompose{}
	docker := &fakeUpdateDocker{images: map[string]*models.ImageDetail{
		"sha256:web-old": imageDetail("sha256:web-old", "docker.io/library/rollback-web@"+digestA),
	}}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), nil, nil)
	manager.Scope = scope
	manager.Compose = compose

	plan, err := manager.PlanRollback(ctx, historyID)
	if err != nil {
		t.Fatalf("PlanRollback() error = %v", err)
	}
	project, services, err := manager.projectWithServices(ctx, projectID)
	if err != nil {
		t.Fatalf("projectWithServices() error = %v", err)
	}
	services[0].BuildTarget = "changed-after-confirmation"
	project.LastSeenAt = project.LastSeenAt.Add(time.Minute)
	services[0].LastSeenAt = project.LastSeenAt
	if err := db.Projects().SaveSnapshot(ctx, scope, []store.ProjectRecord{project}, services, project.LastSeenAt, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(changed rollback configuration) error = %v", err)
	}

	if _, err := manager.ApplyRollback(ctx, plan.PlanID); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("ApplyRollback(changed configuration) error = %v, want PlanExpired", err)
	}
	if len(docker.tags) != 0 || len(compose.calls) != 0 {
		t.Fatalf("rollback mutated after configuration change: tags=%#v compose=%#v", docker.tags, compose.calls)
	}
}

func TestContextWithPeerCancellationObservesAlreadyCanceledPeerSynchronously(t *testing.T) {
	peer, cancelPeer := context.WithCancel(context.Background())
	cancelPeer()

	ctx, cleanup := contextWithPeerCancellation(context.Background(), peer)
	defer cleanup()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("merged context error = %v, want context canceled", ctx.Err())
	}
}

func TestUpdatePlanStorePrunesExpiredAndEnforcesCap(t *testing.T) {
	now := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	manager := &Manager{
		Now:   func() time.Time { return now },
		plans: map[string]updatePlanRecord{},
	}
	for i := 0; i < maxPendingUpdatePlans; i++ {
		planID := fmt.Sprintf("update-plan-%03d", i)
		if err := manager.saveUpdatePlan(updatePlanRecord{
			Plan:      models.UpdatePlan{PlanID: planID},
			ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("saveUpdatePlan(%d) error = %v", i, err)
		}
	}
	if err := manager.saveUpdatePlan(updatePlanRecord{
		Plan:      models.UpdatePlan{PlanID: "update-plan-over-cap"},
		ExpiresAt: now.Add(time.Hour),
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("saveUpdatePlan(over cap) error = %v, want Conflict", err)
	}

	manager.planMu.Lock()
	expired := manager.plans["update-plan-000"]
	expired.ExpiresAt = now
	manager.plans["update-plan-000"] = expired
	manager.planMu.Unlock()
	if err := manager.saveUpdatePlan(updatePlanRecord{
		Plan:      models.UpdatePlan{PlanID: "update-plan-replacement"},
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("saveUpdatePlan(after prune) error = %v", err)
	}
	manager.planMu.Lock()
	_, expiredStillPresent := manager.plans["update-plan-000"]
	pending := len(manager.plans)
	manager.planMu.Unlock()
	if expiredStillPresent || pending != maxPendingUpdatePlans {
		t.Fatalf("plans after save prune: expiredPresent=%t pending=%d, want false/%d", expiredStillPresent, pending, maxPendingUpdatePlans)
	}

	manager.planMu.Lock()
	expired = manager.plans["update-plan-001"]
	expired.ExpiresAt = now
	manager.plans["update-plan-001"] = expired
	manager.planMu.Unlock()
	if _, err := manager.takeUpdatePlan(context.Background(), "update-plan-002"); err != nil {
		t.Fatalf("takeUpdatePlan(valid) error = %v", err)
	}
	manager.planMu.Lock()
	_, expiredStillPresent = manager.plans["update-plan-001"]
	manager.planMu.Unlock()
	if expiredStillPresent {
		t.Fatal("takeUpdatePlan did not prune another expired plan")
	}
}

func TestProjectRemovalCancelsAndJoinsInFlightUpdate(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		stale bool
	}{
		{name: "explicit removal"},
		{name: "stale reconciliation", stale: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			db := openUpdatesStore(t)
			scope := runtimescope.Must("linux_native", "default")
			projectID := "linux_native/removal-" + strings.ReplaceAll(testCase.name, " ", "-")
			seenAt := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
			if testCase.stale {
				seenAt = seenAt.Add(-48 * time.Hour)
			}
			project := store.ProjectRecord{
				ID:           projectID,
				ProviderID:   scope.ProviderID(),
				ContextName:  scope.ContextName(),
				Name:         strings.TrimPrefix(projectID, "linux_native/"),
				WorkingDir:   t.TempDir(),
				ComposeFiles: []string{"compose.yaml"},
				Source:       store.ProjectSourceImported,
				LastSeenAt:   seenAt,
			}
			if err := os.WriteFile(filepath.Join(project.WorkingDir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx:1.25\n"), 0o600); err != nil {
				t.Fatalf("write Compose fixture: %v", err)
			}
			service := serviceRecord(projectID, "web", "nginx:1.25", "")
			service.LastSeenAt = seenAt
			if testCase.stale {
				project.Source = store.ProjectSourceLabels
				if _, err := db.Projects().SaveDetectedSnapshot(ctx, scope, []store.ProjectRecord{project}, []store.ServiceRecord{service}, seenAt, time.Time{}); err != nil {
					t.Fatalf("SaveDetectedSnapshot(seed) error = %v", err)
				}
			} else if err := db.Projects().SaveSnapshot(ctx, scope, []store.ProjectRecord{project}, []store.ServiceRecord{service}, seenAt, time.Time{}); err != nil {
				t.Fatalf("SaveSnapshot(seed) error = %v", err)
			}
			insertCheck(t, ctx, db, store.UpdateCheckRecord{
				ProviderID:        scope.ProviderID(),
				ContextName:       scope.ContextName(),
				ProjectID:         projectID,
				ServiceID:         service.ID,
				Kind:              models.UpdateKindServiceImage,
				ImageRef:          service.ImageRef,
				LocalImageID:      "sha256:old",
				LocalDigest:       digestA,
				RemoteDigest:      digestB,
				Confidence:        models.ConfidenceMedium,
				RecommendedAction: models.RecommendedActionPullRecreate,
				Status:            models.UpdateStatusServiceImageUpdateAvailable,
				CheckedAt:         seenAt.Add(time.Minute),
			})

			eventBus := bus.New()
			t.Cleanup(eventBus.Close)
			doneEvents := eventBus.Subscribe(ctx, bus.TopicJobDone, 4)
			compose := newBlockingUpdateCompose()
			manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), eventBus, nil)
			manager.Scope = scope
			manager.Compose = compose
			t.Cleanup(manager.StopAll)

			plan, err := manager.PlanProjectUpdate(ctx, projectID)
			if err != nil {
				t.Fatalf("PlanProjectUpdate() error = %v", err)
			}
			if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID}); err != nil {
				t.Fatalf("ApplyUpdate() error = %v", err)
			}
			select {
			case <-compose.started:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for Compose pull to start")
			}

			removalDone := make(chan error, 1)
			go func() {
				if testCase.stale {
					_, err := db.Projects().SaveDetectedSnapshot(
						ctx,
						scope,
						nil,
						nil,
						seenAt.Add(48*time.Hour),
						seenAt.Add(24*time.Hour),
					)
					removalDone <- err
					return
				}
				removalDone <- db.Projects().ForgetAndDeleteInScope(ctx, scope, projectID, seenAt.Add(2*time.Hour))
			}()
			select {
			case err := <-removalDone:
				if err != nil {
					t.Fatalf("project removal error = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("project removal did not cancel and join the update worker")
			}
			select {
			case <-compose.returned:
			default:
				t.Fatal("project removal returned before the Compose call exited")
			}
			waitUpdateDone(t, doneEvents, updateResultFailed)

			if _, err := db.Projects().GetInScope(ctx, scope, projectID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("GetInScope(after removal) error = %v, want sql.ErrNoRows", err)
			}
			history, err := db.Updates().ListHistory(ctx, models.UpdateHistoryFilter{})
			if err != nil {
				t.Fatalf("ListHistory() error = %v", err)
			}
			if len(history) != 1 ||
				history[0].Result != updateResultFailed ||
				history[0].RollbackStatus != rollbackStatusUnavailable ||
				!strings.Contains(history[0].Error, context.Canceled.Error()) {
				t.Fatalf("history after removal = %#v, want one canceled unavailable row", history)
			}
			if got := compose.Calls(); strings.Join(got, ",") != "pull:web" {
				t.Fatalf("Compose calls after removal = %#v, want only the canceled pull", got)
			}
		})
	}
}

func TestManagerHealthPassRequiresEveryExpectedReplica(t *testing.T) {
	projectID := "linux_native/replicas"
	docker := &fakeUpdateDocker{
		containers: []models.ContainerSummary{
			{
				ID:        "web-1",
				Name:      "replicas-web-1",
				ProjectID: projectID,
				Service:   "web",
				State:     "running",
				Health:    models.HealthStatusHealthy,
			},
		},
	}
	manager := &Manager{Docker: docker}
	record := updatePlanRecord{
		Project: store.ProjectRecord{ID: projectID},
		Up:      []string{"web"},
		Snapshots: []updateSnapshot{{
			Service:        store.ServiceRecord{Name: "web"},
			HasHealthcheck: true,
		}},
		HealthBaselines: map[string]updateHealthBaseline{
			"web": {ExpectedReplicas: 2, Restarts: map[string]int{"id:web-1": 0, "id:web-2": 0}},
		},
	}

	result, err := manager.healthPass(context.Background(), record, time.Now().Add(-time.Minute))
	if err != nil || result != "pending" {
		t.Fatalf("healthPass(one of two replicas) = %q, %v, want pending", result, err)
	}

	docker.containers = append(docker.containers, models.ContainerSummary{
		ID:        "web-2",
		Name:      "replicas-web-2",
		ProjectID: projectID,
		Service:   "web",
		State:     "running",
		Health:    models.HealthStatusUnhealthy,
	})
	result, err = manager.healthPass(context.Background(), record, time.Now().Add(-time.Minute))
	if err != nil || result != "pending" {
		t.Fatalf("healthPass(unhealthy sibling) = %q, %v, want pending", result, err)
	}

	docker.containers[1].Health = models.HealthStatusHealthy
	result, err = manager.healthPass(context.Background(), record, time.Now().Add(-time.Minute))
	if err != nil || result != healthResultSuccess {
		t.Fatalf("healthPass(all replicas healthy) = %q, %v, want success", result, err)
	}
}

func TestManagerHealthPassUsesPostUpdateRestartDelta(t *testing.T) {
	projectID := "linux_native/restarts"
	docker := &fakeUpdateDocker{containers: []models.ContainerSummary{{
		ID:        "web-1",
		Name:      "restarts-web-1",
		ProjectID: projectID,
		Service:   "web",
		State:     "running",
		Health:    models.HealthStatusHealthy,
		Restarts:  12,
	}}}
	manager := &Manager{Docker: docker}
	record := updatePlanRecord{
		Project: store.ProjectRecord{ID: projectID},
		Up:      []string{"web"},
		Snapshots: []updateSnapshot{{
			Service:        store.ServiceRecord{Name: "web"},
			HasHealthcheck: true,
		}},
		HealthBaselines: map[string]updateHealthBaseline{
			"web": {ExpectedReplicas: 1, Restarts: map[string]int{"id:web-1": 11}},
		},
	}

	result, err := manager.healthPass(context.Background(), record, time.Now().Add(-time.Minute))
	if err != nil || result != healthResultSuccess {
		t.Fatalf("healthPass(one new restart) = %q, %v, want success", result, err)
	}

	docker.containers[0].Restarts = 13
	if _, err := manager.healthPass(context.Background(), record, time.Now().Add(-time.Minute)); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("healthPass(two new restarts) error = %v, want Conflict", err)
	}

	// A recreated container has a new identity, so its counter is entirely
	// post-update even when Compose reuses the replica name.
	docker.containers[0].ID = "web-2"
	docker.containers[0].Restarts = 2
	if _, err := manager.healthPass(context.Background(), record, time.Now().Add(-time.Minute)); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("healthPass(recreated container restarts) error = %v, want Conflict", err)
	}
}

func TestManagerWatchHealthRequiresContinuousStabilityWithoutHealthcheck(t *testing.T) {
	projectID := "linux_native/stability"
	docker := &fakeUpdateDocker{containers: []models.ContainerSummary{{
		ID:        "web-1",
		Name:      "stability-web-1",
		ProjectID: projectID,
		Service:   "web",
		State:     "running",
		Health:    models.HealthStatusUnknown,
	}}}
	manager := &Manager{
		Docker:                  docker,
		HealthWindow:            100 * time.Millisecond,
		HealthPollInterval:      time.Millisecond,
		HealthStabilityInterval: 5 * time.Millisecond,
	}
	record := updatePlanRecord{
		Project: store.ProjectRecord{ID: projectID},
		Up:      []string{"web"},
		Snapshots: []updateSnapshot{{
			Service: store.ServiceRecord{Name: "web"},
		}},
		HealthBaselines: map[string]updateHealthBaseline{
			"web": {ExpectedReplicas: 1, Restarts: map[string]int{"id:web-1": 0}},
		},
	}

	result, err := manager.watchHealth(context.Background(), record)
	if err != nil || result != healthResultSuccessWarn {
		t.Fatalf("watchHealth() = %q, %v, want success_warn", result, err)
	}
	if docker.listCalls < 2 {
		t.Fatalf("ListContainers calls = %d, want repeated stable observations", docker.listCalls)
	}
}

func TestManagerCaptureUpdateHealthBaselinesUsesDesiredReplicaCount(t *testing.T) {
	projectID := "linux_native/baseline"
	docker := &fakeUpdateDocker{containers: []models.ContainerSummary{
		{ID: "web-1", ProjectID: projectID, Service: "web", Restarts: 7},
		{ID: "web-2", ProjectID: projectID, Service: "web", Restarts: 3},
	}}
	manager := &Manager{Docker: docker}
	record := updatePlanRecord{
		Project:  store.ProjectRecord{ID: projectID},
		Services: map[string]store.ServiceRecord{"web": {Name: "web", ReplicasTotal: 3}},
		Up:       []string{"web"},
	}

	baselines, err := manager.captureUpdateHealthBaselines(context.Background(), record)
	if err != nil {
		t.Fatalf("captureUpdateHealthBaselines() error = %v", err)
	}
	baseline := baselines["web"]
	if baseline.ExpectedReplicas != 3 {
		t.Fatalf("ExpectedReplicas = %d, want 3", baseline.ExpectedReplicas)
	}
	if baseline.Restarts["id:web-1"] != 7 || baseline.Restarts["id:web-2"] != 3 {
		t.Fatalf("restart baselines = %#v", baseline.Restarts)
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
	manager.Scope = runtimescope.Must("linux_native", "default")
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
	manager.Scope = runtimescope.Must("linux_native", "default")
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
	manager.Scope = runtimescope.Must("linux_native", "default")
	manager.Compose = &fakeUpdateCompose{}
	manager.Backups = backups
	manager.HealthWindow = 100 * time.Millisecond
	manager.HealthPollInterval = time.Millisecond
	manager.HealthStabilityInterval = 5 * time.Millisecond

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
		ContextName:    "default",
		ProjectID:      projectID,
		ServiceID:      projectID + "/web",
		UpdateKind:     models.UpdateKindBaseImage,
		ImageRef:       "rollback-web:local",
		BaseImageRef:   "alpine:3.20",
		OldImageID:     "sha256:web-old",
		OldDigest:      digestA,
		OldBaseDigest:  digestA,
		NewImageID:     "sha256:web-new",
		NewDigest:      digestB,
		NewBaseDigest:  digestB,
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
	progress := eventBus.Subscribe(ctx, bus.TopicJobProgress, 8)
	compose := &fakeUpdateCompose{}
	docker := &fakeUpdateDocker{images: map[string]*models.ImageDetail{
		"sha256:web-old": imageDetail("sha256:web-old", "docker.io/library/rollback-web@"+digestA),
	}}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), eventBus, nil)
	manager.Scope = runtimescope.Must("linux_native", "default")
	manager.Compose = compose

	if _, err := manager.Rollback(ctx, historyID); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("Rollback() error = %v, want confirmation required", err)
	}
	plan, err := manager.PlanRollback(ctx, historyID)
	if err != nil {
		t.Fatalf("PlanRollback() error = %v", err)
	}
	if plan == nil || plan.PlanID == "" || len(plan.Commands) != 2 {
		t.Fatalf("PlanRollback() plan = %#v", plan)
	}
	_, err = manager.ApplyRollback(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("ApplyRollback() error = %v", err)
	}
	waitUpdateDone(t, done, updateResultRolledBack)
	waitUpdateProgressMessage(t, progress, "stdout", "up-no-build:web")
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
	if history.NewImageID != "sha256:web-new" || history.NewDigest != digestB || history.NewBaseDigest != digestB {
		t.Fatalf("history new image evidence was not preserved: %#v", history)
	}
}

func TestManagerPlanRollbackRejectsForeignScopeBeforeDockerLookup(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	now := time.Now().UTC()
	foreignScope := runtimescope.Must("linux_native", "socket:foreign")
	foreignProjectID := "linux_native/foreign-rollback"
	if err := db.Projects().SaveSnapshot(ctx, foreignScope, []store.ProjectRecord{{
		ID: foreignProjectID, ProviderID: "linux_native", ContextName: foreignScope.ContextName(), Name: "foreign-rollback", WorkingDir: t.TempDir(), LastSeenAt: now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(foreign) error = %v", err)
	}
	historyID, err := db.Updates().InsertHistory(ctx, store.UpdateHistoryRecord{
		ProviderID: "linux_native", ContextName: foreignScope.ContextName(), ProjectID: foreignProjectID, ServiceID: foreignProjectID + "/web",
		UpdateKind: models.UpdateKindServiceImage, ImageRef: "nginx:1.25", OldImageID: "sha256:old",
		Result: updateResultSuccess, RollbackStatus: rollbackStatusAvailable, StartedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertHistory() error = %v", err)
	}
	docker := &fakeUpdateDocker{images: map[string]*models.ImageDetail{"sha256:old": imageDetail("sha256:old", "nginx@"+digestA)}}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), nil, nil)
	manager.Scope = runtimescope.Must("linux_native", "default")
	manager.Compose = &fakeUpdateCompose{}
	if _, err := manager.PlanRollback(ctx, historyID); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("PlanRollback(foreign) error = %v, want not found", err)
	}
	if docker.getImageCalls != 0 {
		t.Fatalf("Docker GetImage called before scope authorization: %d", docker.getImageCalls)
	}
}

func TestManagerPlanRollbackRejectsHistoryFromPreviousContextAfterProjectIDReuse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openUpdatesStore(t)
	now := time.Date(2026, 7, 16, 17, 0, 0, 0, time.UTC)
	projectID := "linux_native/reused-rollback"
	scopeA := runtimescope.Must("linux_native", "socket:a")
	scopeB := runtimescope.Must("linux_native", "socket:b")
	project := store.ProjectRecord{
		ID: projectID, ProviderID: scopeA.ProviderID(), ContextName: scopeA.ContextName(),
		Name: "reused-rollback", WorkingDir: t.TempDir(), ComposeFiles: []string{"compose.yaml"},
		Source: store.ProjectSourceImported, LastSeenAt: now,
	}
	service := serviceRecord(projectID, "web", "nginx:latest", "")
	if err := db.Projects().SaveSnapshot(ctx, scopeA, []store.ProjectRecord{project}, []store.ServiceRecord{service}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(A): %v", err)
	}
	historyID, err := db.Updates().InsertHistoryInScope(ctx, scopeA, store.UpdateHistoryRecord{
		ProviderID: scopeA.ProviderID(), ContextName: scopeA.ContextName(), ProjectID: projectID,
		ServiceID: projectID + "/web", UpdateKind: models.UpdateKindServiceImage,
		ImageRef: "nginx:latest", OldImageID: "sha256:scope-a-old", Result: updateResultSuccess,
		RollbackStatus: rollbackStatusAvailable, StartedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertHistoryInScope(A): %v", err)
	}
	if err := db.Projects().DeleteInScope(ctx, scopeA, projectID); err != nil {
		t.Fatalf("DeleteInScope(A): %v", err)
	}
	project.ContextName = scopeB.ContextName()
	project.LastSeenAt = now.Add(time.Hour)
	service.LastSeenAt = project.LastSeenAt
	if err := db.Projects().SaveSnapshot(ctx, scopeB, []store.ProjectRecord{project}, []store.ServiceRecord{service}, project.LastSeenAt, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(B): %v", err)
	}

	docker := &fakeUpdateDocker{images: map[string]*models.ImageDetail{
		"sha256:scope-a-old": imageDetail("sha256:scope-a-old", "nginx@"+digestA),
	}}
	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), nil, nil)
	manager.Scope = scopeB
	manager.Compose = &fakeUpdateCompose{}
	if _, err := manager.PlanRollback(ctx, historyID); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("PlanRollback(previous context) error = %v, want not found", err)
	}
	if docker.getImageCalls != 0 {
		t.Fatalf("Docker GetImage called before exact-context history authorization: %d", docker.getImageCalls)
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
	manager.Scope = runtimescope.Must("linux_native", "default")

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
	if _, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyUpdate(warning-only retry) error = %v, want conflict not expired", err)
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
	manager.Scope = runtimescope.Must("linux_native", "default")
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
	if got := serviceNameFromID("plain", ""); got != "plain" {
		t.Fatalf("serviceNameFromID plain = %q", got)
	}
	if got := serviceNameFromID("linux_native/demo/api/v1", "linux_native/demo"); got != "api/v1" {
		t.Fatalf("serviceNameFromID project-prefixed = %q, want api/v1", got)
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
	for _, input := range []string{"panic: boom", "Fatal error: failed to boot", `Exception in thread "main"`, "exit-on-start"} {
		if !fatalLogDetected(input) {
			t.Fatalf("fatalLogDetected(%q) = false", input)
		}
	}
	for _, input := range []string{"server started normally", "FatalErrorCode=0", "Exception in thread pool size is configured"} {
		if fatalLogDetected(input) {
			t.Fatalf("fatalLogDetected(%q) = true", input)
		}
	}
}

func TestContainerHasFatalLogsRejectsOversizedTail(t *testing.T) {
	logs := strings.Repeat("x", maxUpdateHealthLogBytes) + "\npanic: signature beyond bounded prefix\n"
	manager := &Manager{Docker: &fakeUpdateDocker{logs: map[string]string{"container-web": logs}}}

	fatal, err := manager.containerHasFatalLogs(context.Background(), "container-web", time.Now().Add(-time.Minute))
	if fatal {
		t.Fatal("containerHasFatalLogs(oversized) reported a partial-scan result")
	}
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("containerHasFatalLogs(oversized) error = %v, want Conflict", err)
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
	manager.Scope = runtimescope.Must("linux_native", "default")
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
		ContextName:     "default",
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
	manager.Scope = runtimescope.Must("linux_native", "default")

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
	manager.Scope = runtimescope.Must("linux_native", "default")
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
	if got := platformFromDockerInfo(models.DockerInfo{OperatingSystem: "Docker Desktop", Architecture: "aarch64"}); got.OS != "linux" || got.Architecture != "arm64" {
		t.Fatalf("platformFromDockerInfo = %#v, want linux/arm64", got)
	}
	if got := platformFromDockerInfo(models.DockerInfo{OperatingSystem: "Windows Server", Architecture: "x86_64"}); got.OS != "windows" || got.Architecture != "amd64" {
		t.Fatalf("platformFromDockerInfo windows = %#v, want windows/amd64", got)
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
	manager.Scope = runtimescope.Must("linux_native", "default")

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
	manager.Scope = runtimescope.Must("linux_native", "default")
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
	offlineManager.Scope = runtimescope.Must("linux_native", "default")
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
	manager.Scope = runtimescope.Must("linux_native", "default")
	if err := manager.IgnoreUpdate(ctx, models.IgnoreUpdateRequest{ID: 404}); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("ignore missing error = %v, want not found", err)
	}
	digest, imageID := manager.localDigest(ctx, "alpine:3.20", "sha256:local")
	if digest != digestA || imageID != "sha256:local" {
		t.Fatalf("localDigest via imageID = %q/%q", digest, imageID)
	}
	remote, err := manager.remoteDigest(ctx, "partial.local/app:1", "linux/amd64")
	if remote.Primary() != digestB || !apperror.IsCode(err, apperror.RegistryRateLimit) {
		t.Fatalf("remoteDigest partial = %#v/%v", remote, err)
	}
	if err := manager.updateBaseRef(ctx, 0, "", "", models.UpdateStatusUnknown, time.Time{}, ""); err != nil {
		t.Fatalf("updateBaseRef zero id error = %v", err)
	}
	interval, enabled := manager.schedulerInterval(ctx)
	if !enabled || interval != 24*time.Hour {
		t.Fatalf("nil-settings scheduler interval = %v/%v", interval, enabled)
	}
	got := manager.jitter(time.Hour)
	if got < 0 || got >= 6*time.Minute {
		t.Fatalf("default jitter = %v, want [0, 6m)", got)
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
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx:1.25\n"), 0o600); err != nil {
		t.Fatalf("write Compose fixture: %v", err)
	}
	if err := db.Projects().SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []store.ProjectRecord{{
		ID:           projectID,
		ProviderID:   "linux_native",
		ContextName:  "default",
		Name:         strings.TrimPrefix(projectID, "linux_native/"),
		WorkingDir:   workdir,
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
		ContextName:     "default",
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
	digests      map[string]string
	indexDigests map[string]string
	errs         map[string]error
	emptyResult  map[string]bool
	resultErrs   map[string]registryResultError
	platforms    map[string]registrycore.Platform
}

type registryResultError struct {
	digest string
	err    error
}

type controlledRegistry struct {
	digest  string
	calls   chan struct{}
	release <-chan struct{}
}

func (r *controlledRegistry) ResolveDigest(ctx context.Context, _ string, _ registrycore.ResolveOptions) (*registrycore.DigestResult, error) {
	r.calls <- struct{}{}
	select {
	case <-r.release:
		return &registrycore.DigestResult{ManifestDigest: r.digest}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *fakeRegistry) ResolveDigest(_ context.Context, image string, opts registrycore.ResolveOptions) (*registrycore.DigestResult, error) {
	if r.platforms == nil {
		r.platforms = map[string]registrycore.Platform{}
	}
	r.platforms[image] = opts.Platform
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
		return &registrycore.DigestResult{ManifestDigest: digest, IndexDigest: r.indexDigests[image]}, nil
	}
	return nil, apperror.New(apperror.NotFound, "not found")
}

type fakeImages struct {
	details map[string]*models.ImageDetail
	pingErr error
	info    *models.DockerInfo
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

func (i fakeImages) Info(context.Context) (*models.DockerInfo, error) {
	if i.info != nil {
		return i.info, nil
	}
	return nil, errors.New("docker info unavailable")
}

type fakeUpdateCompose struct {
	calls []string
	errs  map[string]error
}

type blockingUpdateCompose struct {
	started  chan struct{}
	returned chan struct{}
	once     sync.Once
	mu       sync.Mutex
	calls    []string
}

func newBlockingUpdateCompose() *blockingUpdateCompose {
	return &blockingUpdateCompose{
		started:  make(chan struct{}),
		returned: make(chan struct{}),
	}
}

func (f *blockingUpdateCompose) PullServices(ctx context.Context, _ composecore.ProjectOptions, services []string) (*providers.CommandResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "pull:"+strings.Join(services, ","))
	f.mu.Unlock()
	f.once.Do(func() { close(f.started) })
	<-ctx.Done()
	close(f.returned)
	return nil, ctx.Err()
}

func (f *blockingUpdateCompose) Build(_ context.Context, _ composecore.ProjectOptions, build composecore.BuildOptions) (*providers.CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "build:"+strings.Join(build.Services, ","))
	return &providers.CommandResult{}, nil
}

func (f *blockingUpdateCompose) UpServices(_ context.Context, _ composecore.ProjectOptions, up composecore.UpOptions) (*providers.CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "up:"+strings.Join(up.Services, ","))
	return &providers.CommandResult{}, nil
}

func (f *blockingUpdateCompose) Config(context.Context, composecore.ProjectOptions) (*composecore.ConfigResult, error) {
	return &composecore.ConfigResult{Valid: true}, nil
}

func (f *blockingUpdateCompose) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
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
	providerID    string
	images        map[string]*models.ImageDetail
	containers    []models.ContainerSummary
	details       map[string]*models.ContainerDetail
	logs          map[string]string
	tags          []string
	getImageCalls int
	listCalls     int
}

func (f *fakeUpdateDocker) ProviderID() string {
	if f.providerID != "" {
		return f.providerID
	}
	return "linux_native"
}

func (f *fakeUpdateDocker) GetImage(_ context.Context, id string) (*models.ImageDetail, error) {
	f.getImageCalls++
	if detail := f.images[id]; detail != nil {
		return detail, nil
	}
	return nil, apperror.New(apperror.NotFound, "image not found")
}

func (f *fakeUpdateDocker) ListContainers(_ context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	f.listCalls++
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

type blockingUpdateBackups struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingUpdateBackups() *blockingUpdateBackups {
	return &blockingUpdateBackups{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (f *blockingUpdateBackups) RunBackupVolume(ctx context.Context, _ models.BackupVolumeRequest) error {
	f.once.Do(func() { close(f.started) })
	select {
	case <-f.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type failingDiscoverer struct{}

func (failingDiscoverer) DiscoverProjectLineage(context.Context, string) ([]models.ImageLineage, error) {
	return nil, errors.New("discover failed")
}

func insertCheck(t *testing.T, ctx context.Context, db *store.Store, record store.UpdateCheckRecord) int64 {
	t.Helper()
	if strings.TrimSpace(record.ContextName) == "" && strings.TrimSpace(record.ProjectID) != "" {
		project, err := db.Projects().Get(ctx, record.ProjectID)
		if err != nil {
			t.Fatalf("Get project scope for update check: %v", err)
		}
		record.ContextName = project.ContextName
	}
	scope := runtimescope.Must(record.ProviderID, record.ContextName)
	startedAt := record.CheckedAt
	if startedAt.IsZero() {
		startedAt = time.Date(2026, 6, 13, 12, 30, 0, 0, time.UTC)
	}
	run, err := db.Updates().BeginServiceCheckRunInScope(ctx, scope, record.ProjectID, record.ServiceID, startedAt)
	if err != nil {
		t.Fatalf("BeginServiceCheckRunInScope() error = %v", err)
	}
	published, accepted, err := db.Updates().PublishCheckRunInScope(ctx, scope, run.ID, []store.UpdateCheckRecord{record}, startedAt)
	if err != nil || !accepted || len(published) != 1 {
		t.Fatalf("PublishCheckRunInScope() = %#v/%v/%v", published, accepted, err)
	}
	return published[0].ID
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

func waitUpdateProgressMessage(t *testing.T, events <-chan bus.Event, phase string, contains string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			payload, ok := event.Payload.(jobProgressPayload)
			if !ok {
				continue
			}
			if payload.Phase == phase && strings.Contains(payload.Message, contains) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for update progress %s containing %q", phase, contains)
		}
	}
}
