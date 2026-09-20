//go:build windows

package terminal

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

// This opt-in check uses an existing running container. It creates no Docker
// resources and sends only a printf marker and exit to its temporary shell.
func TestManagerExistingWSLContainerTerminalIntegration(t *testing.T) {
	distro := os.Getenv("CAIRN_REAL_WSL_DISTRO")
	containerID := os.Getenv("CAIRN_REAL_WSL_TERMINAL_CONTAINER")
	if distro == "" || containerID == "" {
		t.Skip("set CAIRN_REAL_WSL_DISTRO and CAIRN_REAL_WSL_TERMINAL_CONTAINER to test an existing WSL container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	provider := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: distro})
	status, err := provider.Detect(ctx)
	if err != nil || !status.Healthy {
		t.Fatalf("WSL provider not healthy: status=%#v, error=%v", status, err)
	}
	eventBus := bus.New()
	defer eventBus.Close()
	dataEvents := eventBus.Subscribe(ctx, bus.TopicTerminalData, 64)
	closedEvents := eventBus.Subscribe(ctx, bus.TopicTerminalClosed, 8)
	client := dockercore.New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = client.Close() }()
	manager := NewManager(provider, client, nil, eventBus, Options{})
	defer manager.StopAll()
	shells, err := manager.DetectContainerShells(ctx, containerID)
	if err != nil || len(shells) == 0 {
		t.Fatalf("DetectContainerShells: shells=%v, error=%v", shells, err)
	}
	listing, err := client.ListContainerFiles(ctx, containerID, "/")
	if err != nil || listing == nil || len(listing.Entries) == 0 {
		t.Fatalf("ListContainerFiles(/): listing=%#v, error=%v", listing, err)
	}
	info, err := manager.OpenContainerTerminal(ctx, containerID, models.ContainerTerminalOptions{Cols: 100, Rows: 32})
	if err != nil {
		t.Fatalf("OpenContainerTerminal: %v", err)
	}
	if err := manager.ResizeTerminal(ctx, info.ID, 110, 34); err != nil {
		t.Fatalf("ResizeTerminal: %v", err)
	}
	// Keep the expected marker out of the echoed command, so its presence
	// proves execution and output delivery, not merely PTY input echo.
	if err := manager.WriteTerminal(ctx, info.ID, []byte("printf 'cairn-%s\\n' 'wsl-terminal-ready'\n")); err != nil {
		t.Fatalf("WriteTerminal: %v", err)
	}
	output := ""
	for !strings.Contains(output, "cairn-wsl-terminal-ready") {
		select {
		case event := <-dataEvents:
			payload, ok := event.Payload.(DataPayload)
			if !ok || payload.SessionID != info.ID {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(payload.DataBase64)
			if err != nil {
				t.Fatal(err)
			}
			output += string(data)
		case <-ctx.Done():
			t.Fatalf("terminal marker not received: %v; output=%q", ctx.Err(), output)
		}
	}
	if err := manager.WriteTerminal(ctx, info.ID, []byte("exit 0\n")); err != nil {
		t.Fatalf("WriteTerminal(exit): %v", err)
	}
	closed := waitTerminalClosed(t, ctx, closedEvents, info.ID)
	if closed.ExitCode != 0 || len(manager.ListTerminalSessions()) != 0 {
		t.Fatalf("terminal did not close cleanly: exit=%d, sessions=%v", closed.ExitCode, manager.ListTerminalSessions())
	}
	t.Logf("verified shell discovery, attach, resize, input/output, and close via %s (%s)", distro, info.Shell)
}
