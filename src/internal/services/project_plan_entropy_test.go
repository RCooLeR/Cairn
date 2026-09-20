package services

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

type failingProjectPlanEntropyReader struct{}

func (failingProjectPlanEntropyReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

type projectPlanEventRecorder struct {
	events []bus.Event
}

func (r *projectPlanEventRecorder) Publish(event bus.Event) {
	r.events = append(r.events, event)
}

func (*projectPlanEventRecorder) Subscribe(context.Context, bus.Topic, int) <-chan bus.Event {
	return make(chan bus.Event)
}

func TestProjectServiceApplyPlanEntropyFailureLeavesPlanRetryable(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "entropy-retry")
	scope := runtimescope.Must("linux_native", "default")
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	project := store.ProjectRecord{
		ID:           "linux_native/entropy-retry",
		ProviderID:   scope.ProviderID(),
		ContextName:  scope.ContextName(),
		Name:         "entropy-retry",
		WorkingDir:   root,
		ComposeFiles: []string{composeFile},
		LastSeenAt:   now,
	}
	if err := db.Projects().SaveSnapshot(ctx, scope, []store.ProjectRecord{project}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}

	runner := newFakeComposeRunner()
	events := &projectPlanEventRecorder{}
	plans := security.NewProjectPlanStore(func() time.Time { return now })
	t.Cleanup(plans.Close)
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Audit:    db.Audit(),
		Plans:    plans,
		Events:   events,
		Scope:    scope,
		Now:      func() time.Time { return now },
	}

	plan, err := service.PlanDownProject(ctx, project.ID, false)
	if err != nil {
		t.Fatalf("PlanDownProject() error = %v", err)
	}
	events.events = nil
	baselineCalls := runner.callsSnapshot()
	baselineAudit, err := db.Audit().List(ctx, models.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("List(audit baseline) error = %v", err)
	}

	service.IDs = security.NewIDSource(failingProjectPlanEntropyReader{})
	err = service.ApplyProjectPlan(ctx, plan.PlanID, project.Name)
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("ApplyProjectPlan(entropy failure) error = %v, want %s", err, apperror.Internal)
	}
	if calls := runner.callsSnapshot(); len(calls) != len(baselineCalls) {
		t.Fatalf("Compose calls after entropy failure = %#v, want %#v", calls, baselineCalls)
	}
	auditAfterFailure, err := db.Audit().List(ctx, models.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("List(audit after failure) error = %v", err)
	}
	if len(auditAfterFailure) != len(baselineAudit) {
		t.Fatalf("Audit entries after entropy failure = %d, want %d", len(auditAfterFailure), len(baselineAudit))
	}
	if len(events.events) != 0 {
		t.Fatalf("Events after entropy failure = %#v, want none", events.events)
	}

	// Exactly one job identifier is available. A second generation attempt in
	// the execution path would exhaust this reader and consume the plan again.
	service.IDs = security.NewIDSource(bytes.NewReader(make([]byte, 16)))
	if err := service.ApplyProjectPlan(ctx, plan.PlanID, project.Name); err != nil {
		t.Fatalf("ApplyProjectPlan(retry) error = %v", err)
	}
	if !runner.hasCall(root + "|-f " + composeFile + " down") {
		t.Fatalf("Compose calls after retry = %#v, want down action", runner.callsSnapshot())
	}
}
