package shell

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/services"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
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
	agentFilePlans := security.NewAgentFileEditPlanStore(nil)
	registryManager := registrycore.NewManager(providerManager, auditRepo)
	registryManager.Settings = db.Settings()
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
	composeService := &services.ComposeService{Projects: projectRepo, Audit: auditRepo, Events: eventBus, RuntimeMu: runtimeMu}
	metricsService := &services.MetricsService{RuntimeMu: runtimeMu}
	logsService := &services.LogsService{RuntimeMu: runtimeMu}
	terminalService := &services.TerminalService{RuntimeMu: runtimeMu}
	updateService := &services.UpdateService{RuntimeMu: runtimeMu}
	lineageService := &services.ImageLineageService{RuntimeMu: runtimeMu}
	backupService := &services.BackupService{RuntimeMu: runtimeMu}
	registryService := &services.RegistryService{Manager: registryManager}
	agentService := &services.AgentService{
		Settings: db.Settings(),
		Audit:    auditRepo,
		Docker:   dockerService,
		Project:  projectService,
		Logs:     logsService,
		Update:   updateService,
		Plans:    agentFilePlans,
	}
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
		maybeAutoInstallWindowsDockerCLIShim(ctx, db.Settings(), runtimeProvider)
		if _, err := runtimeController.RebindProvider(ctx, runtimeProvider); err != nil {
			cancel()
			eventBus.Close()
			_ = db.Close()
			return err
		}
	}

	notificationService := wailsnotifications.New()
	app := application.New(application.Options{
		Name:         appName,
		Description:  appDescription,
		Icon:         icon,
		MarshalError: apperror.Marshal,
		Services: []application.Service{
			application.NewService(notificationService),
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
			application.NewService(agentService),
			application.NewService(&services.SettingsService{
				Audit:         auditRepo,
				Notifications: db.Notifications(),
				Settings:      db.Settings(),
				Autostart:     services.NewAutostartManager(),
			}),
		},
		OnShutdown: func() {
			cancel()
			runtimeController.StopAll()
			agentFilePlans.Close()
			eventBus.Close()
			_ = db.Close()
		},
		Assets: application.AssetOptions{
			Handler:        application.AssetFileServerFS(assets),
			DisableLogging: true,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Linux: application.LinuxOptions{
			ProgramName:                   "cairn",
			DisableQuitOnLastWindowClosed: true,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	})

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            appName,
		Width:            1280,
		Height:           800,
		MinWidth:         1024,
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
	quitRequested := &atomic.Bool{}
	trayNoticeShown := &atomic.Bool{}
	configureSystemTray(app, mainWindow, icon, notificationService, quitRequested, trayNoticeShown)
	startDesktopNotificationBridge(ctx, eventBus, notificationService)
	forwardBusEvents(ctx, eventBus, mainWindow, []bus.Topic{
		bus.TopicProviderChanged,
		bus.TopicDockerConnected,
		bus.TopicDockerReconnecting,
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
		bus.TopicNotification,
	})

	return app.Run()
}

func maybeAutoInstallWindowsDockerCLIShim(ctx context.Context, settings *store.SettingsRepository, provider providers.PlatformProvider) {
	if runtime.GOOS != "windows" || provider == nil || provider.Type() != providers.TypeWindowsWSL {
		return
	}
	status, err := services.EnsureWindowsDockerCLIShim(ctx, settings)
	if err != nil {
		slog.Debug("Windows Docker CLI shim auto-install skipped", "error", err)
		return
	}
	if status != nil && status.Installed && strings.TrimSpace(status.DockerOnPath) == "" {
		slog.Info("Windows Docker CLI shim installed; open a new shell to use docker commands", "path", status.CommandPath)
	}
}

func configureSystemTray(
	app *application.App,
	window application.Window,
	icon []byte,
	notificationService *wailsnotifications.NotificationService,
	quitRequested *atomic.Bool,
	trayNoticeShown *atomic.Bool,
) {
	if app == nil || window == nil {
		return
	}
	showWindow := func() {
		window.Show()
		if window.IsMinimised() {
			window.UnMinimise()
		}
		window.Focus()
	}
	hideWindow := func() {
		window.Hide()
	}
	quitApp := func() {
		quitRequested.Store(true)
		app.Quit()
	}

	menu := application.NewMenu()
	menu.Add("Show Cairn").OnClick(func(*application.Context) { showWindow() })
	menu.Add("Hide to tray").OnClick(func(*application.Context) { hideWindow() })
	menu.AddSeparator()
	menu.Add("Quit Cairn").OnClick(func(*application.Context) { quitApp() })

	tray := app.SystemTray.New()
	tray.SetIcon(icon).
		SetMenu(menu).
		OnClick(showWindow).
		OnDoubleClick(showWindow)
	tray.SetTooltip(appName)

	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if quitRequested.Load() {
			return
		}
		event.Cancel()
		hideWindow()
		if trayNoticeShown.CompareAndSwap(false, true) {
			sendDesktopNotification(notificationService, "cairn-hidden-to-tray", "Cairn is still running", "Use the tray icon to restore or quit Cairn.", nil)
		}
	})
	window.OnWindowEvent(events.Common.WindowMinimise, func(*application.WindowEvent) {
		if quitRequested.Load() {
			return
		}
		hideWindow()
	})
}

func startDesktopNotificationBridge(ctx context.Context, eventBus bus.Bus, notificationService *wailsnotifications.NotificationService) {
	if eventBus == nil || notificationService == nil {
		return
	}
	// Track whether an unresolved disconnect notification is showing so a later
	// recovery can replace it with a single "reconnected" toast. Deliberately
	// does NOT subscribe to TopicDockerReconnecting — grace-period blips must
	// stay silent.
	wasDisconnected := &atomic.Bool{}
	for _, topic := range []bus.Topic{bus.TopicNotification, bus.TopicDockerDisconnected, bus.TopicDockerConnected} {
		topic := topic
		events := eventBus.Subscribe(ctx, topic, 16)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case event, ok := <-events:
					if !ok {
						return
					}
					sendDesktopNotificationForEvent(notificationService, event, wasDisconnected)
				}
			}
		}()
	}
}

func sendDesktopNotificationForEvent(notificationService *wailsnotifications.NotificationService, event bus.Event, wasDisconnected *atomic.Bool) {
	switch event.Topic {
	case bus.TopicNotification:
		switch payload := event.Payload.(type) {
		case models.Notification:
			sendDesktopNotification(
				notificationService,
				fmt.Sprintf("cairn-notification-%d", payload.ID),
				payload.Title,
				payload.Body,
				map[string]any{"topic": payload.Topic, "level": payload.Level, "id": payload.ID},
			)
		case *models.Notification:
			if payload != nil {
				sendDesktopNotification(
					notificationService,
					fmt.Sprintf("cairn-notification-%d", payload.ID),
					payload.Title,
					payload.Body,
					map[string]any{"topic": payload.Topic, "level": payload.Level, "id": payload.ID},
				)
			}
		}
	case bus.TopicDockerDisconnected:
		if wasDisconnected != nil {
			wasDisconnected.Store(true)
		}
		body := "Cairn lost connection to Docker."
		if payload, ok := event.Payload.(dockercore.DisconnectedPayload); ok && strings.TrimSpace(payload.Reason) != "" {
			body = "Cairn lost connection to Docker: " + payload.Reason
		}
		sendDesktopNotification(notificationService, "cairn-docker-disconnected", "Docker disconnected", body, map[string]any{"topic": string(event.Topic)})
	case bus.TopicDockerConnected:
		// Only notify on recovery if a disconnect notification is outstanding.
		// Reusing the same notification ID replaces the stale "disconnected"
		// toast rather than stacking a second one.
		if wasDisconnected != nil && wasDisconnected.CompareAndSwap(true, false) {
			sendDesktopNotification(notificationService, "cairn-docker-disconnected", "Docker reconnected", "Cairn reconnected to Docker.", map[string]any{"topic": string(event.Topic)})
		}
	}
}

func sendDesktopNotification(
	notificationService *wailsnotifications.NotificationService,
	id string,
	title string,
	body string,
	data map[string]any,
) {
	if notificationService == nil || strings.TrimSpace(title) == "" {
		return
	}
	authorized, err := notificationService.CheckNotificationAuthorization()
	if err != nil {
		slog.Debug("desktop notification authorization check failed", "error", err)
		return
	}
	if !authorized {
		authorized, err = notificationService.RequestNotificationAuthorization()
		if err != nil {
			slog.Debug("desktop notification authorization request failed", "error", err)
			return
		}
	}
	if !authorized {
		return
	}
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("cairn-%d", time.Now().UnixNano())
	}
	err = notificationService.SendNotification(wailsnotifications.NotificationOptions{
		ID:    id,
		Title: strings.TrimSpace(title),
		Body:  strings.TrimSpace(body),
		Data:  data,
	})
	if err != nil {
		slog.Debug("desktop notification send failed", "error", err)
	}
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
