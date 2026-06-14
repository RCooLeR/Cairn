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
	store.Save(plan)

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
	if err := RequireConfirmation(models.CommandPlan{Risk: models.RiskDangerous, RequiresTypedName: " web "}, "web"); err != nil {
		t.Fatalf("trimmed typed name error = %v, want nil", err)
	}
}

func TestPlanStoreContextAndExpiry(t *testing.T) {
	now := time.Date(2026, 6, 13, 11, 0, 0, 0, time.UTC)
	store := NewPlanStore(func() time.Time { return now })
	store.Save(ContainerPlan{Plan: models.CommandPlan{PlanID: "expired", ExpiresAt: now.Add(-time.Second)}})
	if _, err := store.Take(context.Background(), "expired", ""); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("expired Take error = %v, want E_PLAN_EXPIRED", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store.Save(ContainerPlan{Plan: models.CommandPlan{PlanID: "ctx", ExpiresAt: now.Add(time.Minute)}})
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
			if len(plan.Plan.Effects) == 0 || plan.Plan.Effects[0] != tt.wantEffect {
				t.Fatalf("effects = %#v, want first %q", plan.Plan.Effects, tt.wantEffect)
			}
			if tt.opts.RemoveVolumes && !slices.Contains(plan.Plan.Effects, "Anonymous volumes attached to the container will also be removed.") {
				t.Fatalf("missing anonymous volume effect in %#v", plan.Plan.Effects)
			}
			if tt.wantRisk == models.RiskDangerous && len(tt.containers) == 1 && plan.Plan.RequiresTypedName != tt.containers[0].Name {
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
}

func TestProjectPlanStoreTake(t *testing.T) {
	now := time.Date(2026, 6, 13, 13, 0, 0, 0, time.UTC)
	store := NewProjectPlanStore(func() time.Time { return now })
	store.Save(ProjectPlan{
		Plan: models.CommandPlan{
			PlanID:            "project-plan",
			Risk:              models.RiskDangerous,
			RequiresTypedName: "app-db",
			ExpiresAt:         now.Add(DefaultPlanTTL),
		},
		Action:        ProjectActionDown,
		ProjectID:     "linux_native/app-db",
		RemoveVolumes: true,
	})

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
	expiring.Save(ProjectPlan{Plan: models.CommandPlan{PlanID: "expired", ExpiresAt: now.Add(-time.Second)}})
	if _, err := expiring.Take(context.Background(), "expired", ""); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("expired project Take error = %v, want E_PLAN_EXPIRED", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := expiring.Take(ctx, "missing", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled project Take error = %v, want context.Canceled", err)
	}
}
