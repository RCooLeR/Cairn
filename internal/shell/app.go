package shell

import (
	"context"
	"io/fs"
	"runtime"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/providers"
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
	var dockerClient *dockercore.Client
	if len(providerSet) > 0 {
		dockerClient = dockercore.New(providerSet[0], eventBus)
		dockerClient.SetObjectCache(db.Objects())
		dockerClient.StartHealthLoop(ctx)
		dockerClient.StartObjectEventLoop(ctx)
		dockerClient.StartReconcileLoop(ctx)
	}

	app := application.New(application.Options{
		Name:         appName,
		Description:  appDescription,
		Icon:         icon,
		MarshalError: apperror.Marshal,
		Services: []application.Service{
			application.NewService(&services.ProviderService{Manager: providerManager}),
			application.NewService(&services.DockerService{Client: dockerClient}),
			application.NewService(&services.ProjectService{}),
			application.NewService(&services.ComposeService{}),
			application.NewService(&services.MetricsService{}),
			application.NewService(&services.LogsService{}),
			application.NewService(&services.TerminalService{}),
			application.NewService(&services.UpdateService{}),
			application.NewService(&services.ImageLineageService{}),
			application.NewService(&services.BackupService{}),
			application.NewService(&services.RegistryService{}),
			application.NewService(&services.SettingsService{}),
		},
		OnShutdown: func() {
			cancel()
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
	})

	return app.Run()
}

func defaultProviderSet() []providers.PlatformProvider {
	if runtime.GOOS == "linux" {
		return []providers.PlatformProvider{providers.NewLinuxNative(providers.LinuxNativeOptions{})}
	}
	return nil
}

func forwardBusEvents(ctx context.Context, eventBus bus.Bus, window application.Window, topics []bus.Topic) {
	for _, topic := range topics {
		topic := topic
		ch := eventBus.Subscribe(ctx, topic, 32)
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
