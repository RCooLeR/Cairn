package security

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

type ProviderPlan struct {
	Plan       models.CommandPlan
	Action     string
	ProviderID string
}

type ProviderPlanStore struct {
	mu    sync.Mutex
	now   func() time.Time
	plans map[string]ProviderPlan
}

func NewProviderPlanStore(now func() time.Time) *ProviderPlanStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ProviderPlanStore{now: now, plans: map[string]ProviderPlan{}}
}

func (s *ProviderPlanStore) Save(plan ProviderPlan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[plan.Plan.PlanID] = plan
}

func (s *ProviderPlanStore) Take(ctx context.Context, planID string, typedName string) (ProviderPlan, error) {
	if err := ctx.Err(); err != nil {
		return ProviderPlan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[planID]
	if !ok {
		return ProviderPlan{}, apperror.New(apperror.PlanExpired, "Plan expired or was not found")
	}
	if s.now().After(plan.Plan.ExpiresAt) {
		delete(s.plans, planID)
		return ProviderPlan{}, apperror.New(apperror.PlanExpired, "Plan expired")
	}
	if err := RequireConfirmation(plan.Plan, typedName); err != nil {
		return ProviderPlan{}, err
	}
	delete(s.plans, planID)
	return plan, nil
}

func NewProviderLifecyclePlan(action string, providerID string, providerName string, command string, risk models.Risk, now time.Time) (ProviderPlan, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return ProviderPlan{}, apperror.New(apperror.Conflict, "Provider ID is required")
	}
	if action != "restart" {
		return ProviderPlan{}, apperror.New(apperror.Conflict, "Unsupported provider action", apperror.WithDetail(action))
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if risk == "" {
		risk = models.RiskNeedsConfirmation
	}
	if providerName == "" {
		providerName = providerID
	}
	if command == "" {
		command = "restart Docker backend for " + providerName
	}
	plan := models.CommandPlan{
		PlanID:   NewPlanID(),
		Title:    "Restart Docker backend",
		Risk:     risk,
		Commands: []models.PlannedCommand{{Order: 1, Command: command, Risk: risk, Explanation: "Restarts the selected Docker backend."}},
		Effects: []string{
			"Active Docker connections, log streams, metrics streams, and terminal sessions may be interrupted.",
			"Containers managed by Docker are not intentionally removed.",
		},
		ExpiresAt: now.Add(DefaultPlanTTL),
	}
	return ProviderPlan{Plan: plan, Action: action, ProviderID: providerID}, nil
}
