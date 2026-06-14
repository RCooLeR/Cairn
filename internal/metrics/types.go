package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/docker/docker/api/types/container"
)

const (
	ScopeAll       = "all"
	ScopeProject   = "project"
	ScopeService   = "service"
	ScopeContainer = "container"

	defaultVisibleInterval    = 2 * time.Second
	defaultBackgroundInterval = 10 * time.Second
	defaultPublishInterval    = time.Second
	defaultPersistInterval    = 10 * time.Second
	defaultRetainInterval     = time.Hour
	defaultTopN               = 8
)

type DockerClient interface {
	ProviderID() string
	Info(context.Context) (*models.DockerInfo, error)
	DiskUsage(context.Context) (*models.DiskUsage, error)
	ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error)
	ListImages(context.Context) ([]models.ImageSummary, error)
	ListVolumes(context.Context) ([]models.VolumeSummary, error)
	ContainerStats(context.Context, string, dockercore.StatsOptions) (*dockercore.StatsReader, error)
}

type Options struct {
	VisibleInterval    time.Duration
	BackgroundInterval time.Duration
	PublishInterval    time.Duration
	PersistInterval    time.Duration
	RetainInterval     time.Duration
	TopN               int
	Now                func() time.Time
}

type Manager struct {
	Docker     DockerClient
	Repository *store.MetricsRepository
	Projects   *store.ProjectRepository
	Audit      *store.AuditRepository
	Events     bus.Bus

	visibleInterval    time.Duration
	backgroundInterval time.Duration
	publishInterval    time.Duration
	persistInterval    time.Duration
	retainInterval     time.Duration
	topN               int
	now                func() time.Time

	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	started      bool
	watchers     map[string]*containerWatcher
	sessions     map[string]*streamSession
	containers   map[string]models.ContainerSummary
	latest       map[string]Sample
	previous     map[string]container.StatsResponse
	lastAccepted map[string]time.Time
	pending      []store.MetricsSampleRecord
	lastRetain   time.Time
	onlineCPUs   uint32
	flushMu      sync.Mutex
}

type Sample struct {
	ProviderID       string              `json:"providerID"`
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
	StreamID string   `json:"streamID"`
	Samples  []Sample `json:"samples"`
}

type containerWatcher struct {
	id     string
	cancel context.CancelFunc
}

type streamSession struct {
	id      string
	scope   models.StatsScope
	ctx     context.Context
	cancel  context.CancelFunc
	manager *Manager
	done    chan struct{}
}
