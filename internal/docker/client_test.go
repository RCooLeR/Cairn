package docker

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
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
