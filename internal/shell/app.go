package shell

import (
	"io/fs"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/services"
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

	app := application.New(application.Options{
		Name:         appName,
		Description:  appDescription,
		Icon:         icon,
		MarshalError: apperror.Marshal,
		Services: []application.Service{
			application.NewService(&services.ProviderService{}),
			application.NewService(&services.DockerService{}),
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

	app.Window.NewWithOptions(application.WebviewWindowOptions{
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

	return app.Run()
}
