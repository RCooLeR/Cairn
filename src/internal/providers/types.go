package providers

import (
	"context"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
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

	WarningSystemdMissing          = "SYSTEMD_MISSING"
	WarningBrewMissing             = "BREW_MISSING"
	WarningUnencryptedTCP          = "UNENCRYPTED_TCP_CONTEXT"
	WarningDockerPackagesOutdated  = "DOCKER_PACKAGES_OUTDATED"
	WarningNVIDIARuntimeMissing    = "NVIDIA_RUNTIME_MISSING"
	WarningDockerBridgeUnavailable = "DOCKER_BRIDGE_UNAVAILABLE"
)

type CommandResult struct {
	Command         []string
	Workdir         string
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	ExitCode        int
	Duration        time.Duration
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

type RuntimeScopeProvider interface {
	ID() string
	DockerContext(context.Context) (string, error)
}

// RuntimeSnapshotCloser releases resources owned only by an immutable runtime
// provider snapshot. It must not stop or reconfigure the underlying backend.
type RuntimeSnapshotCloser interface {
	CloseRuntime() error
}

func CloseRuntimeProvider(provider PlatformProvider) error {
	closer, ok := provider.(RuntimeSnapshotCloser)
	if !ok || closer == nil {
		return nil
	}
	return closer.CloseRuntime()
}

// ResolveRuntimeScope returns the stable, non-empty identity for one runtime
// binding. Managed backends use their static identity so a stopped backend can
// still bind for repair; other providers use their Docker context.
func ResolveRuntimeScope(ctx context.Context, provider RuntimeScopeProvider) (runtimescope.Scope, error) {
	if provider == nil || strings.TrimSpace(provider.ID()) == "" {
		return runtimescope.Scope{}, apperror.New(apperror.ProviderNotReady, "Runtime provider is not configured")
	}
	contextName := ""
	if identityProvider, ok := provider.(BackendIdentityProvider); ok {
		identity, err := identityProvider.BackendIdentity(ctx)
		if err != nil {
			return runtimescope.Scope{}, apperror.New(apperror.ProviderNotReady, "Managed backend identity is not available", apperror.WithCause(err))
		}
		contextName = strings.TrimSpace(identity)
		if contextName == "" {
			return runtimescope.Scope{}, apperror.New(apperror.ProviderNotReady, "Managed backend identity is empty")
		}
	} else {
		dockerContext, err := provider.DockerContext(ctx)
		if err != nil {
			return runtimescope.Scope{}, apperror.New(apperror.ProviderNotReady, "Docker runtime context is not available", apperror.WithCause(err))
		}
		contextName = strings.TrimSpace(dockerContext)
	}
	runtimeScope, ok := runtimescope.New(provider.ID(), contextName)
	if !ok {
		return runtimescope.Scope{}, apperror.New(apperror.ProviderNotReady, "Docker runtime scope is incomplete")
	}
	return runtimeScope, nil
}

// SnapshotRuntimeProvider freezes mutable provider settings for one runtime
// generation. Managers and clients must use only the returned provider.
func SnapshotRuntimeProvider(ctx context.Context, provider PlatformProvider) (PlatformProvider, error) {
	if provider == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Runtime provider is not configured")
	}
	switch typed := provider.(type) {
	case *WindowsWSLProvider:
		return NewWindowsWSL(WindowsWSLOptions{
			Distro:      typed.configuredDistro(),
			Runner:      typed.runner,
			StdioDialer: typed.stdioDialer,
			IDs:         typed.ids,
		}), nil
	case *MacOSColimaProvider:
		typed.configMu.RLock()
		profile := typed.profile
		cpu := typed.cpu
		memoryGB := typed.memoryGB
		diskGB := typed.diskGB
		typed.configMu.RUnlock()
		host, err := typed.DockerHost(ctx)
		if err != nil {
			return nil, err
		}
		frozen := NewMacOSColima(MacOSColimaOptions{
			Profile:  profile,
			CPU:      cpu,
			MemoryGB: memoryGB,
			DiskGB:   diskGB,
			Runner:   typed.runner,
			HomeDir:  typed.homeDir,
			IDs:      typed.ids,
		})
		frozen.runtimeSocket = host
		return frozen, nil
	case *LinuxNativeProvider:
		return NewLinuxNative(LinuxNativeOptions{
			SocketPath: typed.detectSocketPath(),
			Runner:     typed.runner,
			Probe:      typed.probe,
			IDs:        typed.ids,
		}), nil
	case *ExistingContextProvider:
		frozen := NewExistingContext(ExistingContextOptions{
			ContextName: typed.configuredContext(),
			Runner:      typed.runner,
			StdioDialer: typed.stdioDialer,
		})
		frozen.freezeTarget = true
		if err := frozen.freezeRuntimeTarget(ctx); err != nil {
			return nil, err
		}
		return frozen, nil
	default:
		return nil, apperror.New(apperror.ProviderNotReady, "Runtime provider type cannot be frozen safely")
	}
}
