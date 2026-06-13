package security

import (
	"context"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	ProjectActionStart    = "start"
	ProjectActionStop     = "stop"
	ProjectActionRestart  = "restart"
	ProjectActionPull     = "pull"
	ProjectActionRedeploy = "redeploy"
	ProjectActionDown     = "down"
)

type ProjectPlan struct {
	Plan          models.CommandPlan
	Action        string
	ProjectID     string
	RemoveVolumes bool
}

type ProjectPlanStore struct {
	mu    sync.Mutex
	now   func() time.Time
	plans map[string]ProjectPlan
}

func NewProjectPlanStore(now func() time.Time) *ProjectPlanStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ProjectPlanStore{now: now, plans: map[string]ProjectPlan{}}
}

func (s *ProjectPlanStore) Save(plan ProjectPlan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[plan.Plan.PlanID] = plan
}

func (s *ProjectPlanStore) Take(ctx context.Context, planID string, typedName string) (ProjectPlan, error) {
	if err := ctx.Err(); err != nil {
		return ProjectPlan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[planID]
	if !ok {
		return ProjectPlan{}, apperror.New(apperror.PlanExpired, "Plan expired or was not found")
	}
	if s.now().After(plan.Plan.ExpiresAt) {
		delete(s.plans, planID)
		return ProjectPlan{}, apperror.New(apperror.PlanExpired, "Plan expired")
	}
	if err := RequireConfirmation(plan.Plan, typedName); err != nil {
		return ProjectPlan{}, err
	}
	delete(s.plans, planID)
	return plan, nil
}
