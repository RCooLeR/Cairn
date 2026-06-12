package models

import "time"

type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthStarting  HealthStatus = "starting"
)

type ProjectStatus string

const (
	ProjectUnknown ProjectStatus = "unknown"
	ProjectRunning ProjectStatus = "running"
	ProjectStopped ProjectStatus = "stopped"
	ProjectPartial ProjectStatus = "partial"
	ProjectError   ProjectStatus = "error"
)

type Project struct {
	ID           string
	Name         string
	ProviderID   string
	ContextName  string
	WorkingDir   string
	ComposeFiles []string
	Services     []ServiceSummary
	Containers   []ContainerSummary
	Status       ProjectStatus
	Stats        ProjectStats
	Updates      []ImageUpdate
	Health       HealthSummary
	LastSeenAt   time.Time
}

type ServiceSummary struct {
	ID              string
	ProjectID       string
	Name            string
	ImageRef        string
	BuildContext    string
	RunningReplicas int
	TotalReplicas   int
	Status          string
	Health          HealthStatus
	CPUPercent      float64
	MemoryBytes     int64
	Ports           []PortBinding
}

type ContainerSummary struct {
	ID           string
	Name         string
	ProjectID    string
	ServiceID    string
	ImageRef     string
	ImageID      string
	Status       string
	Health       HealthStatus
	Ports        []PortBinding
	CPUPercent   float64
	MemoryBytes  int64
	RestartCount int
	CreatedAt    time.Time
}

type PortBinding struct {
	HostIP        string
	HostPort      string
	ContainerPort string
	Protocol      string
}

type ProjectStats struct {
	CPUPercent      float64
	MemoryBytes     int64
	NetworkRXBytes  int64
	NetworkTXBytes  int64
	BlockReadBytes  int64
	BlockWriteBytes int64
}

type HealthSummary struct {
	Healthy   int
	Unhealthy int
	Starting  int
	Unknown   int
}

type UpdateStatus string

const (
	UpdateUnknown               UpdateStatus = "unknown"
	UpdateChecking              UpdateStatus = "checking"
	UpdateUpToDate              UpdateStatus = "up_to_date"
	UpdateAvailable             UpdateStatus = "update_available"
	UpdateServiceImageAvailable UpdateStatus = "service_image_update_available"
	UpdateBaseImageAvailable    UpdateStatus = "base_image_update_available"
	UpdateRebuildRequired       UpdateStatus = "rebuild_required"
	UpdateUnknownBaseImage      UpdateStatus = "unknown_base_image"
	UpdatePinnedDigest          UpdateStatus = "pinned_digest"
	UpdateBuiltLocally          UpdateStatus = "built_locally"
	UpdateAuthRequired          UpdateStatus = "auth_required"
	UpdateRateLimited           UpdateStatus = "rate_limited"
	UpdateError                 UpdateStatus = "error"
	UpdateIgnored               UpdateStatus = "ignored"
)

type UpdateKind string

const (
	UpdateKindServiceImage UpdateKind = "service_image"
	UpdateKindBaseImage    UpdateKind = "base_image"
)

type LineageSource string

const (
	LineageSourceComposeDockerfile LineageSource = "compose_dockerfile"
	LineageSourceOCIAnnotation     LineageSource = "oci_annotation"
	LineageSourceCairnLabel        LineageSource = "cairn_label"
	LineageSourceUnknown           LineageSource = "unknown"
)

type ConfidenceLevel string

const (
	ConfidenceHigh    ConfidenceLevel = "high"
	ConfidenceMedium  ConfidenceLevel = "medium"
	ConfidenceLow     ConfidenceLevel = "low"
	ConfidenceUnknown ConfidenceLevel = "unknown"
)

type ImageUpdate struct {
	ProviderID   string
	ProjectID    string
	ServiceName  string
	ContainerID  string
	Kind         UpdateKind
	ImageRef     string
	BaseImageRef string
	LocalDigest  string
	RemoteDigest string
	Status       UpdateStatus
	Confidence   ConfidenceLevel
	CheckedAt    time.Time
	Error        string
}

type ImageLineage struct {
	ProjectID       string
	ServiceName     string
	ContainerID     string
	ServiceImageRef string
	ServiceImageID  string
	ServiceDigest   string
	BuildContext    string
	DockerfilePath  string
	BuildTarget     string
	BaseImages      []BaseImageRef
	Source          LineageSource
	Confidence      ConfidenceLevel
}

type BaseImageRef struct {
	Name             string
	Tag              string
	ImageRef         string
	Platform         string
	StageName        string
	StageIndex       int
	IsFinalStageBase bool
	BuildTimeDigest  string
	LocalDigest      string
	RemoteDigest     string
	LastCheckedAt    time.Time
	Status           UpdateStatus
	Error            string
}
