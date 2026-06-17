package services

import (
	"context"
	"errors"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/google/uuid"
)

type providerInstallProgressPayload struct {
	PlanID     string `json:"planID"`
	StreamID   string `json:"streamID"`
	Step       int    `json:"step"`
	TotalSteps int    `json:"totalSteps"`
	Message    string `json:"message"`
	Done       bool   `json:"done"`
	Error      string `json:"error,omitempty"`
}

type ProviderService struct {
	Manager *providers.Manager
	Events  bus.Bus
	Audit   *store.AuditRepository
	Plans   *security.ProviderPlanStore
	Runtime ProviderRuntime
}

type ProviderRuntime interface {
	RebindProvider(context.Context, providers.PlatformProvider) (*models.ProviderSummary, error)
}

func (s *ProviderService) ListProviders(ctx context.Context) ([]models.ProviderSummary, error) {
	if s.Manager != nil {
		return s.Manager.ListProviders(ctx)
	}
	return nil, notReady()
}

func (s *ProviderService) GetProvider(ctx context.Context, providerID string) (*models.ProviderDetail, error) {
	if s.Manager != nil {
		return s.Manager.GetProvider(ctx, providerID)
	}
	return nil, notReady()
}

func (s *ProviderService) Detect(ctx context.Context, providerID string) (*models.ProviderStatus, error) {
	if s.Manager != nil {
		return s.Manager.Detect(ctx, providerID)
	}
	return nil, notReady()
}

func (s *ProviderService) DetectAll(ctx context.Context) (map[string]*models.ProviderStatus, error) {
	if s.Manager != nil {
		return s.Manager.DetectAll(ctx)
	}
	return nil, notReady()
}

func (s *ProviderService) PlanInstall(ctx context.Context, providerID string, opts models.InstallOptions) (*models.CommandPlan, error) {
	if s.Manager != nil {
		return s.Manager.PlanInstall(ctx, providerID, opts)
	}
	return nil, notReady()
}

func (s *ProviderService) ApplyInstall(ctx context.Context, planID string) (*models.InstallProgressHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Manager == nil {
		return nil, notReady()
	}
	streamID := uuid.NewString()
	progress := make(chan providers.InstallProgress, 8)
	providerID, command, risk := s.Manager.InstallPlanAuditContext(planID)
	go s.runProviderInstall(ctx, planID, streamID, providerID, command, risk, progress)
	return &models.InstallProgressHandle{PlanID: planID, StreamID: streamID}, nil
}

func (s *ProviderService) runProviderInstall(ctx context.Context, planID string, streamID string, providerID string, command string, risk models.Risk, progress chan providers.InstallProgress) {
	auditCtx := context.WithoutCancel(ctx)
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		defer close(progress)
		done <- s.Manager.ApplyInstall(ctx, planID, progress)
	}()
	last := providers.InstallProgress{}
	for item := range progress {
		last = item
		s.publishProviderInstallProgress(planID, streamID, item, "")
	}
	if err := <-done; err != nil {
		if auditErr := s.recordProviderInstallAudit(auditCtx, planID, providerID, command, risk, "failed", time.Since(started), err); auditErr != nil {
			err = errors.Join(err, auditErr)
		}
		s.publishProviderInstallProgress(planID, streamID, providers.InstallProgress{
			Step:       last.Step,
			TotalSteps: last.TotalSteps,
			Message:    "Install failed",
			Done:       true,
		}, err.Error())
	} else {
		if auditErr := s.recordProviderInstallAudit(auditCtx, planID, providerID, command, risk, "success", time.Since(started), nil); auditErr != nil {
			s.publishProviderInstallProgress(planID, streamID, providers.InstallProgress{
				Step:       last.Step,
				TotalSteps: last.TotalSteps,
				Message:    "Install failed",
				Done:       true,
			}, auditErr.Error())
			return
		}
		s.publishProviderInstallProgress(planID, streamID, providers.InstallProgress{
			Step:       last.Step,
			TotalSteps: last.TotalSteps,
			Message:    "Install complete",
			Done:       true,
		}, "")
	}
}

func (s *ProviderService) publishProviderInstallProgress(planID string, streamID string, progress providers.InstallProgress, errText string) {
	if s.Events == nil {
		return
	}
	s.Events.Publish(bus.Event{
		Topic: bus.TopicProviderInstallProgress,
		Payload: providerInstallProgressPayload{
			PlanID:     planID,
			StreamID:   streamID,
			Step:       progress.Step,
			TotalSteps: progress.TotalSteps,
			Message:    progress.Message,
			Done:       progress.Done,
			Error:      errText,
		},
	})
}

func (s *ProviderService) recordProviderInstallAudit(ctx context.Context, planID string, providerID string, command string, risk models.Risk, status string, duration time.Duration, actionErr error) error {
	if s.Audit == nil {
		return nil
	}
	if risk == "" {
		risk = models.RiskNeedsConfirmation
	}
	targetID := providerID
	if targetID == "" {
		targetID = planID
	}
	var exitCode *int
	if status == "success" {
		code := 0
		exitCode = &code
	}
	message := ""
	if actionErr != nil {
		message = actionErr.Error()
	}
	_, err := s.Audit.Insert(ctx, store.AuditRecord{
		Action:     "provider.install",
		TargetType: "provider",
		TargetID:   targetID,
		ProviderID: providerID,
		Command:    command,
		Risk:       risk,
		Status:     status,
		ExitCode:   exitCode,
		Duration:   duration,
		Error:      message,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record provider install audit entry failed", err)
	}
	return nil
}

func (s *ProviderService) providerPlanStore() *security.ProviderPlanStore {
	if s.Plans == nil {
		s.Plans = security.NewProviderPlanStore(nil)
	}
	return s.Plans
}

func (s *ProviderService) runProviderLifecycle(ctx context.Context, action string, providerID string, risk models.Risk) error {
	if s.Manager == nil {
		return notReady()
	}
	command, commandErr := s.Manager.LifecycleCommand(ctx, action, providerID)
	if commandErr != nil {
		return commandErr
	}
	started := time.Now().UTC()
	if err := s.recordProviderLifecycleAudit(ctx, action, providerID, command, risk, "started", 0, nil); err != nil {
		return err
	}
	var err error
	switch action {
	case "start":
		err = s.Manager.Start(ctx, providerID)
	case "stop":
		err = s.Manager.Stop(ctx, providerID)
	case "restart":
		err = s.Manager.Restart(ctx, providerID)
	default:
		err = apperror.New(apperror.Conflict, "Unsupported provider action", apperror.WithDetail(action))
	}
	duration := time.Since(started)
	if err != nil {
		_ = s.recordProviderLifecycleAudit(ctx, action, providerID, command, risk, "failed", duration, err)
		return err
	}
	if err := s.recordProviderLifecycleAudit(ctx, action, providerID, command, risk, "success", duration, nil); err != nil {
		return err
	}
	if action == "start" || action == "restart" {
		return s.rebindActiveProvider(ctx)
	}
	if action == "stop" {
		return s.clearRuntimeIfActiveProvider(ctx, providerID)
	}
	return nil
}

func (s *ProviderService) recordProviderLifecycleAudit(ctx context.Context, action string, providerID string, command string, risk models.Risk, status string, duration time.Duration, actionErr error) error {
	if s.Audit == nil {
		return nil
	}
	if risk == "" {
		risk = models.RiskSafe
	}
	var exitCode *int
	if status == "success" {
		code := 0
		exitCode = &code
	}
	message := ""
	if actionErr != nil {
		message = actionErr.Error()
	}
	_, err := s.Audit.Insert(ctx, store.AuditRecord{
		Action:     "provider." + action,
		TargetType: "provider",
		TargetID:   providerID,
		ProviderID: providerID,
		Command:    command,
		Risk:       risk,
		Status:     status,
		ExitCode:   exitCode,
		Duration:   duration,
		Error:      message,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record provider lifecycle audit entry failed", err)
	}
	return nil
}

func (s *ProviderService) rebindActiveProvider(ctx context.Context) error {
	if s.Runtime == nil || s.Manager == nil {
		return nil
	}
	activeProvider, err := s.Manager.ActiveProvider(ctx)
	if err != nil {
		return err
	}
	_, err = s.Runtime.RebindProvider(ctx, activeProvider)
	return err
}

func (s *ProviderService) clearRuntimeIfActiveProvider(ctx context.Context, providerID string) error {
	if s.Runtime == nil || s.Manager == nil {
		return nil
	}
	if s.Manager.ActiveProviderID(ctx) != providerID {
		return nil
	}
	_, err := s.Runtime.RebindProvider(ctx, nil)
	return err
}

func (s *ProviderService) Start(ctx context.Context, providerID string) error {
	if s.Manager == nil {
		return notReady()
	}
	return s.runProviderLifecycle(ctx, "start", providerID, models.RiskSafe)
}

func (s *ProviderService) Stop(ctx context.Context, providerID string) error {
	if s.Manager == nil {
		return notReady()
	}
	return s.runProviderLifecycle(ctx, "stop", providerID, models.RiskSafe)
}

func (s *ProviderService) Restart(_ context.Context, providerID string) error {
	return apperror.New(
		apperror.ConfirmationRequired,
		"Restart Docker backend requires a confirmed plan",
		apperror.WithDetail("Provider "+providerID+" must be restarted through PlanRestart and ApplyProviderPlan."),
	)
}

func (s *ProviderService) PlanRestart(ctx context.Context, providerID string) (*models.CommandPlan, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	plan, err := s.Manager.PlanLifecycle(ctx, "restart", providerID)
	if err != nil {
		return nil, err
	}
	if err := s.providerPlanStore().Save(plan); err != nil {
		return nil, err
	}
	return &plan.Plan, nil
}

func (s *ProviderService) ApplyProviderPlan(ctx context.Context, planID string, typedName string) error {
	if s.Manager == nil {
		return notReady()
	}
	plan, err := s.providerPlanStore().Take(ctx, planID, typedName)
	if err != nil {
		return err
	}
	return s.runProviderLifecycle(ctx, plan.Action, plan.ProviderID, plan.Plan.Risk)
}

func (s *ProviderService) SetActiveProvider(ctx context.Context, providerID string) error {
	if s.Manager == nil {
		return notReady()
	}
	if err := s.Manager.SetActiveProvider(ctx, providerID); err != nil {
		return err
	}
	return s.rebindActiveProvider(ctx)
}

func (s *ProviderService) ListDockerContexts(ctx context.Context) ([]models.DockerContextInfo, error) {
	if s.Manager != nil {
		return s.Manager.ListDockerContexts(ctx)
	}
	return nil, notReady()
}

func (s *ProviderService) ListWSLDistros(ctx context.Context) ([]models.WSLDistroInfo, error) {
	if s.Manager != nil {
		return s.Manager.ListWSLDistros(ctx)
	}
	return nil, notReady()
}

func (s *ProviderService) SetDockerContext(ctx context.Context, name string) error {
	if s.Manager == nil {
		return notReady()
	}
	if err := s.Manager.SetDockerContext(ctx, name); err != nil {
		return err
	}
	return s.rebindActiveProvider(ctx)
}
