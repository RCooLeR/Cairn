package docker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
	cerrdefs "github.com/containerd/errdefs"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/api/types/volume"
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

func writeDockerfile(t *testing.T, path string) {
	t.Helper()
	content := "FROM scratch\nLABEL org.opencontainers.image.title=\"cairn object test\"\nCMD [\"/cairn-noop\"]\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
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
	if containers[0].ProjectID != "cairn-test" || containers[0].Service != "web" {
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
	closed            bool
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
