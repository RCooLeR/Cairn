package logsvc

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	ScopeContainer = "container"
	ScopeService   = "service"
	ScopeProject   = "project"
	ScopeAll       = "all"

	defaultRingSize                  = 50000
	defaultInputBuffer               = 1000
	defaultBatchMaxLines             = 200
	defaultBatchWindow               = 50 * time.Millisecond
	defaultFetchTail                 = 5000
	defaultRetryAttempts             = 8
	defaultRetryInitial              = 250 * time.Millisecond
	defaultRetryMaximum              = 5 * time.Second
	defaultRetryHealthyAfter         = 30 * time.Second
	defaultStopTimeout               = 5 * time.Second
	defaultMaxStreams                = 8
	defaultMaxScopeStreams           = 4
	defaultMaxReadersPerStream       = 64
	defaultMaxReaders                = 128
	defaultMaxOperations             = 2
	defaultRingBytes           int64 = 8 * 1024 * 1024
	defaultInputBytes          int64 = 4 * 1024 * 1024
	defaultBatchBytes          int64 = 4 * 1024 * 1024
	minimumBatchBytes          int64 = 1024
	defaultFetchTimeout              = 30 * time.Second
	defaultPageLimit                 = 200
	maximumPageLimit                 = 1000
	defaultPageSnapshotTTL           = 2 * time.Minute
	defaultMaxPageSnapshots          = 8
	defaultPageSnapshotLines         = 50000
	defaultPageSnapshotBytes   int64 = 8 * 1024 * 1024
	defaultPageSnapshotsBytes  int64 = 32 * 1024 * 1024
	defaultExportTimeout             = 2 * time.Minute
	defaultExportLines               = 500000
	defaultExportBytes         int64 = 64 * 1024 * 1024
)

type DockerClient interface {
	ContainerLogs(context.Context, string, dockercore.LogOptions) (io.ReadCloser, error)
	ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error)
	GetContainer(context.Context, string) (*models.ContainerDetail, error)
}

type Options struct {
	RingSize            int
	InputBuffer         int
	BatchMaxLines       int
	BatchWindow         time.Duration
	ReaderRetryAttempts int
	ReaderRetryInitial  time.Duration
	ReaderRetryMaximum  time.Duration
	ReaderRetryHealthy  time.Duration
	StopTimeout         time.Duration
	MaxStreams          int
	MaxScopeStreams     int
	MaxReadersPerStream int
	MaxReaders          int
	RingBytes           int64
	InputBytes          int64
	BatchBytes          int64
	MaxOperations       int
	FetchTimeout        time.Duration
	PageSnapshotTTL     time.Duration
	MaxPageSnapshots    int
	PageSnapshotLines   int
	PageSnapshotBytes   int64
	PageSnapshotsBytes  int64
	ExportTimeout       time.Duration
	ExportLines         int
	ExportBytes         int64
	ExportDirectory     string
	Now                 func() time.Time
}

type Manager struct {
	Docker DockerClient
	Events bus.Bus

	ringSize            int
	inputBuffer         int
	batchMaxLines       int
	batchWindow         time.Duration
	retryAttempts       int
	retryInitial        time.Duration
	retryMaximum        time.Duration
	retryHealthy        time.Duration
	stopTimeout         time.Duration
	maxStreams          int
	maxScopeStreams     int
	maxReadersPerStream int
	maxReaders          int
	ringBytes           int64
	inputBytes          int64
	batchBytes          int64
	maxOperations       int
	fetchTimeout        time.Duration
	pageSnapshotTTL     time.Duration
	maxPageSnapshots    int
	pageSnapshotLines   int
	pageSnapshotBytes   int64
	pageSnapshotsBytes  int64
	exportTimeout       time.Duration
	exportLines         int
	exportBytes         int64
	exportDirectory     string
	now                 func() time.Time

	mu                     sync.Mutex
	sessions               map[string]*session
	draining               map[string]*session
	pendingScopeStreams    map[string]int
	pendingStarts          sync.WaitGroup
	pendingStreams         int
	reservedReaders        int
	activeOperations       int
	operations             sync.WaitGroup
	pageSnapshots          map[string]*logPageSnapshot
	pageSnapshotBytesInUse int64
	pageCursorKey          [32]byte
	rootCtx                context.Context
	rootCancel             context.CancelFunc
	closed                 bool
}

type LinesPayload struct {
	StreamID string           `json:"streamID"`
	Lines    []models.LogLine `json:"lines"`
}

type EOFPayload struct {
	StreamID string `json:"streamID"`
}

type ErrorPayload struct {
	StreamID string `json:"streamID"`
	Error    string `json:"error"`
}

type sourceInfo struct {
	ContainerID   string
	ContainerName string
	Service       string
}
