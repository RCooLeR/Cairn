package soak

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/logsvc"
	"github.com/RCooLeR/Cairn/internal/metrics"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestPhase4ProviderChaos(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("phase 4 chaos runs only on Linux")
	}
	if os.Getenv("CAIRN_PHASE4_CHAOS") != "1" {
		t.Skip("set CAIRN_PHASE4_CHAOS=1 to run the Phase 4 daemon chaos test")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}

	duration := phase4ChaosDuration(t)
	ctx, cancel := context.WithTimeout(context.Background(), duration+3*time.Minute)
	defer cancel()
	if err := waitDockerCLI(ctx); err != nil {
		t.Fatalf("Docker daemon is not ready: %v", err)
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	imageRef := "cairn-phase4-chaos:" + suffix
	phase4BuildChaosImage(t, ctx, imageRef)
	activeContainers := []string{}
	if name := phase4RunChaosContainer(ctx, suffix, imageRef); name != "" {
		activeContainers = append(activeContainers, name)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		cleanupPhase4ChaosDocker(cleanupCtx, suffix, imageRef)
	})

	baselineGoroutines := runtime.NumGoroutine()
	var peakGoroutines atomic.Int64
	recordPeak(&peakGoroutines)

	eventBus := bus.New()
	defer eventBus.Close()
	db, err := store.Open(ctx, t.TempDir()+"/cairn-chaos.db")
	if err != nil {
		t.Fatalf("store Open() error = %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("store Migrate() error = %v", err)
	}

	provider := providers.NewLinuxNative(providers.LinuxNativeOptions{})
	client := dockercore.New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Docker Connect() error = %v", err)
	}
	defer func() {
		_ = client.Close()
	}()

	logManager := logsvc.NewManager(client, eventBus, logsvc.Options{
		BatchWindow:   50 * time.Millisecond,
		BatchMaxLines: 200,
	})
	defer logManager.StopAll()
	metricsManager := metrics.NewManager(client, db.Metrics(), db.Projects(), db.Audit(), eventBus, metrics.Options{
		VisibleInterval:    time.Second,
		BackgroundInterval: 2 * time.Second,
		PublishInterval:    time.Second,
		PersistInterval:    5 * time.Second,
	})
	defer metricsManager.StopAll()

	logStreamID, err := logManager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:      logsvc.ScopeAll,
		Follow:     true,
		Tail:       0,
		Timestamps: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	statsStreamID, err := metricsManager.StartStatsStream(ctx, models.StatsScope{Kind: metrics.ScopeAll})
	if err != nil {
		t.Fatalf("StartStatsStream() error = %v", err)
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var churns int
	var daemonCycles int
	nextDaemonCycle := time.Now().Add(phase4ChaosJitter(rng, duration))
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()

	t.Logf("phase 4 chaos started: duration=%s image=%s baseline_goroutines=%d", duration, imageRef, baselineGoroutines)
	for {
		select {
		case <-ticker.C:
			recordPeak(&peakGoroutines)
			churns += phase4ChurnContainer(ctx, suffix, imageRef, &activeContainers)
			if time.Now().After(nextDaemonCycle) {
				daemonCycles++
				phase4RestartDocker(t, ctx)
				if err := client.Connect(ctx); err != nil {
					t.Fatalf("Docker reconnect after chaos cycle %d: %v", daemonCycles, err)
				}
				nextDaemonCycle = time.Now().Add(phase4ChaosJitter(rng, duration))
			}
			if _, err := metricsManager.GetDashboardMetrics(ctx); err != nil && phase4DockerReady() {
				t.Fatalf("dashboard read after healthy daemon failed: %v", err)
			}
		case <-deadline.C:
			goto finished
		case <-ctx.Done():
			t.Fatalf("chaos context ended: %v", ctx.Err())
		}
	}

finished:
	if err := logManager.StopStream(logStreamID); err != nil {
		t.Fatalf("StopLogStream() error = %v", err)
	}
	if err := metricsManager.StopStream(statsStreamID); err != nil {
		t.Fatalf("StopStatsStream() error = %v", err)
	}
	logManager.StopAll()
	metricsManager.StopAll()
	eventBus.Close()
	if err := client.Close(); err != nil {
		t.Fatalf("Docker Close() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("store Close() error = %v", err)
	}
	cleanupPhase4ChaosDocker(ctx, suffix, imageRef)
	finalGoroutines := waitForGoroutines(baselineGoroutines, 10, 15*time.Second)
	if finalGoroutines > baselineGoroutines+10 {
		t.Fatalf("goroutine leak suspected: baseline=%d peak=%d final=%d allowed_final=%d\n%s",
			baselineGoroutines, peakGoroutines.Load(), finalGoroutines, baselineGoroutines+10, phase4GoroutineProfile())
	}
	if daemonCycles == 0 {
		t.Fatalf("chaos run completed without a daemon stop/start cycle")
	}
	t.Logf("phase 4 chaos complete: duration=%s churns=%d daemon_cycles=%d baseline_goroutines=%d peak_goroutines=%d final_goroutines=%d",
		duration, churns, daemonCycles, baselineGoroutines, peakGoroutines.Load(), finalGoroutines)
}

func phase4ChaosDuration(t *testing.T) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("CAIRN_PHASE4_CHAOS_DURATION"))
	if raw == "" {
		return time.Hour
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("CAIRN_PHASE4_CHAOS_DURATION=%q is invalid: %v", raw, err)
	}
	if duration <= 0 {
		t.Fatalf("CAIRN_PHASE4_CHAOS_DURATION must be positive, got %s", duration)
	}
	return duration
}

func phase4BuildChaosImage(t *testing.T, ctx context.Context, imageRef string) {
	t.Helper()
	dir := t.TempDir()
	dockerfile := `FROM busybox:1.36
CMD ["sh", "-c", "i=0; while true; do echo cairn-chaos-log $i $(date); i=$((i+1)); sleep 1; done"]
`
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write chaos Dockerfile: %v", err)
	}
	runDockerCommand(t, ctx, "build", "-q", "-t", imageRef, dir)
}

func phase4ChaosJitter(rng *rand.Rand, duration time.Duration) time.Duration {
	if duration <= 45*time.Second {
		return 10 * time.Second
	}
	minDelay := 20 * time.Second
	maxDelay := 90 * time.Second
	if duration/4 < maxDelay {
		maxDelay = duration / 4
	}
	if maxDelay <= minDelay {
		return minDelay
	}
	return minDelay + time.Duration(rng.Int63n(int64(maxDelay-minDelay)))
}

func phase4ChurnContainer(ctx context.Context, suffix string, imageRef string, active *[]string) int {
	if len(*active) >= 5 {
		name := (*active)[0]
		*active = (*active)[1:]
		_ = dockerCommand(ctx, "rm", "-f", name)
		return 0
	}
	name := phase4RunChaosContainer(ctx, suffix, imageRef)
	if name == "" {
		return 0
	}
	*active = append(*active, name)
	return 1
}

func phase4RunChaosContainer(ctx context.Context, suffix string, imageRef string) string {
	name := fmt.Sprintf("cairn-phase4-chaos-%s-%d", suffix, time.Now().UnixNano())
	err := dockerCommand(ctx, "run", "-d", "--name", name, "--label", "cairn.test=phase4-chaos", "--label", "cairn.test.suffix="+suffix, imageRef)
	if err != nil {
		return ""
	}
	return name
}

func phase4RestartDocker(t *testing.T, ctx context.Context) {
	t.Helper()
	controlCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := phase4ControlDockerService(controlCtx, "stop"); err != nil {
		t.Fatalf("stop Docker during chaos: %v", err)
	}
	if err := phase4WaitDockerCLIDown(controlCtx, 45*time.Second); err != nil {
		t.Fatalf("wait for Docker down during chaos: %v", err)
	}
	if err := phase4ControlDockerService(controlCtx, "start"); err != nil {
		t.Fatalf("start Docker during chaos: %v", err)
	}
	if err := waitDockerCLI(controlCtx); err != nil {
		t.Fatalf("wait for Docker up during chaos: %v", err)
	}
}

func phase4ControlDockerService(ctx context.Context, action string) error {
	commands := phase4DockerControlCommands(action)
	errs := make([]error, 0, len(commands))
	for _, command := range commands {
		if _, err := exec.LookPath(command[0]); err != nil {
			errs = append(errs, err)
			continue
		}
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		output, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		errs = append(errs, fmt.Errorf("%s: %w: %s", strings.Join(command, " "), err, strings.TrimSpace(string(output))))
	}
	return errors.Join(errs...)
}

func phase4DockerControlCommands(action string) [][]string {
	if action == "stop" {
		return [][]string{
			{"systemctl", "stop", "docker.socket", "docker.service"},
			{"systemctl", "stop", "docker.service"},
			{"service", "docker", "stop"},
			{"sudo", "-n", "systemctl", "stop", "docker.socket", "docker.service"},
			{"sudo", "-n", "systemctl", "stop", "docker.service"},
			{"sudo", "-n", "service", "docker", "stop"},
		}
	}
	if action == "start" {
		return [][]string{
			{"systemctl", "start", "docker.socket", "docker.service"},
			{"systemctl", "start", "docker.service"},
			{"service", "docker", "start"},
			{"sudo", "-n", "systemctl", "start", "docker.socket", "docker.service"},
			{"sudo", "-n", "systemctl", "start", "docker.service"},
			{"sudo", "-n", "service", "docker", "start"},
		}
	}
	return [][]string{
		{"systemctl", action, "docker.service"},
		{"sudo", "-n", "systemctl", action, "docker.service"},
	}
}

func phase4WaitDockerCLIDown(ctx context.Context, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastOutput string
	for {
		infoCtx, infoCancel := context.WithTimeout(waitCtx, 2*time.Second)
		cmd := exec.CommandContext(infoCtx, "docker", "info")
		output, err := cmd.CombinedOutput()
		infoCancel()
		lastOutput = strings.TrimSpace(string(output))
		if err != nil {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("%w; last docker info output: %s", waitCtx.Err(), lastOutput)
		case <-ticker.C:
		}
	}
}

func phase4DockerReady() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return waitDockerCLI(ctx) == nil
}

func phase4GoroutineProfile() string {
	var buf bytes.Buffer
	if profile := pprof.Lookup("goroutine"); profile != nil {
		_ = profile.WriteTo(&buf, 2)
	}
	return buf.String()
}

func cleanupPhase4ChaosDocker(ctx context.Context, suffix string, imageRef string) {
	output, err := dockerCommandOutput(ctx, "ps", "-aq", "--filter", "label=cairn.test.suffix="+suffix)
	if err == nil && strings.TrimSpace(output) != "" {
		args := append([]string{"rm", "-f"}, strings.Fields(output)...)
		_ = dockerCommand(ctx, args...)
	}
	_ = dockerCommand(ctx, "rmi", "-f", imageRef)
}
