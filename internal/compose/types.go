package compose

import (
	"context"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

const MinimumVersion = "2.20.0"

type Runner interface {
	RunCompose(context.Context, string, ...string) (*providers.CommandResult, error)
}

type EnvRunner interface {
	RunComposeEnv(context.Context, string, []string, ...string) (*providers.CommandResult, error)
}

type PathMapper interface {
	MapPathToBackend(string) (string, error)
	MapPathToHost(string) (string, error)
}

type Client struct {
	runner Runner
}

func NewClient(runner Runner) *Client {
	return &Client{runner: runner}
}

type ProjectOptions struct {
	Workdir     string
	Files       []string
	ProjectName string
	Profiles    []string
	Env         []string
}

type BuildOptions struct {
	Pull     bool
	Labels   map[string]string
	Services []string
}

type UpOptions struct {
	ForceRecreate bool
	NoBuild       bool
	Services      []string
}

type ListOptions struct {
	All bool
}

type Version struct {
	Version   string
	GitCommit string
}

type Project struct {
	Name        string
	Status      string
	ConfigFiles []string
}

type Publisher struct {
	HostIP        string
	TargetPort    string
	PublishedPort string
	Protocol      string
}

type ContainerStatus struct {
	ID         string
	Name       string
	Project    string
	Service    string
	State      string
	Health     string
	ExitCode   int
	Publishers []Publisher
}

type ConfigResult struct {
	Raw      string
	Services []ServiceConfig
	EnvFiles []string
	Valid    bool
	Errors   []string
	API      models.ComposeConfigResult
}

type ServiceConfig struct {
	Name           string
	Image          string
	BuildContext   string
	DockerfilePath string
	BuildTarget    string
	BuildArgs      map[string]string
	Ports          []string
	DependsOn      []string
	HasHealthcheck bool
	EnvFiles       []string
	Profiles       []string
}
