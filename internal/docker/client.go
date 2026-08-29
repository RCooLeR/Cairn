package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/store"
	cerrdefs "github.com/containerd/errdefs"
	dockerclient "github.com/moby/moby/client"
)

const (
	minimumAPIVersion       = "1.41"
	defaultTimeout          = 10 * time.Second
	defaultInventoryTimeout = 60 * time.Second
	defaultPingEvery        = 10 * time.Second
	defaultReconcileEvery   = time.Minute
	defaultEventBatchWindow = 250 * time.Millisecond
	defaultBackoffMin       = time.Second
	defaultBackoffMax       = 30 * time.Second
	// defaultFailureThreshold is how many consecutive recovery attempts must
	// fail after a ping error before the connection is reported as
	// disconnected. Until then the client only reports "reconnecting", so a
	// brief backend blip does not flash a disconnected banner.
	defaultFailureThreshold = 3
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
	Ping(context.Context, dockerclient.PingOptions) (dockerclient.PingResult, error)
	Info(context.Context, dockerclient.InfoOptions) (dockerclient.SystemInfoResult, error)
	ServerVersion(context.Context, dockerclient.ServerVersionOptions) (dockerclient.ServerVersionResult, error)
	DiskUsage(context.Context, dockerclient.DiskUsageOptions) (dockerclient.DiskUsageResult, error)
	ContainerList(context.Context, dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error)
	ContainerInspect(context.Context, string, dockerclient.ContainerInspectOptions) (dockerclient.ContainerInspectResult, error)
	ContainerStart(context.Context, string, dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error)
	ContainerStop(context.Context, string, dockerclient.ContainerStopOptions) (dockerclient.ContainerStopResult, error)
	ContainerRestart(context.Context, string, dockerclient.ContainerRestartOptions) (dockerclient.ContainerRestartResult, error)
	ContainerKill(context.Context, string, dockerclient.ContainerKillOptions) (dockerclient.ContainerKillResult, error)
	ContainerRemove(context.Context, string, dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error)
	ContainerUnpause(context.Context, string, dockerclient.ContainerUnpauseOptions) (dockerclient.ContainerUnpauseResult, error)
	ContainerLogs(context.Context, string, dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error)
	ContainerStats(context.Context, string, dockerclient.ContainerStatsOptions) (dockerclient.ContainerStatsResult, error)
	ContainerTop(context.Context, string, dockerclient.ContainerTopOptions) (dockerclient.ContainerTopResult, error)
	ExecCreate(context.Context, string, dockerclient.ExecCreateOptions) (dockerclient.ExecCreateResult, error)
	ExecAttach(context.Context, string, dockerclient.ExecAttachOptions) (dockerclient.ExecAttachResult, error)
	ExecResize(context.Context, string, dockerclient.ExecResizeOptions) (dockerclient.ExecResizeResult, error)
	ExecInspect(context.Context, string, dockerclient.ExecInspectOptions) (dockerclient.ExecInspectResult, error)
	ContainerCreate(context.Context, dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error)
	ContainerRename(context.Context, string, dockerclient.ContainerRenameOptions) (dockerclient.ContainerRenameResult, error)
	ImageList(context.Context, dockerclient.ImageListOptions) (dockerclient.ImageListResult, error)
	ImageInspect(context.Context, string, ...dockerclient.ImageInspectOption) (dockerclient.ImageInspectResult, error)
	ImagePull(context.Context, string, dockerclient.ImagePullOptions) (dockerclient.ImagePullResponse, error)
	ImageTag(context.Context, dockerclient.ImageTagOptions) (dockerclient.ImageTagResult, error)
	ImagePush(context.Context, string, dockerclient.ImagePushOptions) (dockerclient.ImagePushResponse, error)
	ImageSave(context.Context, []string, ...dockerclient.ImageSaveOption) (dockerclient.ImageSaveResult, error)
	ImageLoad(context.Context, io.Reader, ...dockerclient.ImageLoadOption) (dockerclient.ImageLoadResult, error)
	ImageSearch(context.Context, string, dockerclient.ImageSearchOptions) (dockerclient.ImageSearchResult, error)
	ImageRemove(context.Context, string, dockerclient.ImageRemoveOptions) (dockerclient.ImageRemoveResult, error)
	ImagePrune(context.Context, dockerclient.ImagePruneOptions) (dockerclient.ImagePruneResult, error)
	ContainerPrune(context.Context, dockerclient.ContainerPruneOptions) (dockerclient.ContainerPruneResult, error)
	BuildCachePrune(context.Context, dockerclient.BuildCachePruneOptions) (dockerclient.BuildCachePruneResult, error)
	VolumeList(context.Context, dockerclient.VolumeListOptions) (dockerclient.VolumeListResult, error)
	VolumeInspect(context.Context, string, dockerclient.VolumeInspectOptions) (dockerclient.VolumeInspectResult, error)
	VolumeCreate(context.Context, dockerclient.VolumeCreateOptions) (dockerclient.VolumeCreateResult, error)
	VolumeRemove(context.Context, string, dockerclient.VolumeRemoveOptions) (dockerclient.VolumeRemoveResult, error)
	VolumePrune(context.Context, dockerclient.VolumePruneOptions) (dockerclient.VolumePruneResult, error)
	NetworkList(context.Context, dockerclient.NetworkListOptions) (dockerclient.NetworkListResult, error)
	NetworkInspect(context.Context, string, dockerclient.NetworkInspectOptions) (dockerclient.NetworkInspectResult, error)
	NetworkCreate(context.Context, string, dockerclient.NetworkCreateOptions) (dockerclient.NetworkCreateResult, error)
	NetworkRemove(context.Context, string, dockerclient.NetworkRemoveOptions) (dockerclient.NetworkRemoveResult, error)
	NetworkPrune(context.Context, dockerclient.NetworkPruneOptions) (dockerclient.NetworkPruneResult, error)
	Events(context.Context, dockerclient.EventsListOptions) dockerclient.EventsResult
	Close() error
}

type ConnectedPayload struct {
	Host    string `json:"host"`
	Context string `json:"context"`
}

type DisconnectedPayload struct {
	Reason string `json:"reason"`
}

type ReconnectingPayload struct {
	Reason  string `json:"reason"`
	Attempt int    `json:"attempt"`
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
	registryAuth      func(context.Context, string) (string, error)

	reconnectMu      sync.Mutex
	mu               sync.RWMutex
	api              APIClient
	host             string
	contextName      string
	expectedScope    runtimescope.Scope
	runtimeScope     runtimescope.Scope
	processBacked    bool
	unaryTimeout     time.Duration
	pingInterval     time.Duration
	reconcileEvery   time.Duration
	eventBatch       time.Duration
	backoffMin       time.Duration
	backoffMax       time.Duration
	failureThreshold int
	connectedOnce    bool
	shellCache       map[string][]string
	objectGates      map[string]chan struct{}
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
		failureThreshold:  defaultFailureThreshold,
		objectGates:       newObjectReconcileGates(),
	}
}

func (c *Client) SetObjectCache(cache *store.ObjectCacheRepository) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = cache
}

func (c *Client) BindRuntimeScope(scope runtimescope.Scope) error {
	if c == nil || !scope.Valid() {
		return apperror.New(apperror.ProviderNotReady, "Docker runtime scope is required")
	}
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.expectedScope.Valid() && !c.expectedScope.Equal(scope) {
		return apperror.New(apperror.Conflict, "Docker client is already bound to another runtime context")
	}
	if c.runtimeScope.Valid() && !c.runtimeScope.Equal(scope) {
		return apperror.New(apperror.Conflict, "Connected Docker client belongs to another runtime context")
	}
	c.expectedScope = scope
	return nil
}

func (c *Client) Connect(ctx context.Context) error {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	runtimeScope, err := providers.ResolveRuntimeScope(ctx, c.provider)
	if err != nil {
		return err
	}
	c.mu.RLock()
	expectedScope := c.expectedScope
	c.mu.RUnlock()
	if expectedScope.Valid() {
		if !runtimeScope.Equal(expectedScope) {
			return apperror.New(apperror.NotFound, "Docker provider target changed; reconnect the runtime")
		}
	} else {
		expectedScope = runtimeScope
	}
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
	api, err := c.newAPIClient(host, dialContext)
	if err != nil {
		return mapDockerError("create Docker client", err)
	}

	pingCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	ping, err := api.Ping(pingCtx, dockerclient.PingOptions{NegotiateAPIVersion: true})
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
	finalScope, err := providers.ResolveRuntimeScope(ctx, c.provider)
	if err != nil {
		_ = api.Close()
		return err
	}
	if !finalScope.Equal(expectedScope) {
		_ = api.Close()
		return apperror.New(apperror.NotFound, "Docker provider target changed while connecting")
	}

	c.mu.Lock()
	if c.expectedScope.Valid() && !c.expectedScope.Equal(expectedScope) {
		c.mu.Unlock()
		_ = api.Close()
		return apperror.New(apperror.NotFound, "Docker runtime context changed while connecting")
	}
	old := c.api
	c.api = api
	c.host = host
	c.contextName = expectedScope.ContextName()
	c.expectedScope = expectedScope
	c.runtimeScope = expectedScope
	c.processBacked = dialContext != nil
	c.connectedOnce = true
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	c.publish(bus.TopicDockerConnected, ConnectedPayload{Host: host, Context: expectedScope.ContextName()})
	return nil
}

func (c *Client) Close() error {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	c.mu.Lock()
	api := c.api
	c.api = nil
	c.processBacked = false
	c.mu.Unlock()
	if api == nil {
		return nil
	}
	return api.Close()
}

func (c *Client) Ping(ctx context.Context) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	ping, err := api.Ping(callCtx, dockerclient.PingOptions{NegotiateAPIVersion: true})
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
	info, err := api.Info(callCtx, dockerclient.InfoOptions{})
	if err != nil {
		return nil, mapDockerError("read Docker info", err)
	}
	return mapInfo(info.Info), nil
}

func (c *Client) Version(ctx context.Context) (*models.DockerVersion, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	version, err := api.ServerVersion(callCtx, dockerclient.ServerVersionOptions{})
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
	usage, err := api.DiskUsage(callCtx, dockerclient.DiskUsageOptions{})
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if err := c.Ping(ctx); err == nil {
			timer.Reset(c.pingInterval)
			continue
		} else if c.handleConnectionLoss(ctx, err) {
			timer.Reset(c.pingInterval)
		} else {
			return
		}
	}
}

// handleConnectionLoss runs the grace period and reconnect loop after a ping
// failure. To avoid alarming the user over a transient blip, it publishes a
// "reconnecting" event and retries quietly with exponential backoff; it only
// publishes "disconnected" once failureThreshold consecutive recovery attempts
// have failed. It returns true once the connection is restored (Connect has
// published "connected"), or false if ctx was cancelled.
func (c *Client) handleConnectionLoss(ctx context.Context, firstErr error) bool {
	threshold := max(c.failureThreshold, 1)
	attempt := 1
	backoff := c.backoffMin
	disconnected := false
	lastErr := firstErr
	// Only announce a grace period when one actually exists. With threshold<=1
	// there is no grace window, so we go straight to disconnect below without a
	// transient reconnecting flash.
	if threshold > 1 {
		c.publish(bus.TopicDockerReconnecting, ReconnectingPayload{Reason: firstErr.Error(), Attempt: attempt})
	}

	for {
		if !disconnected && attempt >= threshold {
			c.disconnect(lastErr)
			disconnected = true
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}

		if err := c.Connect(ctx); err == nil {
			return true
		} else {
			lastErr = err
		}

		attempt++
		if !disconnected {
			c.publish(bus.TopicDockerReconnecting, ReconnectingPayload{Reason: lastErr.Error(), Attempt: attempt})
		}
		backoff *= 2
		if backoff > c.backoffMax {
			backoff = c.backoffMax
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
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	c.mu.Lock()
	api := c.api
	c.api = nil
	c.processBacked = false
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

func (c *Client) publishCritical(topic bus.Topic, payload any) {
	if c.bus == nil {
		return
	}
	if err := bus.PublishCriticalBounded(c.bus, bus.Event{Topic: topic, TS: c.now(), Payload: payload}); err != nil {
		slog.Warn("publish critical Docker event failed", "topic", topic, "error", err)
	}
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.unaryTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (c *Client) withInventoryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := max(c.unaryTimeout, defaultInventoryTimeout)
	return context.WithTimeout(ctx, timeout)
}

func newSDKClient(host string) (APIClient, error) {
	return newSDKClientWithDialer(host, nil)
}

func newSDKClientWithDialer(host string, dialContext func(context.Context, string, string) (net.Conn, error)) (APIClient, error) {
	opts := []dockerclient.Opt{}
	if dialContext != nil {
		opts = append(opts, dockerclient.WithHTTPClient(processBackedHTTPClient()))
	}
	opts = append(opts, dockerclient.WithHost(host))
	if dialContext != nil {
		// Process-backed dialers terminate at a Docker unix socket and carry
		// plain HTTP. Be explicit because cloning a custom transport can give it
		// a non-nil TLS config, which makes the Moby client infer HTTPS.
		opts = append(opts, dockerclient.WithScheme("http"), dockerclient.WithDialContext(dialContext))
	}
	return dockerclient.New(opts...)
}

func processBackedHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        4,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     10 * time.Second,
		},
		CheckRedirect: dockerclient.CheckRedirect,
	}
}

func (c *Client) newAPIClient(host string, dialContext func(context.Context, string, string) (net.Conn, error)) (APIClient, error) {
	if dialContext != nil && c.factoryWithDialer != nil {
		return c.factoryWithDialer(host, dialContext)
	}
	return c.factory(host)
}

func (c *Client) usesProcessBackedTransport() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.processBacked
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
	if isDockerConflictMessage(err) {
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

func isDockerConflictMessage(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "port is already allocated") ||
		strings.Contains(message, "address already in use") ||
		strings.Contains(message, "is already in use")
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
