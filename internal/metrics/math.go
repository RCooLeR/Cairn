package metrics

import (
	"math"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

func CPUPercent(previous container.CPUStats, current container.CPUStats) float64 {
	return CPUPercentWithFallback(previous, current, 0)
}

func CPUPercentWithFallback(previous container.CPUStats, current container.CPUStats, fallbackOnlineCPUs uint32) float64 {
	if current.CPUUsage.TotalUsage < previous.CPUUsage.TotalUsage || current.SystemUsage < previous.SystemUsage {
		return 0
	}
	cpuDelta := current.CPUUsage.TotalUsage - previous.CPUUsage.TotalUsage
	systemDelta := current.SystemUsage - previous.SystemUsage
	if cpuDelta == 0 || systemDelta == 0 {
		return 0
	}
	onlineCPUs := float64(current.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(current.CPUUsage.PercpuUsage))
	}
	if onlineCPUs == 0 {
		onlineCPUs = float64(fallbackOnlineCPUs)
	}
	if onlineCPUs == 0 {
		onlineCPUs = 1
	}
	value := (float64(cpuDelta) / float64(systemDelta)) * onlineCPUs * 100
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return value
}

func CounterRate(previous uint64, current uint64, elapsed time.Duration) float64 {
	if elapsed <= 0 || current < previous {
		return 0
	}
	return float64(current-previous) / elapsed.Seconds()
}

func memoryUsageBytes(stats container.MemoryStats) int64 {
	usage := stats.Usage
	if usage == 0 {
		usage = stats.PrivateWorkingSet
	}
	if stats.Stats != nil {
		for _, key := range []string{"total_inactive_file", "inactive_file", "cache"} {
			if inactive := stats.Stats[key]; inactive > 0 && inactive < usage {
				usage -= inactive
				break
			}
		}
	}
	return uintToInt64(usage)
}

func memoryLimitBytes(stats container.MemoryStats) int64 {
	if stats.Limit > 0 {
		return uintToInt64(stats.Limit)
	}
	if stats.CommitPeak > 0 {
		return uintToInt64(stats.CommitPeak)
	}
	return 0
}

func networkBytes(stats map[string]container.NetworkStats) (uint64, uint64) {
	var rx uint64
	var tx uint64
	for _, item := range stats {
		rx += item.RxBytes
		tx += item.TxBytes
	}
	return rx, tx
}

func blockBytes(stats container.StatsResponse) (uint64, uint64) {
	var read uint64
	var write uint64
	for _, item := range stats.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(item.Op) {
		case "read":
			read += item.Value
		case "write":
			write += item.Value
		}
	}
	if stats.StorageStats.ReadSizeBytes > 0 || stats.StorageStats.WriteSizeBytes > 0 {
		read += stats.StorageStats.ReadSizeBytes
		write += stats.StorageStats.WriteSizeBytes
	}
	return read, write
}

func pids(stats container.StatsResponse) int64 {
	if stats.PidsStats.Current > 0 {
		return uintToInt64(stats.PidsStats.Current)
	}
	return int64(stats.NumProcs)
}

func uintToInt64(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(value)
}
