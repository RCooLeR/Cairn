//go:build windows

package docker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

func TestWindowsWSLDockerConnection(t *testing.T) {
	if os.Getenv("CAIRN_REAL_WSL_DOCKER") != "1" {
		t.Skip("set CAIRN_REAL_WSL_DOCKER=1 to run against the local cairn-dev WSL distro")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	provider := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: "cairn-dev"})
	status, err := provider.Detect(ctx)
	if err != nil {
		t.Fatalf("provider Detect() error = %v", err)
	}
	if !status.Healthy {
		t.Fatalf("cairn-dev WSL provider is not healthy: %#v", status.Problems)
	}
	if status.DockerHost != "wsl+stdio://cairn-dev" {
		t.Fatalf("DockerHost marker = %q, want wsl+stdio://cairn-dev", status.DockerHost)
	}

	eventBus := bus.New()
	defer eventBus.Close()
	client := New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.ServerVersion == "" || info.DockerRootDir == "" {
		t.Fatalf("info missing required fields: %#v", info)
	}
	version, err := client.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version.APIVersion == "" {
		t.Fatalf("version missing API version: %#v", version)
	}
	if _, err := client.ListContainers(ctx, models.ContainerListOptions{All: true}); err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
}
