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
	plan, err := security.NewPushImagePlan(imageRef, time.Now().UTC(), s.IDs)
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
	plan, err := security.NewRunImagePlan(req, risk, command, targetID, time.Now().UTC(), s.IDs)
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
	plan, err := security.NewContainerActionPlan(action, containers, timeoutSeconds, opts, time.Now().UTC(), s.IDs)
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
	plan, err := security.NewRemoveImagePlan(detail.Summary, force, time.Now().UTC(), s.IDs)
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
	if !s.Scope.Valid() {
		return nil, apperror.New(apperror.Conflict, "Volume runtime scope could not be verified")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apperror.New(apperror.Conflict, "Volume name is required")
	}
	detail, err := s.Client.GetVolume(ctx, name)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, apperror.New(apperror.Conflict, "Volume identity could not be verified", apperror.WithDetail("Docker returned an empty volume inspection. Refresh volumes and create a new removal plan."))
	}
	if strings.TrimSpace(detail.Summary.Name) != name {
		return nil, apperror.New(
			apperror.Conflict,
			"Volume identity could not be verified",
			apperror.WithDetail("Docker inspected a different volume name than the requested target. Refresh volumes and try again."),
		)
	}
	plan, err := security.NewRemoveVolumePlan(*detail, s.Scope, force, time.Now().UTC(), s.IDs)
	if err != nil {
		return nil, err
	}
	if err := s.objectPlanStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}

func (s *DockerService) validateRemoveVolumePlanTarget(ctx context.Context, plan security.DockerObjectPlan) error {
	if !plan.TargetScope.Valid() || !s.Scope.Equal(plan.TargetScope) {
		return apperror.New(
			apperror.Conflict,
			"Docker runtime changed after the volume removal plan was created",
			apperror.WithDetail("Return to the intended provider and context, then inspect the volume and create a new removal plan."),
		)
	}
	if strings.TrimSpace(plan.TargetFingerprint) == "" {
		return apperror.New(
			apperror.Conflict,
			"Volume identity could not be verified",
			apperror.WithDetail("The removal plan has no volume incarnation fingerprint. Create a new plan before deleting the volume."),
		)
	}
	detail, err := s.Client.GetVolume(ctx, plan.TargetID)
	if err != nil {
		if apperror.IsCode(err, apperror.NotFound) {
			return apperror.Wrap(
				apperror.Conflict,
				"Volume changed after the removal plan was created",
				err,
				apperror.WithDetail("The planned volume no longer exists. Refresh volumes and create a new removal plan."),
			)
		}
		return err
	}
	if detail == nil {
		return apperror.New(
			apperror.Conflict,
			"Volume identity could not be verified",
			apperror.WithDetail("Docker returned an empty volume inspection. Refresh volumes and create a new removal plan."),
		)
	}
	if strings.TrimSpace(detail.Summary.Name) != plan.TargetID {
		return apperror.New(
			apperror.Conflict,
			"Volume changed after the removal plan was created",
			apperror.WithDetail("Docker inspected a different volume name than the planned target. Refresh volumes and create a new removal plan."),
		)
	}
	fingerprint, err := security.VolumeIncarnationFingerprint(*detail)
	if err != nil {
		return err
	}
	if fingerprint != plan.TargetFingerprint {
		return apperror.New(
			apperror.Conflict,
			"Volume changed after the removal plan was created",
			apperror.WithDetail("The volume name now refers to a different Docker volume. Review it and create a new removal plan."),
		)
	}
	return nil
}

func (s *DockerService) planRemoveNetwork(ctx context.Context, id string) (*models.CommandPlan, error) {
	if s.Client == nil {
		return nil, notReady()
	}
	detail, err := s.Client.GetNetwork(ctx, id)
	if err != nil {
		return nil, err
	}
	plan, err := security.NewRemoveNetworkPlan(detail.Summary, time.Now().UTC(), s.IDs)
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
	plan, err := security.NewPrunePlan(kind, time.Now().UTC(), s.IDs)
	if err != nil {
		return nil, err
	}
	if err := s.objectPlanStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}
