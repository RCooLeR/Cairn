package security

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	ContainerActionStart   = "start"
	ContainerActionStop    = "stop"
	ContainerActionRestart = "restart"
	ContainerActionKill    = "kill"
	ContainerActionRemove  = "remove"

	DefaultPlanTTL = 10 * time.Minute
)

type ContainerPlan struct {
	Plan           models.CommandPlan
	Action         string
	IDs            []string
	TimeoutSeconds int
	RemoveOptions  models.RemoveContainerOptions
}

type PlanStore struct {
	mu    sync.Mutex
	now   func() time.Time
	plans map[string]ContainerPlan
}

func NewPlanStore(now func() time.Time) *PlanStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PlanStore{now: now, plans: map[string]ContainerPlan{}}
}

func (s *PlanStore) Save(plan ContainerPlan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pruneExpiredPlans(s.now(), s.plans, func(plan ContainerPlan) models.CommandPlan { return plan.Plan })
	s.plans[plan.Plan.PlanID] = plan
}

func (s *PlanStore) Take(ctx context.Context, planID string, typedName string) (ContainerPlan, error) {
	if err := ctx.Err(); err != nil {
		return ContainerPlan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[planID]
	if !ok {
		return ContainerPlan{}, apperror.New(apperror.PlanExpired, "Plan expired or was not found")
	}
	if s.now().After(plan.Plan.ExpiresAt) {
		delete(s.plans, planID)
		return ContainerPlan{}, apperror.New(apperror.PlanExpired, "Plan expired")
	}
	pruneExpiredPlans(s.now(), s.plans, func(plan ContainerPlan) models.CommandPlan { return plan.Plan })
	if err := RequireConfirmation(plan.Plan, typedName); err != nil {
		return ContainerPlan{}, err
	}
	delete(s.plans, planID)
	return plan, nil
}

func RequireConfirmation(plan models.CommandPlan, typedName string) error {
	if plan.Risk == models.RiskSafe {
		return nil
	}
	required := strings.TrimSpace(plan.RequiresTypedName)
	if required == "" {
		if requiresTypedConfirmation(plan.Risk) {
			return apperror.New(
				apperror.ConfirmationRequired,
				"Typed confirmation is required",
				apperror.WithDetail("This high-risk action did not declare a confirmation phrase."),
			)
		}
		return nil
	}
	if strings.TrimSpace(typedName) != required {
		return apperror.New(
			apperror.ConfirmationRequired,
			"Typed confirmation is required",
			apperror.WithDetail("Type "+required+" to confirm this action."),
		)
	}
	return nil
}

func NewContainerActionPlan(action string, containers []models.ContainerSummary, timeoutSeconds int, opts models.RemoveContainerOptions, now time.Time) (ContainerPlan, error) {
	action = strings.TrimSpace(strings.ToLower(action))
	if !slices.Contains([]string{
		ContainerActionStart,
		ContainerActionStop,
		ContainerActionRestart,
		ContainerActionKill,
		ContainerActionRemove,
	}, action) {
		return ContainerPlan{}, apperror.New(apperror.Conflict, "Unsupported container action", apperror.WithDetail(action))
	}
	if len(containers) == 0 {
		return ContainerPlan{}, apperror.New(apperror.NotFound, "No containers selected")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	ids := make([]string, 0, len(containers))
	names := make([]string, 0, len(containers))
	risk := models.RiskSafe
	for _, container := range containers {
		ids = append(ids, container.ID)
		names = append(names, container.Name)
		nextRisk := ContainerRisk(action, container, opts)
		if riskRank(nextRisk) > riskRank(risk) {
			risk = nextRisk
		}
	}

	command := containerCommand(action, names, timeoutSeconds, opts)
	requiresTypedName := ""
	if requiresTypedConfirmation(risk) {
		requiresTypedName = containerConfirmationName(containers)
	}
	plan := models.CommandPlan{
		PlanID:            NewTypedPlanID("container"),
		Title:             containerPlanTitle(action, containers),
		Risk:              risk,
		Commands:          []models.PlannedCommand{{Order: 1, Command: command, Risk: risk, Explanation: containerActionExplanation(action, opts)}},
		Effects:           containerEffects(action, containers, opts),
		RequiresTypedName: requiresTypedName,
		ExpiresAt:         now.Add(DefaultPlanTTL),
	}
	return ContainerPlan{
		Plan:           plan,
		Action:         action,
		IDs:            ids,
		TimeoutSeconds: timeoutSeconds,
		RemoveOptions:  opts,
	}, nil
}

func ContainerRisk(action string, container models.ContainerSummary, opts models.RemoveContainerOptions) models.Risk {
	switch action {
	case ContainerActionKill:
		return models.RiskNeedsConfirmation
	case ContainerActionRemove:
		if opts.Force && container.State == "running" {
			return models.RiskDestructive
		}
		return models.RiskNeedsConfirmation
	default:
		return models.RiskSafe
	}
}

func containerPlanTitle(action string, containers []models.ContainerSummary) string {
	label := "container"
	if len(containers) != 1 {
		label = fmt.Sprintf("%d containers", len(containers))
	} else if containers[0].Name != "" {
		label = containers[0].Name
	}
	return titleWord(action) + " " + label
}

func containerCommand(action string, names []string, timeoutSeconds int, opts models.RemoveContainerOptions) string {
	targets := strings.Join(names, " ")
	switch action {
	case ContainerActionStop:
		if timeoutSeconds > 0 {
			return fmt.Sprintf("docker stop --time %d %s", timeoutSeconds, targets)
		}
	case ContainerActionRestart:
		if timeoutSeconds > 0 {
			return fmt.Sprintf("docker restart --time %d %s", timeoutSeconds, targets)
		}
	case ContainerActionRemove:
		flags := []string{}
		if opts.Force {
			flags = append(flags, "--force")
		}
		if opts.RemoveVolumes {
			flags = append(flags, "--volumes")
		}
		if len(flags) > 0 {
			return fmt.Sprintf("docker rm %s %s", strings.Join(flags, " "), targets)
		}
		return "docker rm " + targets
	}
	return "docker " + action + " " + targets
}

func containerActionExplanation(action string, opts models.RemoveContainerOptions) string {
	switch action {
	case ContainerActionStart:
		return "Starts the selected container. Paused containers are unpaused."
	case ContainerActionStop:
		return "Stops the selected container gracefully before Docker applies its timeout."
	case ContainerActionRestart:
		return "Restarts the selected container with Docker's normal stop/start sequence."
	case ContainerActionKill:
		return "Immediately sends SIGKILL to the selected container."
	case ContainerActionRemove:
		if opts.Force {
			return "Removes the selected container using Docker force removal."
		}
		return "Removes the selected stopped container."
	default:
		return "Runs a Docker container action."
	}
}

func containerEffects(action string, containers []models.ContainerSummary, opts models.RemoveContainerOptions) []string {
	effects := make([]string, 0, len(containers)+1)
	for _, container := range containers {
		name := container.Name
		if name == "" {
			name = shortID(container.ID)
		}
		effects = append(effects, fmt.Sprintf("%s: %s", name, containerActionExplanation(action, opts)))
	}
	if action == ContainerActionRemove && opts.RemoveVolumes {
		effects = append(effects, "Anonymous volumes attached to the container will also be removed.")
	}
	return effects
}

func riskRank(risk models.Risk) int {
	switch risk {
	case models.RiskDangerous:
		return 4
	case models.RiskDestructive:
		return 3
	case models.RiskNeedsConfirmation:
		return 2
	default:
		return 1
	}
}

func requiresTypedConfirmation(risk models.Risk) bool {
	return risk == models.RiskDangerous || risk == models.RiskDestructive
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func titleWord(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func NewPlanID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(fmt.Sprintf("generate plan id: %v", err))
	}
	return "plan-" + hex.EncodeToString(buf[:])
}

func NewTypedPlanID(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return '-'
		default:
			return -1
		}
	}, kind)
	kind = strings.Trim(kind, "-")
	if kind == "" {
		return NewPlanID()
	}
	return "plan-" + kind + "-" + strings.TrimPrefix(NewPlanID(), "plan-")
}

func NewJobID(prefix string) string {
	prefix = strings.TrimSpace(strings.TrimSuffix(prefix, "-"))
	if prefix == "" {
		prefix = "job"
	}
	return prefix + "-" + strings.TrimPrefix(NewPlanID(), "plan-")
}

func containerConfirmationName(containers []models.ContainerSummary) string {
	if len(containers) == 1 {
		if name := strings.TrimSpace(containers[0].Name); name != "" {
			return name
		}
		return shortID(containers[0].ID)
	}
	return "containers"
}

func pruneExpiredPlans[T any](now time.Time, plans map[string]T, toCommandPlan func(T) models.CommandPlan) {
	for id, plan := range plans {
		expiresAt := toCommandPlan(plan).ExpiresAt
		if !expiresAt.IsZero() && now.After(expiresAt) {
			delete(plans, id)
		}
	}
}
