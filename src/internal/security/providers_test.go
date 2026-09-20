package security

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestNewProviderLifecyclePlanValidationAndDefaults(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)

	scope := runtimescope.Must("linux_native", "unix:///var/run/docker.sock")
	if _, err := NewProviderLifecyclePlan("start", "linux_native", "Linux", "", "", scope, now); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("unsupported action error = %v, want E_CONFLICT", err)
	}
	if _, err := NewProviderLifecyclePlan("restart", "  ", "Linux", "", "", scope, now); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("blank provider error = %v, want E_CONFLICT", err)
	}

	plan, err := NewProviderLifecyclePlan(" RESTART ", "linux_native", "", "", "", scope, now)
	if err != nil {
		t.Fatalf("NewProviderLifecyclePlan() error = %v", err)
	}
	if plan.Action != "restart" || plan.ProviderID != "linux_native" {
		t.Fatalf("provider plan metadata = %#v", plan)
	}
	if !plan.Scope.Equal(scope) {
		t.Fatalf("provider plan scope = %#v, want exact runtime scope", plan.Scope)
	}
	if !strings.HasPrefix(plan.Plan.PlanID, "plan-provider-") {
		t.Fatalf("PlanID = %q, want plan-provider-*", plan.Plan.PlanID)
	}
	if plan.Plan.Risk != models.RiskNeedsConfirmation || plan.Plan.RequiresTypedName != "" {
		t.Fatalf("default risk/confirmation = %q/%q", plan.Plan.Risk, plan.Plan.RequiresTypedName)
	}
	if got, want := plan.Plan.Commands[0].Command, "restart Docker backend for selected Docker backend"; got != want {
		t.Fatalf("default command = %q, want %q", got, want)
	}
	if plan.Plan.ExpiresAt != now.Add(DefaultPlanTTL) {
		t.Fatalf("ExpiresAt = %v, want %v", plan.Plan.ExpiresAt, now.Add(DefaultPlanTTL))
	}
}

func TestProviderLifecyclePlanRequiresTypedNameForHighRisk(t *testing.T) {
	now := time.Date(2026, 6, 16, 11, 0, 0, 0, time.UTC)
	plan, err := NewProviderLifecyclePlan("restart", "windows_wsl_ubuntu", "Windows WSL Ubuntu", "wsl docker restart", models.RiskDangerous, runtimescope.Must("windows_wsl_ubuntu", "wsl:ubuntu"), now)
	if err != nil {
		t.Fatalf("NewProviderLifecyclePlan() error = %v", err)
	}
	if plan.Plan.RequiresTypedName != "Windows WSL Ubuntu" {
		t.Fatalf("RequiresTypedName = %q, want provider name", plan.Plan.RequiresTypedName)
	}

	store := NewProviderPlanStore(func() time.Time { return now })
	if err := store.Save(plan); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := store.Take(context.Background(), plan.Plan.PlanID, "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("wrong typed name error = %v, want E_CONFIRMATION_REQUIRED", err)
	}
	taken, err := store.Take(context.Background(), plan.Plan.PlanID, "Windows WSL Ubuntu")
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	if taken.Action != "restart" || taken.ProviderID != "windows_wsl_ubuntu" {
		t.Fatalf("taken plan = %#v", taken)
	}
	if _, err := store.Take(context.Background(), plan.Plan.PlanID, "Windows WSL Ubuntu"); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("consumed plan error = %v, want E_PLAN_EXPIRED", err)
	}
}

func TestProviderLifecyclePlanDescribesStopImpact(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 16, 11, 30, 0, 0, time.UTC)
	plan, err := NewProviderLifecyclePlan(
		"stop",
		"windows_wsl_ubuntu",
		"Windows WSL Ubuntu",
		"wsl.exe -d Ubuntu -u root -- systemctl stop docker",
		models.RiskNeedsConfirmation,
		runtimescope.Must("windows_wsl_ubuntu", "wsl:ubuntu"),
		now,
	)
	if err != nil {
		t.Fatalf("NewProviderLifecyclePlan(stop) error = %v", err)
	}
	if plan.Plan.Title != "Stop Docker backend" || len(plan.Plan.Effects) != 2 {
		t.Fatalf("stop plan = %#v, want explicit stop impact", plan.Plan)
	}
	if !strings.Contains(plan.Plan.Effects[0], "will stop") || !strings.Contains(plan.Plan.Effects[1], "Running containers may stop") {
		t.Fatalf("stop effects = %#v", plan.Plan.Effects)
	}
}
