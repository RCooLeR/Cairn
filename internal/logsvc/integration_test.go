package logsvc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

func TestManagerRealDockerBigLogsIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real Docker log integration runs only on Linux")
	}
	if os.Getenv("CAIRN_REAL_DOCKER_LOGS") != "1" {
		t.Skip("set CAIRN_REAL_DOCKER_LOGS=1 to run real Docker log integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	projectDir := logsvcRepoPath(t, "testdata", "projects", "big-logs")
	projectName := fmt.Sprintf("cairn-big-logs-%d", time.Now().UnixNano())
	compose := func(args ...string) error {
		fullArgs := append([]string{"compose", "-p", projectName, "-f", filepath.Join(projectDir, "compose.yaml")}, args...)
		cmd := exec.CommandContext(ctx, "docker", fullArgs...)
		cmd.Dir = projectDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("docker %s: %w\n%s", strings.Join(fullArgs, " "), err, output)
		}
		return nil
	}
	defer func() {
		downCtx, downCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer downCancel()
		cmd := exec.CommandContext(downCtx, "docker", "compose", "-p", projectName, "-f", filepath.Join(projectDir, "compose.yaml"), "down", "--volumes", "--remove-orphans")
		cmd.Dir = projectDir
		_ = cmd.Run()
	}()
	if err := compose("up", "-d"); err != nil {
		t.Fatalf("compose up: %v", err)
	}

	eventBus := bus.New()
	defer eventBus.Close()
	client := dockercore.New(providers.NewLinuxNative(providers.LinuxNativeOptions{}), eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Docker Connect() error = %v", err)
	}
	defer func() {
		_ = client.Close()
	}()

	manager := NewManager(client, eventBus, Options{BatchWindow: 50 * time.Millisecond, BatchMaxLines: 200})
	linesCh := eventBus.Subscribe(ctx, bus.TopicLogsLines, 1024)
	errorsCh := eventBus.Subscribe(ctx, bus.TopicLogsError, 8)
	streamID, err := manager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:      ScopeProject,
		IDs:        []string{"linux_native/" + projectName},
		Follow:     true,
		Tail:       0,
		Timestamps: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}

	const sustainedWindow = 60 * time.Second
	const targetLines = 5000 * 60
	deadline := time.NewTimer(sustainedWindow)
	defer deadline.Stop()
	var count int
	for running := true; running; {
		select {
		case event := <-linesCh:
			payload, ok := event.Payload.(LinesPayload)
			if !ok {
				t.Fatalf("lines payload = %#v", event.Payload)
			}
			if payload.StreamID != streamID {
				continue
			}
			for _, line := range payload.Lines {
				if strings.Contains(line.Text, "lines skipped") {
					t.Fatalf("unexpected skip marker after %d lines: %#v", count, line)
				}
				count++
			}
		case event := <-errorsCh:
			t.Fatalf("logs:error event = %#v", event.Payload)
		case <-deadline.C:
			running = false
		case <-ctx.Done():
			t.Fatalf("context ended: %v", ctx.Err())
		}
	}
	if count < targetLines {
		t.Fatalf("received %d lines in %s, want at least %d", count, sustainedWindow, targetLines)
	}
	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
}

func logsvcRepoPath(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs(%q) error = %v", path, err)
	}
	return abs
}
