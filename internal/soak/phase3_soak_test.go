package soak

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
	"github.com/RCooLeR/Cairn/internal/terminal"
)

func TestPhase3StreamsTerminalDashboardSoak(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("phase 3 soak runs only on Linux")
	}
	if os.Getenv("CAIRN_PHASE3_SOAK") != "1" {
		t.Skip("set CAIRN_PHASE3_SOAK=1 to run the Phase 3 soak")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go CLI unavailable: %v", err)
	}

	duration := soakDuration(t)
	ctx, cancel := context.WithTimeout(context.Background(), duration+2*time.Minute)
	defer cancel()
	if err := waitDockerCLI(ctx); err != nil {
		t.Fatalf("Docker daemon is not ready: %v", err)
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	imageRef := "cairn-phase3-soak:" + suffix
	containerName := "cairn-phase3-soak-" + suffix
	buildSoakImage(t, ctx, imageRef)
	containerID := runDockerCommand(t, ctx, "run", "-d", "--name", containerName, "--label", "cairn.test=phase3-soak", imageRef)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = dockerCommand(cleanupCtx, "rm", "-f", containerName)
		_ = dockerCommand(cleanupCtx, "rmi", "-f", imageRef)
	})

	baselineGoroutines := runtime.NumGoroutine()
	var peakGoroutines atomic.Int64
	recordPeak(&peakGoroutines)

	eventBus := bus.New()
	defer eventBus.Close()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn-soak.db"))
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
	terminalManager := terminal.NewManager(provider, client, db.Projects(), eventBus, terminal.Options{})
	defer terminalManager.StopAll()

	logLines := eventBus.Subscribe(ctx, bus.TopicLogsLines, 4096)
	logErrors := eventBus.Subscribe(ctx, bus.TopicLogsError, 16)
	statSamples := eventBus.Subscribe(ctx, bus.TopicStatsSample, 1024)
	terminalData := eventBus.Subscribe(ctx, bus.TopicTerminalData, 1024)
	terminalClosed := eventBus.Subscribe(ctx, bus.TopicTerminalClosed, 16)

	var logCount atomic.Int64
	var statsCount atomic.Int64
	var terminalBytes atomic.Int64
	var dashboardReads atomic.Int64
	var lastLog atomic.Int64
	var lastStats atomic.Int64
	var lastTerminal atomic.Int64
	nowNano := time.Now().UnixNano()
	lastLog.Store(nowNano)
	lastStats.Store(nowNano)
	lastTerminal.Store(nowNano)

	errCh := make(chan error, 8)
	doneCh := make(chan struct{})
	go consumeLogs(ctx, logLines, logErrors, &logCount, &lastLog, errCh)
	go consumeStats(ctx, statSamples, &statsCount, &lastStats)
	go consumeTerminal(ctx, terminalData, terminalClosed, &terminalBytes, &lastTerminal, errCh)
	defer close(doneCh)

	logStreamID, err := logManager.StartLogStream(ctx, models.LogStreamRequest{
		Scope:      logsvc.ScopeContainer,
		IDs:        []string{containerID},
		Follow:     true,
		Tail:       0,
		Timestamps: true,
	})
	if err != nil {
		t.Fatalf("StartLogStream() error = %v", err)
	}
	statsStreamID, err := metricsManager.StartStatsStream(ctx, models.StatsScope{
		Kind: metrics.ScopeContainer,
		IDs:  []string{containerID},
	})
	if err != nil {
		t.Fatalf("StartStatsStream() error = %v", err)
	}
	session, err := terminalManager.OpenContainerTerminal(ctx, containerID, models.ContainerTerminalOptions{Cols: 100, Rows: 32})
	if err != nil {
		t.Fatalf("OpenContainerTerminal() error = %v", err)
	}
	if err := terminalManager.ResizeTerminal(ctx, session.ID, 120, 40); err != nil {
		t.Fatalf("ResizeTerminal() error = %v", err)
	}
	if err := terminalManager.WriteTerminal(ctx, session.ID, []byte("echo cairn-soak-start\n")); err != nil {
		t.Fatalf("WriteTerminal(start) error = %v", err)
	}

	waitForActivity(t, ctx, &logCount, &statsCount, &terminalBytes, &dashboardReads, func() error {
		return readDashboard(ctx, metricsManager, &dashboardReads)
	})

	healthEvery := boundedInterval(duration/16, 5*time.Second, time.Minute)
	terminalEvery := boundedInterval(duration/12, 2*time.Second, 30*time.Second)
	dashboardEvery := boundedInterval(duration/12, 2*time.Second, 30*time.Second)
	healthTicker := time.NewTicker(healthEvery)
	defer healthTicker.Stop()
	terminalTicker := time.NewTicker(terminalEvery)
	defer terminalTicker.Stop()
	dashboardTicker := time.NewTicker(dashboardEvery)
	defer dashboardTicker.Stop()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()

	t.Logf("phase 3 soak started: duration=%s container=%s baseline_goroutines=%d", duration, containerID, baselineGoroutines)
	for {
		select {
		case err := <-errCh:
			t.Fatal(err)
		case <-dashboardTicker.C:
			if err := readDashboard(ctx, metricsManager, &dashboardReads); err != nil {
				t.Fatalf("GetDashboardMetrics() error = %v", err)
			}
		case <-terminalTicker.C:
			line := fmt.Sprintf("echo cairn-soak-%d\n", time.Now().Unix())
			if err := terminalManager.WriteTerminal(ctx, session.ID, []byte(line)); err != nil {
				t.Fatalf("WriteTerminal(periodic) error = %v", err)
			}
		case <-healthTicker.C:
			recordPeak(&peakGoroutines)
			assertRecent(t, "logs", lastLog.Load(), 2*time.Minute)
			assertRecent(t, "stats", lastStats.Load(), 2*time.Minute)
			assertRecent(t, "terminal", lastTerminal.Load(), 2*time.Minute)
			t.Logf("phase 3 soak heartbeat: logs=%d stats=%d terminal_bytes=%d dashboard_reads=%d goroutines=%d",
				logCount.Load(), statsCount.Load(), terminalBytes.Load(), dashboardReads.Load(), runtime.NumGoroutine())
		case <-deadline.C:
			goto finished
		case <-ctx.Done():
			t.Fatalf("soak context ended: %v", ctx.Err())
		}
	}

finished:
	if err := terminalManager.CloseTerminal(session.ID); err != nil {
		t.Fatalf("CloseTerminal() error = %v", err)
	}
	if err := logManager.StopStream(logStreamID); err != nil {
		t.Fatalf("StopLogStream() error = %v", err)
	}
	if err := metricsManager.StopStream(statsStreamID); err != nil {
		t.Fatalf("StopStatsStream() error = %v", err)
	}
	terminalManager.StopAll()
	logManager.StopAll()
	metricsManager.StopAll()
	eventBus.Close()
	if err := client.Close(); err != nil {
		t.Fatalf("Docker Close() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("store Close() error = %v", err)
	}
	finalGoroutines := waitForGoroutines(baselineGoroutines, 8, 10*time.Second)
	if finalGoroutines > baselineGoroutines+8 {
		t.Fatalf("goroutine leak suspected: baseline=%d peak=%d final=%d allowed_final=%d",
			baselineGoroutines, peakGoroutines.Load(), finalGoroutines, baselineGoroutines+8)
	}
	t.Logf("phase 3 soak complete: duration=%s logs=%d stats=%d terminal_bytes=%d dashboard_reads=%d baseline_goroutines=%d peak_goroutines=%d final_goroutines=%d",
		duration, logCount.Load(), statsCount.Load(), terminalBytes.Load(), dashboardReads.Load(), baselineGoroutines, peakGoroutines.Load(), finalGoroutines)
}

func soakDuration(t *testing.T) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("CAIRN_PHASE3_SOAK_DURATION"))
	if raw == "" {
		return 4 * time.Hour
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("CAIRN_PHASE3_SOAK_DURATION=%q is invalid: %v", raw, err)
	}
	if duration <= 0 {
		t.Fatalf("CAIRN_PHASE3_SOAK_DURATION must be positive, got %s", duration)
	}
	return duration
}

func buildSoakImage(t *testing.T, ctx context.Context, imageRef string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "soak.go")
	binary := filepath.Join(dir, "soak")
	if err := os.WriteFile(source, []byte(soakProgramSource), 0o644); err != nil {
		t.Fatalf("write soak source: %v", err)
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, source)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build soak fixture: %v: %s", err, strings.TrimSpace(string(output)))
	}
	dockerfile := "FROM scratch\nCOPY soak /soak\nCOPY soak /bin/sh\nCMD [\"/soak\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	runDockerCommand(t, ctx, "build", "-q", "-t", imageRef, dir)
}

const soakProgramSource = `package main

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
	if filepath.Base(os.Args[0]) == "sh" {
		runShell()
		return
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for range ticker.C {
		fmt.Printf("cairn-soak-log %d %s\n", i, time.Now().UTC().Format(time.RFC3339Nano))
		i++
	}
}

func runShell() {
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
	case line == "id -u":
		fmt.Println("0")
	case line == "stty size":
		fmt.Println("40 120")
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

func consumeLogs(ctx context.Context, lines <-chan bus.Event, errors <-chan bus.Event, count *atomic.Int64, last *atomic.Int64, errCh chan<- error) {
	for {
		select {
		case event := <-lines:
			payload, ok := event.Payload.(logsvc.LinesPayload)
			if !ok {
				errCh <- fmt.Errorf("logs payload = %#v", event.Payload)
				return
			}
			if len(payload.Lines) > 0 {
				count.Add(int64(len(payload.Lines)))
				last.Store(time.Now().UnixNano())
			}
		case event := <-errors:
			errCh <- fmt.Errorf("logs:error event = %#v", event.Payload)
			return
		case <-ctx.Done():
			return
		}
	}
}

func consumeStats(ctx context.Context, samples <-chan bus.Event, count *atomic.Int64, last *atomic.Int64) {
	for {
		select {
		case event := <-samples:
			payload, ok := event.Payload.(metrics.SamplePayload)
			if ok && len(payload.Samples) > 0 {
				count.Add(int64(len(payload.Samples)))
				last.Store(time.Now().UnixNano())
			}
		case <-ctx.Done():
			return
		}
	}
}

func consumeTerminal(ctx context.Context, data <-chan bus.Event, closed <-chan bus.Event, bytesSeen *atomic.Int64, last *atomic.Int64, errCh chan<- error) {
	for {
		select {
		case event := <-data:
			payload, ok := event.Payload.(terminal.DataPayload)
			if !ok {
				errCh <- fmt.Errorf("terminal payload = %#v", event.Payload)
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(payload.DataBase64)
			if err != nil {
				errCh <- fmt.Errorf("decode terminal data: %w", err)
				return
			}
			if len(decoded) > 0 {
				bytesSeen.Add(int64(len(decoded)))
				last.Store(time.Now().UnixNano())
			}
		case event := <-closed:
			payload, _ := event.Payload.(terminal.ClosedPayload)
			errCh <- fmt.Errorf("terminal closed during soak: %#v", payload)
			return
		case <-ctx.Done():
			return
		}
	}
}

func waitForActivity(
	t *testing.T,
	ctx context.Context,
	logCount *atomic.Int64,
	statsCount *atomic.Int64,
	terminalBytes *atomic.Int64,
	dashboardReads *atomic.Int64,
	readDashboard func() error,
) {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	for {
		if logCount.Load() > 0 && statsCount.Load() > 0 && terminalBytes.Load() > 0 && dashboardReads.Load() > 0 {
			return
		}
		select {
		case <-ticker.C:
			if dashboardReads.Load() == 0 {
				if err := readDashboard(); err != nil {
					t.Fatalf("GetDashboardMetrics() while waiting for activity: %v", err)
				}
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for activity: logs=%d stats=%d terminal_bytes=%d dashboard_reads=%d",
				logCount.Load(), statsCount.Load(), terminalBytes.Load(), dashboardReads.Load())
		case <-ctx.Done():
			t.Fatalf("context ended while waiting for activity: %v", ctx.Err())
		}
	}
}

func readDashboard(ctx context.Context, manager *metrics.Manager, count *atomic.Int64) error {
	metrics, err := manager.GetDashboardMetrics(ctx)
	if err != nil {
		return err
	}
	if metrics == nil || metrics.Containers == 0 {
		return fmt.Errorf("dashboard metrics did not include containers: %#v", metrics)
	}
	count.Add(1)
	return nil
}

func assertRecent(t *testing.T, label string, lastNano int64, maxAge time.Duration) {
	t.Helper()
	if lastNano == 0 {
		t.Fatalf("%s has no activity timestamp", label)
	}
	age := time.Since(time.Unix(0, lastNano))
	if age > maxAge {
		t.Fatalf("%s activity stale: age=%s max=%s", label, age, maxAge)
	}
}

func boundedInterval(value time.Duration, minValue time.Duration, maxValue time.Duration) time.Duration {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func recordPeak(peak *atomic.Int64) {
	current := int64(runtime.NumGoroutine())
	for {
		previous := peak.Load()
		if current <= previous || peak.CompareAndSwap(previous, current) {
			return
		}
	}
}

func waitForGoroutines(baseline int, allowedDelta int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	final := runtime.NumGoroutine()
	for time.Now().Before(deadline) {
		runtime.GC()
		final = runtime.NumGoroutine()
		if final <= baseline+allowedDelta {
			return final
		}
		time.Sleep(200 * time.Millisecond)
	}
	return final
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
