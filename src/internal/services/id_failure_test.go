package services

import (
	"context"
	"errors"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

type failingServiceEntropyReader struct{}

func (failingServiceEntropyReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestComposeJobEntropyFailureDoesNotRunAction(t *testing.T) {
	service := &ComposeService{IDs: security.NewIDSource(failingServiceEntropyReader{})}
	runCalled := false
	err := service.runComposeServiceAction(
		context.Background(),
		store.ProjectRecord{ID: "project-1", ProviderID: "linux_native"},
		"service.restart",
		"docker compose restart api",
		func() (*providers.CommandResult, error) {
			runCalled = true
			return &providers.CommandResult{}, nil
		},
	)
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("runComposeServiceAction() error = %v, want %s", err, apperror.Internal)
	}
	if runCalled {
		t.Fatal("compose action ran despite job ID generation failure")
	}
}

func TestDockerPlanEntropyFailureDoesNotMutateDocker(t *testing.T) {
	client := newFakeDockerClient()
	plans := security.NewDockerObjectPlanStore(nil)
	t.Cleanup(plans.Close)
	service := &DockerService{
		Client:      client,
		ObjectPlans: plans,
		IDs:         security.NewIDSource(failingServiceEntropyReader{}),
		Scope:       runtimescope.Must("linux_native", "default"),
	}

	plan, err := service.PlanPrune(context.Background(), "images")
	if plan != nil {
		t.Fatalf("PlanPrune() plan = %#v, want nil", plan)
	}
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("PlanPrune() error = %v, want %s", err, apperror.Internal)
	}
	if len(client.pruned) != 0 {
		t.Fatalf("Docker prune calls = %#v, want none", client.pruned)
	}
	if _, err := plans.Take(context.Background(), "", ""); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("empty plan lookup error = %v, want empty plan store", err)
	}
}
