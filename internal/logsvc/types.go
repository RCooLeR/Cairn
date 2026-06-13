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

	defaultRingSize      = 50000
	defaultInputBuffer   = 1000
	defaultBatchMaxLines = 200
	defaultBatchWindow   = 50 * time.Millisecond
	defaultFetchTail     = 5000
)

type DockerClient interface {
	ContainerLogs(context.Context, string, dockercore.LogOptions) (io.ReadCloser, error)
	ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error)
	GetContainer(context.Context, string) (*models.ContainerDetail, error)
}

type Options struct {
	RingSize      int
	InputBuffer   int
	BatchMaxLines int
	BatchWindow   time.Duration
	Now           func() time.Time
}

type Manager struct {
	Docker DockerClient
	Events bus.Bus

	ringSize      int
	inputBuffer   int
	batchMaxLines int
	batchWindow   time.Duration
	now           func() time.Time

	mu       sync.Mutex
	sessions map[string]*session
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
