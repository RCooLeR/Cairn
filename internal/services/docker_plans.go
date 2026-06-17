package services

import (
	"context"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/security"
)

func (s *DockerService) PlanKillContainer(ctx context.Context, id string) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	return s.planContainerAction(ctx, security.ContainerActionKill, []string{id}, 0, models.RemoveContainerOptions{})
}

func (s *DockerService) PlanRemoveContainer(ctx context.Context, id string, opts models.RemoveContainerOptions) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	return s.planContainerAction(ctx, security.ContainerActionRemove, []string{id}, 0, opts)
}

func (s *DockerService) ApplyContainerPlan(ctx context.Context, planID string, typedName string) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil {
		return notReady()
	}
	if strings.HasPrefix(planID, "plan-object-") {
		objectPlan, err := s.objectPlanStore().Take(ctx, planID, typedName)
		if err != nil {
			return err
		}
		return s.runDockerObjectPlan(ctx, objectPlan)
	}
	if strings.HasPrefix(planID, "plan-container-") {
		plan, err := s.planStore().Take(ctx, planID, typedName)
		if err != nil {
			return err
		}
		for _, id := range plan.IDs {
			if err := s.runContainerAction(ctx, plan.Action, id, plan.TimeoutSeconds, plan.RemoveOptions); err != nil {
				return err
			}
		}
		return nil
	}
	return apperror.New(
		apperror.PlanExpired,
		"Plan expired or was not found",
		apperror.WithDetail("Unsupported Docker plan kind."),
	)
}

func (s *DockerService) PlanPushImage(ctx context.Context, imageRef string) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil {
		return nil, notReady()
	}
	plan, err := security.NewPushImagePlan(imageRef, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.objectPlanStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}

func (s *DockerService) ApplyPushImagePlan(ctx context.Context, planID string) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil {
		return "", notReady()
	}
	plan, err := s.objectPlanStore().Take(ctx, planID, "")
	if err != nil {
		return "", err
	}
	if plan.Action != security.DockerActionPushImage {
		return "", apperror.New(apperror.Conflict, "Plan is not an image push plan")
	}
	return s.runPushImagePlan(ctx, plan)
}

func (s *DockerService) PlanRunImage(ctx context.Context, req models.RunImageRequest) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil {
		return nil, notReady()
	}
	command := dockerRunCommand(req)
	risk := runImageRisk(req)
	targetID := runImageTarget(req)
	plan, err := security.NewRunImagePlan(req, risk, command, targetID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.objectPlanStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}

func (s *DockerService) ApplyRunImagePlan(ctx context.Context, planID string, typedName string) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil {
		return "", notReady()
	}
	plan, err := s.objectPlanStore().Take(ctx, planID, typedName)
	if err != nil {
		return "", err
	}
	if plan.Action != security.DockerActionRunImage {
		return "", apperror.New(apperror.Conflict, "Plan is not a run image plan")
	}
	return s.runRunImagePlan(ctx, plan)
}

func (s *DockerService) planContainerAction(ctx context.Context, action string, ids []string, timeoutSeconds int, opts models.RemoveContainerOptions) (*models.CommandPlan, error) {
	if s.Client == nil {
		return nil, notReady()
	}
	containers := make([]models.ContainerSummary, 0, len(ids))
	for _, id := range ids {
		detail, err := s.Client.GetContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		containers = append(containers, detail.Summary)
	}
	plan, err := security.NewContainerActionPlan(action, containers, timeoutSeconds, opts, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.planStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}

func (s *DockerService) planRemoveImage(ctx context.Context, imageID string, force bool) (*models.CommandPlan, error) {
	if s.Client == nil {
		return nil, notReady()
	}
	detail, err := s.Client.GetImage(ctx, imageID)
	if err != nil {
		return nil, err
	}
	plan, err := security.NewRemoveImagePlan(detail.Summary, force, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	plan.TargetID = imageID
	if err := s.objectPlanStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}

func (s *DockerService) planRemoveVolume(ctx context.Context, name string, force bool) (*models.CommandPlan, error) {
	if s.Client == nil {
		return nil, notReady()
	}
	detail, err := s.Client.GetVolume(ctx, name)
	if err != nil {
		return nil, err
	}
	plan, err := security.NewRemoveVolumePlan(detail.Summary, force, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.objectPlanStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}

func (s *DockerService) planRemoveNetwork(ctx context.Context, id string) (*models.CommandPlan, error) {
	if s.Client == nil {
		return nil, notReady()
	}
	detail, err := s.Client.GetNetwork(ctx, id)
	if err != nil {
		return nil, err
	}
	plan, err := security.NewRemoveNetworkPlan(detail.Summary, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	plan.TargetID = id
	if err := s.objectPlanStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}

func (s *DockerService) PlanPrune(_ context.Context, kind string) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil {
		return nil, notReady()
	}
	plan, err := security.NewPrunePlan(kind, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.objectPlanStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}
