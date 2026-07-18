package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

const (
	DockerActionRemoveImage   = "remove-image"
	DockerActionPushImage     = "push-image"
	DockerActionRunImage      = "run-image"
	DockerActionPrune         = "prune"
	DockerActionRemoveVolume  = "remove-volume"
	DockerActionRemoveNetwork = "remove-network"
)

type DockerObjectPlan struct {
	Plan              models.CommandPlan
	Action            string
	Kind              string
	TargetID          string
	TargetFingerprint string             `json:"-"`
	TargetScope       runtimescope.Scope `json:"-"`
	Force             bool
	PruneKind         string
	RunImage          models.RunImageRequest
}

type DockerObjectPlanStore struct {
	*commandPlanStore[DockerObjectPlan]
}

func NewDockerObjectPlanStore(now func() time.Time) *DockerObjectPlanStore {
	return &DockerObjectPlanStore{commandPlanStore: newCommandPlanStore(now, func(plan DockerObjectPlan) models.CommandPlan { return plan.Plan })}
}

func NewRemoveImagePlan(image models.ImageSummary, force bool, now time.Time, sources ...*IDSource) (DockerObjectPlan, error) {
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
	plan, err := commandPlan(now, "object", "Remove image "+target, risk, command, "Removes the selected image from the Docker backend.", idSource(sources))
	if err != nil {
		return DockerObjectPlan{}, err
	}
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

func NewPushImagePlan(imageRef string, now time.Time, sources ...*IDSource) (DockerObjectPlan, error) {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Image reference is required")
	}
	plan, err := commandPlan(now, "push", "Push image "+imageRef, models.RiskNeedsConfirmation, "docker push "+quotePlanArg(imageRef), "Publishes the selected image reference to its registry.", idSource(sources))
	if err != nil {
		return DockerObjectPlan{}, err
	}
	plan.Effects = []string{
		"Image " + imageRef + " will be pushed to its registry.",
		"Registry credentials configured for this Docker backend may be used by Docker.",
	}
	return DockerObjectPlan{
		Plan:     plan,
		Action:   DockerActionPushImage,
		Kind:     "image",
		TargetID: imageRef,
	}, nil
}

func NewRunImagePlan(req models.RunImageRequest, risk models.Risk, command string, target string, now time.Time, sources ...*IDSource) (DockerObjectPlan, error) {
	req.ImageRef = strings.TrimSpace(req.ImageRef)
	req.Name = strings.TrimSpace(req.Name)
	target = strings.TrimSpace(target)
	if req.ImageRef == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Image reference is required")
	}
	if target == "" {
		if req.Name != "" {
			target = req.Name
		} else {
			target = req.ImageRef
		}
	}
	if strings.TrimSpace(command) == "" {
		command = "docker run " + quotePlanArg(req.ImageRef)
	}
	plan, err := commandPlan(now, "run-image", "Run image "+req.ImageRef, risk, command, "Creates a container from the selected image.", idSource(sources))
	if err != nil {
		return DockerObjectPlan{}, err
	}
	plan.Effects = []string{
		"A new container will be created from " + req.ImageRef + ".",
	}
	for _, mount := range req.Volumes {
		mountType := strings.TrimSpace(mount.Type)
		if mountType == "" {
			mountType = "volume"
		}
		if mountType == "bind" {
			access := "read/write"
			if mount.ReadOnly {
				access = "read-only"
			}
			plan.Effects = append(plan.Effects, "Bind mount "+strings.TrimSpace(mount.Source)+" -> "+strings.TrimSpace(mount.Target)+" will be attached as "+access+".")
		}
	}
	if requiresTypedConfirmation(risk) {
		plan.RequiresTypedName = target
	}
	return DockerObjectPlan{
		Plan:     plan,
		Action:   DockerActionRunImage,
		Kind:     "container",
		TargetID: target,
		RunImage: req,
	}, nil
}

func NewRemoveVolumePlan(volume models.VolumeDetail, scope runtimescope.Scope, force bool, now time.Time, sources ...*IDSource) (DockerObjectPlan, error) {
	name := strings.TrimSpace(volume.Summary.Name)
	if name == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Volume name is required")
	}
	if !scope.Valid() {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Volume runtime scope could not be verified")
	}
	fingerprint, err := VolumeIncarnationFingerprint(volume)
	if err != nil {
		return DockerObjectPlan{}, err
	}
	command := "docker volume rm " + quotePlanArg(name)
	if force {
		command = "docker volume rm --force " + quotePlanArg(name)
	}
	plan, err := commandPlan(now, "object", "Delete volume "+name, models.RiskDangerous, command, "Deletes the selected Docker volume.", idSource(sources))
	if err != nil {
		return DockerObjectPlan{}, err
	}
	plan.RequiresTypedName = name
	plan.Effects = []string{
		"Volume " + name + " and its data will be deleted from the active Docker backend.",
		"The volume identity will be revalidated immediately before deletion.",
	}
	if volume.Summary.InUse {
		plan.Effects = append(plan.Effects, "The volume appears to be in use; Docker may reject deletion unless force is supported by the backend.")
	}
	return DockerObjectPlan{
		Plan:              plan,
		Action:            DockerActionRemoveVolume,
		Kind:              "volume",
		TargetID:          name,
		TargetFingerprint: fingerprint,
		TargetScope:       scope,
		Force:             force,
	}, nil
}

type volumeFingerprintValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type volumeFingerprintDocument struct {
	Version    int                      `json:"version"`
	Name       string                   `json:"name"`
	CreatedAt  string                   `json:"createdAt"`
	Driver     string                   `json:"driver"`
	Mountpoint string                   `json:"mountpoint"`
	Labels     []volumeFingerprintValue `json:"labels"`
	Options    []volumeFingerprintValue `json:"options"`
}

// VolumeIncarnationFingerprint derives an opaque, deterministic identity from
// stable Docker inspect metadata. Driver options and labels participate in the
// digest but are never copied into a command plan or user-facing effect.
// Ordinary local Docker volumes have no daemon object ID, so this fingerprint
// is a stale-plan guard rather than a conditional-delete token: identical
// metadata within the daemon timestamp's precision cannot be distinguished.
func VolumeIncarnationFingerprint(volume models.VolumeDetail) (string, error) {
	name := strings.TrimSpace(volume.Summary.Name)
	if name == "" {
		return "", apperror.New(apperror.Conflict, "Volume identity could not be verified", apperror.WithDetail("Docker did not report a volume name. Create a new removal plan after refreshing volumes."))
	}
	if volume.CreatedAt.IsZero() {
		return "", apperror.New(apperror.Conflict, "Volume identity could not be verified", apperror.WithDetail("Docker did not report a volume creation timestamp. Cairn will not create a destructive removal plan without a stable incarnation signal."))
	}
	document := volumeFingerprintDocument{
		Version:    1,
		Name:       name,
		CreatedAt:  volume.CreatedAt.UTC().Format(time.RFC3339Nano),
		Driver:     volume.Summary.Driver,
		Mountpoint: volume.Summary.Mountpoint,
		Labels:     sortedVolumeFingerprintValues(volume.Summary.Labels),
		Options:    sortedVolumeFingerprintValues(volume.Options),
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", apperror.Wrap(apperror.Internal, "Volume identity could not be encoded", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func sortedVolumeFingerprintValues(values map[string]string) []volumeFingerprintValue {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]volumeFingerprintValue, 0, len(keys))
	for _, key := range keys {
		result = append(result, volumeFingerprintValue{Key: key, Value: values[key]})
	}
	return result
}

func NewRemoveNetworkPlan(network models.NetworkSummary, now time.Time, sources ...*IDSource) (DockerObjectPlan, error) {
	id := strings.TrimSpace(network.ID)
	if id == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Network ID is required")
	}
	label := strings.TrimSpace(network.Name)
	if label == "" {
		label = id
	}
	plan, err := commandPlan(now, "object", "Remove network "+label, models.RiskNeedsConfirmation, "docker network rm "+quotePlanArg(label), "Removes the selected Docker network.", idSource(sources))
	if err != nil {
		return DockerObjectPlan{}, err
	}
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

func NewPrunePlan(kind string, now time.Time, sources ...*IDSource) (DockerObjectPlan, error) {
	kind = normalizePruneKind(kind)
	if kind == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Prune kind is required")
	}
	risk, typedName := pruneRisk(kind)
	command := pruneCommand(kind)
	if command == "" {
		return DockerObjectPlan{}, apperror.New(apperror.Conflict, "Unsupported prune kind", apperror.WithDetail(kind))
	}
	plan, err := commandPlan(now, "object", "Prune "+pruneTitle(kind), risk, command, "Removes unused Docker data for the selected category.", idSource(sources))
	if err != nil {
		return DockerObjectPlan{}, err
	}
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

func commandPlan(now time.Time, kind string, title string, risk models.Risk, command string, explanation string, source *IDSource) (models.CommandPlan, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	planID, err := source.NewTypedPlanID(kind)
	if err != nil {
		return models.CommandPlan{}, err
	}
	return models.CommandPlan{
		PlanID:    planID,
		Title:     title,
		Risk:      risk,
		Commands:  []models.PlannedCommand{{Order: 1, Command: command, Risk: risk, Explanation: explanation}},
		Effects:   []string{explanation},
		ExpiresAt: now.Add(DefaultPlanTTL),
	}, nil
}

func imageTarget(image models.ImageSummary) string {
	tags := make([]string, 0, len(image.RepoTags))
	for _, tag := range image.RepoTags {
		tag = strings.TrimSpace(tag)
		if tag != "" && tag != "<none>:<none>" {
			tags = append(tags, tag)
		}
	}
	if len(tags) == 1 {
		return tags[0]
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
