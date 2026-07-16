package security

import (
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

const (
	ProjectActionStart    = "start"
	ProjectActionStop     = "stop"
	ProjectActionRestart  = "restart"
	ProjectActionPull     = "pull"
	ProjectActionDeploy   = "deploy"
	ProjectActionRedeploy = "redeploy"
	ProjectActionDown     = "down"
)

type ProjectPlan struct {
	Plan          models.CommandPlan
	Action        string
	ProjectID     string
	RemoveVolumes bool
	Scope         runtimescope.Scope
}

type ProjectPlanStore struct {
	*commandPlanStore[ProjectPlan]
}

func NewProjectPlanStore(now func() time.Time) *ProjectPlanStore {
	return &ProjectPlanStore{commandPlanStore: newCommandPlanStore(now, func(plan ProjectPlan) models.CommandPlan { return plan.Plan })}
}

func NewProjectActionPlan(plan models.CommandPlan, action string, projectID string, removeVolumes bool, runtimeScope runtimescope.Scope) (ProjectPlan, error) {
	if !runtimeScope.Valid() {
		return ProjectPlan{}, apperror.New(apperror.ProviderNotReady, "Project plan runtime scope is required")
	}
	return newProjectActionPlan(plan, action, projectID, removeVolumes, runtimeScope)
}

func newProjectActionPlan(plan models.CommandPlan, action string, projectID string, removeVolumes bool, runtimeScope runtimescope.Scope) (ProjectPlan, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case ProjectActionStart, ProjectActionStop, ProjectActionRestart, ProjectActionPull, ProjectActionDeploy, ProjectActionRedeploy, ProjectActionDown:
	default:
		return ProjectPlan{}, apperror.New(apperror.Conflict, "Unsupported project action", apperror.WithDetail(action))
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ProjectPlan{}, apperror.New(apperror.Conflict, "Project ID is required")
	}
	if requiresTypedConfirmation(plan.Risk) && strings.TrimSpace(plan.RequiresTypedName) == "" {
		return ProjectPlan{}, apperror.New(apperror.ConfirmationRequired, "Typed confirmation is required")
	}
	return ProjectPlan{
		Plan:          plan,
		Action:        action,
		ProjectID:     projectID,
		RemoveVolumes: removeVolumes,
		Scope:         runtimeScope,
	}, nil
}
