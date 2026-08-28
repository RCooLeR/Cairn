package metrics

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/moby/moby/api/types/container"
)

const (
	ScopeAll       = "all"
	ScopeProject   = "project"
	ScopeService   = "service"
	ScopeContainer = "container"

	defaultVisibleInterval     = 2 * time.Second
	defaultBackgroundInterval  = 10 * time.Second
	defaultPublishInterval     = time.Second
	defaultPersistInterval     = 10 * time.Second
	defaultRetainInterval      = time.Hour
	defaultRetainRetryInterval = 30 * time.Second
	minimumRetainRetryInterval = time.Second
	defaultGPUCacheTTL         = 5 * time.Second
	defaultTopN                = 8
	defaultMaxStreams          = 16
	watcherStopTimeout         = 5 * time.Second
	maxPendingPersistSamples   = 10000
	streamRetryFallbackSamples = 5
)

type DockerClient interface {
	ProviderID() string
	Info(context.Context) (*models.DockerInfo, error)
	DiskUsage(context.Context) (*models.DiskUsage, error)
	ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error)
	ListImages(context.Context) ([]models.ImageSummary, error)
	ListVolumes(context.Context) ([]models.VolumeSummary, error)
	ContainerStats(context.Context, string, dockercore.StatsOptions) (*dockercore.StatsReader, error)
	ContainerProcessPIDs(context.Context, string) ([]int, error)
}

type Options struct {
	Scope                 runtimescope.Scope
	VisibleInterval       time.Duration
	BackgroundInterval    time.Duration
	PublishInterval       time.Duration
	PersistInterval       time.Duration
	RetainInterval        time.Duration
	RetainRetryInterval   time.Duration
	RawRetention          time.Duration
	GPUCacheTTL           time.Duration
	TopN                  int
	MaxStreams            int
	DisableStreamingStats bool
	StatsConcurrency      int
	Now                   func() time.Time
	GPUProbe              GPUProbe
	RetentionFunc         RetentionFunc
}

type RetentionFunc func(context.Context, time.Time) error

type GPUProbe interface {
	ProbeGPUs(context.Context) models.GPUMetrics
}

type GPUProbeFunc func(context.Context) models.GPUMetrics

func (f GPUProbeFunc) ProbeGPUs(ctx context.Context) models.GPUMetrics {
	return f(ctx)
}

type Manager struct {
	Docker     DockerClient
	Repository *store.MetricsRepository
	Projects   *store.ProjectRepository
	Audit      *store.AuditRepository
	Events     bus.Bus
	Scope      runtimescope.Scope

	visibleInterval       time.Duration
	backgroundInterval    time.Duration
	publishInterval       time.Duration
	persistInterval       time.Duration
	retainInterval        time.Duration
	retainRetryInterval   time.Duration
	rawRetention          time.Duration
	gpuCacheTTL           time.Duration
	topN                  int
	maxStreams            int
	disableStreamingStats bool
	statsSemaphore        chan struct{}
	now                   func() time.Time
	gpuProbe              GPUProbe
	retentionFunc         RetentionFunc

	mu                sync.Mutex
	ctx               context.Context
	cancel            context.CancelFunc
	started           bool
	watchers          map[string]*containerWatcher
	sessions          map[string]*streamSession
	reconcileRequests chan struct{}
	containers        map[string]models.ContainerSummary
	latest            map[string]Sample
	previous          map[string]container.StatsResponse
	lastAccepted      map[string]time.Time
	pending           []store.MetricsSampleRecord
	lastRetain        time.Time
	lastRetainAttempt time.Time
	onlineCPUs        uint32
	gpuCache          models.GPUMetrics
	gpuCacheAt        time.Time
	gpuUsage          map[string]containerGPUUsage
	flushMu           sync.Mutex
	wg                sync.WaitGroup
}

type containerGPUUsage struct {
	memoryBytes        int64
	utilizationPercent float64
	deviceIDs          []string
}

type Sample struct {
	ProviderID       string              `json:"providerID"`
	ContextName      string              `json:"contextName"`
	ProjectID        string              `json:"projectID,omitempty"`
	ServiceID        string              `json:"serviceID,omitempty"`
	ContainerID      string              `json:"containerID"`
	ContainerName    string              `json:"containerName,omitempty"`
	Health           models.HealthStatus `json:"health,omitempty"`
	RestartCount     int                 `json:"restartCount,omitempty"`
	UptimeSeconds    int64               `json:"uptimeSeconds,omitempty"`
	CPUPercent       float64             `json:"cpuPercent"`
	MemoryBytes      int64               `json:"memoryBytes"`
	MemoryLimitBytes int64               `json:"memoryLimitBytes,omitempty"`
	GPUMemoryBytes   int64               `json:"gpuMemoryBytes"`
	GPULoadPercent   float64             `json:"gpuUtilizationPercent"`
	GPUDeviceIDs     []string            `json:"gpuDeviceIDs"`
	NetworkRXBytes   int64               `json:"networkRxBytes"`
	NetworkTXBytes   int64               `json:"networkTxBytes"`
	NetworkRXRate    float64             `json:"networkRxRate"`
	NetworkTXRate    float64             `json:"networkTxRate"`
	BlockReadBytes   int64               `json:"blockReadBytes"`
	BlockWriteBytes  int64               `json:"blockWriteBytes"`
	BlockReadRate    float64             `json:"blockReadRate"`
	BlockWriteRate   float64             `json:"blockWriteRate"`
	PIDs             int64               `json:"pids"`
	SampledAt        time.Time           `json:"sampledAt"`
}

type SamplePayload struct {
	StreamID string            `json:"streamID"`
	Samples  []Sample          `json:"samples"`
	GPU      models.GPUMetrics `json:"gpu"`
}

type containerWatcher struct {
	id           string
	cancel       context.CancelFunc
	done         chan struct{}
	activeReader io.Closer
	mu           sync.Mutex
}

type streamSession struct {
	id      string
	scope   models.StatsScope
	ctx     context.Context
	cancel  context.CancelFunc
	manager *Manager
	done    chan struct{}
}
