package lineage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

const (
	ociBaseNameLabel   = "org.opencontainers.image.base.name"
	ociBaseDigestLabel = "org.opencontainers.image.base.digest"

	cairnProjectLabel     = "io.cairn.project"
	cairnServiceLabel     = "io.cairn.service"
	cairnComposeFileLabel = "io.cairn.compose.file"
	cairnDockerfileLabel  = "io.cairn.dockerfile"
	cairnBaseNameLabel    = "io.cairn.base.name"
	cairnBaseDigestLabel  = "io.cairn.base.digest"
	cairnBuildTimeLabel   = "io.cairn.build.time"
	cairnPlatformLabel    = "io.cairn.build.platform"
)

type ImageInspector interface {
	GetImage(context.Context, string) (*models.ImageDetail, error)
}

type Manager struct {
	Projects   *store.ProjectRepository
	Repository *store.LineageRepository
	Objects    *store.ObjectCacheRepository
	Images     ImageInspector
	Now        func() time.Time
}

func NewManager(projects *store.ProjectRepository, repository *store.LineageRepository, objects *store.ObjectCacheRepository, images ImageInspector) *Manager {
	return &Manager{
		Projects:   projects,
		Repository: repository,
		Objects:    objects,
		Images:     images,
	}
}

func (m *Manager) DiscoverProjectLineage(ctx context.Context, projectID string) ([]models.ImageLineage, error) {
	if m == nil || m.Projects == nil || m.Repository == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Image lineage manager is not ready")
	}
	project, err := m.Projects.Get(ctx, projectID)
	if err != nil {
		return nil, mapLineageStoreError(err, "Project was not found")
	}
	services, err := m.Projects.ListServices(ctx, projectID)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List project services for lineage failed", err)
	}
	containers := m.containersByService(ctx, project)
	records := make([]store.LineageRecord, 0, len(services))
	for _, service := range services {
		records = append(records, m.discoverService(ctx, project, service, containers[service.Name]))
	}
	if err := m.Repository.ReplaceProject(ctx, projectID, records); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Persist project lineage failed", err)
	}
	persisted, err := m.Repository.ListProject(ctx, projectID)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Load project lineage failed", err)
	}
	return store.LineageRecordsToModels(persisted), nil
}

func (m *Manager) GetProjectLineage(ctx context.Context, projectID string) ([]models.ImageLineage, error) {
	if m == nil || m.Repository == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Image lineage manager is not ready")
	}
	records, err := m.Repository.ListProject(ctx, projectID)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Load project lineage failed", err)
	}
	return store.LineageRecordsToModels(records), nil
}

func (m *Manager) GetServiceLineage(ctx context.Context, projectID string, service string) (*models.ImageLineage, error) {
	if m == nil || m.Repository == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Image lineage manager is not ready")
	}
	record, err := m.Repository.GetService(ctx, projectID, service)
	if err != nil {
		if store.IsStoreNotFound(err) {
			return nil, nil
		}
		return nil, apperror.Wrap(apperror.Internal, "Load service lineage failed", err)
	}
	model := record.ToModel()
	return &model, nil
}

func (m *Manager) GetContainerLineage(ctx context.Context, containerID string) (*models.ImageLineage, error) {
	if m == nil || m.Repository == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Image lineage manager is not ready")
	}
	record, err := m.Repository.GetContainer(ctx, containerID)
	if err != nil {
		if store.IsStoreNotFound(err) {
			return nil, nil
		}
		return nil, apperror.Wrap(apperror.Internal, "Load container lineage failed", err)
	}
	model := record.ToModel()
	return &model, nil
}

func (m *Manager) RefreshServiceLineage(ctx context.Context, projectID string, serviceName string) (*models.ImageLineage, error) {
	if m == nil || m.Projects == nil || m.Repository == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Image lineage manager is not ready")
	}
	project, err := m.Projects.Get(ctx, projectID)
	if err != nil {
		return nil, mapLineageStoreError(err, "Project was not found")
	}
	services, err := m.Projects.ListServices(ctx, projectID)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List project services for lineage failed", err)
	}
	for _, service := range services {
		if service.Name != serviceName {
			continue
		}
		record := m.discoverService(ctx, project, service, m.containersByService(ctx, project)[service.Name])
		if err := m.Repository.ReplaceService(ctx, projectID, serviceName, record); err != nil {
			return nil, apperror.Wrap(apperror.Internal, "Persist service lineage failed", err)
		}
		persisted, err := m.Repository.GetService(ctx, projectID, serviceName)
		if err != nil {
			return nil, apperror.Wrap(apperror.Internal, "Load service lineage failed", err)
		}
		model := persisted.ToModel()
		return &model, nil
	}
	return nil, apperror.New(apperror.NotFound, "Service was not found", apperror.WithDetail(serviceName))
}

func (m *Manager) discoverService(ctx context.Context, project store.ProjectRecord, service store.ServiceRecord, container *store.ContainerCacheRecord) store.LineageRecord {
	now := m.now()
	record := store.LineageRecord{
		ProviderID:      project.ProviderID,
		ProjectID:       project.ID,
		ServiceID:       service.ID,
		ServiceName:     service.Name,
		ServiceImageRef: service.ImageRef,
		BuildContext:    service.BuildContext,
		DockerfilePath:  service.DockerfilePath,
		BuildTarget:     service.BuildTarget,
		BuildArgs:       buildArgsFromMetadata(service.Metadata),
		Source:          models.LineageSourceUnknown,
		Confidence:      models.ConfidenceUnknown,
		DiscoveredAt:    now,
		UpdatedAt:       now,
	}
	if container != nil {
		record.ContainerID = container.Summary.ID
		record.ServiceImageID = container.Summary.ImageID
		if record.ServiceImageRef == "" {
			record.ServiceImageRef = container.Summary.Image
		}
	}

	labels := m.imageLabels(ctx, record.ServiceImageID, record.ServiceImageRef)
	if service.BuildContext == "" {
		if applyImageBaseLabels(&record, labels) {
			return record
		}
		return record
	}

	contextDir := resolveBuildContext(project.WorkingDir, service.BuildContext)
	dockerfilePath := resolveDockerfilePath(contextDir, service.DockerfilePath)
	if record.DockerfilePath == "" {
		record.DockerfilePath = "Dockerfile"
	}
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		record.Source = models.LineageSourceComposeDockerfile
		record.Confidence = models.ConfidenceUnknown
		return record
	}
	record.DockerfileHash = dockerfileHash(content)
	parsed := ParseDockerfile(string(content), ParseOptions{
		BuildArgs: record.BuildArgs,
		Target:    service.BuildTarget,
	})
	record.Source = models.LineageSourceComposeDockerfile
	record.Confidence = models.ConfidenceMedium
	record.BaseRefs = baseRefsFromParse(parsed)
	if len(parsed.Stages) == 0 {
		record.Confidence = models.ConfidenceUnknown
		return record
	}
	if len(parsed.UnresolvedArgs) > 0 {
		record.Confidence = models.ConfidenceLow
	}
	labelsApplied := applyImageBaseLabels(&record, labels)
	if labelsApplied && labels[cairnBaseNameLabel] != "" {
		record.Source = models.LineageSourceCairnLabel
	}
	if len(parsed.UnresolvedArgs) == 0 && recordHasFinalBuildDigest(record) {
		record.Confidence = models.ConfidenceHigh
	}
	return record
}

func baseRefsFromParse(parsed DockerfileParseResult) []store.BaseImageRefRecord {
	finalExternal := parsed.FinalExternalStageIndex()
	refs := []store.BaseImageRefRecord{}
	for _, stage := range parsed.Stages {
		if stage.Scratch || stage.Internal {
			continue
		}
		name, tag := splitImageNameTag(stage.BaseResolved)
		status := models.UpdateStatusUnknown
		if stage.Pinned {
			status = models.UpdateStatusPinnedDigest
		}
		if stage.Unresolved {
			status = models.UpdateStatusUnknownBaseImage
		}
		refs = append(refs, store.BaseImageRefRecord{
			Name:             name,
			Tag:              tag,
			ImageRef:         stage.BaseResolved,
			Platform:         stage.Platform,
			StageName:        stage.Name,
			StageIndex:       stage.Index,
			IsFinalStageBase: stage.Index == finalExternal,
			Status:           status,
		})
	}
	return refs
}

func applyImageBaseLabels(record *store.LineageRecord, labels map[string]string) bool {
	if len(labels) == 0 {
		return false
	}
	source := models.LineageSourceOCIAnnotation
	baseName := strings.TrimSpace(labels[ociBaseNameLabel])
	baseDigest := strings.TrimSpace(labels[ociBaseDigestLabel])
	platform := ""
	if cairnBase := strings.TrimSpace(labels[cairnBaseNameLabel]); cairnBase != "" {
		source = models.LineageSourceCairnLabel
		baseName = cairnBase
		baseDigest = strings.TrimSpace(labels[cairnBaseDigestLabel])
		platform = strings.TrimSpace(labels[cairnPlatformLabel])
	}
	if baseName == "" {
		return false
	}
	if len(record.BaseRefs) == 0 {
		name, tag := splitImageNameTag(baseName)
		record.BaseRefs = []store.BaseImageRefRecord{{
			Name:             name,
			Tag:              tag,
			ImageRef:         baseName,
			Platform:         platform,
			IsFinalStageBase: true,
			BuildTimeDigest:  baseDigest,
			Status:           statusForLabelBase(baseName),
		}}
		record.Source = source
		record.Confidence = models.ConfidenceLow
		if source == models.LineageSourceCairnLabel && baseDigest != "" {
			record.Confidence = models.ConfidenceHigh
		}
		return true
	}
	applied := false
	for i := range record.BaseRefs {
		if !baseRefMatches(record.BaseRefs[i], baseName) {
			continue
		}
		if baseDigest != "" {
			record.BaseRefs[i].BuildTimeDigest = baseDigest
		}
		if platform != "" && record.BaseRefs[i].Platform == "" {
			record.BaseRefs[i].Platform = platform
		}
		applied = true
	}
	if applied && source == models.LineageSourceCairnLabel {
		record.Source = models.LineageSourceCairnLabel
	}
	return applied
}

func statusForLabelBase(baseName string) models.UpdateStatus {
	if strings.Contains(baseName, "@sha256:") {
		return models.UpdateStatusPinnedDigest
	}
	return models.UpdateStatusUnknown
}

func baseRefMatches(ref store.BaseImageRefRecord, baseName string) bool {
	baseName = strings.TrimSpace(baseName)
	if baseName == "" {
		return false
	}
	return ref.ImageRef == baseName || ref.Name == baseName || ref.Name+":"+ref.Tag == baseName
}

func recordHasFinalBuildDigest(record store.LineageRecord) bool {
	for _, ref := range record.BaseRefs {
		if ref.IsFinalStageBase && ref.BuildTimeDigest != "" {
			return true
		}
	}
	return false
}

func (m *Manager) imageLabels(ctx context.Context, imageID string, imageRef string) map[string]string {
	if m.Images == nil {
		return nil
	}
	for _, candidate := range []string{imageID, imageRef} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		detail, err := m.Images.GetImage(ctx, candidate)
		if err == nil && detail != nil {
			return detail.Labels
		}
	}
	return nil
}

func (m *Manager) containersByService(ctx context.Context, project store.ProjectRecord) map[string]*store.ContainerCacheRecord {
	result := map[string]*store.ContainerCacheRecord{}
	if m.Objects == nil || project.ProviderID == "" {
		return result
	}
	records, err := m.Objects.ListContainers(ctx, project.ProviderID)
	if err != nil {
		return result
	}
	for i := range records {
		record := records[i]
		if record.Summary.ProjectID != project.ID || record.Summary.Service == "" {
			continue
		}
		if _, exists := result[record.Summary.Service]; !exists {
			result[record.Summary.Service] = &record
		}
	}
	return result
}

func resolveBuildContext(projectWorkdir string, buildContext string) string {
	buildContext = strings.TrimSpace(buildContext)
	if buildContext == "" {
		buildContext = "."
	}
	if filepath.IsAbs(buildContext) {
		return filepath.Clean(buildContext)
	}
	return filepath.Clean(filepath.Join(projectWorkdir, buildContext))
}

func resolveDockerfilePath(contextDir string, dockerfilePath string) string {
	dockerfilePath = strings.TrimSpace(dockerfilePath)
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	if filepath.IsAbs(dockerfilePath) {
		return filepath.Clean(dockerfilePath)
	}
	return filepath.Clean(filepath.Join(contextDir, dockerfilePath))
}

func dockerfileHash(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func splitImageNameTag(imageRef string) (string, string) {
	imageRef = strings.TrimSpace(imageRef)
	withoutDigest, _, _ := strings.Cut(imageRef, "@")
	name := withoutDigest
	tag := ""
	lastColon := strings.LastIndex(withoutDigest, ":")
	lastSlash := strings.LastIndex(withoutDigest, "/")
	if lastColon > lastSlash {
		name = withoutDigest[:lastColon]
		tag = withoutDigest[lastColon+1:]
	}
	if name == "" {
		name = imageRef
	}
	return name, tag
}

func buildArgsFromMetadata(metadata map[string]any) map[string]string {
	args := map[string]string{}
	raw, ok := metadata["buildArgs"]
	if !ok {
		return args
	}
	switch values := raw.(type) {
	case map[string]string:
		for key, value := range values {
			args[key] = value
		}
	case map[string]any:
		for key, value := range values {
			args[key] = strings.TrimSpace(toString(value))
		}
	}
	return args
}

func toString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func (m *Manager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func mapLineageStoreError(err error, message string) error {
	if store.IsStoreNotFound(err) {
		return apperror.New(apperror.NotFound, message)
	}
	return apperror.Wrap(apperror.Internal, message, err)
}
