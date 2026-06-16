//go:build windows && wslintegration

package updates

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestManagerRealWSLUpdateAndRebuildSmoke(t *testing.T) {
	if os.Getenv("CAIRN_REAL_WSL_DOCKER_UPDATES") != "1" {
		t.Skip("set CAIRN_REAL_WSL_DOCKER_UPDATES=1 to run against the local cairn-dev WSL distro")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	provider := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: "cairn-dev"})
	status, err := provider.Detect(ctx)
	if err != nil {
		t.Fatalf("provider Detect() error = %v", err)
	}
	if !status.Healthy {
		t.Fatalf("cairn-dev WSL provider is not healthy: %#v", status.Problems)
	}
	if status.DockerHost != "wsl+stdio://cairn-dev" {
		t.Fatalf("DockerHost marker = %q, want wsl+stdio://cairn-dev", status.DockerHost)
	}

	eventBus := bus.New()
	defer eventBus.Close()
	client := dockercore.New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Docker Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	compose := composecore.NewClient(provider)

	suffix := strings.ToLower(time.Now().UTC().Format("20060102150405"))
	projectName := "cairn-wsl-updates-" + suffix
	projectID := composecore.ProjectID(provider.ID(), projectName)
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	scratchRoot := filepath.Join(repoRoot, "..", "..", ".scratch", "wsl-updates")
	if err := os.MkdirAll(scratchRoot, 0o755); err != nil {
		t.Fatalf("create scratch root: %v", err)
	}
	workdir, err := os.MkdirTemp(scratchRoot, projectName+"-")
	if err != nil {
		t.Fatalf("create WSL update workdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(workdir)
	})
	appRef := ""
	baseRef := ""
	builderRef := ""
	registryName := "cairn-wsl-updates-registry-" + suffix

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		opts := composecore.ProjectOptions{Workdir: workdir, Files: []string{"compose.yaml"}, ProjectName: projectName}
		_, _ = compose.Down(cleanupCtx, opts, true)
		_, _ = provider.RunDocker(cleanupCtx, "rm", "-f", registryName)
		refs := []string{appRef, baseRef, builderRef, "cairn-wsl-app-old:" + suffix, "cairn-wsl-app-new:" + suffix, "cairn-wsl-base-old:" + suffix, "cairn-wsl-base-new:" + suffix}
		for _, ref := range refs {
			if strings.TrimSpace(ref) != "" {
				_, _ = provider.RunDocker(cleanupCtx, "image", "rm", "-f", ref)
			}
		}
	})

	ensureWSLImage(t, ctx, provider, "registry:2")
	ensureWSLImage(t, ctx, provider, "alpine:3")
	startWSLRegistry(t, ctx, provider, registryName)
	registryHost := waitForWSLRegistry(t, ctx, provider, registryName)
	appRef = registryHost + "/cairn/wsl-app:latest"
	baseRef = registryHost + "/cairn/wsl-base:latest"
	builderRef = registryHost + "/cairn/wsl-builder:latest"

	buildAndPushWSLImage(t, ctx, provider, workdir, "cairn-wsl-app-old:"+suffix, appRef, "old-service")
	buildAndPushWSLImage(t, ctx, provider, workdir, "cairn-wsl-base-old:"+suffix, baseRef, "old-base")
	writeWSLUpdateComposeProject(t, workdir, appRef, baseRef, builderRef)

	opts := composecore.ProjectOptions{Workdir: workdir, Files: []string{"compose.yaml"}, ProjectName: projectName}
	if result, err := compose.PullServices(ctx, opts, []string{"web"}); err != nil || result.ExitCode != 0 {
		t.Fatalf("initial compose pull web: result=%#v err=%v", result, err)
	}
	if result, err := compose.Build(ctx, opts, composecore.BuildOptions{Pull: true, Services: []string{"builder"}}); err != nil || result.ExitCode != 0 {
		t.Fatalf("initial compose build builder: result=%#v err=%v", result, err)
	}
	if result, err := compose.UpServices(ctx, opts, composecore.UpOptions{Services: []string{"web", "builder"}}); err != nil || result.ExitCode != 0 {
		t.Fatalf("initial compose up: result=%#v err=%v", result, err)
	}

	oldWebImageID := waitServiceImageID(t, ctx, client, projectID, "web")
	oldBuilderImageID := waitServiceImageID(t, ctx, client, projectID, "builder")

	buildAndPushWSLImage(t, ctx, provider, workdir, "cairn-wsl-app-new:"+suffix, appRef, "new-service")
	buildAndPushWSLImage(t, ctx, provider, workdir, "cairn-wsl-base-new:"+suffix, baseRef, "new-base")

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	seedWSLUpdateProject(t, ctx, db, provider.ID(), projectID, projectName, workdir, appRef, builderRef)
	insertWSLUpdateCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        provider.ID(),
		ProjectID:         projectID,
		ServiceID:         composecore.ServiceID(projectID, "web"),
		Kind:              models.UpdateKindServiceImage,
		ImageRef:          appRef,
		LocalImageID:      oldWebImageID,
		LocalDigest:       "sha256:oldservice",
		RemoteDigest:      "sha256:newservice",
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionPullRecreate,
		Status:            models.UpdateStatusServiceImageUpdateAvailable,
	})
	insertWSLUpdateCheck(t, ctx, db, store.UpdateCheckRecord{
		ProviderID:        provider.ID(),
		ProjectID:         projectID,
		ServiceID:         composecore.ServiceID(projectID, "builder"),
		Kind:              models.UpdateKindBaseImage,
		ImageRef:          builderRef,
		BaseImageRef:      baseRef,
		LocalImageID:      oldBuilderImageID,
		LocalDigest:       "sha256:oldbase",
		RemoteDigest:      "sha256:newbase",
		Confidence:        models.ConfidenceHigh,
		RecommendedAction: models.RecommendedActionRebuildRedeploy,
		Status:            models.UpdateStatusRebuildRequired,
	})
	currentChecks, err := db.Updates().ListCurrent(ctx, models.UpdateFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListCurrent() error = %v", err)
	}
	currentByService := map[string]store.UpdateCheckRecord{}
	for _, check := range currentChecks {
		currentByService[serviceNameFromID(check.ServiceID, check.ProjectID)] = check
	}
	if currentByService["web"].LocalImageID != oldWebImageID {
		t.Fatalf("web check local image = %q, want %q", currentByService["web"].LocalImageID, oldWebImageID)
	}
	if currentByService["builder"].LocalImageID != oldBuilderImageID {
		t.Fatalf("builder check local image = %q, want %q", currentByService["builder"].LocalImageID, oldBuilderImageID)
	}

	manager := NewManager(db.Projects(), db.Lineage(), db.Updates(), db.Objects(), client, staticRegistry{}, db.Settings(), eventBus, nil)
	manager.Compose = compose
	manager.HealthWindow = 15 * time.Second
	manager.HealthPollInterval = 500 * time.Millisecond
	done := eventBus.Subscribe(ctx, bus.TopicJobDone, 4)

	plan, err := manager.PlanProjectUpdate(ctx, projectID)
	if err != nil {
		t.Fatalf("PlanProjectUpdate() error = %v", err)
	}
	gotCommands := commandTexts(plan.Commands)
	wantCommands := []string{
		"docker compose -f compose.yaml pull web",
		"docker compose -f compose.yaml build --pull builder",
		"docker compose -f compose.yaml up -d web builder",
	}
	if strings.Join(gotCommands, "\n") != strings.Join(wantCommands, "\n") {
		t.Fatalf("commands = %#v, want %#v", gotCommands, wantCommands)
	}

	jobID, err := manager.ApplyUpdate(ctx, models.ApplyUpdateRequest{PlanID: plan.PlanID, WatchHealth: true})
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}
	if jobID == "" {
		t.Fatal("ApplyUpdate() jobID is empty")
	}
	waitWSLUpdateDone(t, done, updateResultSuccessWarn)

	newWebImageID := waitServiceImageID(t, ctx, client, projectID, "web")
	newBuilderImageID := waitServiceImageID(t, ctx, client, projectID, "builder")
	if newWebImageID == "" || newWebImageID == oldWebImageID {
		t.Fatalf("web image did not change: old=%s new=%s", oldWebImageID, newWebImageID)
	}
	if newBuilderImageID == "" || newBuilderImageID == oldBuilderImageID {
		t.Fatalf("builder image did not change: old=%s new=%s", oldBuilderImageID, newBuilderImageID)
	}

	history, err := manager.ListUpdateHistory(ctx, models.UpdateHistoryFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListUpdateHistory() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %#v, want 2 rows", history)
	}
	records, err := db.Updates().ListHistory(ctx, models.UpdateHistoryFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListHistory() error = %v", err)
	}
	byService := map[string]store.UpdateHistoryRecord{}
	for _, item := range records {
		full, err := db.Updates().GetHistory(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetHistory(%d) error = %v", item.ID, err)
		}
		byService[serviceNameFromID(full.ServiceID, full.ProjectID)] = full
	}
	assertWSLUpdateHistory(t, byService["web"], models.UpdateKindServiceImage, updateResultSuccessWarn, oldWebImageID, newWebImageID)
	assertWSLUpdateHistory(t, byService["builder"], models.UpdateKindBaseImage, updateResultSuccessWarn, oldBuilderImageID, newBuilderImageID)
}

type staticRegistry struct{}

func (staticRegistry) ResolveDigest(context.Context, string, registrycore.ResolveOptions) (*registrycore.DigestResult, error) {
	return nil, apperror.New(apperror.NotFound, "not used by the WSL update smoke")
}

func ensureWSLImage(t *testing.T, ctx context.Context, provider providers.PlatformProvider, imageRef string) {
	t.Helper()
	if result, err := provider.RunDocker(ctx, "image", "inspect", imageRef); err == nil && result.ExitCode == 0 {
		return
	}
	if result, err := provider.RunDocker(ctx, "pull", imageRef); err != nil || result.ExitCode != 0 {
		t.Fatalf("pull %s: result=%#v err=%v", imageRef, result, err)
	}
}

func startWSLRegistry(t *testing.T, ctx context.Context, provider providers.PlatformProvider, name string) {
	t.Helper()
	result, err := provider.RunDocker(ctx,
		"run", "-d", "--rm", "--name", name,
		"--label", "cairn.test=wsl-update-smoke",
		"-p", "127.0.0.1::5000",
		"registry:2",
	)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("start registry: result=%#v err=%v", result, err)
	}
}

func waitForWSLRegistry(t *testing.T, ctx context.Context, provider providers.PlatformProvider, name string) string {
	t.Helper()
	var registryHost string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		result, err := provider.RunDocker(ctx, "port", name, "5000/tcp")
		if err == nil && result.ExitCode == 0 {
			registryHost = normalizeWSLPort(result.Stdout)
			if registryHost != "" {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+registryHost+"/v2/", nil)
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						return registryHost
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("registry %s did not become reachable at %s", name, registryHost)
	return ""
}

func normalizeWSLPort(stdout string) string {
	fields := strings.Fields(strings.TrimSpace(stdout))
	if len(fields) == 0 {
		return ""
	}
	host := fields[len(fields)-1]
	if strings.HasPrefix(host, "0.0.0.0") {
		host = strings.Replace(host, "0.0.0.0", "127.0.0.1", 1)
	}
	return host
}

func buildAndPushWSLImage(t *testing.T, ctx context.Context, provider providers.PlatformProvider, root string, localRef string, remoteRef string, marker string) {
	t.Helper()
	dir := filepath.Join(root, strings.ReplaceAll(localRef, ":", "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	dockerfile := fmt.Sprintf("FROM alpine:3\nLABEL cairn.marker=%q\nCMD [\"sh\", \"-c\", \"sleep 300\"]\n", marker)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	backendDir, err := provider.MapPathToBackend(dir)
	if err != nil {
		t.Fatalf("map image dir: %v", err)
	}
	if result, err := provider.RunDocker(ctx, "build", "-t", localRef, backendDir); err != nil || result.ExitCode != 0 {
		t.Fatalf("build %s: result=%#v err=%v", localRef, result, err)
	}
	if result, err := provider.RunDocker(ctx, "tag", localRef, remoteRef); err != nil || result.ExitCode != 0 {
		t.Fatalf("tag %s -> %s: result=%#v err=%v", localRef, remoteRef, result, err)
	}
	if result, err := provider.RunDocker(ctx, "push", remoteRef); err != nil || result.ExitCode != 0 {
		t.Fatalf("push %s: result=%#v err=%v", remoteRef, result, err)
	}
}

func writeWSLUpdateComposeProject(t *testing.T, root string, appRef string, baseRef string, builderRef string) {
	t.Helper()
	composeYAML := fmt.Sprintf(`services:
  web:
    image: %s
    command: ["sh", "-c", "sleep 300"]
  builder:
    image: %s
    build:
      context: .
      dockerfile: Dockerfile.builder
    command: ["sh", "-c", "sleep 300"]
`, appRef, builderRef)
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(composeYAML), 0o644); err != nil {
		t.Fatalf("write compose.yaml: %v", err)
	}
	dockerfile := fmt.Sprintf("FROM %s\nLABEL cairn.marker=\"builder\"\nCMD [\"sh\", \"-c\", \"sleep 300\"]\n", baseRef)
	if err := os.WriteFile(filepath.Join(root, "Dockerfile.builder"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile.builder: %v", err)
	}
}

func seedWSLUpdateProject(t *testing.T, ctx context.Context, db *store.Store, providerID string, projectID string, projectName string, workdir string, appRef string, builderRef string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
		ID:          providerID,
		Type:        providers.TypeWindowsWSL,
		Platform:    providers.PlatformWindows,
		DisplayName: "Windows WSL Ubuntu",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	services := []store.ServiceRecord{
		{
			ID:         composecore.ServiceID(projectID, "web"),
			ProjectID:  projectID,
			Name:       "web",
			ImageRef:   appRef,
			LastSeenAt: now,
		},
		{
			ID:             composecore.ServiceID(projectID, "builder"),
			ProjectID:      projectID,
			Name:           "builder",
			ImageRef:       builderRef,
			BuildContext:   ".",
			DockerfilePath: "Dockerfile.builder",
			LastSeenAt:     now,
		},
	}
	if err := db.Projects().SaveSnapshot(ctx, providerID, []store.ProjectRecord{{
		ID:           projectID,
		ProviderID:   providerID,
		Name:         projectName,
		WorkingDir:   workdir,
		ComposeFiles: []string{"compose.yaml"},
		Source:       store.ProjectSourceImported,
		LastSeenAt:   now,
	}}, services, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
}

func insertWSLUpdateCheck(t *testing.T, ctx context.Context, db *store.Store, record store.UpdateCheckRecord) {
	t.Helper()
	record.CheckedAt = time.Now().UTC()
	if _, err := db.Updates().InsertCheck(ctx, record); err != nil {
		t.Fatalf("InsertCheck() error = %v", err)
	}
}

func waitServiceImageID(t *testing.T, ctx context.Context, client *dockercore.Client, projectID string, service string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		containers, err := client.ListContainers(ctx, models.ContainerListOptions{All: true, ProjectID: projectID, Service: service})
		if err == nil {
			for _, container := range containers {
				if container.ProjectID == projectID && container.Service == service && container.ImageID != "" && container.State == "running" {
					return container.ImageID
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("service %s did not reach running state in project %s", service, projectID)
	return ""
}

func waitWSLUpdateDone(t *testing.T, ch <-chan bus.Event, want string) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case event := <-ch:
			if event.Topic != bus.TopicJobDone {
				continue
			}
			if payload, ok := event.Payload.(jobDonePayload); ok {
				if payload.Result != want {
					t.Fatalf("job result = %#v, want %s", payload, want)
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for update result %s", want)
		}
	}
}

func assertWSLUpdateHistory(t *testing.T, item store.UpdateHistoryRecord, kind models.UpdateKind, result string, oldImageID string, newImageID string) {
	t.Helper()
	if item.ServiceID == "" {
		t.Fatalf("missing history row for %s", kind)
	}
	if item.UpdateKind != kind || item.Result != result {
		t.Fatalf("history item = %#v, want kind=%s result=%s", item, kind, result)
	}
	if item.OldImageID != oldImageID || item.NewImageID != newImageID {
		t.Fatalf("history images = old %q/%q new %q/%q", item.OldImageID, oldImageID, item.NewImageID, newImageID)
	}
	if item.RollbackStatus != rollbackStatusAvailable {
		t.Fatalf("history rollback = %q, want %s", item.RollbackStatus, rollbackStatusAvailable)
	}
}
