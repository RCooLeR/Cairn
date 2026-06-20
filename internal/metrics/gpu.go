package metrics

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	nvidiaSMISource = "nvidia-smi"
	gpuProbeTimeout = 2 * time.Second
)

type nvidiaSMIProbe struct{}

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
		"--query-gpu=index,name,driver_version,temperature.gpu,utilization.gpu,memory.used,memory.total",
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
	return parseNVIDIASMI(output, now)
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
		if len(record) < 7 {
			continue
		}
		index, _ := strconv.Atoi(cleanGPUField(record[0]))
		usedMiB := parseGPUFloat(record[5])
		totalMiB := parseGPUFloat(record[6])
		devices = append(devices, models.GPUDeviceMetric{
			ID:                 cleanGPUField(record[0]),
			Index:              index,
			Name:               cleanGPUField(record[1]),
			DriverVersion:      cleanGPUField(record[2]),
			TemperatureCelsius: parseGPUFloat(record[3]),
			UtilizationPercent: parseGPUFloat(record[4]),
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
	return value
}
