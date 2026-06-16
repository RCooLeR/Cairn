package shell

import (
	"context"
	"io/fs"
	"log/slog"
	"runtime"
	"strings"
	"sync"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/services"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	appName        = "Cairn"
	appDescription = "A clean Compose-first Docker manager for Windows, macOS, and Linux."
)

// Run owns all Wails-specific bootstrapping so the domain core stays free of
// Wails imports, as required by the architecture spec.
func Run(assets fs.FS) error {
	icon, err := fs.ReadFile(assets, "assets/cairn-icon.png")
	if err != nil {
		slog.Warn("failed to read application icon", "error", err)
	}
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
	objectPlans := security.NewDockerObjectPlanStore(nil)
	providerPlans := security.NewProviderPlanStore(nil)
	projectPlans := security.NewProjectPlanStore(nil)
	var registryManager *registrycore.Manager
	registryManager = registrycore.NewManager(providerManager, auditRepo)
	providerService := &services.ProviderService{Manager: providerManager, Events: eventBus, Audit: auditRepo, Plans: providerPlans}
	runtimeMu := &sync.RWMutex{}
	dockerService := &services.DockerService{Audit: auditRepo, Plans: containerPlans, ObjectPlans: objectPlans, RuntimeMu: runtimeMu}
	projectService := &services.ProjectService{
		Projects:  db.Projects(),
		Objects:   db.Objects(),
		Updates:   db.Updates(),
		Audit:     auditRepo,
		Plans:     projectPlans,
		Events:    eventBus,
		RuntimeMu: runtimeMu,
	}
	composeService := &services.ComposeService{Projects: projectRepo, RuntimeMu: runtimeMu}
	metricsService := &services.MetricsService{RuntimeMu: runtimeMu}
	logsService := &services.LogsService{RuntimeMu: runtimeMu}
	terminalService := &services.TerminalService{RuntimeMu: runtimeMu}
	updateService := &services.UpdateService{RuntimeMu: runtimeMu}
	lineageService := &services.ImageLineageService{RuntimeMu: runtimeMu}
	backupService := &services.BackupService{RuntimeMu: runtimeMu}
	registryService := &services.RegistryService{Manager: registryManager}
	runtimeController := newAppRuntime(appRuntimeConfig{
		RootCtx:         ctx,
		DB:              db,
		ProviderManager: providerManager,
		RegistryManager: registryManager,
		Audit:           auditRepo,
		Projects:        projectRepo,
		Events:          eventBus,
		ServiceMu:       runtimeMu,
		DockerService:   dockerService,
		ProjectService:  projectService,
		ComposeService:  composeService,
		MetricsService:  metricsService,
		LogsService:     logsService,
		TerminalService: terminalService,
		UpdateService:   updateService,
		LineageService:  lineageService,
		BackupService:   backupService,
	})
	providerService.Runtime = runtimeController
	if len(providerSet) > 0 {
		runtimeProvider := providerSet[0]
		if activeProvider, err := providerManager.ActiveProvider(ctx); err == nil && activeProvider != nil {
			runtimeProvider = activeProvider
		}
		if _, err := runtimeController.RebindProvider(ctx, runtimeProvider); err != nil {
			cancel()
			eventBus.Close()
			_ = db.Close()
			return err
		}
	}

	app := application.New(application.Options{
		Name:         appName,
		Description:  appDescription,
		Icon:         icon,
		MarshalError: apperror.Marshal,
		Services: []application.Service{
			application.NewService(providerService),
			application.NewService(dockerService),
			application.NewService(projectService),
			application.NewService(composeService),
			application.NewService(metricsService),
			application.NewService(logsService),
			application.NewService(terminalService),
			application.NewService(updateService),
			application.NewService(lineageService),
			application.NewService(backupService),
			application.NewService(registryService),
			application.NewService(&services.SettingsService{
				Audit:         auditRepo,
				Notifications: db.Notifications(),
				Settings:      db.Settings(),
			}),
		},
		OnShutdown: func() {
			cancel()
			runtimeController.StopAll()
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
		bus.TopicProviderChanged,
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
	return []providers.PlatformProvider{providers.NewExistingContext(providers.ExistingContextOptions{ContextName: "default"})}
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
