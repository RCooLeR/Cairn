package updates

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

type failingUpdateEntropyReader struct{}

func (failingUpdateEntropyReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestUpdatePlanAndJobEntropyFailuresLeaveNoMutation(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/entropy"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "nginx:1.25", ""),
	})
	insertCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        "linux_native",
		ContextName:       "default",
		ProjectID:         projectID,
		ServiceID:         projectID + "/web",
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          "nginx:1.25",
		LocalDigest:       digestA,
		RemoteDigest:      digestB,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	failingIDs := security.NewIDSource(failingUpdateEntropyReader{})

	planManager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)
	planManager.Scope = runtimescope.Must("linux_native", "default")
	planManager.IDs = failingIDs
	plan, err := planManager.PlanProjectUpdate(ctx, projectID)
	if plan != nil {
		t.Fatalf("PlanProjectUpdate() plan = %#v, want nil", plan)
	}
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("PlanProjectUpdate() error = %v, want %s", err, apperror.Internal)
	}
	if count := updatePlanCount(planManager); count != 0 {
		t.Fatalf("saved plans after plan ID failure = %d, want 0", count)
	}

	compose := &fakeUpdateCompose{}
	jobManager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), fakeImages{}, &fakeRegistry{}, db.Settings(), nil, nil)
	jobManager.Scope = runtimescope.Must("linux_native", "default")
	jobManager.Compose = compose
	plan, err = jobManager.PlanProjectUpdate(ctx, projectID)
	if err != nil {
		t.Fatalf("PlanProjectUpdate() setup error = %v", err)
	}
	jobManager.IDs = failingIDs
	jobID, err := jobManager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID})
	if jobID != "" {
		t.Fatalf("ApplyUpdate() job ID = %q, want empty", jobID)
	}
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("ApplyUpdate() error = %v, want %s", err, apperror.Internal)
	}
	if count := updateJobCount(jobManager); count != 0 {
		t.Fatalf("jobs after job ID failure = %d, want 0", count)
	}
	if !updatePlanSaved(jobManager, plan.PlanID) {
		t.Fatal("confirmed update plan was not restored after job ID failure")
	}
	if len(compose.calls) != 0 {
		t.Fatalf("Compose calls after job ID failure = %#v, want none", compose.calls)
	}

	if jobID, err = jobManager.CheckAllUpdates(ctx); jobID != "" || !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("CheckAllUpdates() = %q, %v; want empty/%s", jobID, err, apperror.Internal)
	}
	if count := updateJobCount(jobManager); count != 0 {
		t.Fatalf("jobs after check ID failure = %d, want 0", count)
	}
	if err := jobManager.runScheduledCheck(ctx); !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("runScheduledCheck() error = %v, want %s", err, apperror.Internal)
	}
}

func TestRollbackPlanAndJobEntropyFailuresLeaveNoMutation(t *testing.T) {
	ctx := context.Background()
	db := openUpdatesStore(t)
	projectID := "linux_native/entropy-rollback"
	seedUpdateProject(t, ctx, db, projectID, []store.ServiceRecord{
		serviceRecord(projectID, "web", "rollback-web:local", "."),
	})
	historyID, err := db.Updates().InsertHistory(ctx, store.UpdateHistoryRecord{
		ProviderID:     "linux_native",
		ContextName:    "default",
		ProjectID:      projectID,
		ServiceID:      projectID + "/web",
		UpdateKind:     models.UpdateKindBaseImage,
		ImageRef:       "rollback-web:local",
		OldImageID:     "sha256:web-old",
		OldDigest:      digestA,
		NewDigest:      digestB,
		Result:         updateResultSuccess,
		RollbackStatus: rollbackStatusAvailable,
		StartedAt:      time.Now().UTC().Add(-time.Minute),
		FinishedAt:     time.Now().UTC().Add(-30 * time.Second),
	})
	if err != nil {
		t.Fatalf("InsertHistory() error = %v", err)
	}
	failingIDs := security.NewIDSource(failingUpdateEntropyReader{})
	docker := &fakeUpdateDocker{images: map[string]*models.ImageDetail{
		"sha256:web-old": imageDetail("sha256:web-old", "docker.io/library/rollback-web@"+digestA),
	}}

	planManager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), nil, nil)
	planManager.Scope = runtimescope.Must("linux_native", "default")
	planManager.Compose = &fakeUpdateCompose{}
	planManager.IDs = failingIDs
	plan, err := planManager.PlanRollback(ctx, historyID)
	if plan != nil {
		t.Fatalf("PlanRollback() plan = %#v, want nil", plan)
	}
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("PlanRollback() error = %v, want %s", err, apperror.Internal)
	}
	if count := updatePlanCount(planManager); count != 0 {
		t.Fatalf("saved rollback plans after ID failure = %d, want 0", count)
	}
	if len(docker.tags) != 0 {
		t.Fatalf("Docker tags after plan ID failure = %#v, want none", docker.tags)
	}

	compose := &fakeUpdateCompose{}
	jobManager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), docker, &fakeRegistry{}, db.Settings(), nil, nil)
	jobManager.Scope = runtimescope.Must("linux_native", "default")
	jobManager.Compose = compose
	plan, err = jobManager.PlanRollback(ctx, historyID)
	if err != nil {
		t.Fatalf("PlanRollback() setup error = %v", err)
	}
	jobManager.IDs = failingIDs
	jobID, err := jobManager.ApplyRollback(ctx, plan.PlanID)
	if jobID != "" {
		t.Fatalf("ApplyRollback() job ID = %q, want empty", jobID)
	}
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("ApplyRollback() error = %v, want %s", err, apperror.Internal)
	}
	if count := updateJobCount(jobManager); count != 0 {
		t.Fatalf("jobs after rollback job ID failure = %d, want 0", count)
	}
	if !updatePlanSaved(jobManager, plan.PlanID) {
		t.Fatal("confirmed rollback plan was not restored after job ID failure")
	}
	if len(compose.calls) != 0 || len(docker.tags) != 0 {
		t.Fatalf("rollback mutation after job ID failure: compose=%#v tags=%#v", compose.calls, docker.tags)
	}
}

func updatePlanCount(manager *Manager) int {
	manager.planMu.Lock()
	defer manager.planMu.Unlock()
	return len(manager.plans)
}

func updatePlanSaved(manager *Manager, planID string) bool {
	manager.planMu.Lock()
	defer manager.planMu.Unlock()
	_, ok := manager.plans[planID]
	return ok
}

func updateJobCount(manager *Manager) int {
	manager.jobsMu.Lock()
	defer manager.jobsMu.Unlock()
	return len(manager.jobs)
}
