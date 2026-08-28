package docker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"
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

func newObjectReconcileGates() map[string]chan struct{} {
	return map[string]chan struct{}{
		objectKindContainer: make(chan struct{}, 1),
		objectKindImage:     make(chan struct{}, 1),
		objectKindVolume:    make(chan struct{}, 1),
		objectKindNetwork:   make(chan struct{}, 1),
	}
}

// objectReconcileScheduler bounds event-triggered inventory work to one
// worker and one queued dirty signal per object kind. A burst that arrives
// while a scan is running therefore produces at most one follow-up scan
// instead of one goroutine per event batch.
type objectReconcileScheduler struct {
	client   *Client
	ctx      context.Context
	cancel   context.CancelFunc
	requests map[string]chan struct{}
	wg       sync.WaitGroup
}

func newObjectReconcileScheduler(ctx context.Context, client *Client) *objectReconcileScheduler {
	workerCtx, cancel := context.WithCancel(ctx)
	scheduler := &objectReconcileScheduler{
		client:   client,
		ctx:      workerCtx,
		cancel:   cancel,
		requests: make(map[string]chan struct{}, 4),
	}
	for _, kind := range []string{objectKindContainer, objectKindImage, objectKindVolume, objectKindNetwork} {
		requests := make(chan struct{}, 1)
		scheduler.requests[kind] = requests
		scheduler.wg.Add(1)
		go scheduler.run(kind, requests)
	}
	return scheduler
}

func (s *objectReconcileScheduler) run(kind string, requests <-chan struct{}) {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-requests:
			if s.ctx.Err() != nil {
				return
			}
			s.client.reconcileKind(s.ctx, kind)
		}
	}
}

func (s *objectReconcileScheduler) request(kind string) {
	if s == nil || s.ctx.Err() != nil {
		return
	}
	requests := s.requests[kind]
	if requests == nil {
		return
	}
	select {
	case requests <- struct{}{}:
	case <-s.ctx.Done():
	default:
		// One request is already queued while this kind's sole worker is
		// active. That queued request is the dirty follow-up for the burst.
	}
}

func (s *objectReconcileScheduler) stop() {
	if s == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
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

	result, err := api.ContainerList(callCtx, dockerclient.ContainerListOptions{
		All:     opts.All,
		Filters: c.containerFilters(opts),
	})
	if err != nil {
		return nil, mapDockerError("list containers", err)
	}

	summaries := make([]models.ContainerSummary, 0, len(result.Items))
	records := make([]store.ContainerCacheRecord, 0, len(result.Items))
	for _, item := range result.Items {
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
		slog.Debug("cache containers failed", "error", err)
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
		slog.Debug("cache container failed", "container", id, "error", err)
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
	callCtx, cancel := c.withInventoryTimeout(ctx)
	defer cancel()

	result, err := api.ImageList(callCtx, dockerclient.ImageListOptions{})
	if err != nil {
		return nil, mapDockerError("list images", err)
	}

	usedBy := c.imageUsedBy(ctx, api)
	summaries := make([]models.ImageSummary, 0, len(result.Items))
	records := make([]store.ImageCacheRecord, 0, len(result.Items))
	for _, item := range result.Items {
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
		slog.Debug("cache images failed", "error", err)
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

	inspected, err := api.ImageInspect(callCtx, id)
	if err != nil {
		return nil, mapDockerError("inspect image", err)
	}
	detail := mapImageDetail(inspected.InspectResponse)
	users := c.imageUsedBy(ctx, api)[detail.Summary.ID]
	detail.Summary.InUse = len(users) > 0
	if err := c.saveImages(ctx, []store.ImageCacheRecord{{
		Summary:  detail.Summary,
		UsedBy:   users,
		Dangling: imageDangling(detail.Summary.RepoTags),
	}}, false); err != nil {
		slog.Debug("cache image failed", "image", id, "error", err)
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

	result, err := api.VolumeList(callCtx, dockerclient.VolumeListOptions{})
	if err != nil {
		return nil, mapDockerError("list volumes", err)
	}

	usage := c.volumeUsageByName(ctx, api)
	usedBy := c.volumeUsedBy(ctx, api)
	summaries := make([]models.VolumeSummary, 0, len(result.Items))
	records := make([]store.VolumeCacheRecord, 0, len(result.Items))
	for _, item := range result.Items {
		summary := mapVolumeSummary(item)
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
			CreatedAt: volumeCreatedAt(item),
		})
	}
	sortVolumeSummaries(summaries)
	if err := c.saveVolumes(ctx, records, true); err != nil {
		slog.Debug("cache volumes failed", "error", err)
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

	inspected, err := api.VolumeInspect(callCtx, name, dockerclient.VolumeInspectOptions{})
	if err != nil {
		return nil, mapDockerError("inspect volume", err)
	}
	raw := inspected.Volume
	containers := c.containersForVolume(ctx, api, raw.Name)
	detail := mapVolumeDetail(raw, containers)
	usedBy := containerIDs(containers)
	if err := c.saveVolumes(ctx, []store.VolumeCacheRecord{{
		Summary:   detail.Summary,
		UsedBy:    usedBy,
		CreatedAt: volumeCreatedAt(raw),
	}}, false); err != nil {
		slog.Debug("cache volume failed", "volume", name, "error", err)
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

	result, err := api.NetworkList(callCtx, dockerclient.NetworkListOptions{})
	if err != nil {
		return nil, mapDockerError("list networks", err)
	}
	usage, err := listNetworkContainerUsage(callCtx, api, len(result.Items) > 0)
	if err != nil {
		// Container usage is auxiliary network metadata. Keep network inventory
		// available when a second daemon request is denied or fails transiently,
		// consistent with image and volume usage enrichment.
		slog.Debug("list containers for network usage failed", "error", err)
		usage = nil
	}

	summaries := make([]models.NetworkSummary, 0, len(result.Items))
	records := make([]store.NetworkCacheRecord, 0, len(result.Items))
	for _, item := range result.Items {
		summary := mapNetworkSummary(item)
		containerIDs := networkUsageContainerIDs(usage, item.ID, item.Name)
		summary.ContainerCount = len(containerIDs)
		subnet, gateway := networkIPAM(item.Network)
		summaries = append(summaries, summary)
		records = append(records, store.NetworkCacheRecord{
			Summary:    summary,
			Subnet:     subnet,
			Gateway:    gateway,
			Containers: containerIDs,
		})
	}
	sortNetworkSummaries(summaries)
	if err := c.saveNetworks(ctx, records, true); err != nil {
		slog.Debug("cache networks failed", "error", err)
	}
	return summaries, nil
}

func listNetworkContainerUsage(ctx context.Context, api APIClient, needed bool) (map[string]map[string]struct{}, error) {
	usage := map[string]map[string]struct{}{}
	if !needed {
		return usage, nil
	}
	result, err := api.ContainerList(ctx, dockerclient.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	for _, item := range result.Items {
		if item.ID == "" || item.NetworkSettings == nil {
			continue
		}
		for name, endpoint := range item.NetworkSettings.Networks {
			addNetworkUsage(usage, name, item.ID)
			if endpoint != nil {
				addNetworkUsage(usage, endpoint.NetworkID, item.ID)
			}
		}
	}
	return usage, nil
}

func addNetworkUsage(usage map[string]map[string]struct{}, networkRef string, containerID string) {
	networkRef = strings.TrimSpace(networkRef)
	if networkRef == "" {
		return
	}
	containers := usage[networkRef]
	if containers == nil {
		containers = map[string]struct{}{}
		usage[networkRef] = containers
	}
	containers[containerID] = struct{}{}
}

func networkUsageContainerIDs(usage map[string]map[string]struct{}, networkRefs ...string) []string {
	containers := map[string]struct{}{}
	for _, ref := range networkRefs {
		for containerID := range usage[strings.TrimSpace(ref)] {
			containers[containerID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(containers))
	for containerID := range containers {
		ids = append(ids, containerID)
	}
	sort.Strings(ids)
	return ids
}

func (c *Client) GetNetwork(ctx context.Context, id string) (*models.NetworkDetail, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	inspected, err := api.NetworkInspect(callCtx, id, dockerclient.NetworkInspectOptions{})
	if err != nil {
		return nil, mapDockerError("inspect network", err)
	}
	raw := inspected.Network
	containers := c.containersForNetwork(ctx, api, raw)
	rawJSON := strings.TrimSpace(string(inspected.Raw))
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
		slog.Debug("cache network failed", "network", id, "error", err)
	}
	return detail, nil
}

func (c *Client) Reconcile(ctx context.Context) error {
	if _, err := c.ensureConnected(ctx); err != nil {
		return err
	}
	var joined error
	cache := c.objectCache()
	before, compareSnapshots, snapshotErr := c.objectSnapshot(ctx, cache)
	joined = errors.Join(joined, snapshotErr)
	for _, kind := range []string{objectKindContainer, objectKindImage, objectKindVolume, objectKindNetwork} {
		joined = errors.Join(joined, c.reconcileObjectKind(ctx, kind))
	}
	if compareSnapshots && joined == nil {
		after, _, err := c.objectSnapshot(ctx, cache)
		if err != nil {
			joined = errors.Join(joined, err)
		} else {
			c.publishSnapshotChanges(before, after)
		}
	}
	return joined
}

func (c *Client) StartReconcileLoop(ctx context.Context) {
	go func() {
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
	result, err := api.ContainerInspect(callCtx, id, dockerclient.ContainerInspectOptions{Size: getSize})
	if err != nil {
		return container.InspectResponse{}, nil, mapDockerError("inspect container", err)
	}
	return result.Container, result.Raw, nil
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
		if c.usesProcessBackedTransport() {
			// Process-backed transports cannot expose Docker's streaming event
			// API. StartReconcileLoop is their sole periodic inventory owner.
			return
		}
		backoff = c.backoffMin
		if backoff <= 0 {
			backoff = defaultBackoffMin
		}

		stream := api.Events(ctx, dockerclient.EventsListOptions{
			Since: since,
			Filters: dockerclient.Filters{}.Add("type",
				string(events.ContainerEventType),
				string(events.ImageEventType),
				string(events.VolumeEventType),
				string(events.NetworkEventType),
			),
		})
		messages, errs := stream.Messages, stream.Err

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
					slog.Debug("docker event stream ended", "error", err)
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

func (c *Client) objectSnapshot(ctx context.Context, cache *store.ObjectCacheRepository) (store.ObjectCacheSnapshot, bool, error) {
	if cache == nil {
		return store.ObjectCacheSnapshot{}, false, nil
	}
	scope, err := c.objectCacheScope()
	if err != nil {
		return store.ObjectCacheSnapshot{}, false, err
	}
	snapshot, err := cache.SnapshotKeysScoped(ctx, scope)
	if err != nil {
		return store.ObjectCacheSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (c *Client) publishSnapshotChanges(before store.ObjectCacheSnapshot, after store.ObjectCacheSnapshot) {
	for _, change := range []struct {
		kind   string
		before map[string]string
		after  map[string]string
	}{
		{kind: objectKindContainer, before: before.Containers, after: after.Containers},
		{kind: objectKindImage, before: before.Images, after: after.Images},
		{kind: objectKindVolume, before: before.Volumes, after: after.Volumes},
		{kind: objectKindNetwork, before: before.Networks, after: after.Networks},
	} {
		ids := changedSnapshotIDs(change.before, change.after)
		if len(ids) == 0 {
			continue
		}
		c.publish(bus.TopicObjectsChanged, ObjectsChangedPayload{Kind: change.kind, IDs: ids})
	}
}

func changedSnapshotIDs(before map[string]string, after map[string]string) []string {
	changed := map[string]struct{}{}
	for id, beforeValue := range before {
		if afterValue, ok := after[id]; !ok || afterValue != beforeValue {
			changed[id] = struct{}{}
		}
	}
	for id, afterValue := range after {
		if beforeValue, ok := before[id]; !ok || afterValue != beforeValue {
			changed[id] = struct{}{}
		}
	}
	return sortedSet(changed)
}

func (c *Client) objectChangePublisher(ctx context.Context, changes <-chan objectChange) {
	window := c.eventBatch
	if window <= 0 {
		window = defaultEventBatchWindow
	}
	scheduler := newObjectReconcileScheduler(ctx, c)
	defer scheduler.stop()

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
			scheduler.request(kind)
			c.publish(bus.TopicObjectsChanged, payload)
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
	timeout := c.unaryTimeout
	if kind == objectKindImage {
		timeout = max(c.unaryTimeout, defaultInventoryTimeout)
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	reconcileCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_ = c.reconcileObjectKind(reconcileCtx, kind)
}

func (c *Client) reconcileObjectKind(ctx context.Context, kind string) error {
	release, err := c.acquireObjectReconcile(ctx, kind)
	if err != nil {
		return err
	}
	defer release()

	switch kind {
	case objectKindContainer:
		_, err = c.ListContainers(ctx, models.ContainerListOptions{All: true})
	case objectKindImage:
		_, err = c.ListImages(ctx)
	case objectKindVolume:
		_, err = c.ListVolumes(ctx)
	case objectKindNetwork:
		_, err = c.ListNetworks(ctx)
	}
	return err
}

func (c *Client) acquireObjectReconcile(ctx context.Context, kind string) (func(), error) {
	gate := c.objectGates[kind]
	if gate == nil {
		return func() {}, nil
	}
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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

func (c *Client) containerFilters(opts models.ContainerListOptions) dockerclient.Filters {
	args := dockerclient.Filters{}
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
	if raw.State != nil {
		record.StartedAt = parseDockerTime(raw.State.StartedAt)
	}
	return record
}

func (c *Client) imageUsedBy(ctx context.Context, api APIClient) map[string][]string {
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	containers, err := api.ContainerList(callCtx, dockerclient.ContainerListOptions{All: true})
	if err != nil {
		return nil
	}
	usedBy := map[string][]string{}
	for _, item := range containers.Items {
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
	usage, err := api.DiskUsage(callCtx, dockerclient.DiskUsageOptions{Volumes: true, Verbose: true})
	if err != nil {
		return nil
	}
	byName := map[string]volumeUsage{}
	for _, vol := range usage.Volumes.Items {
		if vol.UsageData == nil {
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
	containers, err := api.ContainerList(callCtx, dockerclient.ContainerListOptions{All: true})
	if err != nil {
		return nil
	}
	usedBy := map[string][]string{}
	for _, item := range containers.Items {
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
	containers, err := api.ContainerList(callCtx, dockerclient.ContainerListOptions{All: true})
	if err != nil {
		return nil
	}
	out := []models.ContainerSummary{}
	for _, item := range containers.Items {
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
	containers, err := api.ContainerList(callCtx, dockerclient.ContainerListOptions{All: true})
	if err != nil {
		return nil
	}
	out := []models.ContainerSummary{}
	for _, item := range containers.Items {
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
	summary.Gateway = firstValidAddress(endpoint.Gateway, endpoint.IPv6Gateway)
	summary.MacAddress = endpoint.MacAddress.String()
	summary.Aliases = sortedStrings(endpoint.Aliases)
}

func endpointAddress(address netip.Addr, prefixLen int) string {
	if !address.IsValid() {
		return ""
	}
	if prefixLen <= 0 {
		return address.String()
	}
	prefix := netip.PrefixFrom(address, prefixLen)
	if !prefix.IsValid() {
		return address.String()
	}
	return prefix.String()
}

func firstValidAddress(values ...netip.Addr) string {
	for _, value := range values {
		if value.IsValid() {
			return value.String()
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
	scope, err := c.objectCacheScope()
	if err != nil {
		return err
	}
	if replace {
		return cache.SaveContainersSnapshotScoped(ctx, scope, records, c.now())
	}
	if len(records) == 0 {
		return nil
	}
	return cache.SaveContainersScoped(ctx, scope, records, c.now())
}

func isContainerInventorySnapshot(opts models.ContainerListOptions) bool {
	return opts.All && opts.ProjectID == "" && opts.Service == "" && len(opts.Filters) == 0
}

func (c *Client) saveImages(ctx context.Context, records []store.ImageCacheRecord, replace bool) error {
	cache := c.objectCache()
	if cache == nil {
		return nil
	}
	scope, err := c.objectCacheScope()
	if err != nil {
		return err
	}
	if replace {
		return cache.SaveImagesSnapshotScoped(ctx, scope, records, c.now())
	}
	if len(records) == 0 {
		return nil
	}
	return cache.SaveImagesScoped(ctx, scope, records, c.now())
}

func (c *Client) saveVolumes(ctx context.Context, records []store.VolumeCacheRecord, replace bool) error {
	cache := c.objectCache()
	if cache == nil {
		return nil
	}
	scope, err := c.objectCacheScope()
	if err != nil {
		return err
	}
	if replace {
		return cache.SaveVolumesSnapshotScoped(ctx, scope, records, c.now())
	}
	if len(records) == 0 {
		return nil
	}
	return cache.SaveVolumesScoped(ctx, scope, records, c.now())
}

func (c *Client) saveNetworks(ctx context.Context, records []store.NetworkCacheRecord, replace bool) error {
	cache := c.objectCache()
	if cache == nil {
		return nil
	}
	scope, err := c.objectCacheScope()
	if err != nil {
		return err
	}
	if replace {
		return cache.SaveNetworksSnapshotScoped(ctx, scope, records, c.now())
	}
	if len(records) == 0 {
		return nil
	}
	return cache.SaveNetworksScoped(ctx, scope, records, c.now())
}

func (c *Client) objectCache() *store.ObjectCacheRepository {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cache
}

func (c *Client) objectCacheScope() (runtimescope.Scope, error) {
	c.mu.RLock()
	runtimeScope := c.runtimeScope
	c.mu.RUnlock()
	if !runtimeScope.Valid() {
		return runtimescope.Scope{}, apperror.New(apperror.ProviderNotReady, "Docker object cache scope is not available")
	}
	return runtimeScope, nil
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
