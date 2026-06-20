package metrics

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

const (
	nvidiaSMISource       = "nvidia-smi"
	gpuProbeTimeout       = 2 * time.Second
	nvidiaSMIGPUQuery     = "--query-gpu=index,uuid,name,driver_version,temperature.gpu,utilization.gpu,memory.used,memory.total"
	nvidiaSMIProcessQuery = "--query-compute-apps=gpu_uuid,pid,process_name,used_memory"
	nvidiaSMICgroupScript = `for pid in "$@"; do
	cid=""
	if [ -r "/proc/$pid/cgroup" ]; then
		cid="$(grep -Eo '[0-9a-fA-F]{64}' "/proc/$pid/cgroup" 2>/dev/null | head -n 1)"
	fi
	printf '%s\t%s\n' "$pid" "$cid"
done`
	ollamaProcessName = "ollama"
	ollamaAPIURL      = "http://127.0.0.1:11434/api/ps"
	ollamaAPICommand  = `if command -v curl >/dev/null 2>&1; then curl -fsS --max-time 2 http://127.0.0.1:11434/api/ps; elif command -v wget >/dev/null 2>&1; then wget -q -T 2 -O - http://127.0.0.1:11434/api/ps; fi`
)

type nvidiaSMIProbe struct{}

type backendCommandRunner interface {
	RunBackendCommand(context.Context, string, ...string) (*providers.CommandResult, error)
}

type backendNVIDIASMIProbe struct {
	runner backendCommandRunner
}

func NewProviderGPUProbe(provider any) GPUProbe {
	if runner, ok := provider.(backendCommandRunner); ok {
		return backendNVIDIASMIProbe{runner: runner}
	}
	return nvidiaSMIProbe{}
}

func (nvidiaSMIProbe) ProbeGPUs(ctx context.Context) models.GPUMetrics {
	now := time.Now().UTC()
	path, ok := findNVIDIASMI()
	if !ok {
		return unavailableGPUMetrics("nvidia-smi was not found", now)
	}

	probeCtx, cancel := context.WithTimeout(ctx, gpuProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		probeCtx,
		path,
		nvidiaSMIGPUQuery,
		"--format=csv,noheader,nounits",
	)
	configureBackgroundCommand(cmd)
	output, err := cmd.Output()
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return unavailableGPUMetrics("GPU probe timed out", now)
	}
	if err != nil {
		return unavailableGPUMetrics("nvidia-smi could not read GPU metrics", now)
	}
	metrics := parseNVIDIASMI(output, now)
	if !metrics.Available {
		return metrics
	}
	processes := probeNVIDIAProcesses(probeCtx, path, metrics.Devices)
	if gpuProcessMemoryTotal(processes) == 0 {
		processes = appendSyntheticOllamaProcesses(processes, probeLocalOllamaProcesses(probeCtx))
	}
	if len(processes) > 0 {
		metrics.Processes = processes
	}
	return metrics
}

func (p backendNVIDIASMIProbe) ProbeGPUs(ctx context.Context) models.GPUMetrics {
	now := time.Now().UTC()
	if p.runner == nil {
		return nvidiaSMIProbe{}.ProbeGPUs(ctx)
	}

	probeCtx, cancel := context.WithTimeout(ctx, gpuProbeTimeout)
	defer cancel()
	result, err := p.runner.RunBackendCommand(
		probeCtx,
		"",
		"nvidia-smi",
		nvidiaSMIGPUQuery,
		"--format=csv,noheader,nounits",
	)
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return unavailableGPUMetrics("GPU probe timed out", now)
	}
	if err != nil || result == nil || result.ExitCode != 0 {
		return unavailableGPUMetrics("nvidia-smi could not read GPU metrics", now)
	}

	metrics := parseNVIDIASMI([]byte(result.Stdout), now)
	if !metrics.Available {
		return metrics
	}
	processResult, err := p.runner.RunBackendCommand(
		probeCtx,
		"",
		"nvidia-smi",
		nvidiaSMIProcessQuery,
		"--format=csv,noheader,nounits",
	)
	if err == nil && processResult != nil && processResult.ExitCode == 0 {
		if processes := parseNVIDIASMIProcesses([]byte(processResult.Stdout), metrics.Devices); len(processes) > 0 {
			metrics.Processes = annotateNVIDIAProcessContainers(probeCtx, p.runner, processes)
		}
	}
	if gpuProcessMemoryTotal(metrics.Processes) == 0 {
		metrics.Processes = appendSyntheticOllamaProcesses(metrics.Processes, probeBackendOllamaProcesses(probeCtx, p.runner))
	}
	return metrics
}

func findNVIDIASMI() (string, bool) {
	if path, err := exec.LookPath("nvidia-smi"); err == nil {
		return path, true
	}
	if runtime.GOOS == "windows" {
		for _, path := range []string{
			`C:\Windows\System32\nvidia-smi.exe`,
			`C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe`,
		} {
			if _, err := os.Stat(path); err == nil {
				return path, true
			}
		}
	}
	return "", false
}

func parseNVIDIASMI(output []byte, checkedAt time.Time) models.GPUMetrics {
	reader := csv.NewReader(bytes.NewReader(output))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return unavailableGPUMetrics("nvidia-smi returned unreadable GPU metrics", checkedAt)
	}

	devices := make([]models.GPUDeviceMetric, 0, len(records))
	for _, record := range records {
		if len(record) < 8 {
			continue
		}
		index, _ := strconv.Atoi(cleanGPUField(record[0]))
		usedMiB := parseGPUFloat(record[6])
		totalMiB := parseGPUFloat(record[7])
		devices = append(devices, models.GPUDeviceMetric{
			ID:                 cleanGPUField(record[0]),
			UUID:               cleanGPUField(record[1]),
			Index:              index,
			Name:               cleanGPUField(record[2]),
			DriverVersion:      cleanGPUField(record[3]),
			TemperatureCelsius: parseGPUFloat(record[4]),
			UtilizationPercent: parseGPUFloat(record[5]),
			MemoryUsedBytes:    mibToBytes(usedMiB),
			MemoryTotalBytes:   mibToBytes(totalMiB),
		})
	}
	if len(devices) == 0 {
		return unavailableGPUMetrics("No NVIDIA GPU metrics were reported", checkedAt)
	}

	var utilization float64
	var temperature float64
	var memoryUsed int64
	var memoryTotal int64
	driverVersion := devices[0].DriverVersion
	for _, device := range devices {
		utilization += device.UtilizationPercent
		if device.TemperatureCelsius > temperature {
			temperature = device.TemperatureCelsius
		}
		memoryUsed += device.MemoryUsedBytes
		memoryTotal += device.MemoryTotalBytes
		if driverVersion == "" {
			driverVersion = device.DriverVersion
		}
	}

	return models.GPUMetrics{
		Available:          true,
		Source:             nvidiaSMISource,
		DeviceCount:        len(devices),
		UtilizationPercent: utilization / float64(len(devices)),
		MemoryUsedBytes:    memoryUsed,
		MemoryTotalBytes:   memoryTotal,
		TemperatureCelsius: temperature,
		DriverVersion:      driverVersion,
		Devices:            devices,
		CheckedAt:          checkedAt,
	}
}

func probeNVIDIAProcesses(ctx context.Context, path string, devices []models.GPUDeviceMetric) []models.GPUProcessMetric {
	cmd := exec.CommandContext(
		ctx,
		path,
		nvidiaSMIProcessQuery,
		"--format=csv,noheader,nounits",
	)
	configureBackgroundCommand(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseNVIDIASMIProcesses(output, devices)
}

func parseNVIDIASMIProcesses(output []byte, devices []models.GPUDeviceMetric) []models.GPUProcessMetric {
	reader := csv.NewReader(bytes.NewReader(output))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil
	}
	byUUID := map[string]models.GPUDeviceMetric{}
	for _, device := range devices {
		if device.UUID != "" {
			byUUID[device.UUID] = device
		}
	}
	processes := make([]models.GPUProcessMetric, 0, len(records))
	for _, record := range records {
		if len(record) < 4 {
			continue
		}
		uuid := cleanGPUField(record[0])
		pid, err := strconv.Atoi(cleanGPUField(record[1]))
		if err != nil || pid <= 0 {
			continue
		}
		device := byUUID[uuid]
		deviceID := device.ID
		if deviceID == "" {
			deviceID = uuid
		}
		processes = append(processes, models.GPUProcessMetric{
			PID:         pid,
			DeviceID:    deviceID,
			DeviceUUID:  uuid,
			DeviceIndex: device.Index,
			ProcessName: cleanGPUField(record[2]),
			MemoryBytes: mibToBytes(parseGPUFloat(record[3])),
		})
	}
	return processes
}

func annotateNVIDIAProcessContainers(ctx context.Context, runner backendCommandRunner, processes []models.GPUProcessMetric) []models.GPUProcessMetric {
	if runner == nil || len(processes) == 0 {
		return processes
	}
	args := []string{"sh", "-lc", nvidiaSMICgroupScript, "cairn-gpu-cgroup"}
	seen := map[int]struct{}{}
	for _, process := range processes {
		if process.PID <= 0 {
			continue
		}
		if _, ok := seen[process.PID]; ok {
			continue
		}
		seen[process.PID] = struct{}{}
		args = append(args, strconv.Itoa(process.PID))
	}
	if len(args) == 4 {
		return processes
	}
	result, err := runner.RunBackendCommand(ctx, "", args...)
	if err != nil || result == nil || result.ExitCode != 0 {
		return processes
	}
	containers := parseNVIDIAProcessContainers(result.Stdout)
	if len(containers) == 0 {
		return processes
	}
	for i := range processes {
		if containerID := containers[processes[i].PID]; containerID != "" {
			processes[i].ContainerID = containerID
		}
	}
	return processes
}

func parseNVIDIAProcessContainers(output string) map[int]string {
	containers := map[int]string{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || pid <= 0 {
			continue
		}
		if containerID := normalizeContainerID(fields[1]); containerID != "" {
			containers[pid] = containerID
		}
	}
	return containers
}

func appendSyntheticOllamaProcesses(processes []models.GPUProcessMetric, ollama []models.GPUProcessMetric) []models.GPUProcessMetric {
	if len(ollama) == 0 || gpuProcessMemoryTotal(processes) > 0 {
		return processes
	}
	return append(processes, ollama...)
}

func gpuProcessMemoryTotal(processes []models.GPUProcessMetric) int64 {
	var total int64
	for _, process := range processes {
		if process.MemoryBytes > 0 {
			total += process.MemoryBytes
		}
	}
	return total
}

func probeLocalOllamaProcesses(ctx context.Context) []models.GPUProcessMetric {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaAPIURL, nil)
	if err != nil {
		return nil
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil
	}
	var payload ollamaPSPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil
	}
	return ollamaProcessesFromPayload(payload)
}

func probeBackendOllamaProcesses(ctx context.Context, runner backendCommandRunner) []models.GPUProcessMetric {
	if runner == nil {
		return nil
	}
	result, err := runner.RunBackendCommand(ctx, "", "sh", "-lc", ollamaAPICommand)
	if err != nil || result == nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) == "" {
		return nil
	}
	return parseOllamaPS([]byte(result.Stdout))
}

type ollamaPSPayload struct {
	Models []struct {
		Name     string `json:"name"`
		Model    string `json:"model"`
		SizeVRAM int64  `json:"size_vram"`
	} `json:"models"`
}

func parseOllamaPS(output []byte) []models.GPUProcessMetric {
	var payload ollamaPSPayload
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil
	}
	return ollamaProcessesFromPayload(payload)
}

func ollamaProcessesFromPayload(payload ollamaPSPayload) []models.GPUProcessMetric {
	processes := make([]models.GPUProcessMetric, 0, len(payload.Models))
	for _, model := range payload.Models {
		if model.SizeVRAM <= 0 {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = strings.TrimSpace(model.Model)
		}
		if name == "" {
			name = "model"
		}
		processes = append(processes, models.GPUProcessMetric{
			ProcessName: ollamaProcessName + ":" + name,
			MemoryBytes: model.SizeVRAM,
		})
	}
	return processes
}

func normalizeContainerID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "docker-")
	value = strings.TrimPrefix(value, "cri-containerd-")
	value = strings.TrimPrefix(value, "libpod-")
	value = strings.TrimSuffix(value, ".scope")
	if len(value) < 12 || len(value) > 64 {
		return ""
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return ""
	}
	return value
}

func unavailableGPUMetrics(message string, checkedAt time.Time) models.GPUMetrics {
	return models.GPUMetrics{
		Available: false,
		Source:    nvidiaSMISource,
		Message:   message,
		CheckedAt: checkedAt,
	}
}

func cleanGPUField(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "[not supported]") || strings.EqualFold(value, "N/A") {
		return ""
	}
	return value
}

func parseGPUFloat(value string) float64 {
	clean := cleanGPUField(value)
	if clean == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(clean, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func mibToBytes(value float64) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value * 1024 * 1024)
}

func cloneGPUMetrics(value models.GPUMetrics) models.GPUMetrics {
	value.Devices = append([]models.GPUDeviceMetric(nil), value.Devices...)
	value.Processes = append([]models.GPUProcessMetric(nil), value.Processes...)
	return value
}
