package terminal

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

func TestManagerRealDockerContainerTerminalIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real Docker terminal integration runs only on Linux")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go CLI unavailable: %v", err)
	}
	if os.Getenv("CAIRN_REAL_DOCKER_TERMINAL") != "1" {
		t.Skip("set CAIRN_REAL_DOCKER_TERMINAL=1 to run real terminal integration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := waitDockerCLI(ctx); err != nil {
		t.Fatalf("Docker daemon is not ready: %v", err)
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	shellImage := "cairn-test-terminal-shell:" + suffix
	shellContainer := "cairn-test-terminal-shell-" + suffix
	noShellImage := "cairn-test-terminal-noshell:" + suffix
	noShellContainer := "cairn-test-terminal-noshell-" + suffix
	buildTerminalShellImage(t, ctx, shellImage)
	buildNoShellImage(t, ctx, noShellImage)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = dockerCommand(cleanupCtx, "rm", "-f", shellContainer)
		_ = dockerCommand(cleanupCtx, "rm", "-f", noShellContainer)
		_ = dockerCommand(cleanupCtx, "rmi", "-f", shellImage, noShellImage)
	})

	shellContainerID := runDockerCommand(t, ctx, "run", "-d", "--name", shellContainer, shellImage)
	noShellContainerID := runDockerCommand(t, ctx, "run", "-d", "--name", noShellContainer, noShellImage)

	eventBus := bus.New()
	defer eventBus.Close()
	dataEvents := eventBus.Subscribe(ctx, bus.TopicTerminalData, 16)
	closedEvents := eventBus.Subscribe(ctx, bus.TopicTerminalClosed, 4)
	provider := providers.NewLinuxNative(providers.LinuxNativeOptions{})
	client := dockercore.New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	manager := NewManager(provider, client, nil, eventBus, Options{})
	t.Cleanup(manager.StopAll)

	shells, err := manager.DetectContainerShells(ctx, shellContainerID)
	if err != nil {
		t.Fatalf("DetectContainerShells(shell) error = %v", err)
	}
	if got := strings.Join(shells, ","); got != "/bin/sh" {
		t.Fatalf("shells = %q", got)
	}
	if _, err := manager.DetectContainerShells(ctx, noShellContainerID); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("DetectContainerShells(no shell) error = %v, want not found", err)
	}

	info, err := manager.OpenContainerTerminal(ctx, shellContainerID, models.ContainerTerminalOptions{Cols: 100, Rows: 32})
	if err != nil {
		t.Fatalf("OpenContainerTerminal() error = %v", err)
	}
	if info.Kind != KindContainer || info.Shell != "/bin/sh" || info.IsRoot || info.User != "" {
		t.Fatalf("session info = %#v", info)
	}
	if err := manager.ResizeTerminal(ctx, info.ID, 132, 43); err != nil {
		t.Fatalf("ResizeTerminal() error = %v", err)
	}
	if err := manager.WriteTerminal(ctx, info.ID, []byte("echo cairn-terminal\n")); err != nil {
		t.Fatalf("WriteTerminal(echo) error = %v", err)
	}
	waitTerminalDataContains(t, ctx, dataEvents, info.ID, "cairn-terminal")
	if err := manager.WriteTerminal(ctx, info.ID, []byte("exit 17\n")); err != nil {
		t.Fatalf("WriteTerminal(exit) error = %v", err)
	}
	closed := waitTerminalClosed(t, ctx, closedEvents, info.ID)
	if closed.ExitCode != 17 {
		t.Fatalf("exit code = %d, want 17", closed.ExitCode)
	}
}

func buildTerminalShellImage(t *testing.T, ctx context.Context, imageRef string) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "tinysh")
	writeTinyShellSource(t, filepath.Join(dir, "tinysh.go"))
	buildLinuxBinary(t, filepath.Join(dir, "tinysh.go"), binary)
	dockerfile := "FROM scratch\nCOPY tinysh /bin/sh\nCOPY tinysh /bin/test\nCMD [\"/bin/sh\", \"-c\", \"block\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	runDockerCommand(t, ctx, "build", "-q", "-t", imageRef, dir)
}

func buildNoShellImage(t *testing.T, ctx context.Context, imageRef string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "sleeper.go")
	binary := filepath.Join(dir, "sleeper")
	if err := os.WriteFile(source, []byte(`package main

import "time"

func main() { time.Sleep(time.Hour) }
`), 0o644); err != nil {
		t.Fatalf("write sleeper source: %v", err)
	}
	buildLinuxBinary(t, source, binary)
	dockerfile := "FROM scratch\nCOPY sleeper /sleeper\nCMD [\"/sleeper\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	runDockerCommand(t, ctx, "build", "-q", "-t", imageRef, dir)
}

func writeTinyShellSource(t *testing.T, path string) {
	t.Helper()
	source := `package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	name := filepath.Base(os.Args[0])
	if name == "test" {
		if len(os.Args) == 3 && os.Args[1] == "-x" {
			info, err := os.Stat(os.Args[2])
			if err == nil && info.Mode()&0111 != 0 {
				os.Exit(0)
			}
		}
		os.Exit(1)
	}
	if len(os.Args) >= 3 && os.Args[1] == "-c" {
		runLine(os.Args[2])
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		runLine(strings.TrimSpace(scanner.Text()))
	}
}

func runLine(line string) {
	switch {
	case line == "block":
		for {
			time.Sleep(time.Hour)
		}
	case line == "id -u":
		fmt.Println("0")
	case line == "stty size":
		fmt.Println("32 100")
	case strings.HasPrefix(line, "echo "):
		fmt.Println(strings.TrimPrefix(line, "echo "))
	case strings.HasPrefix(line, "exit "):
		code, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "exit ")))
		os.Exit(code)
	case line == "exit":
		os.Exit(0)
	default:
		fmt.Println(line)
	}
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write tiny shell source: %v", err)
	}
}

func buildLinuxBinary(t *testing.T, source string, output string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", output, source)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v: %s", source, err, strings.TrimSpace(string(combined)))
	}
}

func runDockerCommand(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	output, err := dockerCommandOutput(ctx, args...)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return output
}

func dockerCommand(ctx context.Context, args ...string) error {
	_, err := dockerCommandOutput(ctx, args...)
	return err
}

func dockerCommandOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return text, fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}

func waitDockerCLI(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if _, err := dockerCommandOutput(ctx, "info"); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %v", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func waitTerminalDataContains(t *testing.T, ctx context.Context, ch <-chan bus.Event, sessionID string, want string) {
	t.Helper()
	for {
		select {
		case event := <-ch:
			payload, ok := event.Payload.(DataPayload)
			if !ok || payload.SessionID != sessionID {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(payload.DataBase64)
			if err != nil {
				t.Fatalf("decode terminal data: %v", err)
			}
			if strings.Contains(string(decoded), want) {
				return
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for terminal data %q: %v", want, ctx.Err())
		}
	}
}

func waitTerminalClosed(t *testing.T, ctx context.Context, ch <-chan bus.Event, sessionID string) ClosedPayload {
	t.Helper()
	for {
		select {
		case event := <-ch:
			payload, ok := event.Payload.(ClosedPayload)
			if ok && payload.SessionID == sessionID {
				return payload
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for terminal close: %v", ctx.Err())
			return ClosedPayload{}
		}
	}
}
