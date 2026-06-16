package security

import (
	"strings"
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
	*commandPlanStore[ProviderPlan]
}

func NewProviderPlanStore(now func() time.Time) *ProviderPlanStore {
	return &ProviderPlanStore{commandPlanStore: newCommandPlanStore(now, func(plan ProviderPlan) models.CommandPlan { return plan.Plan })}
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
	displayName := providerName
	typedName := providerName
	if displayName == "" {
		displayName = "selected Docker backend"
		typedName = providerID
	}
	if command == "" {
		command = "restart Docker backend for " + displayName
	}
	plan := models.CommandPlan{
		PlanID:   NewTypedPlanID("provider"),
		Title:    "Restart Docker backend",
		Risk:     risk,
		Commands: []models.PlannedCommand{{Order: 1, Command: command, Risk: risk, Explanation: "Restarts the selected Docker backend."}},
		Effects: []string{
			"Active Docker connections, log streams, metrics streams, and terminal sessions may be interrupted.",
			"Containers managed by Docker are not intentionally removed.",
		},
		ExpiresAt: now.Add(DefaultPlanTTL),
	}
	if requiresTypedConfirmation(plan.Risk) {
		plan.RequiresTypedName = typedName
	}
	return ProviderPlan{Plan: plan, Action: action, ProviderID: providerID}, nil
}
