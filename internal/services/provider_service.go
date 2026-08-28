package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
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
	Manager          *providers.Manager
	Events           bus.Bus
	Audit            *store.AuditRepository
	Plans            *security.ProviderPlanStore
	Runtime          ProviderRuntime
	InstallLifecycle *ProviderInstallLifecycle

	installLifecycleMu sync.Mutex
}

// ProviderInstallLifecycle owns install jobs independently of short-lived RPC
// request contexts while still making application shutdown cancel-and-join
// every privileged installer before the event bus and database are closed.
type ProviderInstallLifecycle struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	stopped bool
	jobs    sync.WaitGroup
}

func NewProviderInstallLifecycle(parent context.Context) *ProviderInstallLifecycle {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &ProviderInstallLifecycle{ctx: ctx, cancel: cancel}
}

func (l *ProviderInstallLifecycle) begin() (context.Context, func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped || l.ctx == nil || l.ctx.Err() != nil {
		return nil, nil, apperror.New(
			apperror.ProviderNotReady,
			"Provider installer is shutting down",
			apperror.WithRepairHints("Restart Cairn before starting another provider installation."),
		)
	}
	l.jobs.Add(1)
	return l.ctx, l.jobs.Done, nil
}

func (l *ProviderInstallLifecycle) StopAll() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if !l.stopped {
		l.stopped = true
		if l.cancel != nil {
			l.cancel()
		}
	}
	l.mu.Unlock()
	l.jobs.Wait()
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
	jobCtx, jobDone, err := s.providerInstallLifecycle().begin()
	if err != nil {
		return nil, err
	}
	streamID := uuid.New().String()
	progress := make(chan providers.InstallProgress, 8)
	providerID, command, risk := s.Manager.InstallPlanAuditContext(planID)
	go func() {
		defer jobDone()
		s.runProviderInstall(jobCtx, planID, streamID, providerID, command, risk, progress)
	}()
	return &models.InstallProgressHandle{PlanID: planID, StreamID: streamID}, nil
}

func (s *ProviderService) runProviderInstall(ctx context.Context, planID string, streamID string, providerID string, command string, risk models.Risk, progress chan providers.InstallProgress) {
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
	installErr := <-done
	auditCtx, cancelAudit := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelAudit()
	if installErr != nil {
		err := installErr
		if auditErr := s.recordProviderInstallAudit(auditCtx, planID, providerID, command, risk, "failed", time.Since(started), err); auditErr != nil {
			err = errors.Join(err, auditErr)
		}
		s.publishProviderInstallProgress(planID, streamID, providers.InstallProgress{
			Step:       last.Step,
			TotalSteps: last.TotalSteps,
			Message:    "Install failed",
			Done:       true,
		}, providerInstallErrorText(err))
	} else {
		if auditErr := s.recordProviderInstallAudit(auditCtx, planID, providerID, command, risk, "success", time.Since(started), nil); auditErr != nil {
			s.publishProviderInstallProgress(planID, streamID, providers.InstallProgress{
				Step:       last.Step,
				TotalSteps: last.TotalSteps,
				Message:    "Install complete",
				Done:       true,
			}, "Install completed, but the audit entry could not be recorded: "+auditErr.Error())
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

func (s *ProviderService) providerInstallLifecycle() *ProviderInstallLifecycle {
	s.installLifecycleMu.Lock()
	defer s.installLifecycleMu.Unlock()
	if s.InstallLifecycle == nil {
		s.InstallLifecycle = NewProviderInstallLifecycle(context.Background())
	}
	return s.InstallLifecycle
}

func providerInstallErrorText(err error) string {
	if err == nil {
		return ""
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		return err.Error()
	}
	parts := []string{appErr.Error()}
	if detail := strings.TrimSpace(appErr.Detail); detail != "" {
		parts = append(parts, detail)
	}
	if len(appErr.RepairHints) > 0 {
		parts = append(parts, strings.Join(appErr.RepairHints, "\n"))
	}
	return strings.Join(parts, "\n")
}

func (s *ProviderService) publishProviderInstallProgress(planID string, streamID string, progress providers.InstallProgress, errText string) {
	if s.Events == nil {
		return
	}
	event := bus.Event{
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
	}
	// Provider installation has few, security-relevant steps. Keep the full
	// sequence reliable so its terminal event cannot overtake lossy progress.
	publishCriticalEvent(s.Events, event)
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
	return apperror.New(
		apperror.ConfirmationRequired,
		"Stopping the Docker backend requires a confirmed plan",
		apperror.WithDetail("Provider "+providerID+" must be stopped through PlanStop and ApplyProviderPlan."),
	)
}

func (s *ProviderService) Restart(_ context.Context, providerID string) error {
	return apperror.New(
		apperror.ConfirmationRequired,
		"Restart Docker backend requires a confirmed plan",
		apperror.WithDetail("Provider "+providerID+" must be restarted through PlanRestart and ApplyProviderPlan."),
	)
}

func (s *ProviderService) PlanRestart(ctx context.Context, providerID string) (*models.CommandPlan, error) {
	return s.planProviderLifecycle(ctx, "restart", providerID)
}

func (s *ProviderService) PlanStop(ctx context.Context, providerID string) (*models.CommandPlan, error) {
	return s.planProviderLifecycle(ctx, "stop", providerID)
}

func (s *ProviderService) planProviderLifecycle(ctx context.Context, action string, providerID string) (*models.CommandPlan, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	plan, err := s.Manager.PlanLifecycle(ctx, action, providerID)
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
	return s.runPlannedProviderLifecycle(ctx, plan)
}

func (s *ProviderService) runPlannedProviderLifecycle(ctx context.Context, plan security.ProviderPlan) error {
	commandParts := make([]string, 0, len(plan.Plan.Commands))
	for _, planned := range plan.Plan.Commands {
		if command := strings.TrimSpace(planned.Command); command != "" {
			commandParts = append(commandParts, command)
		}
	}
	command := strings.Join(commandParts, "\n")
	started := time.Now().UTC()
	if err := s.recordProviderLifecycleAudit(ctx, plan.Action, plan.ProviderID, command, plan.Plan.Risk, "started", 0, nil); err != nil {
		return err
	}
	if err := s.Manager.ApplyLifecyclePlan(ctx, plan); err != nil {
		_ = s.recordProviderLifecycleAudit(ctx, plan.Action, plan.ProviderID, command, plan.Plan.Risk, "failed", time.Since(started), err)
		return err
	}
	if err := s.recordProviderLifecycleAudit(ctx, plan.Action, plan.ProviderID, command, plan.Plan.Risk, "success", time.Since(started), nil); err != nil {
		return err
	}
	if plan.Action == "restart" {
		return s.rebindActiveProvider(ctx)
	}
	if plan.Action == "stop" {
		return s.clearRuntimeIfActiveProvider(ctx, plan.ProviderID)
	}
	return nil
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
