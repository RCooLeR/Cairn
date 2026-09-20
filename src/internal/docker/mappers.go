package docker

import (
	"maps"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/api/types/volume"
	dockerclient "github.com/moby/moby/client"
)

const (
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"
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

func mapVersion(version dockerclient.ServerVersionResult) *models.DockerVersion {
	var gitCommit string
	var goVersion string
	for _, component := range version.Components {
		if strings.EqualFold(component.Name, "Engine") {
			gitCommit = component.Details["GitCommit"]
			goVersion = component.Details["GoVersion"]
			break
		}
	}
	return &models.DockerVersion{
		ClientVersion: version.Version,
		ServerVersion: version.Version,
		APIVersion:    version.APIVersion,
		MinAPIVersion: version.MinAPIVersion,
		GitCommit:     gitCommit,
		GoVersion:     goVersion,
	}
}

func mapDiskUsage(usage dockerclient.DiskUsageResult) *models.DiskUsage {
	out := &models.DiskUsage{
		Images: models.DiskUsageCategory{
			Count:       int(usage.Images.TotalCount),
			Active:      int(usage.Images.ActiveCount),
			SizeBytes:   positive(usage.Images.TotalSize),
			Reclaimable: positive(usage.Images.Reclaimable),
		},
		Containers: models.DiskUsageCategory{
			Count:       int(usage.Containers.TotalCount),
			Active:      int(usage.Containers.ActiveCount),
			SizeBytes:   positive(usage.Containers.TotalSize),
			Reclaimable: positive(usage.Containers.Reclaimable),
		},
		Volumes: models.DiskUsageCategory{
			Count:       int(usage.Volumes.TotalCount),
			Active:      int(usage.Volumes.ActiveCount),
			SizeBytes:   positive(usage.Volumes.TotalSize),
			Reclaimable: positive(usage.Volumes.Reclaimable),
		},
		BuildCache: models.DiskUsageCategory{
			Count:       int(usage.BuildCache.TotalCount),
			Active:      int(usage.BuildCache.ActiveCount),
			SizeBytes:   positive(usage.BuildCache.TotalSize),
			Reclaimable: positive(usage.BuildCache.Reclaimable),
		},
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

func mapContainerSummary(raw container.Summary) models.ContainerSummary {
	labels := raw.Labels
	state := normalizeContainerState(string(raw.State))
	health := healthFromStatusText(raw.Status)
	return models.ContainerSummary{
		ID:        raw.ID,
		Name:      firstContainerName(raw.Names, raw.ID),
		Image:     raw.Image,
		ImageID:   raw.ImageID,
		Status:    state,
		State:     state,
		Health:    health,
		ProjectID: labels[composeProjectLabel],
		Service:   labels[composeServiceLabel],
		Ports:     mapContainerPorts(raw.Ports),
		CreatedAt: unixTime(raw.Created),
	}
}

func mapContainerDetail(raw container.InspectResponse) *models.ContainerDetail {
	summary := mapContainerInspectSummary(raw)
	detail := &models.ContainerDetail{
		Summary: summary,
		Labels:  map[string]string{},
	}
	if raw.Config != nil {
		detail.Command = append([]string(nil), raw.Config.Cmd...)
		detail.Entrypoint = append([]string(nil), raw.Config.Entrypoint...)
		detail.Env = mapEnv(raw.Config.Env)
		detail.Labels = copyStringMap(raw.Config.Labels)
		detail.WorkingDir = raw.Config.WorkingDir
		detail.User = raw.Config.User
	}
	if raw.HostConfig != nil {
		detail.RestartPolicy = string(raw.HostConfig.RestartPolicy.Name)
	}
	detail.Mounts = mapMounts(raw.Mounts)
	detail.Networks = mapContainerNetworks(raw.NetworkSettings)
	return detail
}

func mapContainerInspectSummary(raw container.InspectResponse) models.ContainerSummary {
	var labels map[string]string
	var image string
	if raw.Config != nil {
		labels = raw.Config.Labels
		image = raw.Config.Image
	}
	if image == "" {
		image = raw.Image
	}

	var stateText string
	var health models.HealthStatus
	if raw.State != nil {
		stateText = normalizeContainerState(string(raw.State.Status))
		health = mapHealthStatus(raw.State.Health)
	}
	if stateText == "" {
		stateText = "created"
	}
	if health == "" {
		health = models.HealthStatusUnknown
	}

	return models.ContainerSummary{
		ID:        raw.ID,
		Name:      strings.TrimPrefix(raw.Name, "/"),
		Image:     image,
		ImageID:   raw.Image,
		Status:    stateText,
		State:     stateText,
		Health:    health,
		ProjectID: labels[composeProjectLabel],
		Service:   labels[composeServiceLabel],
		Ports:     mapInspectPorts(raw.NetworkSettings),
		Restarts:  raw.RestartCount,
		CreatedAt: parseDockerTime(raw.Created),
		StartedAt: containerInspectStartedAt(raw.State),
	}
}

func mapImageSummary(raw image.Summary) models.ImageSummary {
	return models.ImageSummary{
		ID:          raw.ID,
		RepoTags:    sortedStrings(raw.RepoTags),
		RepoDigests: sortedStrings(raw.RepoDigests),
		SizeBytes:   positive(raw.Size),
		CreatedAt:   unixTime(raw.Created),
		InUse:       raw.Containers > 0,
	}
}

func mapImageDetail(raw image.InspectResponse) *models.ImageDetail {
	summary := models.ImageSummary{
		ID:          raw.ID,
		RepoTags:    sortedStrings(raw.RepoTags),
		RepoDigests: sortedStrings(raw.RepoDigests),
		SizeBytes:   positive(raw.Size),
		CreatedAt:   parseDockerTime(raw.Created),
	}
	detail := &models.ImageDetail{
		Summary:      summary,
		Architecture: raw.Architecture,
		OS:           raw.Os,
		Author:       raw.Author,
		Layers:       sortedStrings(raw.RootFS.Layers),
	}
	if raw.Config != nil {
		detail.Labels = copyStringMap(raw.Config.Labels)
	}
	return detail
}

func mapVolumeSummary(raw volume.Volume) models.VolumeSummary {
	summary := models.VolumeSummary{
		Name:       raw.Name,
		Driver:     raw.Driver,
		Mountpoint: raw.Mountpoint,
		Labels:     copyStringMap(raw.Labels),
	}
	if raw.UsageData != nil {
		summary.SizeBytes = positive(raw.UsageData.Size)
		summary.InUse = raw.UsageData.RefCount > 0
	}
	return summary
}

func mapVolumeDetail(raw volume.Volume, containers []models.ContainerSummary) *models.VolumeDetail {
	summary := mapVolumeSummary(raw)
	if len(containers) > 0 {
		summary.InUse = true
	}
	return &models.VolumeDetail{
		Summary:    summary,
		Options:    copyStringMap(raw.Options),
		Containers: containers,
		CreatedAt:  volumeCreatedAt(raw),
	}
}

func mapNetworkSummary(raw network.Summary) models.NetworkSummary {
	return mapNetwork(raw.Network, 0)
}

func mapNetworkInspectSummary(raw network.Inspect) models.NetworkSummary {
	return mapNetwork(raw.Network, len(raw.Containers))
}

func mapNetwork(raw network.Network, containerCount int) models.NetworkSummary {
	subnet, gateway := networkIPAM(raw)
	return models.NetworkSummary{
		ID:             raw.ID,
		Name:           raw.Name,
		Driver:         raw.Driver,
		Scope:          raw.Scope,
		Subnet:         subnet,
		Gateway:        gateway,
		ContainerCount: containerCount,
		Internal:       raw.Internal,
		Attachable:     raw.Attachable,
		Labels:         copyStringMap(raw.Labels),
	}
}

func mapNetworkDetail(raw network.Inspect, containers []models.ContainerSummary, rawJSON string) *models.NetworkDetail {
	subnet, gateway := networkIPAM(raw.Network)
	return &models.NetworkDetail{
		Summary:    mapNetworkInspectSummary(raw),
		Subnet:     subnet,
		Gateway:    gateway,
		Options:    copyStringMap(raw.Options),
		IPAM:       networkIPAMConfigs(raw.Network),
		Containers: containers,
		RawJSON:    rawJSON,
		CreatedAt:  raw.Created,
	}
}

func firstContainerName(names []string, id string) string {
	for _, name := range names {
		name = strings.TrimPrefix(strings.TrimSpace(name), "/")
		if name != "" {
			return name
		}
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func containerInspectStartedAt(state *container.State) time.Time {
	if state == nil {
		return time.Time{}
	}
	return parseDockerTime(state.StartedAt)
}

func normalizeContainerState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "created", "running", "paused", "restarting", "exited", "dead":
		return strings.ToLower(strings.TrimSpace(value))
	case "removing":
		return "exited"
	case "":
		return ""
	default:
		return "unknown"
	}
}

func healthFromStatusText(value string) models.HealthStatus {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "(healthy)"):
		return models.HealthStatusHealthy
	case strings.Contains(lower, "(unhealthy)"):
		return models.HealthStatusUnhealthy
	case strings.Contains(lower, "(health: starting)") || strings.Contains(lower, "(starting)"):
		return models.HealthStatusStarting
	default:
		return models.HealthStatusUnknown
	}
}

func mapHealthStatus(health *container.Health) models.HealthStatus {
	if health == nil {
		return models.HealthStatusUnknown
	}
	switch strings.ToLower(strings.TrimSpace(string(health.Status))) {
	case "healthy":
		return models.HealthStatusHealthy
	case "unhealthy":
		return models.HealthStatusUnhealthy
	case "starting":
		return models.HealthStatusStarting
	default:
		return models.HealthStatusUnknown
	}
}

func mapContainerPorts(ports []container.PortSummary) []models.PortBinding {
	out := make([]models.PortBinding, 0, len(ports))
	seen := map[string]struct{}{}
	for _, port := range ports {
		binding := models.PortBinding{
			HostIP:        portSummaryIP(port),
			ContainerPort: strconv.Itoa(int(port.PrivatePort)),
			Protocol:      port.Type,
		}
		if port.PublicPort > 0 {
			binding.HostPort = strconv.Itoa(int(port.PublicPort))
		}
		if binding.Protocol == "" {
			binding.Protocol = "tcp"
		}
		addPortBinding(&out, seen, binding)
	}
	sortPortBindings(out)
	return out
}

func portSummaryIP(port container.PortSummary) string {
	if !port.IP.IsValid() {
		return ""
	}
	return port.IP.String()
}

func mapInspectPorts(settings *container.NetworkSettings) []models.PortBinding {
	if settings == nil {
		return nil
	}
	return mapPortMap(settings.Ports)
}

func mapPortMap(portMap network.PortMap) []models.PortBinding {
	out := make([]models.PortBinding, 0, len(portMap))
	seen := map[string]struct{}{}
	for port, bindings := range portMap {
		protocol := string(port.Proto())
		if protocol == "" {
			protocol = "tcp"
		}
		if len(bindings) == 0 {
			addPortBinding(&out, seen, models.PortBinding{
				ContainerPort: port.Port(),
				Protocol:      protocol,
			})
			continue
		}
		for _, binding := range bindings {
			addPortBinding(&out, seen, models.PortBinding{
				HostIP:        validAddrString(binding.HostIP),
				HostPort:      binding.HostPort,
				ContainerPort: port.Port(),
				Protocol:      protocol,
			})
		}
	}
	sortPortBindings(out)
	return out
}

func addPortBinding(out *[]models.PortBinding, seen map[string]struct{}, binding models.PortBinding) {
	key := binding.HostIP + "|" + binding.HostPort + "|" + binding.ContainerPort + "|" + binding.Protocol
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*out = append(*out, binding)
}

func sortPortBindings(bindings []models.PortBinding) {
	sort.Slice(bindings, func(i, j int) bool {
		a, b := bindings[i], bindings[j]
		return a.ContainerPort+a.Protocol+a.HostIP+a.HostPort < b.ContainerPort+b.Protocol+b.HostIP+b.HostPort
	})
}

func mapEnv(values []string) []models.EnvVar {
	out := make([]models.EnvVar, 0, len(values))
	for _, raw := range values {
		name, value, _ := strings.Cut(raw, "=")
		if name == "" {
			continue
		}
		if isSecretKey(name) {
			value = "********"
		}
		out = append(out, models.EnvVar{Name: name, Value: value})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func isSecretKey(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{"pass", "password", "token", "secret", "key", "auth"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func mapMounts(mounts []container.MountPoint) []models.MountSpec {
	out := make([]models.MountSpec, 0, len(mounts))
	for _, mount := range mounts {
		out = append(out, models.MountSpec{
			Type:       string(mount.Type),
			Source:     mount.Source,
			Target:     mount.Destination,
			ReadOnly:   !mount.RW,
			VolumeName: mount.Name,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Target < out[j].Target
	})
	return out
}

func mapContainerNetworks(settings *container.NetworkSettings) []string {
	if settings == nil || len(settings.Networks) == 0 {
		return nil
	}
	names := make([]string, 0, len(settings.Networks))
	for name := range settings.Networks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func networkIPAM(raw network.Network) (string, string) {
	for _, cfg := range raw.IPAM.Config {
		if cfg.Subnet.IsValid() || cfg.Gateway.IsValid() {
			return validPrefixString(cfg.Subnet), validAddrString(cfg.Gateway)
		}
	}
	return "", ""
}

func networkIPAMConfigs(raw network.Network) []models.NetworkIPAMConfig {
	configs := make([]models.NetworkIPAMConfig, 0, len(raw.IPAM.Config))
	for _, cfg := range raw.IPAM.Config {
		configs = append(configs, models.NetworkIPAMConfig{
			Subnet:     validPrefixString(cfg.Subnet),
			Gateway:    validAddrString(cfg.Gateway),
			IPRange:    validPrefixString(cfg.IPRange),
			AuxAddress: stringifyAddresses(cfg.AuxAddress),
		})
	}
	return configs
}

func validAddrString(address netip.Addr) string {
	if !address.IsValid() {
		return ""
	}
	return address.String()
}

func validPrefixString(prefix netip.Prefix) string {
	if !prefix.IsValid() {
		return ""
	}
	return prefix.String()
}

func stringifyAddresses(values map[string]netip.Addr) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, address := range values {
		out[key] = validAddrString(address)
	}
	return out
}

func volumeCreatedAt(raw volume.Volume) time.Time {
	return parseDockerTime(raw.CreatedAt)
}

func unixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func parseDockerTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	maps.Copy(out, values)
	return out
}
