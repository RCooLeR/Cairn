package providers

import "context"

type Platform string

const (
    PlatformWindows Platform = "windows"
    PlatformLinux   Platform = "linux"
    PlatformMacOS   Platform = "macos"
)

type ProviderType string

const (
    ProviderWindowsWSL     ProviderType = "windows_wsl_ubuntu"
    ProviderLinuxNative    ProviderType = "linux_native"
    ProviderMacOSColima    ProviderType = "macos_colima"
    ProviderExistingContext ProviderType = "existing_context"
    ProviderRemoteSSH      ProviderType = "remote_ssh"
)

type InstallOptions struct {
    DistroName       string
    DockerContext    string
    EnableAutostart  bool
    AllowSudo        bool
    AddUserToGroup   bool
    ResourceCPU      int
    ResourceMemoryMB int
    ResourceDiskGB   int
}

type CommandResult struct {
    Command  []string
    Stdout   string
    Stderr   string
    ExitCode int
}

type TerminalOptions struct {
    Shell      string
    WorkingDir string
    Env        map[string]string
}

type ContainerTerminalOptions struct {
    Shell      string
    User       string
    WorkingDir string
    Env        map[string]string
}

type TerminalSession struct {
    ID string
}

type ProviderProblem struct {
    Code        string
    Message     string
    RepairHint  string
    Recoverable bool
}

type ProviderWarning struct {
    Code    string
    Message string
}

type ProviderStatus struct {
    Installed        bool
    Running          bool
    Healthy          bool

    DockerInstalled  bool
    DockerRunning    bool
    ComposeInstalled bool
    BuildxInstalled  bool

    DockerVersion    string
    ComposeVersion   string
    BackendVersion   string

    CurrentContext   string
    DockerHost       string

    Problems         []ProviderProblem
    Warnings         []ProviderWarning
}

type PlatformProvider interface {
    ID() string
    DisplayName() string
    Type() ProviderType
    Platform() Platform

    Detect(ctx context.Context) (*ProviderStatus, error)
    Install(ctx context.Context, opts InstallOptions) error

    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Restart(ctx context.Context) error

    DockerHost(ctx context.Context) (string, error)
    DockerContext(ctx context.Context) (string, error)

    RunDocker(ctx context.Context, args ...string) (*CommandResult, error)
    RunCompose(ctx context.Context, workdir string, args ...string) (*CommandResult, error)

    OpenHostTerminal(ctx context.Context, opts TerminalOptions) (*TerminalSession, error)
    OpenContainerTerminal(ctx context.Context, containerID string, opts ContainerTerminalOptions) (*TerminalSession, error)

    MapPathToBackend(hostPath string) (string, error)
    MapPathToHost(backendPath string) (string, error)
}
