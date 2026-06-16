package docker

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
	cerrdefs "github.com/containerd/errdefs"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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

func TestClientConnectUsesProviderDialer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newFakeAPI()
	var gotHost string
	var gotDialer bool
	client := New(fakeDialerProvider{
		host: "unix:///var/run/docker.sock",
		dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dialer should be passed to SDK factory, not called by fake API")
		},
	}, nil)
	client.factoryWithDialer = func(host string, dialer func(context.Context, string, string) (net.Conn, error)) (APIClient, error) {
		gotHost = host
		gotDialer = dialer != nil
		return api, nil
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if gotHost != "unix:///var/run/docker.sock" || !gotDialer {
		t.Fatalf("factory host=%q dialer=%t", gotHost, gotDialer)
	}
}

func TestClientConcurrentConnectSerializesReplacement(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := New(fakeDockerProvider{}, nil)
	releaseFactory := make(chan struct{})
	factoryStarted := make(chan struct{}, 2)
	var mu sync.Mutex
	inFlight := 0
	maxInFlight := 0
	apis := []*fakeAPI{}
	client.factory = func(string) (APIClient, error) {
		api := newFakeAPI()
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		apis = append(apis, api)
		mu.Unlock()
		factoryStarted <- struct{}{}
		select {
		case <-releaseFactory:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		mu.Lock()
		inFlight--
		mu.Unlock()
		return api, nil
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			errs <- client.Connect(ctx)
		}()
	}
	close(start)
	select {
	case <-factoryStarted:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first factory call")
	}
	select {
	case <-factoryStarted:
		t.Fatal("second factory call started before the first connect completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFactory)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Connect() error = %v", err)
		}
	}

	mu.Lock()
	gotMax := maxInFlight
	gotAPIs := append([]*fakeAPI(nil), apis...)
	mu.Unlock()
	if gotMax != 1 {
		t.Fatalf("concurrent factory calls = %d, want 1", gotMax)
	}
	if len(gotAPIs) != 2 {
		t.Fatalf("API clients created = %d, want 2", len(gotAPIs))
	}
	gotAPIs[0].mu.Lock()
	firstClosed := gotAPIs[0].closed
	gotAPIs[0].mu.Unlock()
	gotAPIs[1].mu.Lock()
	secondClosed := gotAPIs[1].closed
	gotAPIs[1].mu.Unlock()
	if !firstClosed || secondClosed {
		t.Fatalf("closed clients first=%t second=%t, want first superseded only", firstClosed, secondClosed)
	}
}

func TestClientContainerStatsUsesStreamAndOneShot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newFakeAPI()
	read := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	api.stats["abc123"] = []container.StatsResponse{{
		ID:   "abc123",
		Read: read,
		CPUStats: container.CPUStats{
			CPUUsage:    container.CPUUsage{TotalUsage: 100},
			SystemUsage: 1000,
		},
	}}

	client := New(fakeDockerProvider{}, nil)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	stream, err := client.ContainerStats(ctx, "abc123", StatsOptions{Stream: true})
	if err != nil {
		t.Fatalf("ContainerStats(stream) error = %v", err)
	}
	defer func() {
		_ = stream.Body.Close()
	}()
	var streamStats container.StatsResponse
	if err := json.NewDecoder(stream.Body).Decode(&streamStats); err != nil {
		t.Fatalf("decode stream stats: %v", err)
	}
	if streamStats.ID != "abc123" {
		t.Fatalf("stream stats = %#v", streamStats)
	}

	oneShot, err := client.ContainerStats(ctx, "abc123", StatsOptions{OneShot: true})
	if err != nil {
		t.Fatalf("ContainerStats(one-shot) error = %v", err)
	}
	defer func() {
		_ = oneShot.Body.Close()
	}()

	if len(api.statsCalls) != 2 {
		t.Fatalf("stats calls = %#v", api.statsCalls)
	}
	if !api.statsCalls[0].Stream || !api.statsCalls[1].OneShot {
		t.Fatalf("stats calls = %#v", api.statsCalls)
	}
}

func TestCancelReadCloserCancelsOnlyOnClose(t *testing.T) {
	t.Parallel()
	canceled := false
	body := cancelReadCloser{
		ReadCloser: io.NopCloser(strings.NewReader("ok")),
		cancel:     func() { canceled = true },
	}
	if _, err := io.ReadAll(body); err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if canceled {
		t.Fatalf("cancel fired before Close")
	}
	if err := body.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !canceled {
		t.Fatalf("cancel did not fire on Close")
	}
}

func TestClientContainerExecAndShellDetection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newFakeAPI()
	api.containerInspects["abc123"] = container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:    "abc123",
			Name:  "/api-1",
			Image: "sha256:image1",
			State: &container.State{Status: "running"},
		},
		Config: &container.Config{Image: "example/api:latest"},
	}
	api.executablePaths["/bin/sh"] = true

	client := New(fakeDockerProvider{}, nil)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	shells, err := client.DetectContainerShells(ctx, "abc123")
	if err != nil {
		t.Fatalf("DetectContainerShells() error = %v", err)
	}
	if got, want := strings.Join(shells, ","), "/bin/sh"; got != want {
		t.Fatalf("shells = %q, want %q", got, want)
	}
	if _, err := client.DetectContainerShells(ctx, "abc123"); err != nil {
		t.Fatalf("DetectContainerShells(cached) error = %v", err)
	}
	if len(api.execCreates) != 3 {
		t.Fatalf("exec create count = %d, want cached second call", len(api.execCreates))
	}

	session, err := client.OpenContainerExec(ctx, "abc123", ExecOptions{
		Cmd:        []string{"/bin/sh"},
		User:       "1000",
		WorkingDir: "/app",
		Env:        map[string]string{"B": "2", "A": "1"},
		TTY:        true,
		Cols:       132,
		Rows:       43,
	})
	if err != nil {
		t.Fatalf("OpenContainerExec() error = %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})
	last := api.execCreates[len(api.execCreates)-1]
	if last.ContainerID != "abc123" || !last.Options.Tty || last.Options.User != "1000" || last.Options.WorkingDir != "/app" {
		t.Fatalf("exec create = %#v", last)
	}
	if fmt.Sprint(last.Options.Cmd) != "[/bin/sh]" || fmt.Sprint(last.Options.Env) != "[A=1 B=2]" {
		t.Fatalf("exec argv/env = %#v %#v", last.Options.Cmd, last.Options.Env)
	}
	if last.Options.ConsoleSize == nil || *last.Options.ConsoleSize != [2]uint{43, 132} {
		t.Fatalf("console size = %#v", last.Options.ConsoleSize)
	}

	if err := client.ResizeContainerExec(ctx, session.ID, 120, 30); err != nil {
		t.Fatalf("ResizeContainerExec() error = %v", err)
	}
	if got := api.execResizes[len(api.execResizes)-1]; got.ExecID != session.ID || got.Options.Width != 120 || got.Options.Height != 30 {
		t.Fatalf("resize = %#v", got)
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

func TestClientObjectsDTOsRawInspectAndCacheReconcile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newFakeAPI()
	seedFakeObjects(api)

	dbPath := filepath.Join(t.TempDir(), "cairn.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	client := New(fakeDockerProvider{}, nil)
	client.SetObjectCache(db.Objects())
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if err := client.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	containers, err := client.ListContainers(ctx, providersDemoContainerFilter())
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("containers = %#v, want 1", containers)
	}
	containerSummary := containers[0]
	if containerSummary.Name != "web" || containerSummary.State != "running" || containerSummary.Health != "healthy" {
		t.Fatalf("container summary = %#v", containerSummary)
	}
	if len(containerSummary.Ports) != 1 || containerSummary.Ports[0].HostPort != "8080" {
		t.Fatalf("container ports = %#v", containerSummary.Ports)
	}

	containerDetail, err := client.GetContainer(ctx, "abc123")
	if err != nil {
		t.Fatalf("GetContainer() error = %v", err)
	}
	if containerDetail.Summary.Restarts != 2 || containerDetail.RestartPolicy != "unless-stopped" {
		t.Fatalf("container detail = %#v", containerDetail)
	}
	if got := envValue(containerDetail.Env, "API_TOKEN"); got != "********" {
		t.Fatalf("API_TOKEN = %q, want redacted", got)
	}
	rawInspect, err := client.InspectContainerRaw(ctx, "abc123")
	if err != nil {
		t.Fatalf("InspectContainerRaw() error = %v", err)
	}
	if !strings.Contains(rawInspect, `"Id"`) {
		t.Fatalf("raw inspect did not look like Docker JSON: %s", rawInspect)
	}

	images, err := client.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages() error = %v", err)
	}
	if len(images) != 1 || !images[0].InUse || images[0].RepoTags[0] != "example/web:latest" {
		t.Fatalf("images = %#v", images)
	}
	imageDetail, err := client.GetImage(ctx, "sha256:image1")
	if err != nil {
		t.Fatalf("GetImage() error = %v", err)
	}
	if imageDetail.Architecture != "amd64" || imageDetail.OS != "linux" || len(imageDetail.Layers) != 1 {
		t.Fatalf("image detail = %#v", imageDetail)
	}

	volumes, err := client.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
	if len(volumes) != 1 || !volumes[0].InUse || volumes[0].SizeBytes != 42 {
		t.Fatalf("volumes = %#v", volumes)
	}
	volumeDetail, err := client.GetVolume(ctx, "demo_data")
	if err != nil {
		t.Fatalf("GetVolume() error = %v", err)
	}
	if len(volumeDetail.Containers) != 1 || volumeDetail.Containers[0].Name != "web" {
		t.Fatalf("volume detail = %#v", volumeDetail)
	}

	networks, err := client.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("ListNetworks() error = %v", err)
	}
	if len(networks) != 1 || networks[0].Name != "demo_default" || !networks[0].Attachable {
		t.Fatalf("networks = %#v", networks)
	}
	networkDetail, err := client.GetNetwork(ctx, "net1")
	if err != nil {
		t.Fatalf("GetNetwork() error = %v", err)
	}
	if networkDetail.Subnet != "172.22.0.0/16" || networkDetail.Gateway != "172.22.0.1" || len(networkDetail.Containers) != 1 {
		t.Fatalf("network detail = %#v", networkDetail)
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sql: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	if got := queryCount(t, ctx, sqlDB, "containers_cache"); got != 1 {
		t.Fatalf("containers_cache count = %d, want 1", got)
	}
	if got := queryCount(t, ctx, sqlDB, "images_cache"); got != 1 {
		t.Fatalf("images_cache count = %d, want 1", got)
	}
	if got := queryCount(t, ctx, sqlDB, "volumes_cache"); got != 1 {
		t.Fatalf("volumes_cache count = %d, want 1", got)
	}
	if got := queryCount(t, ctx, sqlDB, "networks_cache"); got != 1 {
		t.Fatalf("networks_cache count = %d, want 1", got)
	}
	if got := queryString(t, ctx, sqlDB, "SELECT status FROM containers_cache WHERE id = ?", fakeContainerID); got != "running" {
		t.Fatalf("cached container status = %q, want running", got)
	}
	if got := queryString(t, ctx, sqlDB, "SELECT subnet FROM networks_cache WHERE id = ?", "net1"); got != "172.22.0.0/16" {
		t.Fatalf("cached network subnet = %q, want subnet", got)
	}
}

func TestClientObjectEventsAreCoalesced(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventBus := bus.New()
	defer eventBus.Close()
	changed := eventBus.Subscribe(ctx, bus.TopicObjectsChanged, 4)

	api := newFakeAPI()
	client := New(fakeDockerProvider{}, eventBus)
	client.eventBatch = 10 * time.Millisecond
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	client.StartObjectEventLoop(ctx)

	api.events <- events.Message{
		Type:  events.ContainerEventType,
		Actor: events.Actor{ID: fakeContainerID},
		Time:  time.Now().Unix(),
	}
	api.events <- events.Message{
		Type:  events.ContainerEventType,
		Actor: events.Actor{ID: fakeContainerID},
		Time:  time.Now().Unix(),
	}

	payload := waitObjectsChanged(t, ctx, changed, time.Second)
	if payload.Kind != "container" || len(payload.IDs) != 1 || payload.IDs[0] != fakeContainerID {
		t.Fatalf("objects:changed payload = %#v", payload)
	}
}

func TestClientContainerLifecycleMethods(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newFakeAPI()
	seedFakeObjects(api)

	eventBus := bus.New()
	defer eventBus.Close()
	changed := eventBus.Subscribe(ctx, bus.TopicObjectsChanged, 8)

	client := New(fakeDockerProvider{}, eventBus)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if err := client.StartContainer(ctx, fakeContainerID); err != nil {
		t.Fatalf("StartContainer() error = %v", err)
	}
	if len(api.started) != 1 || api.started[0] != fakeContainerID {
		t.Fatalf("started = %#v", api.started)
	}
	waitObjectsChangedKind(t, ctx, changed, objectKindContainer, fakeContainerID, time.Second)

	paused := api.containerInspects[fakeContainerID]
	paused.State.Paused = true
	api.containerInspects[fakeContainerID] = paused
	if err := client.StartContainer(ctx, fakeContainerID); err != nil {
		t.Fatalf("StartContainer(paused) error = %v", err)
	}
	if len(api.unpaused) != 1 || api.unpaused[0] != fakeContainerID {
		t.Fatalf("unpaused = %#v", api.unpaused)
	}

	if err := client.StopContainer(ctx, fakeContainerID, 3); err != nil {
		t.Fatalf("StopContainer() error = %v", err)
	}
	if err := client.RestartContainer(ctx, fakeContainerID, 4); err != nil {
		t.Fatalf("RestartContainer() error = %v", err)
	}
	if err := client.KillContainer(ctx, fakeContainerID); err != nil {
		t.Fatalf("KillContainer() error = %v", err)
	}
	if err := client.RemoveContainer(ctx, fakeContainerID, models.RemoveContainerOptions{Force: true, RemoveVolumes: true}); err != nil {
		t.Fatalf("RemoveContainer() error = %v", err)
	}
	if len(api.stopped) != 1 || len(api.restarted) != 1 || len(api.killed) != 1 || len(api.removed) != 1 {
		t.Fatalf("lifecycle calls stopped=%#v restarted=%#v killed=%#v removed=%#v", api.stopped, api.restarted, api.killed, api.removed)
	}
	if api.killed[0] != fakeContainerID+":KILL" {
		t.Fatalf("killed = %#v", api.killed)
	}
}

func TestClientRunImageRenameAndCreateObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newFakeAPI()
	seedFakeObjects(api)

	eventBus := bus.New()
	defer eventBus.Close()
	changed := eventBus.Subscribe(ctx, bus.TopicObjectsChanged, 8)

	client := New(fakeDockerProvider{}, eventBus)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	containerID, err := client.RunImage(ctx, models.RunImageRequest{
		ImageRef:      "example/web:latest",
		Name:          "new-web",
		Ports:         []models.PortMapping{{HostPort: "0", ContainerPort: "80", Protocol: "tcp"}},
		Env:           []models.EnvVar{{Name: "MODE", Value: "test"}},
		Volumes:       []models.MountSpec{{Type: "volume", VolumeName: "demo_data", Target: "/data", ReadOnly: true}},
		NetworkID:     "demo_default",
		RestartPolicy: "unless-stopped",
		Command:       []string{"sleep", "60"},
		User:          "1000",
		Detach:        true,
	})
	if err != nil {
		t.Fatalf("RunImage() error = %v", err)
	}
	if containerID != "created-new-web" {
		t.Fatalf("containerID = %q", containerID)
	}
	if len(api.createdContainers) != 1 {
		t.Fatalf("created containers = %#v", api.createdContainers)
	}
	call := api.createdContainers[0]
	if call.Name != "new-web" || call.Config.Image != "example/web:latest" || call.Config.User != "1000" {
		t.Fatalf("create call config = %#v", call)
	}
	if got := call.Config.Env; len(got) != 1 || got[0] != "MODE=test" {
		t.Fatalf("env = %#v", got)
	}
	if got := call.HostConfig.RestartPolicy.Name; got != container.RestartPolicyUnlessStopped {
		t.Fatalf("restart policy = %q", got)
	}
	if len(call.HostConfig.Mounts) != 1 || call.HostConfig.Mounts[0].Source != "demo_data" || !call.HostConfig.Mounts[0].ReadOnly {
		t.Fatalf("mounts = %#v", call.HostConfig.Mounts)
	}
	if call.NetworkingConfig.EndpointsConfig["demo_default"] == nil {
		t.Fatalf("networking config = %#v", call.NetworkingConfig)
	}
	if len(api.started) != 1 || api.started[0] != containerID {
		t.Fatalf("started = %#v", api.started)
	}
	waitObjectsChangedKind(t, ctx, changed, objectKindContainer, containerID, time.Second)

	if err := client.RenameContainer(ctx, fakeContainerID, "web-renamed"); err != nil {
		t.Fatalf("RenameContainer() error = %v", err)
	}
	if len(api.renamed) != 1 || api.renamed[0] != fakeContainerID+":web-renamed" {
		t.Fatalf("renamed = %#v", api.renamed)
	}

	volumeSummary, err := client.CreateVolume(ctx, models.CreateVolumeRequest{
		Name:       "cairn_data",
		Driver:     "local",
		DriverOpts: map[string]string{"type": "none"},
		Labels:     map[string]string{"app": "cairn"},
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if volumeSummary.Name != "cairn_data" || volumeSummary.Driver != "local" {
		t.Fatalf("volume summary = %#v", volumeSummary)
	}

	networkSummary, err := client.CreateNetwork(ctx, models.CreateNetworkRequest{
		Name:       "cairn_net",
		Driver:     "bridge",
		Subnet:     "172.30.0.0/16",
		Gateway:    "172.30.0.1",
		Attachable: true,
		Labels:     map[string]string{"app": "cairn"},
	})
	if err != nil {
		t.Fatalf("CreateNetwork() error = %v", err)
	}
	if networkSummary.Name != "cairn_net" || !networkSummary.Attachable {
		t.Fatalf("network summary = %#v", networkSummary)
	}
	if got := api.createdNetworks[0].Options.IPAM.Config[0].Subnet; got != "172.30.0.0/16" {
		t.Fatalf("network subnet = %q", got)
	}
}

func TestClientImagePullSaveLoadAndSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newFakeAPI()
	seedFakeObjects(api)

	eventBus := bus.New()
	defer eventBus.Close()
	pullEvents := eventBus.Subscribe(ctx, bus.TopicImagePullProgress, 8)
	jobDone := eventBus.Subscribe(ctx, bus.TopicJobDone, 8)

	client := New(fakeDockerProvider{}, eventBus)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	streamID, err := client.PullImage(ctx, "alpine:latest")
	if err != nil {
		t.Fatalf("PullImage() error = %v", err)
	}
	if streamID == "" || len(api.pulled) != 1 || api.pulled[0] != "alpine:latest" {
		t.Fatalf("streamID=%q pulled=%#v", streamID, api.pulled)
	}
	if got := waitImageProgress(t, ctx, pullEvents, time.Second); got.StreamID != streamID {
		t.Fatalf("pull progress = %#v, want stream %q", got, streamID)
	}

	dest := filepath.Join(t.TempDir(), "image.tar")
	jobID, err := client.SaveImage(ctx, []string{"example/web:latest"}, dest)
	if err != nil {
		t.Fatalf("SaveImage() error = %v", err)
	}
	if jobID == "" || len(api.saved) != 1 || api.saved[0][0] != "example/web:latest" {
		t.Fatalf("jobID=%q saved=%#v", jobID, api.saved)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "fake image tar" {
		t.Fatalf("saved archive data=%q err=%v", string(data), err)
	}
	if got := waitJobDone(t, ctx, jobDone, time.Second); got.JobID != jobID || got.Error != "" {
		t.Fatalf("save job done = %#v", got)
	}

	src := filepath.Join(t.TempDir(), "load.tar")
	if err := os.WriteFile(src, []byte("load-me"), 0o644); err != nil {
		t.Fatalf("write load archive: %v", err)
	}
	loadJobID, err := client.LoadImage(ctx, src)
	if err != nil {
		t.Fatalf("LoadImage() error = %v", err)
	}
	if loadJobID == "" || len(api.loadedBytes) != 1 || api.loadedBytes[0] != len("load-me") {
		t.Fatalf("loadJobID=%q loadedBytes=%#v", loadJobID, api.loadedBytes)
	}
	if got := waitJobDone(t, ctx, jobDone, time.Second); got.JobID != loadJobID || !strings.Contains(got.Result, "loaded:latest") {
		t.Fatalf("load job done = %#v", got)
	}

	results, err := client.SearchHub(ctx, "alpine", 5)
	if err != nil {
		t.Fatalf("SearchHub() error = %v", err)
	}
	if len(results) != 1 || results[0].Name != "library/alpine" || !results[0].Official {
		t.Fatalf("hub results = %#v", results)
	}
}

func TestClientImageTagAndPush(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newFakeAPI()
	eventBus := bus.New()
	defer eventBus.Close()
	pushEvents := eventBus.Subscribe(ctx, bus.TopicImagePushProgress, 8)

	client := New(fakeDockerProvider{}, eventBus)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if err := client.TagImage(ctx, "sha256:local", "localhost:5000/test/app:1.0"); err != nil {
		t.Fatalf("TagImage() error = %v", err)
	}
	if got, want := api.tagged, []string{"sha256:local->localhost:5000/test/app:1.0"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("tagged = %#v, want %#v", got, want)
	}

	streamID, err := client.PushImage(ctx, "localhost:5000/test/app:1.0")
	if err != nil {
		t.Fatalf("PushImage() error = %v", err)
	}
	if streamID == "" || len(api.pushed) != 1 || api.pushed[0] != "localhost:5000/test/app:1.0" {
		t.Fatalf("streamID=%q pushed=%#v", streamID, api.pushed)
	}
	if len(api.pushAuth) != 1 || api.pushAuth[0] != "" {
		t.Fatalf("push auth = %#v, want anonymous auth", api.pushAuth)
	}
	if got := waitImageProgress(t, ctx, pushEvents, time.Second); got.StreamID != streamID {
		t.Fatalf("push progress = %#v, want stream %q", got, streamID)
	}
}

func TestClientImagePushMapsRegistryAuth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newFakeAPI()
	api.pushBody = `{"errorDetail":{"message":"unauthorized: authentication required"}}` + "\n"

	client := New(fakeDockerProvider{}, nil)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	_, err := client.PushImage(ctx, "localhost:5000/test/app:1.0")
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("PushImage() error = %v, want %s", err, apperror.RegistryAuth)
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || !strings.Contains(appErr.Detail, "localhost:5000") {
		t.Fatalf("registry auth detail = %#v", appErr)
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

const fakeContainerID = "abc1234567890abc1234567890abc1234567890abc1234567890abc1234567890ab"

func seedFakeObjects(api *fakeAPI) {
	created := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	started := created.Add(time.Minute)
	containerLabels := map[string]string{
		composeProjectLabel: "demo",
		composeServiceLabel: "web",
		"cairn.test":        "objects",
	}

	api.containers = []container.Summary{{
		ID:      fakeContainerID,
		Names:   []string{"/web"},
		Image:   "example/web:latest",
		ImageID: "sha256:image1",
		Command: "nginx -g daemon off;",
		Created: created.Unix(),
		Ports: []container.Port{{
			IP:          "0.0.0.0",
			PrivatePort: 80,
			PublicPort:  8080,
			Type:        "tcp",
		}},
		Labels: containerLabels,
		State:  "running",
		Status: "Up 2 minutes (healthy)",
		NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{
			"demo_default": {NetworkID: "net1"},
		}},
		Mounts: []container.MountPoint{{
			Type:        mount.TypeVolume,
			Name:        "demo_data",
			Source:      "/var/lib/docker/volumes/demo_data/_data",
			Destination: "/data",
			RW:          true,
		}},
	}}
	api.containerInspects[fakeContainerID] = container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:           fakeContainerID,
			Created:      created.Format(time.RFC3339Nano),
			Name:         "/web",
			RestartCount: 2,
			Image:        "sha256:image1",
			State: &container.State{
				Status:    "running",
				Running:   true,
				StartedAt: started.Format(time.RFC3339Nano),
				Health:    &container.Health{Status: "healthy"},
			},
			HostConfig: &container.HostConfig{
				RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
			},
		},
		Config: &container.Config{
			Image:      "example/web:latest",
			Cmd:        strslice.StrSlice{"nginx", "-g", "daemon off;"},
			Entrypoint: strslice.StrSlice{"/docker-entrypoint.sh"},
			Env:        []string{"API_TOKEN=secret-value", "MODE=dev"},
			Labels:     containerLabels,
			WorkingDir: "/srv/app",
			User:       "1000",
		},
		Mounts: api.containers[0].Mounts,
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"demo_default": {NetworkID: "net1"},
			},
		},
	}

	api.images = []image.Summary{{
		ID:          "sha256:image1",
		RepoTags:    []string{"example/web:latest"},
		RepoDigests: []string{"example/web@sha256:digest1"},
		Size:        123,
		Created:     created.Unix(),
		Containers:  1,
	}}
	api.imageInspects["sha256:image1"] = image.InspectResponse{
		ID:           "sha256:image1",
		RepoTags:     []string{"example/web:latest"},
		RepoDigests:  []string{"example/web@sha256:digest1"},
		Created:      created.Format(time.RFC3339Nano),
		Size:         123,
		Architecture: "amd64",
		Os:           "linux",
		Author:       "Cairn",
		Config: &dockerspec.DockerOCIImageConfig{
			ImageConfig: ocispec.ImageConfig{Labels: map[string]string{"org.opencontainers.image.title": "web"}},
		},
		RootFS: image.RootFS{Layers: []string{"sha256:layer1"}},
	}
	api.imageInspects["example/web:latest"] = api.imageInspects["sha256:image1"]

	api.volumes = []*volume.Volume{{
		Name:       "demo_data",
		Driver:     "local",
		Mountpoint: "/var/lib/docker/volumes/demo_data/_data",
		Labels:     map[string]string{composeProjectLabel: "demo"},
		Options:    map[string]string{"type": "none"},
		CreatedAt:  created.Format(time.RFC3339Nano),
		UsageData:  &volume.UsageData{Size: 42, RefCount: 1},
	}}
	api.volumeInspects["demo_data"] = *api.volumes[0]
	api.diskUsage.Volumes = api.volumes

	networkInspect := network.Inspect{
		ID:         "net1",
		Name:       "demo_default",
		Driver:     "bridge",
		Scope:      "local",
		Attachable: true,
		Labels:     map[string]string{composeProjectLabel: "demo"},
		IPAM: network.IPAM{Config: []network.IPAMConfig{{
			Subnet:  "172.22.0.0/16",
			Gateway: "172.22.0.1",
		}}},
		Containers: map[string]network.EndpointResource{
			fakeContainerID: {Name: "web"},
		},
	}
	api.networks = []network.Summary{networkInspect}
	api.networkInspects["net1"] = networkInspect
}

func providersDemoContainerFilter() models.ContainerListOptions {
	return models.ContainerListOptions{All: true, ProjectID: "demo", Service: "web"}
}

func envValue(values []models.EnvVar, name string) string {
	for _, value := range values {
		if value.Name == name {
			return value.Value
		}
	}
	return ""
}

func queryCount(t *testing.T, ctx context.Context, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("query %s count: %v", table, err)
	}
	return count
}

func queryString(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		t.Fatalf("query string: %v", err)
	}
	return value
}

func waitObjectsChanged(t *testing.T, ctx context.Context, events <-chan bus.Event, timeout time.Duration) ObjectsChangedPayload {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("context done waiting for objects:changed: %v", ctx.Err())
		case <-timer.C:
			t.Fatal("timed out waiting for objects:changed")
		case event, ok := <-events:
			if !ok {
				t.Fatal("event subscription closed")
			}
			payload, ok := event.Payload.(ObjectsChangedPayload)
			if ok {
				return payload
			}
		}
	}
}

func waitObjectsChangedKind(t *testing.T, ctx context.Context, events <-chan bus.Event, kind string, idPrefix string, timeout time.Duration) ObjectsChangedPayload {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("context done waiting for objects:changed %s: %v", kind, ctx.Err())
		case <-timer.C:
			t.Fatalf("timed out waiting for objects:changed %s", kind)
		case event, ok := <-events:
			if !ok {
				t.Fatal("event subscription closed")
			}
			payload, ok := event.Payload.(ObjectsChangedPayload)
			if !ok || payload.Kind != kind {
				continue
			}
			if idPrefix == "" {
				return payload
			}
			for _, id := range payload.IDs {
				if strings.HasPrefix(id, idPrefix) {
					return payload
				}
			}
		}
	}
}

func waitImageProgress(t *testing.T, ctx context.Context, events <-chan bus.Event, timeout time.Duration) ImageProgressPayload {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("context done waiting for image progress: %v", ctx.Err())
		case <-timer.C:
			t.Fatal("timed out waiting for image progress")
		case event, ok := <-events:
			if !ok {
				t.Fatal("image progress subscription closed")
			}
			payload, ok := event.Payload.(ImageProgressPayload)
			if ok {
				return payload
			}
		}
	}
}

func waitJobDone(t *testing.T, ctx context.Context, events <-chan bus.Event, timeout time.Duration) JobDonePayload {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("context done waiting for job done: %v", ctx.Err())
		case <-timer.C:
			t.Fatal("timed out waiting for job done")
		case event, ok := <-events:
			if !ok {
				t.Fatal("job done subscription closed")
			}
			payload, ok := event.Payload.(JobDonePayload)
			if ok {
				return payload
			}
		}
	}
}

func writeDockerfile(t *testing.T, path string) {
	t.Helper()
	content := "FROM scratch\nLABEL org.opencontainers.image.title=\"cairn object test\"\nCMD [\"/cairn-noop\"]\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
}

func writeSleeperImageContext(t *testing.T, dir string) {
	t.Helper()
	source := filepath.Join(dir, "sleeper.go")
	binary := filepath.Join(dir, "sleeper")
	if err := os.WriteFile(source, []byte(`package main

import "time"

func main() {
	time.Sleep(time.Hour)
}
`), 0o644); err != nil {
		t.Fatalf("write sleeper source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", binary, source)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build sleeper: %v: %s", err, strings.TrimSpace(string(output)))
	}
	dockerfile := "FROM scratch\nCOPY sleeper /sleeper\nEXPOSE 8080\nCMD [\"/sleeper\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write sleeper Dockerfile: %v", err)
	}
}

func runDockerCommand(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	output, err := dockerCommandOutput(ctx, args...)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return output
}

func dockerCommand(ctx context.Context, args ...string) error {
	_, err := dockerCommandOutput(ctx, args...)
	return err
}

func dockerCommandOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return text, fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}

func imageListContains(images []models.ImageSummary, ref string) bool {
	for _, image := range images {
		for _, tag := range image.RepoTags {
			if tag == ref {
				return true
			}
		}
	}
	return false
}

func volumeListContains(volumes []models.VolumeSummary, name string) bool {
	for _, volume := range volumes {
		if volume.Name == name {
			return true
		}
	}
	return false
}

func networkListContains(networks []models.NetworkSummary, name string) bool {
	for _, network := range networks {
		if network.Name == name {
			return true
		}
	}
	return false
}

func TestClientRealDockerObjectsIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real Docker object integration runs only on Linux")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := waitDockerCLI(ctx); err != nil {
		t.Fatalf("Docker daemon is not ready: %v", err)
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	labelKey := "cairn.test.objects"
	label := labelKey + "=" + suffix
	imageRef := "cairn-test-objects:" + suffix
	containerName := "cairn-test-objects-" + suffix
	volumeName := "cairn_test_objects_" + suffix
	networkName := "cairn_test_objects_" + suffix

	buildDir := t.TempDir()
	dockerfilePath := filepath.Join(buildDir, "Dockerfile")
	writeDockerfile(t, dockerfilePath)
	runDockerCommand(t, ctx, "build", "-q", "-t", imageRef, buildDir)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = dockerCommand(cleanupCtx, "rm", "-f", containerName)
		_ = dockerCommand(cleanupCtx, "network", "rm", networkName)
		_ = dockerCommand(cleanupCtx, "volume", "rm", "-f", volumeName)
		_ = dockerCommand(cleanupCtx, "rmi", "-f", imageRef)
	})

	runDockerCommand(t, ctx, "volume", "create", "--label", label, volumeName)
	runDockerCommand(t, ctx, "network", "create", "--label", label, networkName)

	dbPath := filepath.Join(t.TempDir(), "cairn.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	eventBus := bus.New()
	defer eventBus.Close()
	changed := eventBus.Subscribe(ctx, bus.TopicObjectsChanged, 8)
	provider := providers.NewLinuxNative(providers.LinuxNativeOptions{})
	client := New(provider, eventBus)
	client.SetObjectCache(db.Objects())
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	client.StartObjectEventLoop(ctx)

	runDockerCommand(t, ctx, "create",
		"--name", containerName,
		"--label", label,
		"--label", composeProjectLabel+"=cairn-test",
		"--label", composeServiceLabel+"=web",
		"--mount", "type=volume,source="+volumeName+",target=/data",
		"--network", networkName,
		imageRef,
	)
	createEvent := waitObjectsChangedKind(t, ctx, changed, objectKindContainer, "", time.Second)
	if len(createEvent.IDs) == 0 {
		t.Fatalf("container create event had no ids: %#v", createEvent)
	}

	containers, err := client.ListContainers(ctx, models.ContainerListOptions{
		All:     true,
		Filters: map[string]string{"label": label},
	})
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	cliContainerID := strings.TrimSpace(runDockerCommand(t, ctx, "ps", "-a", "--filter", "label="+label, "--format", "{{.ID}}"))
	if len(containers) != 1 || !strings.HasPrefix(containers[0].ID, cliContainerID) {
		t.Fatalf("containers = %#v, docker CLI id = %q", containers, cliContainerID)
	}
	if containers[0].ProjectID != "linux_native/cairn-test" || containers[0].Service != "web" {
		t.Fatalf("compose labels not mapped: %#v", containers[0])
	}

	if _, err := client.GetContainer(ctx, containers[0].ID); err != nil {
		t.Fatalf("GetContainer() error = %v", err)
	}
	images, err := client.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages() error = %v", err)
	}
	if !imageListContains(images, imageRef) {
		t.Fatalf("ListImages() missing %s in %#v", imageRef, images)
	}
	cliImage := strings.TrimSpace(runDockerCommand(t, ctx, "image", "ls", "--filter", "reference="+imageRef, "--format", "{{.Repository}}:{{.Tag}}"))
	if cliImage != imageRef {
		t.Fatalf("docker image ls = %q, want %q", cliImage, imageRef)
	}

	volumes, err := client.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
	if !volumeListContains(volumes, volumeName) {
		t.Fatalf("ListVolumes() missing %s in %#v", volumeName, volumes)
	}
	cliVolume := strings.TrimSpace(runDockerCommand(t, ctx, "volume", "ls", "--filter", "label="+label, "--format", "{{.Name}}"))
	if cliVolume != volumeName {
		t.Fatalf("docker volume ls = %q, want %q", cliVolume, volumeName)
	}

	networks, err := client.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("ListNetworks() error = %v", err)
	}
	if !networkListContains(networks, networkName) {
		t.Fatalf("ListNetworks() missing %s in %#v", networkName, networks)
	}
	cliNetwork := strings.TrimSpace(runDockerCommand(t, ctx, "network", "ls", "--filter", "label="+label, "--format", "{{.Name}}"))
	if cliNetwork != networkName {
		t.Fatalf("docker network ls = %q, want %q", cliNetwork, networkName)
	}

	if err := client.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sql: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	if got := queryCount(t, ctx, sqlDB, "containers_cache"); got == 0 {
		t.Fatalf("containers_cache was empty after reconcile")
	}

	runDockerCommand(t, ctx, "rm", "-f", containerName)
	removeEvent := waitObjectsChangedKind(t, ctx, changed, objectKindContainer, "", time.Second)
	if len(removeEvent.IDs) == 0 {
		t.Fatalf("container remove event had no ids: %#v", removeEvent)
	}
	containers, err = client.ListContainers(ctx, models.ContainerListOptions{
		All:     true,
		Filters: map[string]string{"label": label},
	})
	if err != nil {
		t.Fatalf("ListContainers() after remove error = %v", err)
	}
	if len(containers) != 0 {
		t.Fatalf("containers after external remove = %#v, want none", containers)
	}
}

func TestClientRealDockerCreateRunSaveLoadIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real Docker create/run integration runs only on Linux")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go CLI unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := waitDockerCLI(ctx); err != nil {
		t.Fatalf("Docker daemon is not ready: %v", err)
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	imageRef := "cairn-test-run:" + suffix
	containerName := "cairn-test-run-" + suffix
	portOwnerName := containerName + "-port-owner"
	portConflictName := containerName + "-port-conflict"
	renamedContainer := containerName + "-renamed"
	volumeName := "cairn_test_run_" + suffix
	networkName := "cairn_test_run_" + suffix
	archivePath := filepath.Join(t.TempDir(), "image.tar")

	buildDir := t.TempDir()
	writeSleeperImageContext(t, buildDir)
	runDockerCommand(t, ctx, "build", "-q", "-t", imageRef, buildDir)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = dockerCommand(cleanupCtx, "rm", "-f", containerName, renamedContainer, portOwnerName, portConflictName)
		_ = dockerCommand(cleanupCtx, "network", "rm", networkName)
		_ = dockerCommand(cleanupCtx, "volume", "rm", "-f", volumeName)
		_ = dockerCommand(cleanupCtx, "rmi", "-f", imageRef)
	})

	eventBus := bus.New()
	defer eventBus.Close()
	provider := providers.NewLinuxNative(providers.LinuxNativeOptions{})
	client := New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	volumeSummary, err := client.CreateVolume(ctx, models.CreateVolumeRequest{
		Name:   volumeName,
		Driver: "local",
		Labels: map[string]string{"cairn.test.run": suffix},
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if volumeSummary.Name != volumeName {
		t.Fatalf("volume summary = %#v", volumeSummary)
	}
	if _, err := client.GetVolume(ctx, volumeName); err != nil {
		t.Fatalf("GetVolume(created) error = %v", err)
	}

	networkSummary, err := client.CreateNetwork(ctx, models.CreateNetworkRequest{
		Name:       networkName,
		Driver:     "bridge",
		Attachable: true,
		Labels:     map[string]string{"cairn.test.run": suffix},
	})
	if err != nil {
		t.Fatalf("CreateNetwork() error = %v", err)
	}
	if networkSummary.Name != networkName {
		t.Fatalf("network summary = %#v", networkSummary)
	}
	if _, err := client.GetNetwork(ctx, networkName); err != nil {
		t.Fatalf("GetNetwork(created) error = %v", err)
	}

	portOwnerID, err := client.RunImage(ctx, models.RunImageRequest{
		ImageRef: imageRef,
		Name:     portOwnerName,
		Ports:    []models.PortMapping{{HostIP: "127.0.0.1", HostPort: "0", ContainerPort: "8080", Protocol: "tcp"}},
		Detach:   true,
	})
	if err != nil {
		t.Fatalf("RunImage(port owner) error = %v", err)
	}
	portOwnerDetail, err := client.GetContainer(ctx, portOwnerID)
	if err != nil {
		t.Fatalf("GetContainer(port owner) error = %v", err)
	}
	conflictPort := ""
	for _, binding := range portOwnerDetail.Summary.Ports {
		if binding.ContainerPort == "8080" && binding.Protocol == "tcp" && binding.HostPort != "" {
			conflictPort = binding.HostPort
			break
		}
	}
	if conflictPort == "" {
		t.Fatalf("port owner bindings = %#v, want published 8080/tcp", portOwnerDetail.Summary.Ports)
	}
	if _, err := client.RunImage(ctx, models.RunImageRequest{
		ImageRef: imageRef,
		Name:     portConflictName,
		Ports:    []models.PortMapping{{HostIP: "127.0.0.1", HostPort: conflictPort, ContainerPort: "8080", Protocol: "tcp"}},
		Detach:   true,
	}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("RunImage(port conflict) error = %v, want E_CONFLICT", err)
	}

	containerID, err := client.RunImage(ctx, models.RunImageRequest{
		ImageRef:  imageRef,
		Name:      containerName,
		Ports:     []models.PortMapping{{HostIP: "127.0.0.1", HostPort: "0", ContainerPort: "8080", Protocol: "tcp"}},
		Env:       []models.EnvVar{{Name: "MODE", Value: "integration"}},
		Volumes:   []models.MountSpec{{Type: "volume", VolumeName: volumeName, Target: "/data"}},
		NetworkID: networkName,
		Detach:    true,
	})
	if err != nil {
		t.Fatalf("RunImage() error = %v", err)
	}
	detail, err := client.GetContainer(ctx, containerID)
	if err != nil {
		t.Fatalf("GetContainer(run) error = %v", err)
	}
	if detail.Summary.Name != containerName || envValue(detail.Env, "MODE") != "integration" {
		t.Fatalf("container detail = %#v", detail)
	}
	if len(detail.Summary.Ports) == 0 || detail.Summary.Ports[0].ContainerPort != "8080" {
		t.Fatalf("container ports = %#v", detail.Summary.Ports)
	}
	if len(detail.Mounts) != 1 || detail.Mounts[0].Target != "/data" || detail.Mounts[0].VolumeName != volumeName {
		t.Fatalf("container mounts = %#v", detail.Mounts)
	}
	if len(detail.Networks) != 1 || detail.Networks[0] != networkName {
		t.Fatalf("container networks = %#v", detail.Networks)
	}

	if err := client.RenameContainer(ctx, containerID, renamedContainer); err != nil {
		t.Fatalf("RenameContainer() error = %v", err)
	}
	renamed, err := client.GetContainer(ctx, renamedContainer)
	if err != nil {
		t.Fatalf("GetContainer(renamed) error = %v", err)
	}
	if renamed.Summary.Name != renamedContainer {
		t.Fatalf("renamed container = %#v", renamed.Summary)
	}

	imageDetail, err := client.GetImage(ctx, imageRef)
	if err != nil {
		t.Fatalf("GetImage(before save) error = %v", err)
	}
	originalImageID := imageDetail.Summary.ID
	if _, err := client.SaveImage(ctx, []string{imageRef}, archivePath); err != nil {
		t.Fatalf("SaveImage() error = %v", err)
	}
	if stat, err := os.Stat(archivePath); err != nil || stat.Size() == 0 {
		t.Fatalf("archive stat = %#v err=%v", stat, err)
	}

	runDockerCommand(t, ctx, "rm", "-f", renamedContainer)
	runDockerCommand(t, ctx, "rmi", "-f", imageRef)
	if _, err := client.LoadImage(ctx, archivePath); err != nil {
		t.Fatalf("LoadImage() error = %v", err)
	}
	loadedDetail, err := client.GetImage(ctx, imageRef)
	if err != nil {
		t.Fatalf("GetImage(after load) error = %v", err)
	}
	if loadedDetail.Summary.ID != originalImageID {
		t.Fatalf("loaded image ID = %q, want %q", loadedDetail.Summary.ID, originalImageID)
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
	if err := waitDockerCLIDown(ctx, 10*time.Second); err != nil {
		t.Fatalf("Docker daemon remained reachable after stop: %v", err)
	}
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

func (fakeDockerProvider) ID() string {
	return "linux_native"
}

func (fakeDockerProvider) DockerHost(context.Context) (string, error) {
	return "unix:///var/run/docker.sock", nil
}

func (fakeDockerProvider) DockerContext(context.Context) (string, error) {
	return "default", nil
}

type fakeDialerProvider struct {
	host   string
	dialer func(context.Context, string, string) (net.Conn, error)
}

func (p fakeDialerProvider) ID() string {
	return "windows_wsl_ubuntu"
}

func (p fakeDialerProvider) DockerHost(context.Context) (string, error) {
	return p.host, nil
}

func (p fakeDialerProvider) DockerContext(context.Context) (string, error) {
	return "default", nil
}

func (p fakeDialerProvider) DockerDialContext(context.Context) (func(context.Context, string, string) (net.Conn, error), error) {
	return p.dialer, nil
}

type fakeAPI struct {
	mu                sync.Mutex
	ping              dockertypes.Ping
	pingErr           error
	info              system.Info
	version           dockertypes.Version
	diskUsage         dockertypes.DiskUsage
	containers        []container.Summary
	containerInspects map[string]container.InspectResponse
	containerRaw      map[string][]byte
	images            []image.Summary
	imageInspects     map[string]image.InspectResponse
	imageRaw          map[string][]byte
	volumes           []*volume.Volume
	volumeInspects    map[string]volume.Volume
	volumeRaw         map[string][]byte
	networks          []network.Summary
	networkInspects   map[string]network.Inspect
	networkRaw        map[string][]byte
	events            chan events.Message
	eventErrs         chan error
	stats             map[string][]container.StatsResponse
	statsCalls        []statsCall
	execCreates       []execCreateCall
	execResizes       []execResizeCall
	execInspects      map[string]container.ExecInspect
	execOutputs       map[string]string
	execExitCodes     map[string]int
	executablePaths   map[string]bool
	started           []string
	stopped           []string
	restarted         []string
	killed            []string
	removed           []string
	unpaused          []string
	createdContainers []createdContainerCall
	renamed           []string
	pulled            []string
	tagged            []string
	pushed            []string
	pushAuth          []string
	pushBody          string
	pushErr           error
	saved             [][]string
	loadedBytes       []int
	searches          []string
	removedImages     []string
	pruned            []string
	createdVolumes    []volume.CreateOptions
	removedVolumes    []string
	createdNetworks   []networkCreateCall
	removedNetworks   []string
	closed            bool
}

type createdContainerCall struct {
	Name             string
	Config           *container.Config
	HostConfig       *container.HostConfig
	NetworkingConfig *network.NetworkingConfig
}

type networkCreateCall struct {
	Name    string
	Options network.CreateOptions
}

type statsCall struct {
	ID      string
	Stream  bool
	OneShot bool
}

type execCreateCall struct {
	ID          string
	ContainerID string
	Options     container.ExecOptions
}

type execResizeCall struct {
	ExecID  string
	Options container.ResizeOptions
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		ping:              dockertypes.Ping{APIVersion: "1.51"},
		containerInspects: map[string]container.InspectResponse{},
		containerRaw:      map[string][]byte{},
		imageInspects:     map[string]image.InspectResponse{},
		imageRaw:          map[string][]byte{},
		volumeInspects:    map[string]volume.Volume{},
		volumeRaw:         map[string][]byte{},
		networkInspects:   map[string]network.Inspect{},
		networkRaw:        map[string][]byte{},
		events:            make(chan events.Message, 16),
		eventErrs:         make(chan error, 4),
		stats:             map[string][]container.StatsResponse{},
		execInspects:      map[string]container.ExecInspect{},
		execOutputs:       map[string]string{},
		execExitCodes:     map[string]int{},
		executablePaths:   map[string]bool{},
	}
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

func (a *fakeAPI) ContainerList(context.Context, container.ListOptions) ([]container.Summary, error) {
	return append([]container.Summary(nil), a.containers...), nil
}

func (a *fakeAPI) ContainerInspectWithRaw(_ context.Context, id string, _ bool) (container.InspectResponse, []byte, error) {
	for key, inspect := range a.containerInspects {
		if key == id || strings.HasPrefix(key, id) {
			return inspect, rawOrMarshal(a.containerRaw[key], inspect), nil
		}
	}
	return container.InspectResponse{}, nil, cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("no such container: %s", id))
}

func (a *fakeAPI) ContainerStart(_ context.Context, id string, _ container.StartOptions) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = append(a.started, id)
	return nil
}

func (a *fakeAPI) ContainerStop(_ context.Context, id string, _ container.StopOptions) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopped = append(a.stopped, id)
	return nil
}

func (a *fakeAPI) ContainerRestart(_ context.Context, id string, _ container.StopOptions) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.restarted = append(a.restarted, id)
	return nil
}

func (a *fakeAPI) ContainerKill(_ context.Context, id string, signal string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.killed = append(a.killed, id+":"+signal)
	return nil
}

func (a *fakeAPI) ContainerRemove(_ context.Context, id string, _ container.RemoveOptions) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removed = append(a.removed, id)
	return nil
}

func (a *fakeAPI) ContainerUnpause(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unpaused = append(a.unpaused, id)
	return nil
}

func (a *fakeAPI) ContainerLogs(context.Context, string, container.LogsOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (a *fakeAPI) ContainerStats(_ context.Context, id string, stream bool) (container.StatsResponseReader, error) {
	a.mu.Lock()
	a.statsCalls = append(a.statsCalls, statsCall{ID: id, Stream: stream})
	entries := append([]container.StatsResponse(nil), a.stats[id]...)
	a.mu.Unlock()
	return container.StatsResponseReader{Body: statsReader(entries), OSType: "linux"}, nil
}

func (a *fakeAPI) ContainerStatsOneShot(_ context.Context, id string) (container.StatsResponseReader, error) {
	a.mu.Lock()
	a.statsCalls = append(a.statsCalls, statsCall{ID: id, OneShot: true})
	entries := append([]container.StatsResponse(nil), a.stats[id]...)
	a.mu.Unlock()
	if len(entries) > 1 {
		entries = entries[len(entries)-1:]
	}
	return container.StatsResponseReader{Body: statsReader(entries), OSType: "linux"}, nil
}

func (a *fakeAPI) ContainerExecCreate(_ context.Context, containerID string, opts container.ExecOptions) (container.ExecCreateResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := fmt.Sprintf("exec-%d", len(a.execCreates)+1)
	exitCode := a.execExitCodeLocked(opts.Cmd)
	a.execCreates = append(a.execCreates, execCreateCall{ID: id, ContainerID: containerID, Options: opts})
	a.execInspects[id] = container.ExecInspect{
		ExecID:      id,
		ContainerID: containerID,
		Running:     false,
		ExitCode:    exitCode,
		Pid:         1234,
	}
	return container.ExecCreateResponse{ID: id}, nil
}

func (a *fakeAPI) ContainerExecAttach(_ context.Context, execID string, opts container.ExecAttachOptions) (dockertypes.HijackedResponse, error) {
	a.mu.Lock()
	var output string
	for _, call := range a.execCreates {
		if call.ID == execID {
			output = a.execOutputLocked(call.Options.Cmd)
			break
		}
	}
	a.mu.Unlock()

	clientConn, serverConn := net.Pipe()
	go func() {
		defer func() {
			_ = serverConn.Close()
		}()
		if output == "" {
			return
		}
		if opts.Tty {
			_, _ = serverConn.Write([]byte(output))
			return
		}
		writer := stdcopy.NewStdWriter(serverConn, stdcopy.Stdout)
		_, _ = writer.Write([]byte(output))
	}()
	return dockertypes.NewHijackedResponse(clientConn, ""), nil
}

func (a *fakeAPI) ContainerExecResize(_ context.Context, execID string, opts container.ResizeOptions) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.execResizes = append(a.execResizes, execResizeCall{ExecID: execID, Options: opts})
	return nil
}

func (a *fakeAPI) ContainerExecInspect(_ context.Context, execID string) (container.ExecInspect, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	inspect, ok := a.execInspects[execID]
	if !ok {
		return container.ExecInspect{}, cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("no such exec: %s", execID))
	}
	return inspect, nil
}

func (a *fakeAPI) execExitCodeLocked(cmd []string) int {
	if len(cmd) == 3 && cmd[1] == "-c" && cmd[2] == "exit 0" {
		if a.executablePaths[cmd[0]] {
			return 0
		}
		return 127
	}
	if len(cmd) == 3 && cmd[0] == "test" && cmd[1] == "-x" {
		if a.executablePaths[cmd[2]] {
			return 0
		}
		return 1
	}
	if code, ok := a.execExitCodes[commandKey(cmd)]; ok {
		return code
	}
	return 0
}

func (a *fakeAPI) execOutputLocked(cmd []string) string {
	return a.execOutputs[commandKey(cmd)]
}

func commandKey(cmd []string) string {
	return strings.Join(cmd, "\x00")
}

func (a *fakeAPI) ContainerCreate(_ context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, _ *ocispec.Platform, name string) (container.CreateResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := "created-" + name
	if name == "" {
		id = "created-container"
	}
	a.createdContainers = append(a.createdContainers, createdContainerCall{
		Name:             name,
		Config:           config,
		HostConfig:       hostConfig,
		NetworkingConfig: networkingConfig,
	})
	a.containerInspects[id] = container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:      id,
			Name:    "/" + name,
			Image:   "sha256:image1",
			Created: time.Now().UTC().Format(time.RFC3339Nano),
			State:   &container.State{Status: "created"},
		},
		Config: config,
	}
	a.containers = append(a.containers, container.Summary{
		ID:      id,
		Names:   []string{"/" + name},
		Image:   config.Image,
		ImageID: "sha256:image1",
		State:   "created",
	})
	return container.CreateResponse{ID: id}, nil
}

func statsReader(entries []container.StatsResponse) io.ReadCloser {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, entry := range entries {
		_ = enc.Encode(entry)
	}
	return io.NopCloser(bytes.NewReader(buf.Bytes()))
}

func (a *fakeAPI) ContainerRename(_ context.Context, id string, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.renamed = append(a.renamed, id+":"+name)
	return nil
}

func (a *fakeAPI) ImageList(context.Context, image.ListOptions) ([]image.Summary, error) {
	return append([]image.Summary(nil), a.images...), nil
}

func (a *fakeAPI) ImageInspectWithRaw(_ context.Context, id string) (image.InspectResponse, []byte, error) {
	for key, inspect := range a.imageInspects {
		if key == id || strings.HasPrefix(key, id) {
			return inspect, rawOrMarshal(a.imageRaw[key], inspect), nil
		}
	}
	return image.InspectResponse{}, nil, cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("no such image: %s", id))
}

func (a *fakeAPI) ImagePull(_ context.Context, ref string, _ image.PullOptions) (io.ReadCloser, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pulled = append(a.pulled, ref)
	a.imageInspects[ref] = image.InspectResponse{
		ID:           "sha256:pulled",
		RepoTags:     []string{ref},
		Created:      time.Now().UTC().Format(time.RFC3339Nano),
		Architecture: "amd64",
		Os:           "linux",
	}
	return io.NopCloser(strings.NewReader(`{"status":"pulling","id":"layer","progressDetail":{"current":1,"total":2}}` + "\n" + `{"status":"done"}` + "\n")), nil
}

func (a *fakeAPI) ImageTag(_ context.Context, imageID string, ref string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tagged = append(a.tagged, imageID+"->"+ref)
	a.imageInspects[ref] = image.InspectResponse{
		ID:           imageID,
		RepoTags:     []string{ref},
		Created:      time.Now().UTC().Format(time.RFC3339Nano),
		Architecture: "amd64",
		Os:           "linux",
	}
	return nil
}

func (a *fakeAPI) ImagePush(_ context.Context, ref string, opts image.PushOptions) (io.ReadCloser, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pushed = append(a.pushed, ref)
	a.pushAuth = append(a.pushAuth, opts.RegistryAuth)
	if a.pushErr != nil {
		return nil, a.pushErr
	}
	body := a.pushBody
	if body == "" {
		body = `{"status":"pushing","id":"layer","progressDetail":{"current":1,"total":2}}` + "\n" + `{"status":"done"}` + "\n"
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func (a *fakeAPI) ImageSave(_ context.Context, imageIDs []string, _ ...dockerclient.ImageSaveOption) (io.ReadCloser, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.saved = append(a.saved, append([]string(nil), imageIDs...))
	return io.NopCloser(bytes.NewReader([]byte("fake image tar"))), nil
}

func (a *fakeAPI) ImageLoad(_ context.Context, input io.Reader, _ ...dockerclient.ImageLoadOption) (image.LoadResponse, error) {
	body, err := io.ReadAll(input)
	if err != nil {
		return image.LoadResponse{}, err
	}
	a.mu.Lock()
	a.loadedBytes = append(a.loadedBytes, len(body))
	a.imageInspects["loaded:latest"] = image.InspectResponse{
		ID:       "sha256:loaded",
		RepoTags: []string{"loaded:latest"},
		Created:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	a.mu.Unlock()
	return image.LoadResponse{Body: io.NopCloser(strings.NewReader(`{"stream":"Loaded image: loaded:latest"}`))}, nil
}

func (a *fakeAPI) ImageSearch(_ context.Context, term string, _ registry.SearchOptions) ([]registry.SearchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.searches = append(a.searches, term)
	return []registry.SearchResult{{
		Name:        "library/" + term,
		Description: "test result",
		StarCount:   42,
		IsOfficial:  true,
	}}, nil
}

func (a *fakeAPI) ImageRemove(_ context.Context, id string, _ image.RemoveOptions) ([]image.DeleteResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removedImages = append(a.removedImages, id)
	return []image.DeleteResponse{{Deleted: id}}, nil
}

func (a *fakeAPI) ImagesPrune(context.Context, filters.Args) (image.PruneReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruned = append(a.pruned, "images")
	return image.PruneReport{}, nil
}

func (a *fakeAPI) ContainersPrune(context.Context, filters.Args) (container.PruneReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruned = append(a.pruned, "containers")
	return container.PruneReport{}, nil
}

func (a *fakeAPI) BuildCachePrune(context.Context, build.CachePruneOptions) (*build.CachePruneReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruned = append(a.pruned, "build-cache")
	return &build.CachePruneReport{}, nil
}

func (a *fakeAPI) VolumeList(context.Context, volume.ListOptions) (volume.ListResponse, error) {
	return volume.ListResponse{Volumes: append([]*volume.Volume(nil), a.volumes...)}, nil
}

func (a *fakeAPI) VolumeInspectWithRaw(_ context.Context, name string) (volume.Volume, []byte, error) {
	for key, inspect := range a.volumeInspects {
		if key == name {
			return inspect, rawOrMarshal(a.volumeRaw[key], inspect), nil
		}
	}
	return volume.Volume{}, nil, cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("no such volume: %s", name))
}

func (a *fakeAPI) VolumeCreate(_ context.Context, options volume.CreateOptions) (volume.Volume, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.createdVolumes = append(a.createdVolumes, options)
	created := volume.Volume{
		Name:       options.Name,
		Driver:     options.Driver,
		Mountpoint: "/var/lib/docker/volumes/" + options.Name + "/_data",
		Labels:     options.Labels,
		Options:    options.DriverOpts,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	a.volumes = append(a.volumes, &created)
	a.volumeInspects[options.Name] = created
	return created, nil
}

func (a *fakeAPI) VolumeRemove(_ context.Context, name string, _ bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removedVolumes = append(a.removedVolumes, name)
	return nil
}

func (a *fakeAPI) VolumesPrune(context.Context, filters.Args) (volume.PruneReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruned = append(a.pruned, "volumes")
	return volume.PruneReport{}, nil
}

func (a *fakeAPI) NetworkList(context.Context, network.ListOptions) ([]network.Summary, error) {
	return append([]network.Summary(nil), a.networks...), nil
}

func (a *fakeAPI) NetworkInspectWithRaw(_ context.Context, id string, _ network.InspectOptions) (network.Inspect, []byte, error) {
	for key, inspect := range a.networkInspects {
		if key == id || strings.HasPrefix(key, id) || inspect.Name == id {
			return inspect, rawOrMarshal(a.networkRaw[key], inspect), nil
		}
	}
	return network.Inspect{}, nil, cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("no such network: %s", id))
}

func (a *fakeAPI) NetworkCreate(_ context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := "net-" + name
	a.createdNetworks = append(a.createdNetworks, networkCreateCall{Name: name, Options: options})
	created := network.Inspect{
		ID:         id,
		Name:       name,
		Driver:     options.Driver,
		Scope:      "local",
		Internal:   options.Internal,
		Attachable: options.Attachable,
		Labels:     options.Labels,
	}
	if options.IPAM != nil {
		created.IPAM = *options.IPAM
	}
	a.networks = append(a.networks, created)
	a.networkInspects[id] = created
	return network.CreateResponse{ID: id}, nil
}

func (a *fakeAPI) NetworkRemove(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removedNetworks = append(a.removedNetworks, id)
	return nil
}

func (a *fakeAPI) NetworksPrune(context.Context, filters.Args) (network.PruneReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruned = append(a.pruned, "networks")
	return network.PruneReport{}, nil
}

func (a *fakeAPI) Events(context.Context, events.ListOptions) (<-chan events.Message, <-chan error) {
	return a.events, a.eventErrs
}

func (a *fakeAPI) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return nil
}

func rawOrMarshal[T any](raw []byte, value T) []byte {
	if len(raw) > 0 {
		return raw
	}
	encoded, _ := json.Marshal(value)
	return encoded
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

func waitDockerCLIDown(ctx context.Context, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastOutput string
	for {
		infoCtx, infoCancel := context.WithTimeout(waitCtx, 2*time.Second)
		cmd := exec.CommandContext(infoCtx, "docker", "info")
		output, err := cmd.CombinedOutput()
		infoCancel()
		lastOutput = strings.TrimSpace(string(output))
		if err != nil {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("%w; last docker info output: %s", waitCtx.Err(), lastOutput)
		case <-ticker.C:
		}
	}
}

func controlDockerService(ctx context.Context, action string) error {
	commands := dockerControlCommands(action)
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

func dockerControlCommands(action string) [][]string {
	if action == "stop" {
		return [][]string{
			{"sudo", "systemctl", "stop", "docker.socket", "docker.service"},
			{"sudo", "systemctl", "stop", "docker.service"},
			{"sudo", "service", "docker", "stop"},
		}
	}
	if action == "start" {
		return [][]string{
			{"sudo", "systemctl", "start", "docker.socket", "docker.service"},
			{"sudo", "systemctl", "start", "docker.service"},
			{"sudo", "service", "docker", "start"},
		}
	}
	return [][]string{{"sudo", "systemctl", action, "docker.service"}}
}
