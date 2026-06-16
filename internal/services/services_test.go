package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestAppVersionReturnsVersionInfo(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "local")

	got, err := (&SettingsService{}).AppVersion(context.Background())
	if err != nil {
		t.Fatalf("AppVersion: %v", err)
	}
	if got.Version == "" {
		t.Fatalf("version is empty")
	}
	if got.GoVersion == "" {
		t.Fatalf("go version is empty")
	}
}

func TestSkeletonMethodsReturnProviderNotReady(t *testing.T) {
	err := (&DockerService{}).Ping(context.Background())
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("Ping error = %v, want %s", err, apperror.ProviderNotReady)
	}
}

func TestKnownRegistriesHasDockerHub(t *testing.T) {
	got, err := (&RegistryService{}).KnownRegistries(context.Background())
	if err != nil {
		t.Fatalf("KnownRegistries: %v", err)
	}
	if len(got) == 0 || got[0].Registry != "docker.io" {
		t.Fatalf("first registry = %#v, want Docker Hub preset", got)
	}
}

func TestSettingsServiceGetCheatsheetSafetyContract(t *testing.T) {
	entries, err := (&SettingsService{}).GetCheatsheet(context.Background())
	if err != nil {
		t.Fatalf("GetCheatsheet() error = %v", err)
	}
	if len(entries) < 60 {
		t.Fatalf("entries = %d, want at least 60", len(entries))
	}
	for _, entry := range entries {
		if entry.Runnable && entry.Risk != models.RiskSafe {
			t.Fatalf("non-safe runnable entry = %#v", entry)
		}
	}
}

func TestSettingsServiceRoundTripsPersistedSettings(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	service := &SettingsService{Settings: db.Settings()}

	if err := service.SetSetting(ctx, "linux.sudo_mode", "group"); err != nil {
		t.Fatalf("SetSetting() error = %v", err)
	}
	settings, err := service.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}
	if settings["linux.sudo_mode"] != "group" {
		t.Fatalf("linux.sudo_mode = %#v, want group", settings["linux.sudo_mode"])
	}
	if settings["security.confirm_destructive"] != true {
		t.Fatalf("security.confirm_destructive = %#v, want true", settings["security.confirm_destructive"])
	}
}

func TestSettingsServiceNotifications(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	service := &SettingsService{Notifications: db.Notifications()}

	id, err := db.Notifications().Insert(ctx, store.NotificationRecord{
		Level: "warn",
		Title: "Provider degraded",
		Body:  "Docker daemon stopped",
		Topic: "provider",
	})
	if err != nil {
		t.Fatalf("Insert notification: %v", err)
	}
	notifications, err := service.GetNotifications(ctx, false)
	if err != nil {
		t.Fatalf("GetNotifications() error = %v", err)
	}
	if len(notifications) != 1 || notifications[0].ID != id || notifications[0].Read {
		t.Fatalf("notifications = %#v", notifications)
	}
	if err := service.MarkNotificationsRead(ctx, []int64{id}); err != nil {
		t.Fatalf("MarkNotificationsRead() error = %v", err)
	}
	unread, err := service.GetNotifications(ctx, true)
	if err != nil {
		t.Fatalf("GetNotifications(unread) error = %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread = %#v, want empty", unread)
	}
}

func TestProviderServiceApplyInstallPublishesProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eventBus := bus.New()
	defer eventBus.Close()
	db := openServiceTestStore(t)
	provider := &fakeInstallProvider{}
	manager := providers.NewManager(nil, nil, []providers.PlatformProvider{provider})
	service := &ProviderService{Manager: manager, Events: eventBus, Audit: db.Audit()}

	plan, err := service.PlanInstall(ctx, provider.ID(), models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	subscribeCtx, subscribeCancel := context.WithCancel(context.Background())
	defer subscribeCancel()
	events := eventBus.Subscribe(subscribeCtx, bus.TopicProviderInstallProgress, 8)
	handle, err := service.ApplyInstall(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("ApplyInstall() error = %v", err)
	}
	if handle.PlanID != plan.PlanID || handle.StreamID == "" {
		t.Fatalf("handle = %#v", handle)
	}

	var seen []providerInstallProgressPayload
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("install progress subscription closed after events: %#v", seen)
			}
			payload, ok := event.Payload.(providerInstallProgressPayload)
			if !ok {
				t.Fatalf("payload type = %T", event.Payload)
			}
			seen = append(seen, payload)
			if payload.Done {
				if payload.Error != "" {
					t.Fatalf("final payload error = %q", payload.Error)
				}
				if payload.Message != "Install complete" {
					t.Fatalf("final payload message = %q, want Install complete", payload.Message)
				}
				if payload.TotalSteps != 2 {
					t.Fatalf("final payload totalSteps = %d, want 2", payload.TotalSteps)
				}
				if got, want := provider.executed, []int{0, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
					t.Fatalf("executed = %#v, want %#v", got, want)
				}
				if len(seen) < 3 {
					t.Fatalf("progress events = %#v, want step events plus final", seen)
				}
				entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Topic: "provider.install", Limit: 5})
				if err != nil {
					t.Fatalf("GetAuditLog() error = %v", err)
				}
				if len(entries) != 1 || entries[0].Result != "success" {
					t.Fatalf("provider install audit entries = %#v", entries)
				}
				return
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for install progress: %v", ctx.Err())
		}
	}
}

func TestProviderServiceApplyInstallCancelsRunningProvider(t *testing.T) {
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	installCtx, cancelInstall := context.WithCancel(context.Background())
	eventBus := bus.New()
	defer eventBus.Close()
	db := openServiceTestStore(t)
	provider := &fakeInstallProvider{blockUntilCancel: true, started: make(chan struct{})}
	manager := providers.NewManager(nil, nil, []providers.PlatformProvider{provider})
	service := &ProviderService{Manager: manager, Events: eventBus, Audit: db.Audit()}

	plan, err := service.PlanInstall(waitCtx, provider.ID(), models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	events := eventBus.Subscribe(waitCtx, bus.TopicProviderInstallProgress, 8)
	if _, err := service.ApplyInstall(installCtx, plan.PlanID); err != nil {
		t.Fatalf("ApplyInstall() error = %v", err)
	}
	select {
	case <-provider.started:
	case <-waitCtx.Done():
		t.Fatalf("timed out waiting for install to start: %v", waitCtx.Err())
	}
	cancelInstall()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("install progress subscription closed")
			}
			payload, ok := event.Payload.(providerInstallProgressPayload)
			if !ok {
				t.Fatalf("payload type = %T", event.Payload)
			}
			if payload.Done {
				if payload.Error == "" || !strings.Contains(payload.Error, "context canceled") {
					t.Fatalf("final payload error = %q, want context canceled", payload.Error)
				}
				entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(context.Background(), models.AuditFilter{Topic: "provider.install", Limit: 5})
				if err != nil {
					t.Fatalf("GetAuditLog() error = %v", err)
				}
				if len(entries) != 1 || entries[0].Result != "failed" {
					t.Fatalf("provider install audit entries = %#v, want failed entry", entries)
				}
				return
			}
		case <-waitCtx.Done():
			t.Fatalf("timed out waiting for canceled install progress: %v", waitCtx.Err())
		}
	}
}

func TestDockerServiceLifecycleAuditsAndPlans(t *testing.T) {
	ctx := context.Background()
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

	client := newFakeDockerClient()
	service := &DockerService{Client: client, Audit: db.Audit()}
	if err := service.StartContainer(ctx, "container-1"); err != nil {
		t.Fatalf("StartContainer() error = %v", err)
	}
	if len(client.started) != 1 || client.started[0] != "container-1" {
		t.Fatalf("started = %#v", client.started)
	}
	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Topic: "container.start", Limit: 10})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("audit entries = %#v", entries)
	}
	results := map[string]models.AuditEntry{}
	for _, entry := range entries {
		results[entry.Result] = entry
	}
	success, sawSuccess := results["success"]
	if _, sawStarted := results["started"]; !sawSuccess || !sawStarted {
		t.Fatalf("audit entries = %#v", entries)
	}
	if success.Metadata["command"] != "docker start web" ||
		success.Metadata["risk"] != string(models.RiskSafe) ||
		success.Metadata["targetType"] != "container" {
		t.Fatalf("success audit metadata = %#v", success.Metadata)
	}

	if err := service.KillContainer(ctx, "container-1"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("KillContainer() error = %v, want E_CONFIRMATION_REQUIRED", err)
	}
	plan, err := service.PlanKillContainer(ctx, "container-1")
	if err != nil {
		t.Fatalf("PlanKillContainer() error = %v", err)
	}
	if plan.Risk != models.RiskNeedsConfirmation || len(plan.Effects) == 0 {
		t.Fatalf("kill plan = %#v", plan)
	}
	if err := service.ApplyContainerPlan(ctx, plan.PlanID, ""); err != nil {
		t.Fatalf("ApplyContainerPlan() error = %v", err)
	}
	if len(client.killed) != 1 || client.killed[0] != "container-1" {
		t.Fatalf("killed = %#v", client.killed)
	}
}

func TestDockerCommandBuildersAreStableAndIPv6Safe(t *testing.T) {
	runCommand := dockerRunCommand(models.RunImageRequest{
		ImageRef: "nginx:alpine",
		Ports: []models.PortMapping{{
			HostIP:        "::1",
			HostPort:      "8080",
			ContainerPort: "80",
			Protocol:      "tcp",
		}},
	})
	if !strings.Contains(runCommand, "-p [::1]:8080:80/tcp") {
		t.Fatalf("dockerRunCommand() = %q, want bracketed IPv6 publish", runCommand)
	}

	volumeCommand := dockerVolumeCreateCommand(models.CreateVolumeRequest{
		Name:       "demo",
		Driver:     "local",
		DriverOpts: map[string]string{"zeta": "last", "alpha": "first"},
		Labels:     map[string]string{"team": "platform", "app": "cairn"},
	})
	wantVolume := "docker volume create --driver local --opt alpha=first --opt zeta=last --label app=cairn --label team=platform demo"
	if volumeCommand != wantVolume {
		t.Fatalf("dockerVolumeCreateCommand() = %q, want %q", volumeCommand, wantVolume)
	}

	networkCommand := dockerNetworkCreateCommand(models.CreateNetworkRequest{
		Name:   "demo_net",
		Labels: map[string]string{"team": "platform", "app": "cairn"},
	})
	wantNetwork := "docker network create --label app=cairn --label team=platform demo_net"
	if networkCommand != wantNetwork {
		t.Fatalf("dockerNetworkCreateCommand() = %q, want %q", networkCommand, wantNetwork)
	}
}

func TestDockerServiceObjectCreationAudits(t *testing.T) {
	ctx := context.Background()
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

	client := newFakeDockerClient()
	service := &DockerService{Client: client, Audit: db.Audit()}
	if id, err := service.RunImage(ctx, models.RunImageRequest{
		ImageRef: "alpine:latest",
		Name:     "demo",
		Env:      []models.EnvVar{{Name: "API_TOKEN", Value: "secret-value"}},
		Volumes:  []models.MountSpec{{Type: "volume", Source: "demo_data", Target: "/data"}},
		Detach:   true,
	}); err != nil || id != "container-created" {
		t.Fatalf("RunImage() id=%q err=%v", id, err)
	}
	if err := service.RenameContainer(ctx, "container-1", "web2"); err != nil {
		t.Fatalf("RenameContainer() error = %v", err)
	}
	if _, err := service.PullImage(ctx, "alpine:latest"); err != nil {
		t.Fatalf("PullImage() error = %v", err)
	}
	if err := service.TagImage(ctx, "sha256:local", "localhost:5000/test/app:1.0"); err != nil {
		t.Fatalf("TagImage() error = %v", err)
	}
	if _, err := service.PushImage(ctx, "localhost:5000/test/app:1.0"); err != nil {
		t.Fatalf("PushImage() error = %v", err)
	}
	if _, err := service.SaveImage(ctx, []string{"alpine:latest"}, "/tmp/alpine.tar"); err != nil {
		t.Fatalf("SaveImage() error = %v", err)
	}
	if _, err := service.LoadImage(ctx, "/tmp/alpine.tar"); err != nil {
		t.Fatalf("LoadImage() error = %v", err)
	}
	if _, err := service.CreateVolume(ctx, models.CreateVolumeRequest{Name: "demo_data", Driver: "local"}); err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if _, err := service.CreateNetwork(ctx, models.CreateNetworkRequest{Name: "demo_net", Driver: "bridge", Attachable: true}); err != nil {
		t.Fatalf("CreateNetwork() error = %v", err)
	}
	results, err := service.SearchHub(ctx, "alpine", 5)
	if err != nil {
		t.Fatalf("SearchHub() error = %v", err)
	}
	if len(results) != 1 || client.searchTerm != "alpine" {
		t.Fatalf("SearchHub results=%#v term=%q", results, client.searchTerm)
	}

	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Limit: 30})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(entries) != 18 {
		t.Fatalf("audit entries count = %d, want 18: %#v", len(entries), entries)
	}
	var sawRun bool
	var sawPush bool
	for _, entry := range entries {
		if entry.Action == "container.run" && entry.Result == "success" {
			sawRun = true
			command, _ := entry.Metadata["command"].(string)
			if strings.Contains(command, "secret-value") || !strings.Contains(command, "API_TOKEN=********") {
				t.Fatalf("run command was not redacted: %q", command)
			}
		}
		if entry.Action == "image.push" && entry.Result == "success" {
			sawPush = true
			if entry.Metadata["risk"] != string(models.RiskNeedsConfirmation) {
				t.Fatalf("push risk = %q, want %q", entry.Metadata["risk"], models.RiskNeedsConfirmation)
			}
		}
	}
	if !sawRun {
		t.Fatalf("missing successful container.run audit in %#v", entries)
	}
	if !sawPush {
		t.Fatalf("missing successful image.push audit in %#v", entries)
	}
}

func TestDockerServiceRunImageRejectsBindMountWithoutPlan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
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
	client := newFakeDockerClient()
	service := &DockerService{Client: client, Audit: db.Audit()}

	_, err = service.RunImage(ctx, models.RunImageRequest{
		ImageRef: "alpine:latest",
		Name:     "danger",
		Volumes:  []models.MountSpec{{Type: "bind", Source: "/", Target: "/host"}},
		Detach:   true,
	})
	if !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("RunImage(bind) error = %v, want confirmation required", err)
	}
	if len(client.runImages) != 0 {
		t.Fatalf("RunImage reached Docker client: %#v", client.runImages)
	}

	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Topic: "container.run", Limit: 10})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "failed" || entries[0].Metadata["risk"] != string(models.RiskDangerous) {
		t.Fatalf("audit entries = %#v", entries)
	}
}

func TestDockerServiceObjectPlansAuditAndExecute(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	client := newFakeDockerClient()
	service := &DockerService{Client: client, Audit: db.Audit()}

	imagePlan, err := service.PlanRemoveImage(ctx, client.image.ID, false)
	if err != nil {
		t.Fatalf("PlanRemoveImage() error = %v", err)
	}
	if imagePlan.Risk != models.RiskNeedsConfirmation || !strings.Contains(imagePlan.Commands[0].Command, "docker image rm") {
		t.Fatalf("image plan = %#v", imagePlan)
	}
	if err := service.ApplyContainerPlan(ctx, imagePlan.PlanID, ""); err != nil {
		t.Fatalf("ApplyContainerPlan(image) error = %v", err)
	}
	if len(client.removedImages) != 1 || client.removedImages[0] != client.image.ID {
		t.Fatalf("removed images = %#v", client.removedImages)
	}

	prunePlan, err := service.PlanPrune(ctx, "images")
	if err != nil {
		t.Fatalf("PlanPrune() error = %v", err)
	}
	if prunePlan.Risk != models.RiskDestructive || prunePlan.Commands[0].Command != "docker image prune --all" {
		t.Fatalf("prune plan = %#v", prunePlan)
	}
	if err := service.ApplyContainerPlan(ctx, prunePlan.PlanID, ""); err != nil {
		t.Fatalf("ApplyContainerPlan(prune) error = %v", err)
	}
	if len(client.pruned) != 1 || client.pruned[0] != "images" {
		t.Fatalf("pruned = %#v", client.pruned)
	}

	volumePlan, err := service.PlanRemoveVolume(ctx, client.volume.Name, false)
	if err != nil {
		t.Fatalf("PlanRemoveVolume() error = %v", err)
	}
	if volumePlan.Risk != models.RiskDangerous || volumePlan.RequiresTypedName != client.volume.Name {
		t.Fatalf("volume plan = %#v", volumePlan)
	}
	if err := service.ApplyContainerPlan(ctx, volumePlan.PlanID, "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("ApplyContainerPlan(volume wrong) error = %v, want confirmation", err)
	}
	if err := service.ApplyContainerPlan(ctx, volumePlan.PlanID, client.volume.Name); err != nil {
		t.Fatalf("ApplyContainerPlan(volume) error = %v", err)
	}
	if len(client.removedVolumes) != 1 || client.removedVolumes[0] != client.volume.Name {
		t.Fatalf("removed volumes = %#v", client.removedVolumes)
	}

	networkPlan, err := service.PlanRemoveNetwork(ctx, client.network.ID)
	if err != nil {
		t.Fatalf("PlanRemoveNetwork() error = %v", err)
	}
	if networkPlan.Risk != models.RiskNeedsConfirmation || !strings.Contains(networkPlan.Commands[0].Command, "docker network rm") {
		t.Fatalf("network plan = %#v", networkPlan)
	}
	if err := service.ApplyContainerPlan(ctx, networkPlan.PlanID, ""); err != nil {
		t.Fatalf("ApplyContainerPlan(network) error = %v", err)
	}
	if len(client.removedNetworks) != 1 || client.removedNetworks[0] != client.network.ID {
		t.Fatalf("removed networks = %#v", client.removedNetworks)
	}

	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Limit: 20})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Result == "success" {
			seen[entry.Action] = true
		}
	}
	for _, action := range []string{"image.remove", "docker.prune.images", "volume.remove", "network.remove"} {
		if !seen[action] {
			t.Fatalf("missing audit action %s in %#v", action, entries)
		}
	}
}

func TestProjectServiceImportProject(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root := filepath.Join(t.TempDir(), "app-db")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	composeFile := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composeFile, []byte("services:\n  app:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n  db:\n    image: postgres:16-alpine\n",
	}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
		Now:        func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}

	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if detail.Summary.ID != "linux_native/app-db" || detail.Summary.ServicesTotal != 2 {
		t.Fatalf("detail summary = %#v", detail.Summary)
	}
	if len(detail.Services) != 2 || detail.Services[0].Name != "app" || detail.Services[1].Name != "db" {
		t.Fatalf("services = %#v", detail.Services)
	}
	projects, err := service.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "linux_native/app-db" {
		t.Fatalf("projects = %#v", projects)
	}
}

func TestProjectServiceListProjectsScopesToActiveBackendContext(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	projects := db.Projects()

	if err := projects.SaveSnapshot(ctx, "windows_wsl_ubuntu", []store.ProjectRecord{{
		ID:          "windows_wsl_ubuntu/ubuntu-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:Ubuntu",
		Name:        "ubuntu-app",
		LastSeenAt:  now,
	}, {
		ID:          "windows_wsl_ubuntu/cairn-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
		Name:        "cairn-app",
		LastSeenAt:  now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot windows error = %v", err)
	}
	if err := projects.SaveSnapshot(ctx, "linux_native", []store.ProjectRecord{{
		ID:         "linux_native/linux-app",
		ProviderID: "linux_native",
		Name:       "linux-app",
		LastSeenAt: now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot linux error = %v", err)
	}

	service := &ProjectService{
		Projects:    projects,
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
	}
	summaries, err := service.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "windows_wsl_ubuntu/cairn-app" {
		t.Fatalf("summaries = %#v", summaries)
	}
	if _, err := service.GetProject(ctx, "windows_wsl_ubuntu/ubuntu-app"); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("GetProject stale context error = %v, want %s", err, apperror.NotFound)
	}
}

func TestProjectServiceGetProjectIncludesDetailPayload(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root := serviceTestFixturePath(t, "testdata", "projects", "build-multistage")
	composeFile := filepath.Join(root, "compose.yaml")
	resolvedConfig := "name: build-multistage\nservices:\n  app:\n    build:\n      context: .\n      dockerfile: Dockerfile\n      target: runtime\n      args:\n        BASE_IMAGE: alpine:3.20\n    image: cairn-test/build-multistage:latest\n"
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: resolvedConfig,
	}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		Objects:    db.Objects(),
		ProviderID: "linux_native",
		Now:        func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}

	imported, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if err := db.Objects().SaveContainers(ctx, "linux_native", []store.ContainerCacheRecord{
		{
			Summary: models.ContainerSummary{
				ID:        "container-app",
				Name:      "build-multistage-app-1",
				Image:     "cairn-test/build-multistage:latest",
				Status:    "Up 2 minutes",
				State:     "running",
				Health:    models.HealthStatusHealthy,
				ProjectID: imported.Summary.ID,
				Service:   "app",
				Ports: []models.PortBinding{{
					HostPort:      "18080",
					ContainerPort: "80",
					Protocol:      "tcp",
				}},
			},
		},
		{
			Summary: models.ContainerSummary{
				ID:        "container-other",
				Name:      "other-app-1",
				Image:     "nginx:alpine",
				Status:    "Up",
				State:     "running",
				Health:    models.HealthStatusHealthy,
				ProjectID: "linux_native/other",
				Service:   "app",
			},
		},
	}, time.Date(2026, 6, 13, 6, 5, 0, 0, time.UTC)); err != nil {
		t.Fatalf("SaveContainers() error = %v", err)
	}

	detail, err := service.GetProject(ctx, imported.Summary.ID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if detail.Summary.ID != "linux_native/build-multistage" || detail.Summary.ServicesTotal != 1 {
		t.Fatalf("summary = %#v", detail.Summary)
	}
	if len(detail.Services) != 1 || detail.Services[0].Name != "app" || detail.Services[0].Image != "cairn-test/build-multistage:latest" {
		t.Fatalf("services = %#v", detail.Services)
	}
	if len(detail.Containers) != 1 || detail.Containers[0].ID != "container-app" {
		t.Fatalf("containers = %#v", detail.Containers)
	}
	if detail.Compose == nil || !detail.Compose.Valid || detail.Compose.ResolvedYAML != resolvedConfig {
		t.Fatalf("compose = %#v", detail.Compose)
	}
	if len(detail.Compose.RawFiles) != 1 || detail.Compose.RawFiles[0].Path != composeFile || !strings.Contains(detail.Compose.RawFiles[0].Content, "target: runtime") {
		t.Fatalf("raw files = %#v", detail.Compose.RawFiles)
	}

	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout:   "services:\n  app: [",
		Stderr:   "yaml: line 2: did not find expected node content",
		ExitCode: 1,
	}
	detail, err = service.GetProject(ctx, imported.Summary.ID)
	if err != nil {
		t.Fatalf("GetProject(invalid config) error = %v", err)
	}
	if detail.Compose == nil || detail.Compose.Valid || len(detail.Compose.Errors) == 0 {
		t.Fatalf("invalid compose = %#v", detail.Compose)
	}
	if len(detail.Compose.RawFiles) != 1 {
		t.Fatalf("invalid raw files = %#v", detail.Compose.RawFiles)
	}
}

func TestProjectServiceImportProjectInvalidFolder(t *testing.T) {
	db := openServiceTestStore(t)
	service := &ProjectService{
		Client:     composecore.NewClient(newFakeComposeRunner()),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
	}

	_, err := service.ImportProject(context.Background(), models.ImportProjectRequest{FolderPath: t.TempDir()})
	if !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("ImportProject() error = %v, want %s", err, apperror.ComposeInvalid)
	}
}

func TestProjectServiceStartProjectAuditsAndPublishesProgress(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	runner.outputs[root+"|-f "+composeFile+" start"] = providers.CommandResult{Stdout: "Container app Started\n"}
	eventBus := bus.New()
	defer eventBus.Close()
	progress := eventBus.Subscribe(ctx, bus.TopicJobProgress, 8)
	done := eventBus.Subscribe(ctx, bus.TopicJobDone, 8)
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		Audit:      db.Audit(),
		Events:     eventBus,
		ProviderID: "linux_native",
		Now:        func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}
	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}

	if err := service.StartProject(ctx, detail.Summary.ID); err != nil {
		t.Fatalf("StartProject() error = %v", err)
	}
	if !runner.hasCall(root + "|-f " + composeFile + " start") {
		t.Fatalf("compose calls = %#v, want start", runner.calls)
	}
	entries, err := db.Audit().List(ctx, models.AuditFilter{Topic: "project", Limit: 10})
	if err != nil {
		t.Fatalf("Audit List() error = %v", err)
	}
	if len(entries) < 2 || entries[0].Action != "project.start" || entries[0].Result != "success" {
		t.Fatalf("audit entries = %#v", entries)
	}
	if got := receiveEventPayload(t, progress, time.Second); got == nil {
		t.Fatal("expected job progress event")
	}
	if got := receiveEventPayload(t, done, time.Second); got == nil {
		t.Fatal("expected job done event")
	}
}

func TestProjectServicePlanDownWithVolumesRequiresTypedName(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	runner.outputs[root+"|-f "+composeFile+" down --volumes"] = providers.CommandResult{Stdout: "removed\n"}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		Audit:      db.Audit(),
		ProviderID: "linux_native",
		Now:        func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}
	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}

	plan, err := service.PlanDownProject(ctx, detail.Summary.ID, true)
	if err != nil {
		t.Fatalf("PlanDownProject() error = %v", err)
	}
	if plan.Risk != models.RiskDangerous || plan.RequiresTypedName != "app-db" {
		t.Fatalf("plan = %#v", plan)
	}
	if len(plan.Commands) != 1 || !strings.Contains(plan.Commands[0].Command, "down --volumes") || plan.Commands[0].WorkingDir != root {
		t.Fatalf("commands = %#v", plan.Commands)
	}

	if err := service.ApplyProjectPlan(ctx, plan.PlanID, "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("ApplyProjectPlan(wrong) error = %v, want confirmation", err)
	}
	if err := service.ApplyProjectPlan(ctx, plan.PlanID, "app-db"); err != nil {
		t.Fatalf("ApplyProjectPlan() error = %v", err)
	}
	if !runner.hasCall(root + "|-f " + composeFile + " down --volumes") {
		t.Fatalf("compose calls = %#v, want down --volumes", runner.calls)
	}
}

func TestProjectServiceLifecycleWorkdirMissing(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
	}
	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	err = service.StartProject(ctx, detail.Summary.ID)
	if !apperror.IsCode(err, apperror.WorkdirMissing) {
		t.Fatalf("StartProject() error = %v, want %s", err, apperror.WorkdirMissing)
	}
}

func TestProjectServiceLifecycleMapsBackendPaths(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	hostWorkdir := t.TempDir()
	hostFile := filepath.Join(hostWorkdir, "compose.yaml")
	if err := os.WriteFile(hostFile, []byte("services:\n  app:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	backendWorkdir := "/mnt/e/Development/project"
	backendFile := "/mnt/e/Development/project/compose.yaml"
	now := time.Date(2026, 6, 13, 6, 30, 0, 0, time.UTC)
	if err := db.Projects().SaveSnapshot(ctx, "windows_wsl_ubuntu", []store.ProjectRecord{{
		ID:           "windows_wsl_ubuntu/demo",
		ProviderID:   "windows_wsl_ubuntu",
		ContextName:  "wsl:cairn-dev",
		Name:         "demo",
		WorkingDir:   backendWorkdir,
		ComposeFiles: []string{backendFile},
		Status:       models.ProjectStatusRunning,
		Health:       models.HealthStatusHealthy,
		LastSeenAt:   now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	runner := newFakeComposeRunner()
	runner.backendToHost[backendWorkdir] = hostWorkdir
	runner.backendToHost[backendFile] = hostFile
	runner.hostToBackend[hostWorkdir] = backendWorkdir
	runner.hostToBackend[hostFile] = backendFile
	service := &ProjectService{
		Client:      composecore.NewClient(runner),
		PathMapper:  runner,
		Projects:    db.Projects(),
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
	}

	if err := service.StartProject(ctx, "windows_wsl_ubuntu/demo"); err != nil {
		t.Fatalf("StartProject() error = %v", err)
	}
	if !runner.hasCall(backendWorkdir + "|-f " + backendFile + " start") {
		t.Fatalf("compose calls = %#v, want backend mapped start", runner.calls)
	}
}

type fakeDockerClient struct {
	container       models.ContainerSummary
	image           models.ImageSummary
	volume          models.VolumeSummary
	network         models.NetworkSummary
	started         []string
	stopped         []string
	restarted       []string
	killed          []string
	removed         []string
	renamed         []string
	runImages       []models.RunImageRequest
	pulled          []string
	tagged          []string
	pushed          []string
	saved           []string
	loaded          []string
	removedImages   []string
	pruned          []string
	volumes         []models.CreateVolumeRequest
	removedVolumes  []string
	networks        []models.CreateNetworkRequest
	removedNetworks []string
	searchTerm      string
}

func newFakeDockerClient() *fakeDockerClient {
	return &fakeDockerClient{
		container: models.ContainerSummary{
			ID:        "container-1",
			Name:      "web",
			Image:     "cairn/web:latest",
			Status:    "Up",
			State:     "running",
			Health:    models.HealthStatusHealthy,
			ProjectID: "cairn",
		},
		image: models.ImageSummary{
			ID:        "sha256:local",
			RepoTags:  []string{"cairn/web:latest"},
			SizeBytes: 1024,
			InUse:     false,
			CreatedAt: time.Now().UTC(),
		},
		volume: models.VolumeSummary{
			Name:   "demo_data",
			Driver: "local",
			InUse:  false,
		},
		network: models.NetworkSummary{
			ID:     "network-1",
			Name:   "demo_net",
			Driver: "bridge",
			Scope:  "local",
		},
	}
}

func (f *fakeDockerClient) ProviderID() string {
	return "linux_native"
}

func (f *fakeDockerClient) Ping(context.Context) error {
	return nil
}

func (f *fakeDockerClient) Info(context.Context) (*models.DockerInfo, error) {
	return &models.DockerInfo{}, nil
}

func (f *fakeDockerClient) Version(context.Context) (*models.DockerVersion, error) {
	return &models.DockerVersion{}, nil
}

func (f *fakeDockerClient) DiskUsage(context.Context) (*models.DiskUsage, error) {
	return &models.DiskUsage{}, nil
}

func (f *fakeDockerClient) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	return []models.ContainerSummary{f.container}, nil
}

func (f *fakeDockerClient) GetContainer(context.Context, string) (*models.ContainerDetail, error) {
	return &models.ContainerDetail{Summary: f.container}, nil
}

func (f *fakeDockerClient) InspectContainerRaw(context.Context, string) (string, error) {
	return `{"Id":"container-1"}`, nil
}

func (f *fakeDockerClient) StartContainer(_ context.Context, id string) error {
	f.started = append(f.started, id)
	return nil
}

func (f *fakeDockerClient) StopContainer(_ context.Context, id string, _ int) error {
	f.stopped = append(f.stopped, id)
	return nil
}

func (f *fakeDockerClient) RestartContainer(_ context.Context, id string, _ int) error {
	f.restarted = append(f.restarted, id)
	return nil
}

func (f *fakeDockerClient) KillContainer(_ context.Context, id string) error {
	f.killed = append(f.killed, id)
	return nil
}

func (f *fakeDockerClient) RemoveContainer(_ context.Context, id string, _ models.RemoveContainerOptions) error {
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeDockerClient) RenameContainer(_ context.Context, id string, name string) error {
	f.renamed = append(f.renamed, id+":"+name)
	return nil
}

func (f *fakeDockerClient) RunImage(_ context.Context, req models.RunImageRequest) (string, error) {
	f.runImages = append(f.runImages, req)
	return "container-created", nil
}

func (f *fakeDockerClient) ListImages(context.Context) ([]models.ImageSummary, error) {
	return []models.ImageSummary{f.image}, nil
}

func (f *fakeDockerClient) GetImage(context.Context, string) (*models.ImageDetail, error) {
	return &models.ImageDetail{Summary: f.image}, nil
}

func (f *fakeDockerClient) PullImage(_ context.Context, ref string) (string, error) {
	f.pulled = append(f.pulled, ref)
	return "pull-stream", nil
}

func (f *fakeDockerClient) TagImage(_ context.Context, imageID string, newRef string) error {
	f.tagged = append(f.tagged, imageID+"->"+newRef)
	return nil
}

func (f *fakeDockerClient) PushImage(_ context.Context, ref string) (string, error) {
	f.pushed = append(f.pushed, ref)
	return "push-stream", nil
}

func (f *fakeDockerClient) SaveImage(_ context.Context, refs []string, destPath string) (string, error) {
	f.saved = append(f.saved, strings.Join(refs, ",")+"->"+destPath)
	return "save-job", nil
}

func (f *fakeDockerClient) LoadImage(_ context.Context, srcPath string) (string, error) {
	f.loaded = append(f.loaded, srcPath)
	return "load-job", nil
}

func (f *fakeDockerClient) SearchHub(_ context.Context, query string, _ int) ([]models.HubSearchResult, error) {
	f.searchTerm = query
	return []models.HubSearchResult{{Name: "library/" + query, Stars: 1, Official: true}}, nil
}

func (f *fakeDockerClient) RemoveImage(_ context.Context, id string, _ bool) error {
	f.removedImages = append(f.removedImages, id)
	return nil
}

func (f *fakeDockerClient) Prune(_ context.Context, kind string) error {
	f.pruned = append(f.pruned, kind)
	return nil
}

func (f *fakeDockerClient) ListVolumes(context.Context) ([]models.VolumeSummary, error) {
	return []models.VolumeSummary{f.volume}, nil
}

func (f *fakeDockerClient) GetVolume(context.Context, string) (*models.VolumeDetail, error) {
	return &models.VolumeDetail{Summary: f.volume}, nil
}

func (f *fakeDockerClient) CreateVolume(_ context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	f.volumes = append(f.volumes, req)
	return &models.VolumeSummary{Name: req.Name, Driver: req.Driver}, nil
}

func (f *fakeDockerClient) RemoveVolume(_ context.Context, name string, _ bool) error {
	f.removedVolumes = append(f.removedVolumes, name)
	return nil
}

func (f *fakeDockerClient) ListNetworks(context.Context) ([]models.NetworkSummary, error) {
	return []models.NetworkSummary{f.network}, nil
}

func (f *fakeDockerClient) GetNetwork(context.Context, string) (*models.NetworkDetail, error) {
	return &models.NetworkDetail{Summary: f.network}, nil
}

func (f *fakeDockerClient) CreateNetwork(_ context.Context, req models.CreateNetworkRequest) (*models.NetworkSummary, error) {
	f.networks = append(f.networks, req)
	return &models.NetworkSummary{ID: "network-created", Name: req.Name, Driver: req.Driver, Attachable: req.Attachable}, nil
}

func (f *fakeDockerClient) RemoveNetwork(_ context.Context, id string) error {
	f.removedNetworks = append(f.removedNetworks, id)
	return nil
}

type fakeComposeRunner struct {
	outputs       map[string]providers.CommandResult
	calls         []string
	hostToBackend map[string]string
	backendToHost map[string]string
}

func newFakeComposeRunner() *fakeComposeRunner {
	return &fakeComposeRunner{
		outputs:       map[string]providers.CommandResult{},
		hostToBackend: map[string]string{},
		backendToHost: map[string]string{},
	}
}

func (r *fakeComposeRunner) RunCompose(ctx context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
	return r.RunComposeEnv(ctx, workdir, nil, args...)
}

func (r *fakeComposeRunner) RunComposeEnv(_ context.Context, workdir string, _ []string, args ...string) (*providers.CommandResult, error) {
	key := workdir + "|" + strings.Join(args, " ")
	r.calls = append(r.calls, key)
	result := r.outputs[key]
	result.Workdir = workdir
	result.Command = append([]string{"docker", "compose"}, args...)
	return &result, nil
}

func (r *fakeComposeRunner) hasCall(want string) bool {
	for _, call := range r.calls {
		if call == want {
			return true
		}
	}
	return false
}

func (r *fakeComposeRunner) MapPathToBackend(path string) (string, error) {
	if mapped, ok := r.hostToBackend[path]; ok {
		return mapped, nil
	}
	return path, nil
}

func (r *fakeComposeRunner) MapPathToHost(path string) (string, error) {
	if mapped, ok := r.backendToHost[path]; ok {
		return mapped, nil
	}
	return path, nil
}

type fakeInstallProvider struct {
	executed         []int
	blockUntilCancel bool
	started          chan struct{}
	startOnce        sync.Once
}

func (p *fakeInstallProvider) ID() string          { return "windows_wsl_ubuntu" }
func (p *fakeInstallProvider) DisplayName() string { return "Windows WSL Ubuntu" }
func (p *fakeInstallProvider) Type() string        { return providers.TypeWindowsWSL }
func (p *fakeInstallProvider) Platform() string    { return providers.PlatformWindows }
func (p *fakeInstallProvider) Detect(context.Context) (*models.ProviderStatus, error) {
	return &models.ProviderStatus{}, nil
}
func (p *fakeInstallProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return &models.CommandPlan{
		PlanID: "plan-install",
		Title:  "Install",
		Risk:   models.RiskNeedsConfirmation,
		Commands: []models.PlannedCommand{
			{Order: 1, Command: "step 1", Risk: models.RiskNeedsConfirmation},
			{Order: 2, Command: "step 2", Risk: models.RiskNeedsConfirmation},
		},
		ExpiresAt: time.Now().Add(time.Minute),
	}, nil
}
func (p *fakeInstallProvider) ExecuteInstallStep(ctx context.Context, _ string, step int, progress chan<- providers.InstallProgress) error {
	p.executed = append(p.executed, step)
	if p.started != nil {
		p.startOnce.Do(func() {
			close(p.started)
		})
	}
	if progress != nil {
		progress <- providers.InstallProgress{
			Step:       step + 1,
			TotalSteps: 2,
			Message:    "step complete",
		}
	}
	if p.blockUntilCancel {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (p *fakeInstallProvider) Start(context.Context) error   { return nil }
func (p *fakeInstallProvider) Stop(context.Context) error    { return nil }
func (p *fakeInstallProvider) Restart(context.Context) error { return nil }
func (p *fakeInstallProvider) DockerHost(context.Context) (string, error) {
	return "", nil
}
func (p *fakeInstallProvider) DockerContext(context.Context) (string, error) {
	return "", nil
}
func (p *fakeInstallProvider) RunDocker(context.Context, ...string) (*providers.CommandResult, error) {
	return nil, nil
}
func (p *fakeInstallProvider) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	return nil, nil
}
func (p *fakeInstallProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeInstallProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeInstallProvider) MapPathToBackend(path string) (string, error) { return path, nil }
func (p *fakeInstallProvider) MapPathToHost(path string) (string, error)    { return path, nil }

func openServiceTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
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
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
		ID:          "linux_native",
		Type:        "linux_native",
		Platform:    "linux",
		DisplayName: "Linux Native",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed linux provider: %v", err)
	}
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
		ID:          "windows_wsl_ubuntu",
		Type:        "windows_wsl_ubuntu",
		Platform:    "windows",
		DisplayName: "Windows WSL Ubuntu",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed windows provider: %v", err)
	}
	return db
}

func writeServiceComposeProject(t *testing.T, name string) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	composeFile := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composeFile, []byte("services:\n  app:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	return root, composeFile
}

func serviceTestFixturePath(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs(%q) error = %v", path, err)
	}
	return abs
}

func receiveEventPayload(t *testing.T, events <-chan bus.Event, timeout time.Duration) any {
	t.Helper()
	select {
	case event := <-events:
		return event.Payload
	case <-time.After(timeout):
		return nil
	}
}
