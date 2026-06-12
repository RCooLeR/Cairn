package models

type UpdateStatus string

const (
	UpdateStatusUnknown                     UpdateStatus = "unknown"
	UpdateStatusChecking                    UpdateStatus = "checking"
	UpdateStatusUpToDate                    UpdateStatus = "up_to_date"
	UpdateStatusServiceImageUpdateAvailable UpdateStatus = "service_image_update_available"
	UpdateStatusBaseImageUpdateAvailable    UpdateStatus = "base_image_update_available"
	UpdateStatusRebuildRequired             UpdateStatus = "rebuild_required"
	UpdateStatusPinnedDigest                UpdateStatus = "pinned_digest"
	UpdateStatusBuiltLocally                UpdateStatus = "built_locally"
	UpdateStatusUnknownBaseImage            UpdateStatus = "unknown_base_image"
	UpdateStatusLocalOnlyImage              UpdateStatus = "local_only_image"
	UpdateStatusAuthRequired                UpdateStatus = "auth_required"
	UpdateStatusRateLimited                 UpdateStatus = "rate_limited"
	UpdateStatusError                       UpdateStatus = "error"
	UpdateStatusIgnored                     UpdateStatus = "ignored"
)

type UpdateKind string

const (
	UpdateKindServiceImage UpdateKind = "service_image"
	UpdateKindBaseImage    UpdateKind = "base_image"
)

type Confidence string

const (
	ConfidenceHigh    Confidence = "high"
	ConfidenceMedium  Confidence = "medium"
	ConfidenceLow     Confidence = "low"
	ConfidenceUnknown Confidence = "unknown"
)

type LineageSource string

const (
	LineageSourceComposeDockerfile LineageSource = "compose_dockerfile"
	LineageSourceOCIAnnotation     LineageSource = "oci_annotation"
	LineageSourceCairnLabel        LineageSource = "cairn_label"
	LineageSourceUnknown           LineageSource = "unknown"
)

type Risk string

const (
	RiskSafe              Risk = "safe"
	RiskNeedsConfirmation Risk = "needs_confirmation"
	RiskDestructive       Risk = "destructive"
	RiskDangerous         Risk = "dangerous"
)

type ProjectStatus string

const (
	ProjectStatusRunning ProjectStatus = "running"
	ProjectStatusStopped ProjectStatus = "stopped"
	ProjectStatusPartial ProjectStatus = "partial"
	ProjectStatusError   ProjectStatus = "error"
	ProjectStatusUnknown ProjectStatus = "unknown"
)

type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
	HealthStatusStarting  HealthStatus = "starting"
	HealthStatusUnknown   HealthStatus = "unknown"
)

type RecommendedAction string

const (
	RecommendedActionPullRecreate    RecommendedAction = "pull_recreate"
	RecommendedActionRebuildRedeploy RecommendedAction = "rebuild_redeploy"
	RecommendedActionNone            RecommendedAction = "none"
	RecommendedActionManual          RecommendedAction = "manual"
)
