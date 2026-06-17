package docker

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
)

const (
	objectKindContainer = "container"
	objectKindImage     = "image"
	objectKindVolume    = "volume"
	objectKindNetwork   = "network"
)

type objectChange struct {
	kind string
	id   string
}

type volumeUsage struct {
	sizeBytes int64
	refCount  int64
}

func (c *Client) ListContainers(ctx context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	raw, err := api.ContainerList(callCtx, container.ListOptions{
		All:     opts.All,
		Filters: c.containerFilters(opts),
	})
	if err != nil {
		return nil, mapDockerError("list containers", err)
	}

	summaries := make([]models.ContainerSummary, 0, len(raw))
	records := make([]store.ContainerCacheRecord, 0, len(raw))
	for _, item := range raw {
		summary := mapContainerSummary(item)
		c.qualifyContainerSummary(&summary)
		summaries = append(summaries, summary)
		records = append(records, store.ContainerCacheRecord{
			Summary: summary,
			Labels:  copyStringMap(item.Labels),
		})
	}
	sortContainerSummaries(summaries)
	if err := c.saveContainers(ctx, records, isContainerInventorySnapshot(opts)); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (c *Client) GetContainer(ctx context.Context, id string) (*models.ContainerDetail, error) {
	raw, _, err := c.inspectContainer(ctx, id, false)
	if err != nil {
		return nil, err
	}
	detail := mapContainerDetail(raw)
	c.qualifyContainerSummary(&detail.Summary)
	if err := c.saveContainers(ctx, []store.ContainerCacheRecord{containerRecordFromInspect(raw, detail)}, false); err != nil {
		return nil, err
	}
	return detail, nil
}

func (c *Client) InspectContainerRaw(ctx context.Context, id string) (string, error) {
	_, raw, err := c.inspectContainer(ctx, id, false)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (c *Client) ListImages(ctx context.Context) ([]models.ImageSummary, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	raw, err := api.ImageList(callCtx, image.ListOptions{})
	if err != nil {
		return nil, mapDockerError("list images", err)
	}

	usedBy := c.imageUsedBy(ctx, api)
	summaries := make([]models.ImageSummary, 0, len(raw))
	records := make([]store.ImageCacheRecord, 0, len(raw))
	for _, item := range raw {
		summary := mapImageSummary(item)
		if users := usedBy[summary.ID]; len(users) > 0 {
			summary.InUse = true
		}
		summaries = append(summaries, summary)
		records = append(records, store.ImageCacheRecord{
			Summary:  summary,
			UsedBy:   usedBy[summary.ID],
			Dangling: imageDangling(summary.RepoTags),
		})
	}
	sortImageSummaries(summaries)
	if err := c.saveImages(ctx, records, true); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (c *Client) GetImage(ctx context.Context, id string) (*models.ImageDetail, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	raw, _, err := api.ImageInspectWithRaw(callCtx, id)
	if err != nil {
		return nil, mapDockerError("inspect image", err)
	}
	detail := mapImageDetail(raw)
	users := c.imageUsedBy(ctx, api)[detail.Summary.ID]
	detail.Summary.InUse = len(users) > 0
	if err := c.saveImages(ctx, []store.ImageCacheRecord{{
		Summary:  detail.Summary,
		UsedBy:   users,
		Dangling: imageDangling(detail.Summary.RepoTags),
	}}, false); err != nil {
		return nil, err
	}
	return detail, nil
}

func (c *Client) ListVolumes(ctx context.Context) ([]models.VolumeSummary, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	raw, err := api.VolumeList(callCtx, volume.ListOptions{})
	if err != nil {
		return nil, mapDockerError("list volumes", err)
	}

	usage := c.volumeUsageByName(ctx, api)
	usedBy := c.volumeUsedBy(ctx, api)
	summaries := make([]models.VolumeSummary, 0, len(raw.Volumes))
	records := make([]store.VolumeCacheRecord, 0, len(raw.Volumes))
	for _, item := range raw.Volumes {
		if item == nil {
			continue
		}
		summary := mapVolumeSummary(*item)
		if item.UsageData == nil {
			if usage, ok := usage[item.Name]; ok {
				summary.SizeBytes = usage.sizeBytes
				summary.InUse = usage.refCount > 0
			}
		}
		if users := usedBy[item.Name]; len(users) > 0 {
			summary.InUse = true
		}
		summaries = append(summaries, summary)
		records = append(records, store.VolumeCacheRecord{
			Summary:   summary,
			UsedBy:    usedBy[item.Name],
			CreatedAt: volumeCreatedAt(*item),
		})
	}
	sortVolumeSummaries(summaries)
	if err := c.saveVolumes(ctx, records, true); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (c *Client) GetVolume(ctx context.Context, name string) (*models.VolumeDetail, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	raw, _, err := api.VolumeInspectWithRaw(callCtx, name)
	if err != nil {
		return nil, mapDockerError("inspect volume", err)
	}
	containers := c.containersForVolume(ctx, api, raw.Name)
	detail := mapVolumeDetail(raw, containers)
	usedBy := containerIDs(containers)
	if err := c.saveVolumes(ctx, []store.VolumeCacheRecord{{
		Summary:   detail.Summary,
		UsedBy:    usedBy,
		CreatedAt: volumeCreatedAt(raw),
	}}, false); err != nil {
		return nil, err
	}
	return detail, nil
}

func (c *Client) ListNetworks(ctx context.Context) ([]models.NetworkSummary, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	raw, err := api.NetworkList(callCtx, network.ListOptions{})
	if err != nil {
		return nil, mapDockerError("list networks", err)
	}

	summaries := make([]models.NetworkSummary, 0, len(raw))
	records := make([]store.NetworkCacheRecord, 0, len(raw))
	for _, item := range raw {
		summary := mapNetworkSummary(item)
		subnet, gateway := networkIPAM(item)
		summaries = append(summaries, summary)
		records = append(records, store.NetworkCacheRecord{
			Summary:    summary,
			Subnet:     subnet,
			Gateway:    gateway,
			Containers: networkContainerIDs(item),
		})
	}
	sortNetworkSummaries(summaries)
	if err := c.saveNetworks(ctx, records, true); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (c *Client) GetNetwork(ctx context.Context, id string) (*models.NetworkDetail, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	raw, body, err := api.NetworkInspectWithRaw(callCtx, id, network.InspectOptions{})
	if err != nil {
		return nil, mapDockerError("inspect network", err)
	}
	containers := c.containersForNetwork(ctx, api, raw)
	rawJSON := strings.TrimSpace(string(body))
	if rawJSON == "" {
		if encoded, marshalErr := json.MarshalIndent(raw, "", "  "); marshalErr == nil {
			rawJSON = string(encoded)
		}
	}
	detail := mapNetworkDetail(raw, containers, rawJSON)
	if err := c.saveNetworks(ctx, []store.NetworkCacheRecord{{
		Summary:    detail.Summary,
		Subnet:     detail.Subnet,
		Gateway:    detail.Gateway,
		Containers: containerIDs(containers),
	}}, false); err != nil {
		return nil, err
	}
	return detail, nil
}

func (c *Client) Reconcile(ctx context.Context) error {
	var joined error
	_, err := c.ListContainers(ctx, models.ContainerListOptions{All: true})
	joined = errors.Join(joined, err)
	_, err = c.ListImages(ctx)
	joined = errors.Join(joined, err)
	_, err = c.ListVolumes(ctx)
	joined = errors.Join(joined, err)
	_, err = c.ListNetworks(ctx)
	joined = errors.Join(joined, err)
	if cache := c.objectCache(); cache != nil {
		err = cache.DeleteStale(ctx, c.providerID(), c.now().Add(-24*time.Hour))
		joined = errors.Join(joined, err)
	}
	return joined
}

func (c *Client) StartReconcileLoop(ctx context.Context) {
	go func() {
		_ = c.Reconcile(ctx)
		interval := c.reconcileEvery
		if interval <= 0 {
			interval = defaultReconcileEvery
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = c.Reconcile(ctx)
			}
		}
	}()
}

func (c *Client) StartObjectEventLoop(ctx context.Context) {
	changes := make(chan objectChange, 128)
	go c.objectEventLoop(ctx, changes)
	go c.objectChangePublisher(ctx, changes)
}

func (c *Client) inspectContainer(ctx context.Context, id string, getSize bool) (container.InspectResponse, []byte, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return container.InspectResponse{}, nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	raw, body, err := api.ContainerInspectWithRaw(callCtx, id, getSize)
	if err != nil {
		return container.InspectResponse{}, nil, mapDockerError("inspect container", err)
	}
	return raw, body, nil
}

func (c *Client) objectEventLoop(ctx context.Context, changes chan<- objectChange) {
	defer close(changes)

	var since string
	backoff := c.backoffMin
	if backoff <= 0 {
		backoff = defaultBackoffMin
	}
	for {
		api, err := c.ensureConnected(ctx)
		if err != nil {
			if !sleepContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, c.backoffMax)
			continue
		}
		backoff = c.backoffMin
		if backoff <= 0 {
			backoff = defaultBackoffMin
		}

		messages, errs := api.Events(ctx, events.ListOptions{
			Since: since,
			Filters: filters.NewArgs(
				filters.Arg("type", string(events.ContainerEventType)),
				filters.Arg("type", string(events.ImageEventType)),
				filters.Arg("type", string(events.VolumeEventType)),
				filters.Arg("type", string(events.NetworkEventType)),
			),
		})

		streamOK := true
		for streamOK {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-messages:
				if !ok {
					streamOK = false
					continue
				}
				if msg.Time > 0 {
					since = strconv.FormatInt(msg.Time, 10)
				}
				if change, ok := objectChangeFromEvent(msg); ok {
					select {
					case changes <- change:
					case <-ctx.Done():
						return
					}
				}
			case err, ok := <-errs:
				if !ok {
					streamOK = false
					continue
				}
				if err != nil {
					c.disconnect(mapDockerError("watch Docker events", err))
					streamOK = false
				}
			}
		}
		if !sleepContext(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff, c.backoffMax)
	}
}

func (c *Client) objectChangePublisher(ctx context.Context, changes <-chan objectChange) {
	window := c.eventBatch
	if window <= 0 {
		window = defaultEventBatchWindow
	}

	pending := map[string]map[string]struct{}{}
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	flush := func() {
		if len(pending) == 0 {
			return
		}
		for kind, ids := range pending {
			payload := ObjectsChangedPayload{Kind: kind, IDs: sortedSet(ids)}
			c.publish(bus.TopicObjectsChanged, payload)
			go c.reconcileKind(ctx, kind)
		}
		pending = map[string]map[string]struct{}{}
		if timer != nil {
			timer.Stop()
			timer = nil
			timerC = nil
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case change, ok := <-changes:
			if !ok {
				flush()
				return
			}
			if pending[change.kind] == nil {
				pending[change.kind] = map[string]struct{}{}
			}
			if change.id != "" {
				pending[change.kind][change.id] = struct{}{}
			}
			if timer == nil {
				timer = time.NewTimer(window)
				timerC = timer.C
			}
		case <-timerC:
			flush()
		}
	}
}

func (c *Client) reconcileKind(ctx context.Context, kind string) {
	reconcileCtx, cancel := context.WithTimeout(ctx, c.unaryTimeout)
	defer cancel()
	var err error
	switch kind {
	case objectKindContainer:
		_, err = c.ListContainers(reconcileCtx, models.ContainerListOptions{All: true})
	case objectKindImage:
		_, err = c.ListImages(reconcileCtx)
	case objectKindVolume:
		_, err = c.ListVolumes(reconcileCtx)
	case objectKindNetwork:
		_, err = c.ListNetworks(reconcileCtx)
	}
	_ = err
}

func objectChangeFromEvent(msg events.Message) (objectChange, bool) {
	kind := ""
	switch msg.Type {
	case events.ContainerEventType:
		kind = objectKindContainer
	case events.ImageEventType:
		kind = objectKindImage
	case events.VolumeEventType:
		kind = objectKindVolume
	case events.NetworkEventType:
		kind = objectKindNetwork
	default:
		return objectChange{}, false
	}

	id := strings.TrimSpace(msg.Actor.ID)
	if id == "" {
		id = strings.TrimSpace(msg.Actor.Attributes["name"])
	}
	if id == "" {
		id = strings.TrimSpace(msg.Actor.Attributes["image"])
	}
	return objectChange{kind: kind, id: id}, true
}

func (c *Client) containerFilters(opts models.ContainerListOptions) filters.Args {
	args := filters.NewArgs()
	if opts.ProjectID != "" {
		args.Add("label", composeProjectLabel+"="+composecore.ProjectNameFromID(c.providerID(), opts.ProjectID))
	}
	if opts.Service != "" {
		args.Add("label", composeServiceLabel+"="+opts.Service)
	}
	for key, value := range opts.Filters {
		if strings.TrimSpace(key) == "" {
			continue
		}
		args.Add(key, value)
	}
	return args
}

func (c *Client) qualifyContainerSummary(summary *models.ContainerSummary) {
	if summary == nil || summary.ProjectID == "" {
		return
	}
	summary.ProjectID = composecore.ProjectID(c.providerID(), summary.ProjectID)
}

func containerRecordFromInspect(raw container.InspectResponse, detail *models.ContainerDetail) store.ContainerCacheRecord {
	record := store.ContainerCacheRecord{
		Summary: detail.Summary,
		Labels:  detail.Labels,
	}
	base := raw.ContainerJSONBase
	if base != nil && base.State != nil {
		record.StartedAt = parseDockerTime(base.State.StartedAt)
	}
	return record
}

func (c *Client) imageUsedBy(ctx context.Context, api APIClient) map[string][]string {
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	containers, err := api.ContainerList(callCtx, container.ListOptions{All: true})
	if err != nil {
		return nil
	}
	usedBy := map[string][]string{}
	for _, item := range containers {
		if item.ImageID == "" {
			continue
		}
		usedBy[item.ImageID] = append(usedBy[item.ImageID], item.ID)
	}
	for id := range usedBy {
		sort.Strings(usedBy[id])
	}
	return usedBy
}

func (c *Client) volumeUsageByName(ctx context.Context, api APIClient) map[string]volumeUsage {
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	usage, err := api.DiskUsage(callCtx, dockertypes.DiskUsageOptions{})
	if err != nil {
		return nil
	}
	byName := map[string]volumeUsage{}
	for _, vol := range usage.Volumes {
		if vol == nil || vol.UsageData == nil {
			continue
		}
		byName[vol.Name] = volumeUsage{
			sizeBytes: positive(vol.UsageData.Size),
			refCount:  vol.UsageData.RefCount,
		}
	}
	return byName
}

func (c *Client) volumeUsedBy(ctx context.Context, api APIClient) map[string][]string {
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	containers, err := api.ContainerList(callCtx, container.ListOptions{All: true})
	if err != nil {
		return nil
	}
	usedBy := map[string][]string{}
	for _, item := range containers {
		for _, mount := range item.Mounts {
			if mount.Name == "" {
				continue
			}
			usedBy[mount.Name] = append(usedBy[mount.Name], item.ID)
		}
	}
	for name := range usedBy {
		sort.Strings(usedBy[name])
	}
	return usedBy
}

func (c *Client) containersForVolume(ctx context.Context, api APIClient, volumeName string) []models.ContainerSummary {
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	containers, err := api.ContainerList(callCtx, container.ListOptions{All: true})
	if err != nil {
		return nil
	}
	out := []models.ContainerSummary{}
	for _, item := range containers {
		for _, mount := range item.Mounts {
			if mount.Name == volumeName {
				summary := mapContainerSummary(item)
				c.qualifyContainerSummary(&summary)
				out = append(out, summary)
				break
			}
		}
	}
	sortContainerSummaries(out)
	return out
}

func (c *Client) containersForNetwork(ctx context.Context, api APIClient, nw network.Inspect) []models.ContainerSummary {
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	containers, err := api.ContainerList(callCtx, container.ListOptions{All: true})
	if err != nil {
		return nil
	}
	out := []models.ContainerSummary{}
	for _, item := range containers {
		if item.NetworkSettings == nil {
			continue
		}
		for name, endpoint := range item.NetworkSettings.Networks {
			if name == nw.Name || (endpoint != nil && endpoint.NetworkID == nw.ID) {
				summary := mapContainerSummary(item)
				applyNetworkEndpoint(&summary, name, endpoint)
				c.qualifyContainerSummary(&summary)
				out = append(out, summary)
				break
			}
		}
	}
	sortContainerSummaries(out)
	return out
}

func applyNetworkEndpoint(summary *models.ContainerSummary, name string, endpoint *network.EndpointSettings) {
	summary.NetworkName = name
	if endpoint == nil {
		return
	}
	summary.EndpointID = endpoint.EndpointID
	summary.IPv4Address = endpointAddress(endpoint.IPAddress, endpoint.IPPrefixLen)
	summary.IPv6Address = endpointAddress(endpoint.GlobalIPv6Address, endpoint.GlobalIPv6PrefixLen)
	summary.Gateway = firstNonEmpty(endpoint.Gateway, endpoint.IPv6Gateway)
	summary.MacAddress = endpoint.MacAddress
	summary.Aliases = sortedStrings(endpoint.Aliases)
}

func endpointAddress(address string, prefixLen int) string {
	address = strings.TrimSpace(address)
	if address == "" || strings.Contains(address, "/") || prefixLen <= 0 {
		return address
	}
	return address + "/" + strconv.Itoa(prefixLen)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func networkContainerIDs(raw network.Inspect) []string {
	ids := make([]string, 0, len(raw.Containers))
	for id := range raw.Containers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func containerIDs(containers []models.ContainerSummary) []string {
	ids := make([]string, 0, len(containers))
	for _, container := range containers {
		ids = append(ids, container.ID)
	}
	sort.Strings(ids)
	return ids
}

func imageDangling(tags []string) bool {
	if len(tags) == 0 {
		return true
	}
	for _, tag := range tags {
		if tag != "" && tag != "<none>:<none>" {
			return false
		}
	}
	return true
}

func (c *Client) saveContainers(ctx context.Context, records []store.ContainerCacheRecord, replace bool) error {
	cache := c.objectCache()
	if cache == nil {
		return nil
	}
	if replace {
		return cache.SaveContainersSnapshot(ctx, c.providerID(), records, c.now())
	}
	if len(records) == 0 {
		return nil
	}
	return cache.SaveContainers(ctx, c.providerID(), records, c.now())
}

func isContainerInventorySnapshot(opts models.ContainerListOptions) bool {
	return opts.All && opts.ProjectID == "" && opts.Service == "" && len(opts.Filters) == 0
}

func (c *Client) saveImages(ctx context.Context, records []store.ImageCacheRecord, replace bool) error {
	cache := c.objectCache()
	if cache == nil {
		return nil
	}
	if replace {
		return cache.SaveImagesSnapshot(ctx, c.providerID(), records, c.now())
	}
	if len(records) == 0 {
		return nil
	}
	return cache.SaveImages(ctx, c.providerID(), records, c.now())
}

func (c *Client) saveVolumes(ctx context.Context, records []store.VolumeCacheRecord, replace bool) error {
	cache := c.objectCache()
	if cache == nil {
		return nil
	}
	if replace {
		return cache.SaveVolumesSnapshot(ctx, c.providerID(), records, c.now())
	}
	if len(records) == 0 {
		return nil
	}
	return cache.SaveVolumes(ctx, c.providerID(), records, c.now())
}

func (c *Client) saveNetworks(ctx context.Context, records []store.NetworkCacheRecord, replace bool) error {
	cache := c.objectCache()
	if cache == nil {
		return nil
	}
	if replace {
		return cache.SaveNetworksSnapshot(ctx, c.providerID(), records, c.now())
	}
	if len(records) == 0 {
		return nil
	}
	return cache.SaveNetworks(ctx, c.providerID(), records, c.now())
}

func (c *Client) objectCache() *store.ObjectCacheRepository {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cache
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = defaultBackoffMin
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextBackoff(current, maxBackoff time.Duration) time.Duration {
	if maxBackoff <= 0 {
		maxBackoff = defaultBackoffMax
	}
	next := current * 2
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortContainerSummaries(values []models.ContainerSummary) {
	sort.Slice(values, func(i, j int) bool {
		return values[i].Name < values[j].Name
	})
}

func sortImageSummaries(values []models.ImageSummary) {
	sort.Slice(values, func(i, j int) bool {
		left := firstString(values[i].RepoTags, values[i].ID)
		right := firstString(values[j].RepoTags, values[j].ID)
		return left < right
	})
}

func sortVolumeSummaries(values []models.VolumeSummary) {
	sort.Slice(values, func(i, j int) bool {
		return values[i].Name < values[j].Name
	})
}

func sortNetworkSummaries(values []models.NetworkSummary) {
	sort.Slice(values, func(i, j int) bool {
		return values[i].Name < values[j].Name
	})
}

func firstString(values []string, fallback string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return fallback
}
