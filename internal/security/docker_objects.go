package security

import (
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	DockerActionRemoveImage   = "remove-image"
	DockerActionPrune         = "prune"
	DockerActionRemoveVolume  = "remove-volume"
	DockerActionRemoveNetwork = "remove-network"
)

type DockerObjectPlan struct {
	Plan      models.CommandPlan
	Action    string
	Kind      string
	TargetID  string
	Force     bool
	PruneKind string
}

type DockerObjectPlanStore struct {
	*commandPlanStore[DockerObjectPlan]
}

func NewDockerObjectPlanStore(now func() time.Time) *DockerObjectPlanStore {
	return &DockerObjectPlanStore{commandPlanStore: newCommandPlanStore(now, func(plan DockerObjectPlan) models.CommandPlan { return plan.Plan })}
}

func NewRemoveImagePlan(image models.ImageSummary, force bool, now time.Time) (DockerObjectPlan, error) {
	target := imageTarget(image)
	if strings.TrimSpace(target) == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Image ID is required")
	}
	risk := models.RiskNeedsConfirmation
	if image.InUse || force {
		risk = models.RiskDestructive
	}
	command := "docker image rm " + quotePlanArg(target)
	if force {
		command = "docker image rm --force " + quotePlanArg(target)
	}
	plan := commandPlan(now, "Remove image "+target, risk, command, "Removes the selected image from the Docker backend.")
	plan.Effects = []string{
		"Image " + target + " will be removed from the active Docker backend.",
	}
	if requiresTypedConfirmation(risk) {
		plan.RequiresTypedName = target
	}
	if image.InUse {
		plan.Effects = append(plan.Effects, "Containers currently reference this image; Docker may require force removal or fail the operation.")
	}
	return DockerObjectPlan{
		Plan:     plan,
		Action:   DockerActionRemoveImage,
		Kind:     "image",
		TargetID: target,
		Force:    force,
	}, nil
}

func NewRemoveVolumePlan(volume models.VolumeSummary, force bool, now time.Time) (DockerObjectPlan, error) {
	name := strings.TrimSpace(volume.Name)
	if name == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Volume name is required")
	}
	command := "docker volume rm " + quotePlanArg(name)
	if force {
		command = "docker volume rm --force " + quotePlanArg(name)
	}
	plan := commandPlan(now, "Delete volume "+name, models.RiskDangerous, command, "Deletes the selected Docker volume.")
	plan.RequiresTypedName = name
	plan.Effects = []string{
		"Volume " + name + " and its data will be deleted from the active Docker backend.",
	}
	if volume.InUse {
		plan.Effects = append(plan.Effects, "The volume appears to be in use; Docker may reject deletion unless force is supported by the backend.")
	}
	return DockerObjectPlan{
		Plan:     plan,
		Action:   DockerActionRemoveVolume,
		Kind:     "volume",
		TargetID: name,
		Force:    force,
	}, nil
}

func NewRemoveNetworkPlan(network models.NetworkSummary, now time.Time) (DockerObjectPlan, error) {
	id := strings.TrimSpace(network.ID)
	if id == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Network ID is required")
	}
	label := strings.TrimSpace(network.Name)
	if label == "" {
		label = id
	}
	plan := commandPlan(now, "Remove network "+label, models.RiskNeedsConfirmation, "docker network rm "+quotePlanArg(label), "Removes the selected Docker network.")
	plan.Effects = []string{
		"Network " + label + " will be removed from the active Docker backend.",
	}
	return DockerObjectPlan{
		Plan:     plan,
		Action:   DockerActionRemoveNetwork,
		Kind:     "network",
		TargetID: id,
	}, nil
}

func NewPrunePlan(kind string, now time.Time) (DockerObjectPlan, error) {
	kind = normalizePruneKind(kind)
	if kind == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Prune kind is required")
	}
	risk, typedName := pruneRisk(kind)
	command := pruneCommand(kind)
	if command == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Unsupported prune kind", apperror.WithDetail(kind))
	}
	plan := commandPlan(now, "Prune "+pruneTitle(kind), risk, command, "Removes unused Docker data for the selected category.")
	plan.RequiresTypedName = typedName
	if plan.RequiresTypedName == "" && requiresTypedConfirmation(risk) {
		plan.RequiresTypedName = "prune"
	}
	plan.Effects = pruneEffects(kind)
	return DockerObjectPlan{
		Plan:      plan,
		Action:    DockerActionPrune,
		Kind:      "prune",
		PruneKind: kind,
	}, nil
}

func commandPlan(now time.Time, title string, risk models.Risk, command string, explanation string) models.CommandPlan {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return models.CommandPlan{
		PlanID:    NewTypedPlanID("object"),
		Title:     title,
		Risk:      risk,
		Commands:  []models.PlannedCommand{{Order: 1, Command: command, Risk: risk, Explanation: explanation}},
		Effects:   []string{explanation},
		ExpiresAt: now.Add(DefaultPlanTTL),
	}
}

func imageTarget(image models.ImageSummary) string {
	for _, tag := range image.RepoTags {
		tag = strings.TrimSpace(tag)
		if tag != "" && tag != "<none>:<none>" {
			return tag
		}
	}
	return strings.TrimSpace(image.ID)
}

func pruneRisk(kind string) (models.Risk, string) {
	switch kind {
	case "volumes":
		return models.RiskDangerous, "prune"
	case "system":
		return models.RiskDangerous, "prune"
	case "images", "containers", "build-cache":
		return models.RiskDestructive, "prune"
	case "networks":
		return models.RiskNeedsConfirmation, ""
	default:
		return models.RiskNeedsConfirmation, ""
	}
}

func pruneCommand(kind string) string {
	switch kind {
	case "images":
		return "docker image prune --all"
	case "containers":
		return "docker container prune"
	case "volumes":
		return "docker volume prune"
	case "networks":
		return "docker network prune"
	case "build-cache":
		return "docker builder prune"
	case "system":
		return "docker system prune"
	default:
		return ""
	}
}

func pruneTitle(kind string) string {
	switch kind {
	case "build-cache":
		return "build cache"
	case "system":
		return "Docker system"
	default:
		return kind
	}
}

func pruneEffects(kind string) []string {
	switch kind {
	case "images":
		return []string{"Unused images will be removed from the active Docker backend."}
	case "containers":
		return []string{"Stopped containers will be removed from the active Docker backend."}
	case "volumes":
		return []string{"Unused volumes and their data will be deleted from the active Docker backend."}
	case "networks":
		return []string{"Unused networks will be removed from the active Docker backend."}
	case "build-cache":
		return []string{"Unused build cache will be removed from the active Docker backend."}
	case "system":
		return []string{"Unused containers, networks, images, and build cache will be removed from the active Docker backend."}
	default:
		return []string{"Unused Docker data will be removed from the active Docker backend."}
	}
}

func normalizePruneKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "image", "images":
		return "images"
	case "container", "containers":
		return "containers"
	case "volume", "volumes":
		return "volumes"
	case "network", "networks":
		return "networks"
	case "builder", "build", "build-cache", "build_cache":
		return "build-cache"
	case "system":
		return "system"
	default:
		return kind
	}
}

func quotePlanArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n\"'`$&|;<>()") {
		return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
	}
	return value
}
