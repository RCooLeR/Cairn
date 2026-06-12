package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/providers"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/api/types/volume"
)

func TestClientConnectAndDTOs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	eventBus := bus.New()
	defer eventBus.Close()
	events := eventBus.Subscribe(ctx, bus.TopicDockerConnected, 4)

	api := newFakeAPI()
	api.info = system.Info{
		ID:              "daemon-id",
		Name:            "builder",
		ServerVersion:   "28.5.2",
		Driver:          "overlay2",
		DockerRootDir:   "/var/lib/docker",
		OperatingSystem: "Ubuntu 24.04",
		Architecture:    "x86_64",
		NCPU:            8,
		MemTotal:        16 << 30,
	}
	api.version = dockertypes.Version{
		Version:       "28.5.2",
		APIVersion:    "1.51",
		MinAPIVersion: "1.24",
		GitCommit:     "abc123",
		GoVersion:     "go1.26.4",
	}
	api.diskUsage = dockertypes.DiskUsage{
		Images: []*image.Summary{
			{Size: 100, Containers: 0},
			{Size: 200, Containers: 2},
		},
		Containers: []*container.Summary{
			{SizeRw: 10, SizeRootFs: 30, State: "running"},
			{SizeRw: 5, SizeRootFs: 20, State: "exited"},
		},
		Volumes: []*volume.Volume{
			{UsageData: &volume.UsageData{Size: 50, RefCount: 0}},
			{UsageData: &volume.UsageData{Size: 75, RefCount: 1}},
		},
		BuildCache: []*build.CacheRecord{
			{Size: 7, InUse: false},
			{Size: 8, InUse: true},
		},
	}

	client := New(fakeDockerProvider{}, eventBus)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	select {
	case event := <-events:
		payload, ok := event.Payload.(ConnectedPayload)
		if !ok {
			t.Fatalf("connected payload = %#v", event.Payload)
		}
		if payload.Host != "unix:///var/run/docker.sock" || payload.Context != "default" {
			t.Fatalf("payload = %#v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for docker:connected")
	}

	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.ServerVersion != "28.5.2" || info.CPUs != 8 || info.MemoryBytes != 16<<30 {
		t.Fatalf("info = %#v", info)
	}

	version, err := client.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version.APIVersion != "1.51" || version.GitCommit != "abc123" {
		t.Fatalf("version = %#v", version)
	}

	usage, err := client.DiskUsage(ctx)
	if err != nil {
		t.Fatalf("DiskUsage() error = %v", err)
	}
	if usage.TotalBytes != 505 || usage.Reclaimable != 162 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestClientHealthLoopDisconnectsAndReconnects(t *testing.T) {
	t.Parallel()
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventBus := bus.New()
	defer eventBus.Close()
	connected := eventBus.Subscribe(rootCtx, bus.TopicDockerConnected, 4)
	disconnected := eventBus.Subscribe(rootCtx, bus.TopicDockerDisconnected, 4)

	first := newFakeAPI()
	second := newFakeAPI()
	clients := []APIClient{first, second}
	client := New(fakeDockerProvider{}, eventBus)
	client.pingInterval = 10 * time.Millisecond
	client.backoffMin = 10 * time.Millisecond
	client.backoffMax = 20 * time.Millisecond
	client.factory = func(string) (APIClient, error) {
		if len(clients) == 0 {
			return nil, errors.New("no fake clients left")
		}
		next := clients[0]
		clients = clients[1:]
		return next, nil
	}
	if err := client.Connect(rootCtx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	<-connected
	first.setPingError(errors.New("daemon stopped"))

	client.StartHealthLoop(rootCtx)

	select {
	case event := <-disconnected:
		payload, ok := event.Payload.(DisconnectedPayload)
		if !ok || payload.Reason == "" {
			t.Fatalf("disconnected payload = %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for docker:disconnected")
	}

	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for docker:connected after reconnect")
	}
}

func TestClientRealDockerIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real Docker integration runs only on Linux")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}

	ctx := context.Background()
	provider := providers.NewLinuxNative(providers.LinuxNativeOptions{})
	status, err := provider.Detect(ctx)
	if err != nil {
		t.Fatalf("provider Detect() error = %v", err)
	}
	if !status.DockerRunning {
		t.Fatalf("Docker daemon is not running: %#v", status.Problems)
	}

	eventBus := bus.New()
	defer eventBus.Close()
	client := New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.ServerVersion == "" || info.DockerRootDir == "" {
		t.Fatalf("info missing required fields: %#v", info)
	}
	version, err := client.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version.APIVersion == "" {
		t.Fatalf("version missing API version: %#v", version)
	}
	if _, err := client.DiskUsage(ctx); err != nil {
		t.Fatalf("DiskUsage() error = %v", err)
	}
}

func TestClientRealDockerRestartIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real Docker restart integration runs only on Linux")
	}
	if os.Getenv("CAIRN_REAL_DOCKER_RESTART") != "1" {
		t.Skip("set CAIRN_REAL_DOCKER_RESTART=1 to stop/start the local Docker daemon")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := waitDockerCLI(ctx); err != nil {
		t.Fatalf("Docker daemon was not ready before restart test: %v", err)
	}

	provider := providers.NewLinuxNative(providers.LinuxNativeOptions{})
	eventBus := bus.New()
	defer eventBus.Close()
	connected := eventBus.Subscribe(ctx, bus.TopicDockerConnected, 8)
	disconnected := eventBus.Subscribe(ctx, bus.TopicDockerDisconnected, 8)

	client := New(provider, eventBus)
	client.unaryTimeout = 2 * time.Second
	client.pingInterval = 250 * time.Millisecond
	client.backoffMin = 250 * time.Millisecond
	client.backoffMax = time.Second
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() {
		_ = client.Close()
	}()
	if _, err := waitConnected(ctx, connected, 5*time.Second); err != nil {
		t.Fatalf("initial docker:connected event: %v", err)
	}

	client.StartHealthLoop(ctx)

	stopped := false
	t.Cleanup(func() {
		if !stopped {
			return
		}
		startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer startCancel()
		_ = controlDockerService(startCtx, "start")
		_ = waitDockerCLI(startCtx)
	})

	if err := controlDockerService(ctx, "stop"); err != nil {
		t.Fatalf("stop Docker daemon: %v", err)
	}
	stopped = true
	if _, err := waitDisconnected(ctx, disconnected, 20*time.Second); err != nil {
		t.Fatalf("docker:disconnected event after daemon stop: %v", err)
	}

	if err := controlDockerService(ctx, "start"); err != nil {
		t.Fatalf("start Docker daemon: %v", err)
	}
	if err := waitDockerCLI(ctx); err != nil {
		t.Fatalf("Docker daemon did not become ready after start: %v", err)
	}
	stopped = false
	if _, err := waitConnected(ctx, connected, 45*time.Second); err != nil {
		t.Fatalf("docker:connected event after daemon restart: %v", err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping() after reconnect error = %v", err)
	}
}

type fakeDockerProvider struct{}

func (fakeDockerProvider) DockerHost(context.Context) (string, error) {
	return "unix:///var/run/docker.sock", nil
}

func (fakeDockerProvider) DockerContext(context.Context) (string, error) {
	return "default", nil
}

type fakeAPI struct {
	mu        sync.Mutex
	ping      dockertypes.Ping
	pingErr   error
	info      system.Info
	version   dockertypes.Version
	diskUsage dockertypes.DiskUsage
	closed    bool
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{ping: dockertypes.Ping{APIVersion: "1.51"}}
}

func (a *fakeAPI) setPingError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pingErr = err
}

func (a *fakeAPI) Ping(context.Context) (dockertypes.Ping, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pingErr != nil {
		return dockertypes.Ping{}, a.pingErr
	}
	return a.ping, nil
}

func (a *fakeAPI) Info(context.Context) (system.Info, error) {
	return a.info, nil
}

func (a *fakeAPI) ServerVersion(context.Context) (dockertypes.Version, error) {
	return a.version, nil
}

func (a *fakeAPI) DiskUsage(context.Context, dockertypes.DiskUsageOptions) (dockertypes.DiskUsage, error) {
	return a.diskUsage, nil
}

func (a *fakeAPI) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return nil
}

func waitConnected(ctx context.Context, events <-chan bus.Event, timeout time.Duration) (ConnectedPayload, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ConnectedPayload{}, ctx.Err()
		case <-timer.C:
			return ConnectedPayload{}, context.DeadlineExceeded
		case event, ok := <-events:
			if !ok {
				return ConnectedPayload{}, errors.New("event subscription closed")
			}
			payload, ok := event.Payload.(ConnectedPayload)
			if ok {
				return payload, nil
			}
		}
	}
}

func waitDisconnected(ctx context.Context, events <-chan bus.Event, timeout time.Duration) (DisconnectedPayload, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return DisconnectedPayload{}, ctx.Err()
		case <-timer.C:
			return DisconnectedPayload{}, context.DeadlineExceeded
		case event, ok := <-events:
			if !ok {
				return DisconnectedPayload{}, errors.New("event subscription closed")
			}
			payload, ok := event.Payload.(DisconnectedPayload)
			if ok {
				return payload, nil
			}
		}
	}
}

func waitDockerCLI(ctx context.Context) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		infoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cmd := exec.CommandContext(infoCtx, "docker", "info")
		lastErr = cmd.Run()
		cancel()
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w; last docker info error: %v", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func controlDockerService(ctx context.Context, action string) error {
	commands := [][]string{
		{"sudo", "systemctl", action, "docker"},
		{"sudo", "service", "docker", action},
	}
	errs := make([]error, 0, len(commands))
	for _, command := range commands {
		if _, err := exec.LookPath(command[0]); err != nil {
			errs = append(errs, err)
			continue
		}
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		output, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		errs = append(errs, fmt.Errorf("%s: %w: %s", strings.Join(command, " "), err, strings.TrimSpace(string(output))))
	}
	return errors.Join(errs...)
}
