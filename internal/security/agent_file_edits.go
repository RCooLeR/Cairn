package security

import (
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

type AgentFileEditPlan struct {
	Plan         models.CommandPlan
	ProjectID    string
	ProjectName  string
	WorkingDir   string
	RelativePath string
	AbsolutePath string
	Content      string
	OriginalHash string
	CreateFile   bool
}

type AgentFileEditPlanStore struct {
	*commandPlanStore[AgentFileEditPlan]
}

func NewAgentFileEditPlanStore(now func() time.Time) *AgentFileEditPlanStore {
	return &AgentFileEditPlanStore{
		commandPlanStore: newCommandPlanStore(now, func(plan AgentFileEditPlan) models.CommandPlan {
			return plan.Plan
		}),
	}
}
