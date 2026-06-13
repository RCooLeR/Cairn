package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
	cerrdefs "github.com/containerd/errdefs"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	minimumAPIVersion       = "1.41"
	defaultTimeout          = 10 * time.Second
	defaultPingEvery        = 10 * time.Second
	defaultReconcileEvery   = time.Minute
	defaultEventBatchWindow = 250 * time.Millisecond
	defaultBackoffMin       = time.Second
	defaultBackoffMax       = 30 * time.Second
)

type Provider interface {
	ID() string
	DockerHost(context.Context) (string, error)
	DockerContext(context.Context) (string, error)
}

type DialerProvider interface {
	DockerDialContext(context.Context) (func(context.Context, string, string) (net.Conn, error), error)
}

type APIClient interface {
	Ping(context.Context) (dockertypes.Ping, error)
	Info(context.Context) (system.Info, error)
	ServerVersion(context.Context) (dockertypes.Version, error)
	DiskUsage(context.Context, dockertypes.DiskUsageOptions) (dockertypes.DiskUsage, error)
	ContainerList(context.Context, container.ListOptions) ([]container.Summary, error)
	ContainerInspectWithRaw(context.Context, string, bool) (container.InspectResponse, []byte, error)
	ContainerStart(context.Context, string, container.StartOptions) error
	ContainerStop(context.Context, string, container.StopOptions) error
	ContainerRestart(context.Context, string, container.StopOptions) error
	ContainerKill(context.Context, string, string) error
	ContainerRemove(context.Context, string, container.RemoveOptions) error
	ContainerUnpause(context.Context, string) error
	ContainerLogs(context.Context, string, container.LogsOptions) (io.ReadCloser, error)
	ContainerStats(context.Context, string, bool) (container.StatsResponseReader, error)
	ContainerStatsOneShot(context.Context, string) (container.StatsResponseReader, error)
	ContainerExecCreate(context.Context, string, container.ExecOptions) (container.ExecCreateResponse, error)
	ContainerExecAttach(context.Context, string, container.ExecAttachOptions) (dockertypes.HijackedResponse, error)
	ContainerExecResize(context.Context, string, container.ResizeOptions) error
	ContainerExecInspect(context.Context, string) (container.ExecInspect, error)
	ContainerCreate(context.Context, *container.Config, *container.HostConfig, *network.NetworkingConfig, *ocispec.Platform, string) (container.CreateResponse, error)
	ContainerRename(context.Context, string, string) error
	ImageList(context.Context, image.ListOptions) ([]image.Summary, error)
	ImageInspectWithRaw(context.Context, string) (image.InspectResponse, []byte, error)
	ImagePull(context.Context, string, image.PullOptions) (io.ReadCloser, error)
	ImageSave(context.Context, []string, ...dockerclient.ImageSaveOption) (io.ReadCloser, error)
	ImageLoad(context.Context, io.Reader, ...dockerclient.ImageLoadOption) (image.LoadResponse, error)
	ImageSearch(context.Context, string, registry.SearchOptions) ([]registry.SearchResult, error)
	VolumeList(context.Context, volume.ListOptions) (volume.ListResponse, error)
	VolumeInspectWithRaw(context.Context, string) (volume.Volume, []byte, error)
	VolumeCreate(context.Context, volume.CreateOptions) (volume.Volume, error)
	NetworkList(context.Context, network.ListOptions) ([]network.Summary, error)
	NetworkInspectWithRaw(context.Context, string, network.InspectOptions) (network.Inspect, []byte, error)
	NetworkCreate(context.Context, string, network.CreateOptions) (network.CreateResponse, error)
	Events(context.Context, events.ListOptions) (<-chan events.Message, <-chan error)
	Close() error
}

type ConnectedPayload struct {
	Host    string `json:"host"`
	Context string `json:"context"`
}

type DisconnectedPayload struct {
	Reason string `json:"reason"`
}

type ObjectsChangedPayload struct {
	Kind string   `json:"kind"`
	IDs  []string `json:"ids"`
}

type Client struct {
	provider          Provider
	bus               bus.Bus
	cache             *store.ObjectCacheRepository
	now               func() time.Time
	factory           func(string) (APIClient, error)
	factoryWithDialer func(string, func(context.Context, string, string) (net.Conn, error)) (APIClient, error)

	mu             sync.RWMutex
	api            APIClient
	host           string
	contextName    string
	unaryTimeout   time.Duration
	pingInterval   time.Duration
	reconcileEvery time.Duration
	eventBatch     time.Duration
	backoffMin     time.Duration
	backoffMax     time.Duration
	connectedOnce  bool
	shellCache     map[string][]string
}

func New(provider Provider, eventBus bus.Bus) *Client {
	return &Client{
		provider:          provider,
		bus:               eventBus,
		now:               func() time.Time { return time.Now().UTC() },
		factory:           newSDKClient,
		factoryWithDialer: newSDKClientWithDialer,
		unaryTimeout:      defaultTimeout,
		pingInterval:      defaultPingEvery,
		reconcileEvery:    defaultReconcileEvery,
		eventBatch:        defaultEventBatchWindow,
		backoffMin:        defaultBackoffMin,
		backoffMax:        defaultBackoffMax,
	}
}

func (c *Client) SetObjectCache(cache *store.ObjectCacheRepository) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = cache
}

func (c *Client) Connect(ctx context.Context) error {
	host, err := c.provider.DockerHost(ctx)
	if err != nil {
		return mapDockerError("resolve Docker host", err)
	}
	var dialContext func(context.Context, string, string) (net.Conn, error)
	if provider, ok := c.provider.(DialerProvider); ok {
		dialContext, err = provider.DockerDialContext(ctx)
		if err != nil {
			return mapDockerError("resolve Docker dialer", err)
		}
	}
	contextName, _ := c.provider.DockerContext(ctx)

	api, err := c.newAPIClient(host, dialContext)
	if err != nil {
		return mapDockerError("create Docker client", err)
	}

	pingCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	ping, err := api.Ping(pingCtx)
	if err != nil {
		_ = api.Close()
		return mapDockerError("ping Docker daemon", err)
	}
	if !apiAtLeast(ping.APIVersion, minimumAPIVersion) {
		_ = api.Close()
		return apperror.New(
			apperror.DockerUnreachable,
			"Docker Engine API version is too old",
			apperror.WithDetail(fmt.Sprintf("daemon API %s, minimum %s", ping.APIVersion, minimumAPIVersion)),
		)
	}

	c.mu.Lock()
	old := c.api
	c.api = api
	c.host = host
	c.contextName = contextName
	c.connectedOnce = true
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	c.publish(bus.TopicDockerConnected, ConnectedPayload{Host: host, Context: contextName})
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api == nil {
		return nil
	}
	err := c.api.Close()
	c.api = nil
	return err
}

func (c *Client) Ping(ctx context.Context) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	ping, err := api.Ping(callCtx)
	if err != nil {
		return mapDockerError("ping Docker daemon", err)
	}
	if !apiAtLeast(ping.APIVersion, minimumAPIVersion) {
		return apperror.New(apperror.DockerUnreachable, "Docker Engine API version is too old")
	}
	return nil
}

func (c *Client) Info(ctx context.Context) (*models.DockerInfo, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	info, err := api.Info(callCtx)
	if err != nil {
		return nil, mapDockerError("read Docker info", err)
	}
	return mapInfo(info), nil
}

func (c *Client) Version(ctx context.Context) (*models.DockerVersion, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	version, err := api.ServerVersion(callCtx)
	if err != nil {
		return nil, mapDockerError("read Docker version", err)
	}
	return mapVersion(version), nil
}

func (c *Client) DiskUsage(ctx context.Context) (*models.DiskUsage, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	usage, err := api.DiskUsage(callCtx, dockertypes.DiskUsageOptions{})
	if err != nil {
		return nil, mapDockerError("read Docker disk usage", err)
	}
	return mapDiskUsage(usage), nil
}

func (c *Client) StartHealthLoop(ctx context.Context) {
	go c.healthLoop(ctx)
}

func (c *Client) healthLoop(ctx context.Context) {
	timer := time.NewTimer(c.pingInterval)
	defer timer.Stop()
	backoff := c.backoffMin
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if err := c.Ping(ctx); err == nil {
			backoff = c.backoffMin
			timer.Reset(c.pingInterval)
			continue
		} else {
			c.disconnect(err)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			if err := c.Connect(ctx); err == nil {
				backoff = c.backoffMin
				timer.Reset(c.pingInterval)
				break
			}
			backoff *= 2
			if backoff > c.backoffMax {
				backoff = c.backoffMax
			}
		}
	}
}

func (c *Client) ensureConnected(ctx context.Context) (APIClient, error) {
	c.mu.RLock()
	api := c.api
	c.mu.RUnlock()
	if api != nil {
		return api, nil
	}
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.api == nil {
		return nil, apperror.New(apperror.DockerUnreachable, "Docker client is not connected")
	}
	return c.api, nil
}

func (c *Client) disconnect(err error) {
	c.mu.Lock()
	api := c.api
	c.api = nil
	reason := err.Error()
	c.mu.Unlock()
	if api != nil {
		_ = api.Close()
	}
	c.publish(bus.TopicDockerDisconnected, DisconnectedPayload{Reason: reason})
}

func (c *Client) publish(topic bus.Topic, payload any) {
	if c.bus == nil {
		return
	}
	c.bus.Publish(bus.Event{Topic: topic, TS: c.now(), Payload: payload})
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.unaryTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func newSDKClient(host string) (APIClient, error) {
	return newSDKClientWithDialer(host, nil)
}

func newSDKClientWithDialer(host string, dialContext func(context.Context, string, string) (net.Conn, error)) (APIClient, error) {
	opts := []dockerclient.Opt{
		dockerclient.WithHost(host),
	}
	if dialContext != nil {
		opts = append(opts, dockerclient.WithDialContext(dialContext))
	}
	opts = append(opts, dockerclient.WithAPIVersionNegotiation())
	return dockerclient.NewClientWithOpts(
		opts...,
	)
}

func (c *Client) newAPIClient(host string, dialContext func(context.Context, string, string) (net.Conn, error)) (APIClient, error) {
	if dialContext != nil && c.factoryWithDialer != nil {
		return c.factoryWithDialer(host, dialContext)
	}
	return c.factory(host)
}

func mapDockerError(action string, err error) error {
	if err == nil {
		return nil
	}
	if cerrdefs.IsNotFound(err) {
		return apperror.Wrap(apperror.NotFound, action+" not found", err, apperror.WithDetail(err.Error()))
	}
	if cerrdefs.IsConflict(err) {
		return apperror.Wrap(apperror.Conflict, action+" conflicted", err, apperror.WithDetail(err.Error()))
	}
	if errors.Is(err, context.Canceled) {
		return apperror.Wrap(apperror.Cancelled, action+" cancelled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apperror.Wrap(apperror.Timeout, action+" timed out", err)
	}
	return apperror.Wrap(apperror.DockerUnreachable, action+" failed", err, apperror.WithDetail(err.Error()))
}

func (c *Client) providerID() string {
	if c.provider == nil {
		return ""
	}
	return c.provider.ID()
}

func apiAtLeast(actual, minimum string) bool {
	actualParts := apiVersionParts(actual)
	minimumParts := apiVersionParts(minimum)
	if actualParts[0] != minimumParts[0] {
		return actualParts[0] > minimumParts[0]
	}
	return actualParts[1] >= minimumParts[1]
}

func apiVersionParts(value string) [2]int {
	var parts [2]int
	raw := strings.SplitN(value, ".", 3)
	for i := 0; i < len(raw) && i < 2; i++ {
		n, _ := strconv.Atoi(raw[i])
		parts[i] = n
	}
	return parts
}
