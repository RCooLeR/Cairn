package models

import "time"

type ProviderSummary struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"`
	Active  bool           `json:"active"`
	Status  ProviderStatus `json:"status"`
	Healthy bool           `json:"healthy"`
}

type ProviderDetail struct {
	Summary  ProviderSummary   `json:"summary"`
	Settings map[string]string `json:"settings,omitempty"`
	Problems []ProviderProblem `json:"problems,omitempty"`
}

type ProviderStatus struct {
	Installed              bool              `json:"installed"`
	Running                bool              `json:"running"`
	Healthy                bool              `json:"healthy"`
	DockerInstalled        bool              `json:"dockerInstalled"`
	DockerRunning          bool              `json:"dockerRunning"`
	ComposeInstalled       bool              `json:"composeInstalled"`
	BuildxInstalled        bool              `json:"buildxInstalled"`
	DockerVersion          string            `json:"dockerVersion,omitempty"`
	ComposeVersion         string            `json:"composeVersion,omitempty"`
	BackendVersion         string            `json:"backendVersion,omitempty"`
	CurrentContext         string            `json:"currentContext,omitempty"`
	DockerHost             string            `json:"dockerHost,omitempty"`
	NVIDIAGPUDetected      bool              `json:"nvidiaGPUDetected"`
	NVIDIAContainerRuntime bool              `json:"nvidiaContainerRuntime"`
	Problems               []ProviderProblem `json:"problems,omitempty"`
	Warnings               []ProviderWarning `json:"warnings,omitempty"`
}

type ProviderProblem struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	RepairHint  string `json:"repairHint,omitempty"`
	Recoverable bool   `json:"recoverable"`
}

type ProviderWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type InstallOptions struct {
	Backend     string            `json:"backend,omitempty"`
	Version     string            `json:"version,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
	AcceptTerms bool              `json:"acceptTerms,omitempty"`
}

type InstallProgressHandle struct {
	PlanID   string `json:"planID"`
	StreamID string `json:"streamID"`
}

type DockerContextInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Current     bool   `json:"current"`
	DockerHost  string `json:"dockerHost,omitempty"`
}

type WSLDistroInfo struct {
	Name    string `json:"name"`
	State   string `json:"state,omitempty"`
	Version int    `json:"version,omitempty"`
	Default bool   `json:"default"`
}

type DockerInfo struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name,omitempty"`
	ServerVersion   string `json:"serverVersion,omitempty"`
	StorageDriver   string `json:"storageDriver,omitempty"`
	DockerRootDir   string `json:"dockerRootDir,omitempty"`
	OperatingSystem string `json:"operatingSystem,omitempty"`
	Architecture    string `json:"architecture,omitempty"`
	CPUs            int    `json:"cpus,omitempty"`
	MemoryBytes     int64  `json:"memoryBytes,omitempty"`
}

type DockerVersion struct {
	ClientVersion string `json:"clientVersion,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`
	APIVersion    string `json:"apiVersion,omitempty"`
	MinAPIVersion string `json:"minApiVersion,omitempty"`
	GitCommit     string `json:"gitCommit,omitempty"`
	GoVersion     string `json:"goVersion,omitempty"`
}

type DiskUsage struct {
	Images      DiskUsageCategory `json:"images"`
	Containers  DiskUsageCategory `json:"containers"`
	Volumes     DiskUsageCategory `json:"volumes"`
	BuildCache  DiskUsageCategory `json:"buildCache"`
	TotalBytes  int64             `json:"totalBytes"`
	Reclaimable int64             `json:"reclaimable"`
}

type DiskUsageCategory struct {
	Count       int   `json:"count"`
	Active      int   `json:"active,omitempty"`
	SizeBytes   int64 `json:"sizeBytes"`
	Reclaimable int64 `json:"reclaimable"`
}

type ContainerListOptions struct {
	All       bool              `json:"all"`
	ProjectID string            `json:"projectID,omitempty"`
	Service   string            `json:"service,omitempty"`
	Filters   map[string]string `json:"filters,omitempty"`
}

type ContainerSummary struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Image          string        `json:"image"`
	ImageID        string        `json:"imageID,omitempty"`
	Status         string        `json:"status"`
	State          string        `json:"state"`
	Health         HealthStatus  `json:"health"`
	ProjectID      string        `json:"projectID,omitempty"`
	Service        string        `json:"service,omitempty"`
	Ports          []PortBinding `json:"ports,omitempty"`
	CPUPercent     float64       `json:"cpuPercent,omitempty"`
	MemoryBytes    int64         `json:"memoryBytes,omitempty"`
	MemoryLimit    int64         `json:"memoryLimit,omitempty"`
	GPUMemoryBytes int64         `json:"gpuMemoryBytes,omitempty"`
	GPULoadPercent float64       `json:"gpuUtilizationPercent,omitempty"`
	GPUDeviceIDs   []string      `json:"gpuDeviceIDs,omitempty"`
	NetRxRate      int64         `json:"netRxRate,omitempty"`
	NetTxRate      int64         `json:"netTxRate,omitempty"`
	Restarts       int           `json:"restarts,omitempty"`
	NetworkName    string        `json:"networkName,omitempty"`
	EndpointID     string        `json:"endpointID,omitempty"`
	IPv4Address    string        `json:"ipv4Address,omitempty"`
	IPv6Address    string        `json:"ipv6Address,omitempty"`
	Gateway        string        `json:"gateway,omitempty"`
	MacAddress     string        `json:"macAddress,omitempty"`
	Aliases        []string      `json:"aliases,omitempty"`
	CreatedAt      time.Time     `json:"createdAt"`
}

type ContainerDetail struct {
	Summary       ContainerSummary  `json:"summary"`
	Command       []string          `json:"command,omitempty"`
	Entrypoint    []string          `json:"entrypoint,omitempty"`
	Env           []EnvVar          `json:"env,omitempty"`
	Mounts        []MountSpec       `json:"mounts,omitempty"`
	Networks      []string          `json:"networks,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	WorkingDir    string            `json:"workingDir,omitempty"`
	User          string            `json:"user,omitempty"`
	RestartPolicy string            `json:"restartPolicy,omitempty"`
}

type ContainerFileListing struct {
	ContainerID string               `json:"containerID"`
	Path        string               `json:"path"`
	ParentPath  string               `json:"parentPath,omitempty"`
	Entries     []ContainerFileEntry `json:"entries"`
}

type ContainerFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	SizeBytes  int64     `json:"sizeBytes,omitempty"`
	Mode       string    `json:"mode,omitempty"`
	ModifiedAt time.Time `json:"modifiedAt,omitempty"`
	LinkTarget string    `json:"linkTarget,omitempty"`
}

type PortBinding struct {
	HostIP        string `json:"hostIP,omitempty"`
	HostPort      string `json:"hostPort,omitempty"`
	ContainerPort string `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

type PortMapping struct {
	HostIP        string `json:"hostIP,omitempty"`
	HostPort      string `json:"hostPort,omitempty"`
	ContainerPort string `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type MountSpec struct {
	Type       string `json:"type"`
	Source     string `json:"source,omitempty"`
	Target     string `json:"target"`
	ReadOnly   bool   `json:"readOnly,omitempty"`
	VolumeName string `json:"volumeName,omitempty"`
}

type RunImageRequest struct {
	ImageRef      string        `json:"imageRef"`
	Name          string        `json:"name,omitempty"`
	Ports         []PortMapping `json:"ports,omitempty"`
	Env           []EnvVar      `json:"env,omitempty"`
	Volumes       []MountSpec   `json:"volumes,omitempty"`
	NetworkID     string        `json:"networkID,omitempty"`
	RestartPolicy string        `json:"restartPolicy,omitempty"`
	Command       []string      `json:"command,omitempty"`
	User          string        `json:"user,omitempty"`
	Detach        bool          `json:"detach"`
	PullIfMissing bool          `json:"pullIfMissing"`
}

type RemoveContainerOptions struct {
	Force         bool `json:"force"`
	RemoveVolumes bool `json:"removeVolumes"`
}

type BulkResult struct {
	Total     int              `json:"total"`
	Succeeded int              `json:"succeeded"`
	Failed    int              `json:"failed"`
	Items     []BulkItemResult `json:"items,omitempty"`
}

type BulkItemResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type ImageSummary struct {
	ID           string       `json:"id"`
	RepoTags     []string     `json:"repoTags,omitempty"`
	RepoDigests  []string     `json:"repoDigests,omitempty"`
	SizeBytes    int64        `json:"sizeBytes"`
	CreatedAt    time.Time    `json:"createdAt"`
	InUse        bool         `json:"inUse"`
	UpdateStatus UpdateStatus `json:"updateStatus,omitempty"`
}

type ImageDetail struct {
	Summary      ImageSummary      `json:"summary"`
	Architecture string            `json:"architecture,omitempty"`
	OS           string            `json:"os,omitempty"`
	Author       string            `json:"author,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Layers       []string          `json:"layers,omitempty"`
}

type HubSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Stars       int    `json:"stars"`
	Official    bool   `json:"official"`
	Automated   bool   `json:"automated"`
}

type VolumeSummary struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	SizeBytes  int64             `json:"sizeBytes,omitempty"`
	InUse      bool              `json:"inUse"`
}

type VolumeDetail struct {
	Summary    VolumeSummary      `json:"summary"`
	Options    map[string]string  `json:"options,omitempty"`
	Containers []ContainerSummary `json:"containers,omitempty"`
}

type CreateVolumeRequest struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver,omitempty"`
	DriverOpts map[string]string `json:"driverOpts,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type NetworkSummary struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope,omitempty"`
	Internal   bool              `json:"internal"`
	Attachable bool              `json:"attachable"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type NetworkDetail struct {
	Summary    NetworkSummary      `json:"summary"`
	Subnet     string              `json:"subnet,omitempty"`
	Gateway    string              `json:"gateway,omitempty"`
	Options    map[string]string   `json:"options,omitempty"`
	IPAM       []NetworkIPAMConfig `json:"ipam,omitempty"`
	Containers []ContainerSummary  `json:"containers,omitempty"`
	RawJSON    string              `json:"rawJSON,omitempty"`
	CreatedAt  time.Time           `json:"createdAt,omitempty"`
}

type NetworkIPAMConfig struct {
	Subnet     string            `json:"subnet,omitempty"`
	Gateway    string            `json:"gateway,omitempty"`
	IPRange    string            `json:"ipRange,omitempty"`
	AuxAddress map[string]string `json:"auxAddress,omitempty"`
}

type CreateNetworkRequest struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver,omitempty"`
	Subnet     string            `json:"subnet,omitempty"`
	Gateway    string            `json:"gateway,omitempty"`
	Internal   bool              `json:"internal"`
	Attachable bool              `json:"attachable"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type ProjectSummary struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	ProviderID      string        `json:"providerID"`
	Status          ProjectStatus `json:"status"`
	Health          HealthStatus  `json:"health"`
	ServicesRunning int           `json:"servicesRunning"`
	ServicesTotal   int           `json:"servicesTotal"`
	CPUPercent      float64       `json:"cpuPercent"`
	MemoryBytes     int64         `json:"memoryBytes"`
	GPUMemoryBytes  int64         `json:"gpuMemoryBytes,omitempty"`
	GPULoadPercent  float64       `json:"gpuUtilizationPercent,omitempty"`
	GPUDeviceIDs    []string      `json:"gpuDeviceIDs,omitempty"`
	NetRxRate       int64         `json:"netRxRate"`
	NetTxRate       int64         `json:"netTxRate"`
	UpdateBadges    UpdateBadges  `json:"updateBadges"`
	Ports           []PortBinding `json:"ports,omitempty"`
	WorkingDir      string        `json:"workingDir"`
	LastChangedAt   time.Time     `json:"lastChangedAt"`
}

type UpdateBadges struct {
	ImageUpdates  int `json:"imageUpdates"`
	BaseUpdates   int `json:"baseUpdates"`
	RebuildNeeded int `json:"rebuildNeeded"`
	Pinned        int `json:"pinned"`
	UnknownBase   int `json:"unknownBase"`
}

type ProjectDetail struct {
	Summary    ProjectSummary         `json:"summary"`
	Services   []ComposeServiceStatus `json:"services,omitempty"`
	Containers []ContainerSummary     `json:"containers,omitempty"`
	Compose    *ComposeConfigResult   `json:"compose,omitempty"`
}

type ImportProjectRequest struct {
	FolderPath       string   `json:"folderPath,omitempty"`
	ComposeFilePaths []string `json:"composeFilePaths,omitempty"`
}

type ComposeConfigResult struct {
	RawFiles     []ComposeRawFile `json:"rawFiles,omitempty"`
	ResolvedYAML string           `json:"resolvedYAML"`
	EnvFiles     []string         `json:"envFiles,omitempty"`
	Valid        bool             `json:"valid"`
	Errors       []string         `json:"errors,omitempty"`
}

type ComposeRawFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ComposeServiceStatus struct {
	Name           string        `json:"name"`
	Image          string        `json:"image,omitempty"`
	Replicas       int           `json:"replicas"`
	Running        int           `json:"running"`
	Status         ProjectStatus `json:"status"`
	Health         HealthStatus  `json:"health"`
	Ports          []PortBinding `json:"ports,omitempty"`
	CPUPercent     float64       `json:"cpuPercent,omitempty"`
	MemoryBytes    int64         `json:"memoryBytes,omitempty"`
	GPUMemoryBytes int64         `json:"gpuMemoryBytes,omitempty"`
	GPULoadPercent float64       `json:"gpuUtilizationPercent,omitempty"`
	GPUDeviceIDs   []string      `json:"gpuDeviceIDs,omitempty"`
}

type DashboardMetrics struct {
	Projects     int              `json:"projects"`
	Containers   int              `json:"containers"`
	Images       int              `json:"images"`
	Volumes      int              `json:"volumes"`
	DiskUsage    DiskUsage        `json:"diskUsage"`
	GPU          GPUMetrics       `json:"gpu"`
	Top          []MetricRankItem `json:"top,omitempty"`
	RecentEvents []AuditEntry     `json:"recentEvents,omitempty"`
}

type GPUMetrics struct {
	Available          bool               `json:"available"`
	Source             string             `json:"source,omitempty"`
	Message            string             `json:"message,omitempty"`
	DeviceCount        int                `json:"deviceCount"`
	UtilizationPercent float64            `json:"utilizationPercent,omitempty"`
	MemoryUsedBytes    int64              `json:"memoryUsedBytes,omitempty"`
	MemoryTotalBytes   int64              `json:"memoryTotalBytes,omitempty"`
	TemperatureCelsius float64            `json:"temperatureCelsius,omitempty"`
	DriverVersion      string             `json:"driverVersion,omitempty"`
	Devices            []GPUDeviceMetric  `json:"devices,omitempty"`
	Processes          []GPUProcessMetric `json:"processes,omitempty"`
	CheckedAt          time.Time          `json:"checkedAt"`
}

type GPUDeviceMetric struct {
	ID                 string  `json:"id"`
	UUID               string  `json:"uuid,omitempty"`
	Index              int     `json:"index"`
	Name               string  `json:"name"`
	DriverVersion      string  `json:"driverVersion,omitempty"`
	UtilizationPercent float64 `json:"utilizationPercent,omitempty"`
	MemoryUsedBytes    int64   `json:"memoryUsedBytes,omitempty"`
	MemoryTotalBytes   int64   `json:"memoryTotalBytes,omitempty"`
	TemperatureCelsius float64 `json:"temperatureCelsius,omitempty"`
}

type GPUProcessMetric struct {
	PID            int     `json:"pid"`
	DeviceID       string  `json:"deviceID,omitempty"`
	DeviceUUID     string  `json:"deviceUUID,omitempty"`
	DeviceIndex    int     `json:"deviceIndex,omitempty"`
	ProcessName    string  `json:"processName,omitempty"`
	MemoryBytes    int64   `json:"memoryBytes,omitempty"`
	GPULoadPercent float64 `json:"gpuUtilizationPercent,omitempty"`
	ContainerID    string  `json:"containerID,omitempty"`
	ContainerName  string  `json:"containerName,omitempty"`
	ProjectID      string  `json:"projectID,omitempty"`
	Service        string  `json:"service,omitempty"`
}

type MetricRankItem struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	CPUPercent     float64 `json:"cpuPercent,omitempty"`
	MemoryBytes    int64   `json:"memoryBytes,omitempty"`
	GPUMemoryBytes int64   `json:"gpuMemoryBytes,omitempty"`
	GPULoadPercent float64 `json:"gpuUtilizationPercent,omitempty"`
}

type TimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Step string    `json:"step,omitempty"`
}

type SeriesBundle struct {
	Series []Series `json:"series"`
}

type Series struct {
	Name   string  `json:"name"`
	Unit   string  `json:"unit,omitempty"`
	Points []Point `json:"points"`
}

type Point struct {
	TS    time.Time `json:"ts"`
	Value float64   `json:"value"`
}

type StatsScope struct {
	Kind string   `json:"kind"`
	IDs  []string `json:"ids,omitempty"`
}

type LogStreamRequest struct {
	Scope      string   `json:"scope"`
	IDs        []string `json:"ids,omitempty"`
	Follow     bool     `json:"follow"`
	Tail       int      `json:"tail"`
	Since      string   `json:"since,omitempty"`
	Timestamps bool     `json:"timestamps"`
}

type LogPageRequest struct {
	Scope  string   `json:"scope"`
	IDs    []string `json:"ids,omitempty"`
	Cursor string   `json:"cursor,omitempty"`
	Limit  int      `json:"limit"`
}

type LogPage struct {
	Lines      []LogLine `json:"lines"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type LogLine struct {
	TS            time.Time `json:"ts"`
	ContainerID   string    `json:"containerID,omitempty"`
	ContainerName string    `json:"containerName,omitempty"`
	Service       string    `json:"service,omitempty"`
	Stream        string    `json:"stream"`
	Level         string    `json:"level,omitempty"`
	Text          string    `json:"text"`
}

type ExportLogsRequest struct {
	Scope string   `json:"scope"`
	IDs   []string `json:"ids,omitempty"`
	Path  string   `json:"path"`
}

type ExportResult struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	LineCount int    `json:"lineCount"`
}

type TerminalOptions struct {
	Shell      string            `json:"shell,omitempty"`
	WorkingDir string            `json:"workingDir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Cols       int               `json:"cols,omitempty"`
	Rows       int               `json:"rows,omitempty"`
}

type ContainerTerminalOptions struct {
	Shell      string            `json:"shell,omitempty"`
	User       string            `json:"user,omitempty"`
	WorkingDir string            `json:"workingDir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Cols       int               `json:"cols,omitempty"`
	Rows       int               `json:"rows,omitempty"`
}

type TerminalSessionInfo struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	Shell       string    `json:"shell"`
	User        string    `json:"user,omitempty"`
	WorkingDir  string    `json:"workingDir,omitempty"`
	ContainerID string    `json:"containerID,omitempty"`
	ProjectID   string    `json:"projectID,omitempty"`
	IsRoot      bool      `json:"isRoot"`
	CreatedAt   time.Time `json:"createdAt"`
}

type ImageUpdate struct {
	ID                int64             `json:"id"`
	ProjectID         string            `json:"projectID,omitempty"`
	Service           string            `json:"service,omitempty"`
	ContainerID       string            `json:"containerID,omitempty"`
	Kind              UpdateKind        `json:"kind"`
	Status            UpdateStatus      `json:"status"`
	CurrentImage      string            `json:"currentImage"`
	BaseImage         string            `json:"baseImage,omitempty"`
	LocalDigest       string            `json:"localDigest,omitempty"`
	RemoteDigest      string            `json:"remoteDigest,omitempty"`
	Confidence        Confidence        `json:"confidence"`
	RecommendedAction RecommendedAction `json:"recommendedAction"`
	CheckedAt         time.Time         `json:"checkedAt"`
	Notes             []string          `json:"notes,omitempty"`
}

type UpdateFilter struct {
	ProjectID string         `json:"projectID,omitempty"`
	Status    []UpdateStatus `json:"status,omitempty"`
	Kind      []UpdateKind   `json:"kind,omitempty"`
}

type UpdatePlan struct {
	PlanID    string           `json:"planID"`
	ProjectID string           `json:"projectID"`
	Items     []UpdatePlanItem `json:"items"`
	Commands  []PlannedCommand `json:"commands"`
	Warnings  []string         `json:"warnings,omitempty"`
}

type UpdatePlanItem struct {
	Service      string            `json:"service"`
	Kind         UpdateKind        `json:"kind"`
	CurrentImage string            `json:"currentImage"`
	BaseImage    string            `json:"baseImage,omitempty"`
	LocalDigest  string            `json:"localDigest,omitempty"`
	RemoteDigest string            `json:"remoteDigest,omitempty"`
	Confidence   Confidence        `json:"confidence"`
	Action       RecommendedAction `json:"action"`
}

type ApplyUpdateRequest struct {
	PlanID             string `json:"planID"`
	BackupVolumesFirst bool   `json:"backupVolumesFirst"`
	WatchHealth        bool   `json:"watchHealth"`
	RollbackOnFailure  bool   `json:"rollbackOnFailure"`
}

type IgnoreUpdateRequest struct {
	ID     int64  `json:"id"`
	Reason string `json:"reason,omitempty"`
}

type UpdateHistoryFilter struct {
	ProjectID string `json:"projectID,omitempty"`
	Service   string `json:"service,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type UpdateHistoryItem struct {
	ID             int64      `json:"id"`
	ProjectID      string     `json:"projectID"`
	Service        string     `json:"service,omitempty"`
	Kind           UpdateKind `json:"kind"`
	Result         string     `json:"result"`
	StartedAt      time.Time  `json:"startedAt"`
	FinishedAt     time.Time  `json:"finishedAt,omitempty"`
	RollbackStatus string     `json:"rollbackStatus,omitempty"`
	Error          string     `json:"error,omitempty"`
}

type ImageLineage struct {
	ProjectID   string        `json:"projectID,omitempty"`
	Service     string        `json:"service,omitempty"`
	ContainerID string        `json:"containerID,omitempty"`
	ImageRef    string        `json:"imageRef"`
	ImageID     string        `json:"imageID,omitempty"`
	BaseImage   string        `json:"baseImage,omitempty"`
	BaseDigest  string        `json:"baseDigest,omitempty"`
	Source      LineageSource `json:"source"`
	Confidence  Confidence    `json:"confidence"`
	Reason      string        `json:"reason,omitempty"`
}

type BackupVolumeRequest struct {
	VolumeName string `json:"volumeName"`
	DestPath   string `json:"destPath"`
	ProjectID  string `json:"projectID,omitempty"`
}

type RestoreVolumeRequest struct {
	BackupID   string `json:"backupID,omitempty"`
	SourcePath string `json:"sourcePath,omitempty"`
	VolumeName string `json:"volumeName"`
	Overwrite  bool   `json:"overwrite"`
}

type BackupFilter struct {
	VolumeName string `json:"volumeName,omitempty"`
	ProjectID  string `json:"projectID,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type BackupSummary struct {
	ID           string    `json:"id"`
	ProviderID   string    `json:"providerID,omitempty"`
	VolumeName   string    `json:"volumeName"`
	ProjectID    string    `json:"projectID,omitempty"`
	Path         string    `json:"path"`
	MetadataPath string    `json:"metadataPath,omitempty"`
	SizeBytes    int64     `json:"sizeBytes"`
	Result       string    `json:"result,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type CommandPlan struct {
	PlanID            string           `json:"planID"`
	Title             string           `json:"title"`
	Risk              Risk             `json:"risk"`
	Commands          []PlannedCommand `json:"commands"`
	Effects           []string         `json:"effects"`
	RequiresTypedName string           `json:"requiresTypedName,omitempty"`
	ExpiresAt         time.Time        `json:"expiresAt"`
}

type PlannedCommand struct {
	Order       int    `json:"order"`
	Command     string `json:"command"`
	WorkingDir  string `json:"workingDir,omitempty"`
	Risk        Risk   `json:"risk"`
	Explanation string `json:"explanation"`
}

type RegistryAccount struct {
	Registry       string    `json:"registry"`
	Username       string    `json:"username,omitempty"`
	Source         string    `json:"source"`
	LoggedIn       bool      `json:"loggedIn"`
	LastVerifiedAt time.Time `json:"lastVerifiedAt,omitempty"`
}

type RegistryLoginRequest struct {
	Registry   string `json:"registry,omitempty"`
	Username   string `json:"username"`
	Secret     string `json:"secret"`
	SecretKind string `json:"secretKind"`
}

type RegistryAuthStatus struct {
	Registry   string    `json:"registry"`
	LoggedIn   bool      `json:"loggedIn"`
	Username   string    `json:"username,omitempty"`
	VerifiedAt time.Time `json:"verifiedAt,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type RegistryPreset struct {
	Name     string `json:"name"`
	Registry string `json:"registry"`
	DocURL   string `json:"docURL,omitempty"`
}

type AuditFilter struct {
	Topic string `json:"topic,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type AuditEntry struct {
	ID       int64          `json:"id"`
	TS       time.Time      `json:"ts"`
	Actor    string         `json:"actor,omitempty"`
	Action   string         `json:"action"`
	Target   string         `json:"target,omitempty"`
	Result   string         `json:"result"`
	Error    string         `json:"error,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Notification struct {
	ID        int64     `json:"id"`
	Level     string    `json:"level"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Topic     string    `json:"topic"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"createdAt"`
}

type CheatsheetEntry struct {
	Category     string   `json:"category"`
	Command      string   `json:"command"`
	Description  string   `json:"description"`
	Risk         Risk     `json:"risk"`
	Placeholders []string `json:"placeholders,omitempty"`
	Runnable     bool     `json:"runnable"`
}

type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"buildDate,omitempty"`
	GoVersion string `json:"goVersion"`
}

type AppUpdateNotice struct {
	Version     string `json:"version"`
	URL         string `json:"url"`
	Name        string `json:"name,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
}

type AgentStatus struct {
	Enabled         bool     `json:"enabled"`
	Provider        string   `json:"provider"`
	Endpoint        string   `json:"endpoint"`
	Model           string   `json:"model"`
	Reachable       bool     `json:"reachable"`
	AvailableModels []string `json:"availableModels,omitempty"`
	CandidateModels []string `json:"candidateModels,omitempty"`
	Error           string   `json:"error,omitempty"`
}

type AgentToolSpec struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	ReadOnly         bool   `json:"readOnly"`
	RequiresApproval bool   `json:"requiresApproval"`
	ArgumentSchema   string `json:"argumentSchema,omitempty"`
}

type AgentScope struct {
	ProjectID   string `json:"projectID,omitempty"`
	ContainerID string `json:"containerID,omitempty"`
	NetworkID   string `json:"networkID,omitempty"`
	ImageID     string `json:"imageID,omitempty"`
}

type AgentChatRequest struct {
	Prompt  string     `json:"prompt"`
	Scope   AgentScope `json:"scope,omitempty"`
	ToolIDs []string   `json:"toolIDs,omitempty"`
}

type AgentToolResult struct {
	ToolID  string `json:"toolID"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	Data    string `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

type AgentToolExecutionRequest struct {
	ToolID    string     `json:"toolID"`
	Reason    string     `json:"reason,omitempty"`
	Arguments string     `json:"arguments,omitempty"`
	Scope     AgentScope `json:"scope,omitempty"`
}

type AgentChatResponse struct {
	Message     string            `json:"message"`
	ToolResults []AgentToolResult `json:"toolResults,omitempty"`
	Model       string            `json:"model,omitempty"`
}

type AgentProjectFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type AgentEnvVarHint struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Required bool   `json:"required"`
}

type AgentPortHint struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

type AgentProjectAnalysis struct {
	ProjectID       string            `json:"projectID"`
	ProjectName     string            `json:"projectName,omitempty"`
	WorkingDir      string            `json:"workingDir,omitempty"`
	Stacks          []string          `json:"stacks,omitempty"`
	RuntimeHints    []string          `json:"runtimeHints,omitempty"`
	ConfigFiles     []string          `json:"configFiles,omitempty"`
	EnvVars         []AgentEnvVarHint `json:"envVars,omitempty"`
	Ports           []AgentPortHint   `json:"ports,omitempty"`
	Recommendations []string          `json:"recommendations,omitempty"`
	Warnings        []string          `json:"warnings,omitempty"`
}

type AgentDraftFileRequest struct {
	ProjectID   string `json:"projectID"`
	Path        string `json:"path"`
	Instruction string `json:"instruction"`
}

type AgentDraftFileResponse struct {
	ProjectID string `json:"projectID"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Summary   string `json:"summary,omitempty"`
	Model     string `json:"model,omitempty"`
}

type AgentFileEditRequest struct {
	ProjectID string `json:"projectID"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Reason    string `json:"reason,omitempty"`
}

type AgentFileEditResult struct {
	ProjectID    string    `json:"projectID"`
	Path         string    `json:"path"`
	BytesWritten int       `json:"bytesWritten"`
	AppliedAt    time.Time `json:"appliedAt"`
}
