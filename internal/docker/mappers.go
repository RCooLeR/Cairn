package docker

import (
	"github.com/RCooLeR/Cairn/internal/models"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/system"
)

func mapInfo(info system.Info) *models.DockerInfo {
	return &models.DockerInfo{
		ID:              info.ID,
		Name:            info.Name,
		ServerVersion:   info.ServerVersion,
		StorageDriver:   info.Driver,
		DockerRootDir:   info.DockerRootDir,
		OperatingSystem: info.OperatingSystem,
		Architecture:    info.Architecture,
		CPUs:            info.NCPU,
		MemoryBytes:     info.MemTotal,
	}
}

func mapVersion(version dockertypes.Version) *models.DockerVersion {
	return &models.DockerVersion{
		ClientVersion: version.Version,
		ServerVersion: version.Version,
		APIVersion:    version.APIVersion,
		MinAPIVersion: version.MinAPIVersion,
		GitCommit:     version.GitCommit,
		GoVersion:     version.GoVersion,
	}
}

func mapDiskUsage(usage dockertypes.DiskUsage) *models.DiskUsage {
	out := &models.DiskUsage{
		Images: models.DiskUsageCategory{
			Count: len(usage.Images),
		},
		Containers: models.DiskUsageCategory{
			Count: len(usage.Containers),
		},
		Volumes: models.DiskUsageCategory{
			Count: len(usage.Volumes),
		},
		BuildCache: models.DiskUsageCategory{
			Count: len(usage.BuildCache),
		},
	}

	for _, image := range usage.Images {
		if image == nil {
			continue
		}
		out.Images.SizeBytes += positive(image.Size)
		if image.Containers == 0 {
			out.Images.Reclaimable += positive(image.Size)
		} else if image.Containers > 0 {
			out.Images.Active++
		}
	}

	for _, container := range usage.Containers {
		if container == nil {
			continue
		}
		size := positive(container.SizeRw) + positive(container.SizeRootFs)
		out.Containers.SizeBytes += size
		if container.State == "running" {
			out.Containers.Active++
		} else {
			out.Containers.Reclaimable += positive(container.SizeRw)
		}
	}

	for _, volume := range usage.Volumes {
		if volume == nil || volume.UsageData == nil {
			continue
		}
		size := positive(volume.UsageData.Size)
		out.Volumes.SizeBytes += size
		if volume.UsageData.RefCount > 0 {
			out.Volumes.Active++
		} else {
			out.Volumes.Reclaimable += size
		}
	}

	for _, record := range usage.BuildCache {
		if record == nil {
			continue
		}
		size := positive(record.Size)
		out.BuildCache.SizeBytes += size
		if record.InUse {
			out.BuildCache.Active++
		} else {
			out.BuildCache.Reclaimable += size
		}
	}

	out.TotalBytes = out.Images.SizeBytes + out.Containers.SizeBytes + out.Volumes.SizeBytes + out.BuildCache.SizeBytes
	out.Reclaimable = out.Images.Reclaimable + out.Containers.Reclaimable + out.Volumes.Reclaimable + out.BuildCache.Reclaimable
	return out
}

func positive(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
