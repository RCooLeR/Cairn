package security

import (
	"context"
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
