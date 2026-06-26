package providers

import (
	"context"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	TypeLinuxNative     = "linux_native"
	TypeWindowsWSL      = "windows_wsl_ubuntu"
	TypeMacOSColima     = "macos_colima"
	TypeExistingContext = "existing_context"
	TypeRemoteSSH       = "remote_ssh"

	PlatformLinux   = "linux"
	PlatformWindows = "windows"
	PlatformMacOS   = "macos"
	PlatformAny     = "any"
)

const (
	ProblemWSLMissing                 = "WSL_MISSING"
	ProblemWSLUnavailable             = "WSL_UNAVAILABLE"
	ProblemUbuntuMissing              = "UBUNTU_MISSING"
	ProblemWSL2Required               = "WSL2_REQUIRED"
	ProblemSystemdOff                 = "SYSTEMD_OFF"
	ProblemDesktopIntegrationConflict = "DESKTOP_INTEGRATION_CONFLICT"
	ProblemDockerMissing              = "DOCKER_MISSING"
	ProblemDockerDown                 = "DOCKERD_DOWN"
	ProblemSocketPerm                 = "PERM_SOCKET"
	ProblemComposeMissing             = "COMPOSE_MISSING"
	ProblemBuildxMissing              = "BUILDX_MISSING"
	ProblemColimaMissing              = "COLIMA_MISSING"
	ProblemColimaStopped              = "COLIMA_STOPPED"
	ProblemContextMissing             = "CONTEXT_MISSING"
	ProblemContextNotSelected         = "CONTEXT_NOT_SELECTED"

	WarningSystemdMissing         = "SYSTEMD_MISSING"
	WarningBrewMissing            = "BREW_MISSING"
	WarningUnencryptedTCP         = "UNENCRYPTED_TCP_CONTEXT"
	WarningDockerPackagesOutdated = "DOCKER_PACKAGES_OUTDATED"
	WarningNVIDIARuntimeMissing   = "NVIDIA_RUNTIME_MISSING"
)

type CommandResult struct {
	Command  []string
	Workdir  string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

type InstallProgress struct {
	Step       int
	TotalSteps int
	Message    string
	Done       bool
}

type PlatformProvider interface {
	ID() string
	DisplayName() string
	Type() string
	Platform() string

	Detect(context.Context) (*models.ProviderStatus, error)
	PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error)
	ExecuteInstallStep(context.Context, string, int, chan<- InstallProgress) error

	Start(context.Context) error
	Stop(context.Context) error
	Restart(context.Context) error

	DockerHost(context.Context) (string, error)
	DockerContext(context.Context) (string, error)

	RunDocker(context.Context, ...string) (*CommandResult, error)
	RunCompose(context.Context, string, ...string) (*CommandResult, error)

	HostShellCommand(models.TerminalOptions) ([]string, error)
	BackendShellCommand(models.TerminalOptions) ([]string, error)

	MapPathToBackend(string) (string, error)
	MapPathToHost(string) (string, error)
}

type BackendIdentityProvider interface {
	BackendIdentity(context.Context) (string, error)
}
