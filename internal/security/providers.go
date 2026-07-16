package security

import (
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

type ProviderPlan struct {
	Plan       models.CommandPlan
	Action     string
	ProviderID string
	Scope      runtimescope.Scope
}

type ProviderPlanStore struct {
	*commandPlanStore[ProviderPlan]
}

func NewProviderPlanStore(now func() time.Time) *ProviderPlanStore {
	return &ProviderPlanStore{commandPlanStore: newCommandPlanStore(now, func(plan ProviderPlan) models.CommandPlan { return plan.Plan })}
}

func NewProviderLifecyclePlan(action string, providerID string, providerName string, command string, risk models.Risk, scope runtimescope.Scope, now time.Time) (ProviderPlan, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return ProviderPlan{}, apperror.New(apperror.Conflict, "Provider ID is required")
	}
	if action != "restart" && action != "stop" {
		return ProviderPlan{}, apperror.New(apperror.Conflict, "Unsupported provider action", apperror.WithDetail(action))
	}
	if !scope.Valid() || scope.ProviderID() != providerID {
		return ProviderPlan{}, apperror.New(apperror.Conflict, "Provider runtime scope is required")
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
	verb := "Restart"
	explanation := "Restarts the selected Docker backend."
	effects := []string{
		"Active Docker connections, log streams, metrics streams, and terminal sessions may be interrupted.",
		"Containers managed by Docker are not intentionally removed.",
	}
	if action == "stop" {
		verb = "Stop"
		explanation = "Stops the selected Docker backend."
		effects = []string{
			"Docker API access, log streams, metrics streams, terminal sessions, and background jobs will stop.",
			"Running containers may stop; behavior depends on the daemon live-restore configuration.",
		}
	}
	if command == "" {
		command = strings.ToLower(verb) + " Docker backend for " + displayName
	}
	plan := models.CommandPlan{
		PlanID:    NewTypedPlanID("provider"),
		Title:     verb + " Docker backend",
		Risk:      risk,
		Commands:  []models.PlannedCommand{{Order: 1, Command: command, Risk: risk, Explanation: explanation}},
		Effects:   effects,
		ExpiresAt: now.Add(DefaultPlanTTL),
	}
	if requiresTypedConfirmation(plan.Risk) {
		plan.RequiresTypedName = typedName
	}
	return ProviderPlan{Plan: plan, Action: action, ProviderID: providerID, Scope: scope}, nil
}
