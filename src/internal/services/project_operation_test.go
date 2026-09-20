package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

type mutationGateTestRunner struct {
	mu            sync.Mutex
	blockFirst    bool
	started       chan struct{}
	canceled      chan struct{}
	allowReturn   chan struct{}
	startedOnce   sync.Once
	canceledOnce  sync.Once
	allowOnce     sync.Once
	mutationCalls []string
}

func newBlockingMutationGateTestRunner() *mutationGateTestRunner {
	return &mutationGateTestRunner{
		blockFirst:  true,
		started:     make(chan struct{}),
		canceled:    make(chan struct{}),
		allowReturn: make(chan struct{}),
	}
}

func (r *mutationGateTestRunner) RunCompose(ctx context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
	return r.run(ctx, workdir, args...)
}

func (r *mutationGateTestRunner) RunComposeEnv(ctx context.Context, workdir string, _ []string, args ...string) (*providers.CommandResult, error) {
	return r.run(ctx, workdir, args...)
}

func (r *mutationGateTestRunner) run(ctx context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
	result := &providers.CommandResult{
		Command: append([]string{"docker", "compose"}, args...),
		Workdir: workdir,
	}
	if !isMutationGateTestCommand(args) {
		return result, nil
	}

	r.mu.Lock()
	r.mutationCalls = append(r.mutationCalls, strings.Join(args, " "))
	callNumber := len(r.mutationCalls)
	block := r.blockFirst && callNumber == 1
	r.mu.Unlock()
	if !block {
		return result, nil
	}

	r.startedOnce.Do(func() { close(r.started) })
	<-ctx.Done()
	r.canceledOnce.Do(func() { close(r.canceled) })
	<-r.allowReturn
	return result, ctx.Err()
}

func (r *mutationGateTestRunner) release() {
	if r == nil || r.allowReturn == nil {
		return
	}
	r.allowOnce.Do(func() { close(r.allowReturn) })
}

func (r *mutationGateTestRunner) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.mutationCalls...)
}

func isMutationGateTestCommand(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "start", "stop", "restart", "pull", "build", "up", "down":
			return true
		}
	}
	return false
}

type mutationGateTestDetector struct {
	started      chan struct{}
	canceled     chan struct{}
	allowReturn  chan struct{}
	startedOnce  sync.Once
	canceledOnce sync.Once
	allowOnce    sync.Once
}

func newMutationGateTestDetector() *mutationGateTestDetector {
	return &mutationGateTestDetector{
		started:     make(chan struct{}),
		canceled:    make(chan struct{}),
		allowReturn: make(chan struct{}),
	}
}

func (d *mutationGateTestDetector) Reconcile(ctx context.Context) ([]models.ProjectSummary, error) {
	d.startedOnce.Do(func() { close(d.started) })
	<-ctx.Done()
	d.canceledOnce.Do(func() { close(d.canceled) })
	<-d.allowReturn
	return nil, ctx.Err()
}

func (d *mutationGateTestDetector) release() {
	d.allowOnce.Do(func() { close(d.allowReturn) })
}

func TestProjectMutationGateRemovalCancelsAndJoinsProjectAction(t *testing.T) {
	db, scope, project := seedMutationGateTestProject(t, "delete-project-action")
	runner := newBlockingMutationGateTestRunner()
	t.Cleanup(runner.release)
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    scope,
	}

	actionCtx, cancelAction := context.WithCancel(context.Background())
	t.Cleanup(cancelAction)
	actionDone := make(chan error, 1)
	go func() {
		actionDone <- service.StartProject(actionCtx, project.ID)
	}()
	waitMutationGateSignal(t, runner.started, "project action to start")

	removalDone := make(chan error, 1)
	go func() {
		removalDone <- service.RemoveProjectFromList(context.Background(), project.ID)
	}()
	waitMutationGateSignal(t, runner.canceled, "project removal to cancel the Compose action")
	select {
	case err := <-removalDone:
		t.Fatalf("RemoveProjectFromList() returned before the canceled Compose action exited: %v", err)
	default:
	}

	runner.release()
	if err := waitMutationGateResult(t, actionDone, "project action to return"); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartProject() error = %v, want context canceled", err)
	}
	if err := waitMutationGateResult(t, removalDone, "project removal to finish"); err != nil {
		t.Fatalf("RemoveProjectFromList() error = %v", err)
	}
	if _, err := db.Projects().GetInScope(context.Background(), scope, project.ID); err == nil {
		t.Fatal("project still exists after removal")
	}
}

func TestProjectMutationGateExcludesProjectAndServiceActions(t *testing.T) {
	db, scope, project := seedMutationGateTestProject(t, "exclusive-actions")
	runner := newBlockingMutationGateTestRunner()
	t.Cleanup(runner.release)
	client := composecore.NewClient(runner)
	projects := db.Projects()
	projectService := &ProjectService{Client: client, Projects: projects, Scope: scope}
	composeService := &ComposeService{Client: client, Projects: projects, Scope: scope}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	t.Cleanup(cancelFirst)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- composeService.StartServices(firstCtx, project.ID, []string{"app"})
	}()
	waitMutationGateSignal(t, runner.started, "service action to start")

	if err := projectService.RestartProject(context.Background(), project.ID); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("RestartProject(concurrent) error = %v, want Conflict", err)
	}
	if err := composeService.ScaleService(context.Background(), project.ID, "app", 2); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ScaleService(concurrent) error = %v, want Conflict", err)
	}
	if calls := runner.calls(); len(calls) != 1 {
		t.Fatalf("Compose mutation calls = %#v, want only the admitted service action", calls)
	}

	cancelFirst()
	waitMutationGateSignal(t, runner.canceled, "caller cancellation to reach the Compose action")
	runner.release()
	if err := waitMutationGateResult(t, firstDone, "service action to return"); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartServices() error = %v, want context canceled", err)
	}
	if _, err := projects.ProjectOperationGeneration(scope, project.ID); err != nil {
		t.Fatalf("project mutation gate remained occupied after action exit: %v", err)
	}
}

func TestProjectMutationGateRemovalCancelsAndJoinsFinalReconcile(t *testing.T) {
	db, scope, project := seedMutationGateTestProject(t, "delete-reconcile")
	runner := &mutationGateTestRunner{}
	detector := newMutationGateTestDetector()
	t.Cleanup(detector.release)
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Detector: detector,
		Scope:    scope,
	}

	actionCtx, cancelAction := context.WithCancel(context.Background())
	t.Cleanup(cancelAction)
	actionDone := make(chan error, 1)
	go func() {
		actionDone <- service.StartProject(actionCtx, project.ID)
	}()
	waitMutationGateSignal(t, detector.started, "final project reconciliation to start")

	removalDone := make(chan error, 1)
	go func() {
		removalDone <- service.RemoveProjectFromList(context.Background(), project.ID)
	}()
	waitMutationGateSignal(t, detector.canceled, "project removal to cancel final reconciliation")
	select {
	case err := <-removalDone:
		t.Fatalf("RemoveProjectFromList() returned before reconciliation exited: %v", err)
	default:
	}

	detector.release()
	if err := waitMutationGateResult(t, actionDone, "project action to finish reconciliation"); err != nil {
		t.Fatalf("StartProject() error = %v", err)
	}
	if err := waitMutationGateResult(t, removalDone, "project removal to finish"); err != nil {
		t.Fatalf("RemoveProjectFromList() error = %v", err)
	}
}

func TestProjectMutationGateInvalidatesPreexistingProjectPlan(t *testing.T) {
	db, scope, project := seedMutationGateTestProject(t, "stale-plan")
	runner := &mutationGateTestRunner{}
	plans := security.NewProjectPlanStore(nil)
	t.Cleanup(plans.Close)
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Plans:    plans,
		Scope:    scope,
	}

	plan, err := service.PlanDownProject(context.Background(), project.ID, false)
	if err != nil {
		t.Fatalf("PlanDownProject() error = %v", err)
	}
	if err := service.StartProject(context.Background(), project.ID); err != nil {
		t.Fatalf("StartProject() error = %v", err)
	}
	if err := service.ApplyProjectPlan(context.Background(), plan.PlanID, project.Name); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("ApplyProjectPlan(stale generation) error = %v, want PlanExpired", err)
	}
	for _, call := range runner.calls() {
		if strings.Contains(call, " down") || strings.HasSuffix(call, "down") {
			t.Fatalf("stale project plan reached Compose: %q", call)
		}
	}
}

func TestProjectActionPlanRejectsChangedConfigurationAndInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *store.Store, runtimescope.Scope, *store.ProjectRecord)
	}{
		{
			name: "Compose file content",
			mutate: func(t *testing.T, _ *store.Store, _ runtimescope.Scope, project *store.ProjectRecord) {
				t.Helper()
				if err := os.WriteFile(project.ComposeFiles[0], []byte("services:\n  app:\n    image: redis:7-alpine\n"), 0o600); err != nil {
					t.Fatalf("rewrite Compose file: %v", err)
				}
			},
		},
		{
			name: "Compose file set",
			mutate: func(t *testing.T, db *store.Store, scope runtimescope.Scope, project *store.ProjectRecord) {
				t.Helper()
				override := filepath.Join(project.WorkingDir, "compose.override.yaml")
				if err := os.WriteFile(override, []byte("services:\n  app:\n    environment:\n      MODE: reviewed-later\n"), 0o600); err != nil {
					t.Fatalf("write Compose override: %v", err)
				}
				project.ComposeFiles = append(project.ComposeFiles, override)
				persistMutationGateTestProject(t, db, scope, *project)
			},
		},
		{
			name: "working directory",
			mutate: func(t *testing.T, db *store.Store, scope runtimescope.Scope, project *store.ProjectRecord) {
				t.Helper()
				workdir := t.TempDir()
				composeFile := filepath.Join(workdir, "compose.yaml")
				if err := os.WriteFile(composeFile, []byte("services:\n  app:\n    image: nginx:alpine\n"), 0o600); err != nil {
					t.Fatalf("write replacement Compose file: %v", err)
				}
				project.WorkingDir = workdir
				project.ComposeFiles = []string{composeFile}
				persistMutationGateTestProject(t, db, scope, *project)
			},
		},
		{
			name: "service configuration",
			mutate: func(t *testing.T, db *store.Store, scope runtimescope.Scope, project *store.ProjectRecord) {
				t.Helper()
				services, err := db.Projects().ListServices(context.Background(), project.ID)
				if err != nil {
					t.Fatalf("ListServices() error = %v", err)
				}
				services[0].ImageRef = "nginx:changed"
				if err := db.Projects().SaveSnapshot(context.Background(), scope, []store.ProjectRecord{*project}, services, project.LastSeenAt, time.Time{}); err != nil {
					t.Fatalf("SaveSnapshot(changed service) error = %v", err)
				}
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			db, scope, project := seedMutationGateTestProject(t, "changed-"+strings.ReplaceAll(strings.ToLower(testCase.name), " ", "-"))
			runner := &mutationGateTestRunner{}
			plans := security.NewProjectPlanStore(nil)
			t.Cleanup(plans.Close)
			service := &ProjectService{
				Client:   composecore.NewClient(runner),
				Projects: db.Projects(),
				Plans:    plans,
				Scope:    scope,
			}

			plan, err := service.PlanDownProject(context.Background(), project.ID, false)
			if err != nil {
				t.Fatalf("PlanDownProject() error = %v", err)
			}
			testCase.mutate(t, db, scope, &project)
			if err := service.ApplyProjectPlan(context.Background(), plan.PlanID, project.Name); !apperror.IsCode(err, apperror.PlanExpired) {
				t.Fatalf("ApplyProjectPlan(changed target) error = %v, want PlanExpired", err)
			}
			if calls := runner.calls(); len(calls) != 0 {
				t.Fatalf("changed project plan reached Compose: %#v", calls)
			}
		})
	}
}

func seedMutationGateTestProject(t *testing.T, name string) (*store.Store, runtimescope.Scope, store.ProjectRecord) {
	t.Helper()
	db := openServiceTestStore(t)
	scope := runtimescope.Must("linux_native", "default")
	workdir := t.TempDir()
	composeFile := filepath.Join(workdir, "compose.yaml")
	if err := os.WriteFile(composeFile, []byte("services:\n  app:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("write Compose fixture: %v", err)
	}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	project := store.ProjectRecord{
		ID:           scope.ProviderID() + "/" + name,
		ProviderID:   scope.ProviderID(),
		ContextName:  scope.ContextName(),
		Name:         name,
		WorkingDir:   workdir,
		ComposeFiles: []string{composeFile},
		Status:       models.ProjectStatusStopped,
		Source:       store.ProjectSourceImported,
		LastSeenAt:   now,
	}
	service := store.ServiceRecord{
		ID:         project.ID + "/app",
		ProjectID:  project.ID,
		Name:       "app",
		ImageRef:   "nginx:alpine",
		LastSeenAt: now,
	}
	if err := db.Projects().SaveSnapshot(context.Background(), scope, []store.ProjectRecord{project}, []store.ServiceRecord{service}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	return db, scope, project
}

func persistMutationGateTestProject(t *testing.T, db *store.Store, scope runtimescope.Scope, project store.ProjectRecord) {
	t.Helper()
	services, err := db.Projects().ListServices(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("ListServices() error = %v", err)
	}
	if err := db.Projects().SaveSnapshot(context.Background(), scope, []store.ProjectRecord{project}, services, project.LastSeenAt, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(changed project) error = %v", err)
	}
}

func waitMutationGateSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitMutationGateResult(t *testing.T, result <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}
