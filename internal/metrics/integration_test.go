package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/docker/docker/api/types/container"
)

func TestManagerRealDockerStatsIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real Docker metrics integration runs only on Linux")
	}
	if os.Getenv("CAIRN_REAL_DOCKER_METRICS") != "1" {
		t.Skip("set CAIRN_REAL_DOCKER_METRICS=1 to compare against docker stats")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	name := fmt.Sprintf("cairn-metrics-%d", time.Now().UnixNano())
	runDockerCommand(t, ctx, "run", "-d", "--rm", "--pull=missing", "--name", name,
		"alpine:3.20", "sh", "-c", "yes > /dev/null")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_ = exec.CommandContext(cleanupCtx, "docker", "rm", "-f", name).Run()
	})

	time.Sleep(60 * time.Second)

	client := dockercore.New(providers.NewLinuxNative(providers.LinuxNativeOptions{}), nil)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	containers, err := client.ListContainers(ctx, models.ContainerListOptions{All: false})
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	var summary models.ContainerSummary
	for _, item := range containers {
		if item.Name == name || strings.HasPrefix(item.ID, name) {
			summary = item
			break
		}
	}
	if summary.ID == "" {
		t.Fatalf("container %q not found in %v", name, containers)
	}

	reader, err := client.ContainerStats(ctx, summary.ID, dockercore.StatsOptions{Stream: true})
	if err != nil {
		t.Fatalf("ContainerStats(stream) error = %v", err)
	}
	defer func() {
		_ = reader.Body.Close()
	}()
	decoder := json.NewDecoder(reader.Body)
	var first, second container.StatsResponse
	if err := decoder.Decode(&first); err != nil {
		t.Fatalf("decode first stats: %v", err)
	}
	if err := decoder.Decode(&second); err != nil {
		t.Fatalf("decode second stats: %v", err)
	}
	manager := NewManager(client, nil, nil, nil, nil, Options{})
	manager.ensureReady()
	manager.containers[summary.ID] = summary
	manager.ingest(summary.ID, first)
	manager.ingest(summary.ID, second)
	samples := manager.latestForScope(models.StatsScope{Kind: ScopeAll})
	if len(samples) != 1 {
		t.Fatalf("samples = %#v, want one sample", samples)
	}

	cli := dockerStatsSnapshot(t, ctx, name)
	if math.Abs(samples[0].CPUPercent-cli.CPUPercent) > 10 {
		t.Fatalf("CPU percent = %.2f, docker stats = %.2f", samples[0].CPUPercent, cli.CPUPercent)
	}
	if cli.MemoryBytes > 0 && relativeDiff(float64(samples[0].MemoryBytes), float64(cli.MemoryBytes)) > 0.20 {
		t.Fatalf("memory bytes = %d, docker stats = %d", samples[0].MemoryBytes, cli.MemoryBytes)
	}
}

type cliStats struct {
	CPUPercent  float64
	MemoryBytes int64
}

type dockerStatsJSON struct {
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
}

func runDockerCommand(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func dockerStatsSnapshot(t *testing.T, ctx context.Context, name string) cliStats {
	t.Helper()
	raw := runDockerCommand(t, ctx, "stats", "--no-stream", "--format", "{{json .}}", name)
	var parsed dockerStatsJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("parse docker stats JSON %q: %v", raw, err)
	}
	return cliStats{
		CPUPercent:  parsePercent(parsed.CPUPerc),
		MemoryBytes: parseDockerBytes(strings.Split(parsed.MemUsage, " / ")[0]),
	}
}

func parsePercent(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(value), "%"), 64)
	return parsed
}

func parseDockerBytes(value string) int64 {
	value = strings.TrimSpace(value)
	units := []struct {
		suffix string
		scale  float64
	}{
		{"GiB", 1024 * 1024 * 1024},
		{"MiB", 1024 * 1024},
		{"KiB", 1024},
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"kB", 1000},
		{"B", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			number := strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			parsed, _ := strconv.ParseFloat(number, 64)
			return int64(parsed * unit.scale)
		}
	}
	parsed, _ := strconv.ParseFloat(value, 64)
	return int64(parsed)
}

func relativeDiff(a float64, b float64) float64 {
	if b == 0 {
		return math.Abs(a)
	}
	return math.Abs(a-b) / b
}
