package security

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

type commandPlanStore[T any] struct {
	mu            sync.Mutex
	now           func() time.Time
	plans         map[string]T
	toCommandPlan func(T) models.CommandPlan
}

func newCommandPlanStore[T any](now func() time.Time, toCommandPlan func(T) models.CommandPlan) *commandPlanStore[T] {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &commandPlanStore[T]{
		now:           now,
		plans:         map[string]T{},
		toCommandPlan: toCommandPlan,
	}
}

func (s *commandPlanStore[T]) Save(plan T) error {
	commandPlan := s.toCommandPlan(plan)
	if err := validateStoredPlan(commandPlan); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(s.now())
	s.plans[commandPlan.PlanID] = plan
	return nil
}

func (s *commandPlanStore[T]) Take(ctx context.Context, planID string, typedName string) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[planID]
	if !ok {
		return zero, apperror.New(apperror.PlanExpired, "Plan expired or was not found")
	}
	commandPlan := s.toCommandPlan(plan)
	now := s.now()
	if now.After(commandPlan.ExpiresAt) {
		delete(s.plans, planID)
		return zero, apperror.New(apperror.PlanExpired, "Plan expired")
	}
	s.pruneExpiredLocked(now)
	if err := RequireConfirmation(commandPlan, typedName); err != nil {
		return zero, err
	}
	delete(s.plans, planID)
	return plan, nil
}

func (s *commandPlanStore[T]) pruneExpiredLocked(now time.Time) {
	for id, plan := range s.plans {
		expiresAt := s.toCommandPlan(plan).ExpiresAt
		if !expiresAt.IsZero() && now.After(expiresAt) {
			delete(s.plans, id)
		}
	}
}

func validateStoredPlan(plan models.CommandPlan) error {
	if strings.TrimSpace(plan.PlanID) == "" {
		return apperror.New(apperror.Conflict, "Plan ID is required")
	}
	if requiresTypedConfirmation(plan.Risk) && strings.TrimSpace(plan.RequiresTypedName) == "" {
		return apperror.New(
			apperror.ConfirmationRequired,
			"Typed confirmation is required",
			apperror.WithDetail("High-risk plans must declare a typed confirmation phrase before they can be saved."),
		)
	}
	return nil
}
