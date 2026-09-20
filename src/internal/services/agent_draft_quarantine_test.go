package services

import (
	"context"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

func TestAgentServiceDraftProjectFileIsQuarantinedByDefault(t *testing.T) {
	result, err := (&AgentService{}).DraftProjectFile(context.Background(), models.AgentDraftFileRequest{
		ProjectID:   "project",
		Path:        ".env",
		Instruction: "suggest safe placeholders",
	})
	if result != nil || !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("DraftProjectFile(default) = (%#v, %v), want quarantined conflict", result, err)
	}
}
