package security

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

func TestContainerRiskMapping(t *testing.T) {
	t.Parallel()
	running := models.ContainerSummary{Name: "web", State: "running"}
	stopped := models.ContainerSummary{Name: "worker", State: "exited"}
	tests := []struct {
		name   string
		action string
		input  models.ContainerSummary
		opts   models.RemoveContainerOptions
		want   models.Risk
	}{
		{name: "start safe", action: ContainerActionStart, input: stopped, want: models.RiskSafe},
		{name: "stop safe", action: ContainerActionStop, input: running, want: models.RiskSafe},
		{name: "restart safe", action: ContainerActionRestart, input: running, want: models.RiskSafe},
		{name: "kill needs confirmation", action: ContainerActionKill, input: running, want: models.RiskNeedsConfirmation},
		{name: "remove stopped needs confirmation", action: ContainerActionRemove, input: stopped, want: models.RiskNeedsConfirmation},
		{name: "remove running force destructive", action: ContainerActionRemove, input: running, opts: models.RemoveContainerOptions{Force: true}, want: models.RiskDestructive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ContainerRisk(tt.action, tt.input, tt.opts); got != tt.want {
				t.Fatalf("ContainerRisk() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanStoreExpiresAndRequiresTypedName(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	store := NewPlanStore(func() time.Time { return now })
	plan := ContainerPlan{
		Plan: models.CommandPlan{
			PlanID:            "plan-1",
			Risk:              models.RiskDangerous,
			RequiresTypedName: "web",
			ExpiresAt:         now.Add(DefaultPlanTTL),
		},
		Action: ContainerActionRemove,
		IDs:    []string{"container-1"},
	}
	if err := store.Save(plan); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if _, err := store.Take(context.Background(), "plan-1", "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("Take() error = %v, want E_CONFIRMATION_REQUIRED", err)
	}
	taken, err := store.Take(context.Background(), "plan-1", "web")
	if err != nil {
		t.Fatalf("Take() after corrected typed name error = %v", err)
	}
	if taken.Action != ContainerActionRemove {
		t.Fatalf("plan action = %q", taken.Action)
	}
	if _, err := store.Take(context.Background(), "plan-1", "web"); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("Take() after consumed error = %v, want E_PLAN_EXPIRED", err)
	}
}

func TestRequireConfirmationTrimsTypedNameAndAllowsSafePlans(t *testing.T) {
	t.Parallel()

	if err := RequireConfirmation(models.CommandPlan{Risk: models.RiskSafe, RequiresTypedName: "web"}, "wrong"); err != nil {
		t.Fatalf("safe plan confirmation error = %v, want nil", err)
	}
	if err := RequireConfirmation(models.CommandPlan{Risk: models.RiskNeedsConfirmation}, ""); err != nil {
		t.Fatalf("confirmation-only plan error = %v, want nil", err)
	}
	if err := RequireConfirmation(models.CommandPlan{Risk: models.RiskDestructive}, ""); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("destructive plan without typed name error = %v, want confirmation required", err)
	}
	if err := RequireConfirmation(models.CommandPlan{Risk: models.RiskDangerous, RequiresTypedName: " web "}, "web"); err != nil {
		t.Fatalf("trimmed typed name error = %v, want nil", err)
	}
}

func TestPlanStoreContextAndExpiry(t *testing.T) {
	now := time.Date(2026, 6, 13, 11, 0, 0, 0, time.UTC)
	store := NewPlanStore(func() time.Time { return now })
	if err := store.Save(ContainerPlan{Plan: models.CommandPlan{PlanID: "expired", ExpiresAt: now.Add(-time.Second)}}); err != nil {
		t.Fatalf("Save(expired) error = %v", err)
	}
	if _, err := store.Take(context.Background(), "expired", ""); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("expired Take error = %v, want E_PLAN_EXPIRED", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Save(ContainerPlan{Plan: models.CommandPlan{PlanID: "ctx", ExpiresAt: now.Add(time.Minute)}}); err != nil {
		t.Fatalf("Save(ctx) error = %v", err)
	}
	if _, err := store.Take(ctx, "ctx", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Take error = %v, want context.Canceled", err)
	}
}

func TestNewContainerActionPlanCommandsEffectsAndRisks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	running := models.ContainerSummary{ID: "0123456789abcdef", Name: "web", State: "running"}
	stopped := models.ContainerSummary{ID: "fedcba9876543210", Name: "worker", State: "exited"}

	tests := []struct {
		name       string
		action     string
		containers []models.ContainerSummary
		timeout    int
		opts       models.RemoveContainerOptions
		wantTitle  string
		wantRisk   models.Risk
		wantCmd    string
		wantEffect string
	}{
		{
			name:       "start single",
			action:     " START ",
			containers: []models.ContainerSummary{stopped},
			wantTitle:  "Start worker",
			wantRisk:   models.RiskSafe,
			wantCmd:    "docker start worker",
			wantEffect: "worker: Starts the selected container. Paused containers are unpaused.",
		},
		{
			name:       "stop timeout",
			action:     ContainerActionStop,
			containers: []models.ContainerSummary{running},
			timeout:    15,
			wantTitle:  "Stop web",
			wantRisk:   models.RiskSafe,
			wantCmd:    "docker stop --time 15 web",
			wantEffect: "web: Stops the selected container gracefully before Docker applies its timeout.",
		},
		{
			name:       "stop quoted name",
			action:     ContainerActionStop,
			containers: []models.ContainerSummary{{ID: "space", Name: "web app", State: "running"}},
			wantTitle:  "Stop web app",
			wantRisk:   models.RiskSafe,
			wantCmd:    "docker stop 'web app'",
			wantEffect: "web app: Stops the selected container gracefully before Docker applies its timeout.",
		},
		{
			name:       "restart timeout",
			action:     ContainerActionRestart,
			containers: []models.ContainerSummary{running},
			timeout:    5,
			wantTitle:  "Restart web",
			wantRisk:   models.RiskSafe,
			wantCmd:    "docker restart --time 5 web",
			wantEffect: "web: Restarts the selected container with Docker's normal stop/start sequence.",
		},
		{
			name:       "kill multiple",
			action:     ContainerActionKill,
			containers: []models.ContainerSummary{running, stopped},
			wantTitle:  "Kill 2 containers",
			wantRisk:   models.RiskNeedsConfirmation,
			wantCmd:    "docker kill web worker",
			wantEffect: "web: Immediately sends SIGKILL to the selected container.",
		},
		{
			name:       "remove stopped",
			action:     ContainerActionRemove,
			containers: []models.ContainerSummary{stopped},
			wantTitle:  "Remove worker",
			wantRisk:   models.RiskNeedsConfirmation,
			wantCmd:    "docker rm worker",
			wantEffect: "worker: Removes the selected stopped container.",
		},
		{
			name:       "remove running force volumes",
			action:     ContainerActionRemove,
			containers: []models.ContainerSummary{running},
			opts:       models.RemoveContainerOptions{Force: true, RemoveVolumes: true},
			wantTitle:  "Remove web",
			wantRisk:   models.RiskDestructive,
			wantCmd:    "docker rm --force --volumes web",
			wantEffect: "web: Removes the selected container using Docker force removal.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan, err := NewContainerActionPlan(tt.action, tt.containers, tt.timeout, tt.opts, now)
			if err != nil {
				t.Fatalf("NewContainerActionPlan error = %v", err)
			}
			if plan.Plan.Title != tt.wantTitle ||
				plan.Plan.Risk != tt.wantRisk ||
				plan.Plan.Commands[0].Command != tt.wantCmd ||
				plan.Plan.ExpiresAt != now.Add(DefaultPlanTTL) {
				t.Fatalf("plan = %#v", plan.Plan)
			}
			if !strings.HasPrefix(plan.Plan.PlanID, "plan-container-") {
				t.Fatalf("PlanID = %q, want plan-container-*", plan.Plan.PlanID)
			}
			if len(plan.Plan.Effects) == 0 || plan.Plan.Effects[0] != tt.wantEffect {
				t.Fatalf("effects = %#v, want first %q", plan.Plan.Effects, tt.wantEffect)
			}
			if tt.opts.RemoveVolumes && !slices.Contains(plan.Plan.Effects, "Anonymous volumes attached to the container will also be removed.") {
				t.Fatalf("missing anonymous volume effect in %#v", plan.Plan.Effects)
			}
			if (tt.wantRisk == models.RiskDangerous || tt.wantRisk == models.RiskDestructive) && len(tt.containers) == 1 && plan.Plan.RequiresTypedName != tt.containers[0].Name {
				t.Fatalf("RequiresTypedName = %q, want %q", plan.Plan.RequiresTypedName, tt.containers[0].Name)
			}
		})
	}
}

func TestNewContainerActionPlanValidationAndFallbackLabels(t *testing.T) {
	t.Parallel()

	if _, err := NewContainerActionPlan("pause", []models.ContainerSummary{{Name: "web"}}, 0, models.RemoveContainerOptions{}, time.Time{}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("unsupported action error = %v, want E_CONFLICT", err)
	}
	if _, err := NewContainerActionPlan(ContainerActionStart, nil, 0, models.RemoveContainerOptions{}, time.Time{}); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("empty containers error = %v, want E_NOT_FOUND", err)
	}

	plan, err := NewContainerActionPlan(ContainerActionStart, []models.ContainerSummary{{ID: "1234567890abcdef"}}, 0, models.RemoveContainerOptions{}, time.Time{})
	if err != nil {
		t.Fatalf("fallback plan error = %v", err)
	}
	if !strings.HasPrefix(plan.Plan.Title, "Start container") {
		t.Fatalf("fallback title = %q", plan.Plan.Title)
	}
	if len(plan.Plan.Effects) != 1 || !strings.HasPrefix(plan.Plan.Effects[0], "1234567890ab:") {
		t.Fatalf("fallback effects = %#v", plan.Plan.Effects)
	}
	if got := NewPlanID(); !strings.HasPrefix(got, "plan-") {
		t.Fatalf("NewPlanID() = %q, want plan-*", got)
	}
	if got := NewTypedPlanID("object"); !strings.HasPrefix(got, "plan-object-") {
		t.Fatalf("NewTypedPlanID() = %q, want plan-object-*", got)
	}
	if got := NewJobID("project"); !strings.HasPrefix(got, "project-") || strings.Contains(got, "plan-") {
		t.Fatalf("NewJobID() = %q, want project-* without plan prefix", got)
	}
}

func TestTitleWordHandlesUTF8(t *testing.T) {
	t.Parallel()

	if got, want := titleWord("запуск"), "Запуск"; got != want {
		t.Fatalf("titleWord() = %q, want %q", got, want)
	}
}

func TestProjectPlanStoreTake(t *testing.T) {
	now := time.Date(2026, 6, 13, 13, 0, 0, 0, time.UTC)
	store := NewProjectPlanStore(func() time.Time { return now })
	if err := store.Save(ProjectPlan{
		Plan: models.CommandPlan{
			PlanID:            "project-plan",
			Risk:              models.RiskDangerous,
			RequiresTypedName: "app-db",
			ExpiresAt:         now.Add(DefaultPlanTTL),
		},
		Action:        ProjectActionDown,
		ProjectID:     "linux_native/app-db",
		RemoveVolumes: true,
	}); err != nil {
		t.Fatalf("Save(project) error = %v", err)
	}

	if _, err := store.Take(context.Background(), "project-plan", "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("wrong typed project Take error = %v, want E_CONFIRMATION_REQUIRED", err)
	}
	taken, err := store.Take(context.Background(), "project-plan", "app-db")
	if err != nil {
		t.Fatalf("project Take error = %v", err)
	}
	if taken.Action != ProjectActionDown || !taken.RemoveVolumes {
		t.Fatalf("project plan = %#v", taken)
	}
	if _, err := store.Take(context.Background(), "project-plan", "app-db"); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("consumed project Take error = %v, want E_PLAN_EXPIRED", err)
	}

	expiring := NewProjectPlanStore(func() time.Time { return now })
	if err := expiring.Save(ProjectPlan{Plan: models.CommandPlan{PlanID: "expired", ExpiresAt: now.Add(-time.Second)}}); err != nil {
		t.Fatalf("Save(expired project) error = %v", err)
	}
	if _, err := expiring.Take(context.Background(), "expired", ""); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("expired project Take error = %v, want E_PLAN_EXPIRED", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := expiring.Take(ctx, "missing", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled project Take error = %v, want context.Canceled", err)
	}
}

func TestPlanStoresPruneExpiredEntriesOnSave(t *testing.T) {
	now := time.Date(2026, 6, 13, 14, 0, 0, 0, time.UTC)
	store := NewPlanStore(func() time.Time { return now })
	if err := store.Save(ContainerPlan{Plan: models.CommandPlan{PlanID: "expired", ExpiresAt: now.Add(-time.Second)}}); err != nil {
		t.Fatalf("Save(expired) error = %v", err)
	}
	if err := store.Save(ContainerPlan{Plan: models.CommandPlan{PlanID: "fresh", ExpiresAt: now.Add(time.Minute)}}); err != nil {
		t.Fatalf("Save(fresh) error = %v", err)
	}

	if _, err := store.Take(context.Background(), "expired", ""); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("expired plan error = %v, want E_PLAN_EXPIRED", err)
	}
	if _, err := store.Take(context.Background(), "fresh", ""); err != nil {
		t.Fatalf("fresh plan error = %v", err)
	}
}

func TestPlanStoreJanitorPrunesExpiredEntriesWithoutTraffic(t *testing.T) {
	now := time.Date(2026, 6, 13, 14, 15, 0, 0, time.UTC)
	store := NewPlanStore(func() time.Time { return now })
	store.startJanitor(time.Millisecond)
	t.Cleanup(store.Close)

	if err := store.Save(ContainerPlan{Plan: models.CommandPlan{PlanID: "fresh", ExpiresAt: now.Add(time.Minute)}}); err != nil {
		t.Fatalf("Save(fresh) error = %v", err)
	}
	now = now.Add(2 * time.Minute)

	deadline := time.After(time.Second)
	for {
		store.mu.Lock()
		count := len(store.plans)
		store.mu.Unlock()
		if count == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expired plans were not pruned by janitor; count=%d", count)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestPlanStoreRejectsHighRiskWithoutTypedName(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 13, 14, 30, 0, 0, time.UTC)
	plan := models.CommandPlan{
		PlanID:    "unsafe-plan",
		Risk:      models.RiskDangerous,
		ExpiresAt: now.Add(time.Minute),
	}
	if err := NewPlanStore(func() time.Time { return now }).Save(ContainerPlan{Plan: plan}); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("container Save() error = %v, want E_CONFIRMATION_REQUIRED", err)
	}
	if err := NewDockerObjectPlanStore(func() time.Time { return now }).Save(DockerObjectPlan{Plan: plan}); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("object Save() error = %v, want E_CONFIRMATION_REQUIRED", err)
	}
	if err := NewProjectPlanStore(func() time.Time { return now }).Save(ProjectPlan{Plan: plan}); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("project Save() error = %v, want E_CONFIRMATION_REQUIRED", err)
	}
	if err := NewProviderPlanStore(func() time.Time { return now }).Save(ProviderPlan{Plan: plan}); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("provider Save() error = %v, want E_CONFIRMATION_REQUIRED", err)
	}
}

func TestDockerObjectPlansDeclareTypedConfirmationForHighRisk(t *testing.T) {
	now := time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC)
	imagePlan, err := NewRemoveImagePlan(models.ImageSummary{ID: "sha256:abc", RepoTags: []string{"app:latest"}}, true, now)
	if err != nil {
		t.Fatalf("NewRemoveImagePlan() error = %v", err)
	}
	if imagePlan.Plan.Risk != models.RiskDestructive || imagePlan.Plan.RequiresTypedName != "app:latest" {
		t.Fatalf("image plan = %#v", imagePlan.Plan)
	}

	prunePlan, err := NewPrunePlan("images", now)
	if err != nil {
		t.Fatalf("NewPrunePlan() error = %v", err)
	}
	if prunePlan.Plan.Risk != models.RiskDestructive || prunePlan.Plan.RequiresTypedName != "prune" {
		t.Fatalf("prune plan = %#v", prunePlan.Plan)
	}
}

func TestNewProjectActionPlanRequiresTypedConfirmationForHighRisk(t *testing.T) {
	plan := models.CommandPlan{
		PlanID:    "plan-project",
		Risk:      models.RiskDestructive,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if _, err := NewProjectActionPlan(plan, ProjectActionDown, "provider/app", false); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("project constructor error = %v, want confirmation required", err)
	}
	plan.RequiresTypedName = "app"
	projectPlan, err := NewProjectActionPlan(plan, ProjectActionDown, "provider/app", false)
	if err != nil {
		t.Fatalf("NewProjectActionPlan() error = %v", err)
	}
	if projectPlan.Action != ProjectActionDown || projectPlan.ProjectID != "provider/app" {
		t.Fatalf("project plan = %#v", projectPlan)
	}
}
