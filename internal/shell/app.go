package shell

import (
	"context"
	"io/fs"
	"runtime"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	backupcore "github.com/RCooLeR/Cairn/internal/backups"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	lineagecore "github.com/RCooLeR/Cairn/internal/lineage"
	"github.com/RCooLeR/Cairn/internal/logsvc"
	"github.com/RCooLeR/Cairn/internal/metrics"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/services"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/RCooLeR/Cairn/internal/terminal"
	updatescore "github.com/RCooLeR/Cairn/internal/updates"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	appName        = "Cairn"
	appDescription = "A clean Compose-first Docker manager for Windows, macOS, and Linux."
)

// Run owns all Wails-specific bootstrapping so the domain core stays free of
// Wails imports, as required by the architecture spec.
func Run(assets fs.FS) error {
	icon, _ := fs.ReadFile(assets, "assets/cairn-icon.png")
	ctx, cancel := context.WithCancel(context.Background())
	eventBus := bus.New()

	db, err := store.Open(ctx, "")
	if err != nil {
		cancel()
		eventBus.Close()
		return err
	}
	if err := db.Migrate(ctx); err != nil {
		cancel()
		eventBus.Close()
		_ = db.Close()
		return err
	}

	providerSet := defaultProviderSet()
	providerManager := providers.NewManager(db.Providers(), db.Settings(), providerSet)
	providerManager.ApplySavedSettings(ctx)
	auditRepo := db.Audit()
	projectRepo := db.Projects()
	containerPlans := security.NewPlanStore(nil)
	projectPlans := security.NewProjectPlanStore(nil)
	var dockerClient *dockercore.Client
	var composeClient *composecore.Client
	var projectDetector *composecore.ProjectDetector
	var logsManager *logsvc.Manager
	var metricsManager *metrics.Manager
	var terminalManager *terminal.Manager
	var backupManager *backupcore.Manager
	var registryManager *registrycore.Manager
	var lineageManager *lineagecore.Manager
	var updateManager *updatescore.Manager
	var runtimeProvider providers.PlatformProvider
	runtimeProviderID := ""
	runtimeContextName := ""
	if len(providerSet) > 0 {
		runtimeProvider = providerSet[0]
		if activeProvider, err := providerManager.ActiveProvider(ctx); err == nil && activeProvider != nil {
			runtimeProvider = activeProvider
		}
		runtimeProviderID = runtimeProvider.ID()
		runtimeContextName = backendContextName(ctx, runtimeProvider)
		dockerClient = dockercore.New(runtimeProvider, eventBus)
		dockerClient.SetObjectCache(db.Objects())
		dockerClient.StartHealthLoop(ctx)
		dockerClient.StartObjectEventLoop(ctx)
		dockerClient.StartReconcileLoop(ctx)
		composeClient = composecore.NewClient(runtimeProvider)
		projectDetector = &composecore.ProjectDetector{
			ProviderID:  runtimeProvider.ID(),
			ContextName: runtimeContextName,
			Docker:      dockerClient,
			Compose:     composeClient,
			Projects:    projectRepo,
			Objects:     db.Objects(),
		}
		logsManager = logsvc.NewManager(dockerClient, eventBus, logsvc.Options{})
		metricsManager = metrics.NewManager(dockerClient, db.Metrics(), projectRepo, auditRepo, eventBus, metrics.Options{})
		metricsManager.ContextName = runtimeContextName
		metricsManager.Start(ctx)
		terminalManager = terminal.NewManager(runtimeProvider, dockerClient, projectRepo, eventBus, terminal.Options{})
		backupManager = backupcore.NewManager(providerManager, dockerClient, db.Settings(), db.Backups(), auditRepo, eventBus, services.Version)
		registryManager = registrycore.NewManager(providerManager, auditRepo)
		lineageManager = lineagecore.NewManager(projectRepo, db.Lineage(), db.Objects(), dockerClient)
		updateManager = updatescore.NewManager(projectRepo, db.Lineage(), db.Updates(), db.Objects(), dockerClient, registryManager, db.Settings(), eventBus, lineageManager)
		updateManager.Compose = composeClient
		updateManager.Backups = backupManager
		updateManager.Audit = auditRepo
		updateManager.Notify = db.Notifications()
		updateManager.ContextName = runtimeContextName
		updateManager.Start(ctx)
	}

	app := application.New(application.Options{
		Name:         appName,
		Description:  appDescription,
		Icon:         icon,
		MarshalError: apperror.Marshal,
		Services: []application.Service{
			application.NewService(&services.ProviderService{Manager: providerManager, Events: eventBus, Audit: auditRepo}),
			application.NewService(&services.DockerService{Client: dockerClient, Audit: auditRepo, Plans: containerPlans}),
			application.NewService(&services.ProjectService{
				Detector:    projectDetector,
				Projects:    projectRepo,
				Objects:     db.Objects(),
				Updates:     db.Updates(),
				Client:      composeClient,
				Audit:       auditRepo,
				Plans:       projectPlans,
				Events:      eventBus,
				ProviderID:  runtimeProviderID,
				ContextName: runtimeContextName,
			}),
			application.NewService(&services.ComposeService{Client: composeClient, Projects: projectRepo}),
			application.NewService(&services.MetricsService{Manager: metricsManager}),
			application.NewService(&services.LogsService{Manager: logsManager}),
			application.NewService(&services.TerminalService{Manager: terminalManager}),
			application.NewService(&services.UpdateService{Manager: updateManager}),
			application.NewService(&services.ImageLineageService{Manager: lineageManager}),
			application.NewService(&services.BackupService{Manager: backupManager}),
			application.NewService(&services.RegistryService{Manager: registryManager}),
			application.NewService(&services.SettingsService{
				Audit:         auditRepo,
				Notifications: db.Notifications(),
				Settings:      db.Settings(),
			}),
		},
		OnShutdown: func() {
			cancel()
			if logsManager != nil {
				logsManager.StopAll()
			}
			if metricsManager != nil {
				metricsManager.StopAll()
			}
			if terminalManager != nil {
				terminalManager.StopAll()
			}
			if dockerClient != nil {
				_ = dockerClient.Close()
			}
			eventBus.Close()
			_ = db.Close()
		},
		Assets: application.AssetOptions{
			Handler:        application.AssetFileServerFS(assets),
			DisableLogging: true,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			ProgramName: "cairn",
		},
	})

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            appName,
		Width:            1280,
		Height:           800,
		MinWidth:         1100,
		MinHeight:        700,
		InitialPosition:  application.WindowCentered,
		BackgroundColour: application.NewRGB(13, 17, 23),
		URL:              "/",
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarDefault,
		},
		Linux: application.LinuxWindow{
			Icon: icon,
		},
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
	})
	forwardBusEvents(ctx, eventBus, mainWindow, []bus.Topic{
		bus.TopicDockerConnected,
		bus.TopicDockerDisconnected,
		bus.TopicObjectsChanged,
		bus.TopicProjectChanged,
		bus.TopicProviderInstallProgress,
		bus.TopicImagePullProgress,
		bus.TopicLogsLines,
		bus.TopicLogsEOF,
		bus.TopicLogsError,
		bus.TopicTerminalData,
		bus.TopicTerminalClosed,
		bus.TopicStatsSample,
		bus.TopicJobProgress,
		bus.TopicJobDone,
	})

	return app.Run()
}

func defaultProviderSet() []providers.PlatformProvider {
	switch runtime.GOOS {
	case "linux":
		return []providers.PlatformProvider{providers.NewLinuxNative(providers.LinuxNativeOptions{})}
	case "windows":
		return []providers.PlatformProvider{providers.NewWindowsWSL(providers.WindowsWSLOptions{})}
	case "darwin":
		return []providers.PlatformProvider{providers.NewMacOSColima(providers.MacOSColimaOptions{})}
	}
	return nil
}

func backendContextName(ctx context.Context, provider providers.PlatformProvider) string {
	if provider == nil {
		return ""
	}
	identityProvider, ok := provider.(providers.BackendIdentityProvider)
	if !ok {
		return ""
	}
	identity, err := identityProvider.BackendIdentity(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(identity)
}

func forwardBusEvents(ctx context.Context, eventBus bus.Bus, window application.Window, topics []bus.Topic) {
	for _, topic := range topics {
		topic := topic
		buffer := 32
		if topic == bus.TopicTerminalData || topic == bus.TopicTerminalClosed {
			buffer = 4096
		}
		ch := eventBus.Subscribe(ctx, topic, buffer)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case event, ok := <-ch:
					if !ok {
						return
					}
					window.EmitEvent(string(event.Topic), event.Payload)
				}
			}
		}()
	}
}
