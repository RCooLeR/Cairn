package services

import (
	"context"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
)

type completionAuditFailureRunner struct {
	*fakeComposeRunner
	afterMutation func()
}

func (r *completionAuditFailureRunner) RunCompose(ctx context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
	return r.RunComposeEnv(ctx, workdir, nil, args...)
}

func (r *completionAuditFailureRunner) RunComposeEnv(ctx context.Context, workdir string, env []string, args ...string) (*providers.CommandResult, error) {
	result, err := r.fakeComposeRunner.RunComposeEnv(ctx, workdir, env, args...)
	if isMutationGateTestCommand(args) {
		r.afterMutation()
	}
	return result, err
}

type completionAuditFailureDocker struct {
	*fakeDockerClient
	afterMutation func()
}

func (d *completionAuditFailureDocker) StopContainer(ctx context.Context, id string, timeout int) error {
	err := d.fakeDockerClient.StopContainer(ctx, id, timeout)
	d.afterMutation()
	return err
}

func TestProjectJobsFinishWhenCompletionAuditFails(t *testing.T) {
	for _, kind := range []string{"project", "service", "stale-project"} {
		t.Run(kind, func(t *testing.T) {
			db, scope, project := seedMutationGateTestProject(t, "audit-completion-"+kind)
			auditDB := openServiceTestStore(t)
			closeAudit := func() {
				if err := auditDB.Close(); err != nil {
					t.Fatalf("close audit database after successful mutation: %v", err)
				}
			}
			runner := &completionAuditFailureRunner{fakeComposeRunner: newFakeComposeRunner(), afterMutation: closeAudit}
			events := bus.New()
			t.Cleanup(events.Close)
			ctx := context.Background()
			progress := events.Subscribe(ctx, bus.TopicJobProgress, 8)
			done := events.Subscribe(ctx, bus.TopicJobDone, 8)
			changed := events.Subscribe(ctx, bus.TopicProjectChanged, 8)
			var err error
			switch kind {
			case "project":
				service := &ProjectService{Client: composecore.NewClient(runner), Projects: db.Projects(), Scope: scope, Audit: auditDB.Audit(), Events: events}
				err = service.StartProject(ctx, project.ID)
			case "service":
				service := &ComposeService{Client: composecore.NewClient(runner), Projects: db.Projects(), Scope: scope, Audit: auditDB.Audit(), Events: events}
				err = service.StartServices(ctx, project.ID, []string{"app"})
			case "stale-project":
				docker := &completionAuditFailureDocker{fakeDockerClient: &fakeDockerClient{container: models.ContainerSummary{ID: "app-1", Name: "app", ProjectID: project.ID, State: "running"}}, afterMutation: closeAudit}
				service := &ProjectService{Docker: docker, Projects: db.Projects(), Scope: scope, Audit: auditDB.Audit(), Events: events}
				err = service.runStaleProjectContainerAction(ctx, security.ProjectActionStop, project, false, nil, "")
			}
			if !apperror.IsCode(err, apperror.Internal) {
				t.Fatalf("action error = %v, want audit failure", err)
			}
			started, ok := receiveEventPayload(t, progress, time.Second).(jobProgressPayload)
			if !ok {
				t.Fatal("missing job progress")
			}
			finished, ok := receiveEventPayload(t, done, time.Second).(jobDonePayload)
			if !ok || finished.JobID != started.JobID || finished.Error == "" || finished.Result == "success" {
				t.Fatalf("completion = %#v, want terminal audit error for job %q", finished, started.JobID)
			}
			if got := receiveEventPayload(t, changed, time.Second); got == nil {
				t.Fatal("successful backend mutation did not publish project refresh")
			}
		})
	}
}
