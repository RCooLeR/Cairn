package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/store"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockermount "github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
)

const (
	defaultImageSearchLimit = 25
	maxImageSearchLimit     = 100
	restartPolicyNone       = "no"
)

var dockerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

type ImageProgressPayload struct {
	StreamID string `json:"streamID"`
	LayerID  string `json:"layerID,omitempty"`
	Status   string `json:"status"`
	Current  int64  `json:"current,omitempty"`
	Total    int64  `json:"total,omitempty"`
}

type JobProgressPayload struct {
	JobID   string   `json:"jobID"`
	Phase   string   `json:"phase"`
	Message string   `json:"message"`
	Pct     *float64 `json:"pct,omitempty"`
}

type JobDonePayload struct {
	JobID  string `json:"jobID"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (c *Client) PullImage(ctx context.Context, imageRef string) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	ref := strings.TrimSpace(imageRef)
	if ref == "" {
		return "", apperror.New(apperror.Conflict, "Image reference is required")
	}
	streamID := newJobID("pull")
	if err := c.pullImage(ctx, api, ref, streamID); err != nil {
		return streamID, err
	}
	c.publishImageChanged(ref)
	return streamID, nil
}

func (c *Client) TagImage(ctx context.Context, imageID string, newRef string) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	imageID = strings.TrimSpace(imageID)
	if imageID == "" {
		return apperror.New(apperror.Conflict, "Image ID is required")
	}
	ref, err := registrycore.NormalizeImageRef(newRef)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := api.ImageTag(callCtx, imageID, ref.Normalized); err != nil {
		return mapDockerError("tag image", err)
	}
	c.publishImageChanged(imageID)
	c.publishImageChanged(ref.Normalized)
	return nil
}

func (c *Client) PushImage(ctx context.Context, imageRef string) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	ref, err := registrycore.NormalizeImageRef(imageRef)
	if err != nil {
		return "", err
	}
	streamID := newJobID("push")
	auth, err := c.registryAuthForPush(ctx, ref.Registry)
	if err != nil {
		return streamID, err
	}
	if err := c.pushImage(ctx, api, ref.Normalized, ref.Registry, streamID, auth); err != nil {
		return streamID, err
	}
	c.publishImageChanged(ref.Normalized)
	return streamID, nil
}

func (c *Client) RunImage(ctx context.Context, req models.RunImageRequest) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	req.ImageRef = strings.TrimSpace(req.ImageRef)
	req.Name = strings.TrimSpace(req.Name)
	req.NetworkID = strings.TrimSpace(req.NetworkID)
	req.RestartPolicy = strings.TrimSpace(req.RestartPolicy)
	req.User = strings.TrimSpace(req.User)
	if req.ImageRef == "" {
		return "", apperror.New(apperror.Conflict, "Image reference is required")
	}
	if req.Name != "" && !dockerNamePattern.MatchString(req.Name) {
		return "", apperror.New(apperror.Conflict, "Container name must start with a letter or number and use only letters, numbers, '.', '_' or '-'")
	}
	if req.Name != "" {
		if err := c.validateContainerNameAvailable(ctx, api, req.Name); err != nil {
			return "", err
		}
	}
	if err := validateRunImagePorts(req.Ports); err != nil {
		return "", err
	}
	if err := c.ensureImagePresent(ctx, api, req.ImageRef, req.PullIfMissing); err != nil {
		return "", err
	}

	config, hostConfig, networkingConfig, err := runImageConfig(req)
	if err != nil {
		return "", err
	}
	created, err := api.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, req.Name)
	if err != nil {
		return "", mapDockerError("create container", err)
	}
	if err := api.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", mapDockerError("start container", err)
	}
	c.publishContainerChanged(created.ID)
	return created.ID, nil
}

func (c *Client) RenameContainer(ctx context.Context, id string, newName string) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(newName)
	if !dockerNamePattern.MatchString(name) {
		return apperror.New(apperror.Conflict, "Container name must start with a letter or number and use only letters, numbers, '.', '_' or '-'")
	}
	if err := c.validateContainerNameAvailable(ctx, api, name); err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := api.ContainerRename(callCtx, id, name); err != nil {
		return mapDockerError("rename container", err)
	}
	c.publishContainerChanged(id)
	return nil
}

func (c *Client) SaveImage(ctx context.Context, imageRefs []string, destPath string) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	refs := cleanImageRefs(imageRefs)
	if len(refs) == 0 {
		return "", apperror.New(apperror.Conflict, "At least one image reference is required")
	}
	path := strings.TrimSpace(destPath)
	if path == "" {
		return "", apperror.New(apperror.Conflict, "Destination path is required")
	}

	jobID := newJobID("save-image")
	c.publishJobProgress(jobID, "save", "Starting image save", nil)
	reader, err := api.ImageSave(ctx, refs)
	if err != nil {
		c.publishJobDone(jobID, "", err)
		return jobID, mapDockerError("save image", err)
	}
	defer func() {
		_ = reader.Close()
	}()

	file, err := os.Create(path)
	if err != nil {
		c.publishJobDone(jobID, "", err)
		return jobID, apperror.Wrap(apperror.Internal, "Create image archive failed", err)
	}
	defer func() {
		_ = file.Close()
	}()

	counter := &progressWriter{
		every: 1 << 20,
		onProgress: func(bytes int64) {
			c.publishJobProgress(jobID, "save", fmt.Sprintf("Saved %d bytes", bytes), nil)
		},
	}
	if _, err := io.Copy(file, io.TeeReader(reader, counter)); err != nil {
		c.publishJobDone(jobID, "", err)
		return jobID, mapDockerError("save image archive", err)
	}
	c.publishJobProgress(jobID, "save", "Image archive saved", floatPtr(100))
	c.publishJobDone(jobID, path, nil)
	return jobID, nil
}

func (c *Client) LoadImage(ctx context.Context, srcPath string) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(srcPath)
	if path == "" {
		return "", apperror.New(apperror.Conflict, "Source path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", apperror.Wrap(apperror.NotFound, "Open image archive failed", err)
	}
	defer func() {
		_ = file.Close()
	}()

	var total int64
	if stat, err := file.Stat(); err == nil {
		total = stat.Size()
	}
	jobID := newJobID("load-image")
	c.publishJobProgress(jobID, "load", "Starting image load", nil)
	reader := &progressReader{
		reader: file,
		total:  total,
		every:  1 << 20,
		onProgress: func(bytes int64, pct *float64) {
			c.publishJobProgress(jobID, "load", fmt.Sprintf("Read %d bytes", bytes), pct)
		},
	}
	response, err := api.ImageLoad(ctx, reader)
	if err != nil {
		c.publishJobDone(jobID, "", err)
		return jobID, mapDockerError("load image", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		c.publishJobDone(jobID, "", err)
		return jobID, mapDockerError("read image load response", err)
	}
	result := strings.TrimSpace(string(body))
	c.publishJobProgress(jobID, "load", "Image archive loaded", floatPtr(100))
	c.publishJobDone(jobID, result, nil)
	c.publishImageChanged("")
	return jobID, nil
}

func (c *Client) SearchHub(ctx context.Context, query string, limit int) ([]models.HubSearchResult, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	term := strings.TrimSpace(query)
	if term == "" {
		return []models.HubSearchResult{}, nil
	}
	if limit <= 0 {
		limit = defaultImageSearchLimit
	}
	if limit > maxImageSearchLimit {
		limit = maxImageSearchLimit
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	results, err := api.ImageSearch(callCtx, term, registry.SearchOptions{Limit: limit})
	if err != nil {
		return nil, mapDockerError("search Docker Hub", err)
	}
	out := make([]models.HubSearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, models.HubSearchResult{
			Name:        result.Name,
			Description: result.Description,
			Stars:       result.StarCount,
			Official:    result.IsOfficial,
			Automated:   false,
		})
	}
	return out, nil
}

func (c *Client) CreateVolume(ctx context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Driver = strings.TrimSpace(req.Driver)
	if req.Name == "" {
		return nil, apperror.New(apperror.Conflict, "Volume name is required")
	}
	if !dockerNamePattern.MatchString(req.Name) {
		return nil, apperror.New(apperror.Conflict, "Volume name must start with a letter or number and use only letters, numbers, '.', '_' or '-'")
	}
	if req.Driver == "" {
		req.Driver = "local"
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	raw, err := api.VolumeCreate(callCtx, volume.CreateOptions{
		Name:       req.Name,
		Driver:     req.Driver,
		DriverOpts: req.DriverOpts,
		Labels:     req.Labels,
	})
	if err != nil {
		return nil, mapDockerError("create volume", err)
	}
	summary := mapVolumeSummary(raw)
	if err := c.saveVolumes(ctx, []store.VolumeCacheRecord{{
		Summary:   summary,
		CreatedAt: volumeCreatedAt(raw),
	}}, false); err != nil {
		return nil, err
	}
	c.publishVolumeChanged(summary.Name)
	return &summary, nil
}

func (c *Client) CreateNetwork(ctx context.Context, req models.CreateNetworkRequest) (*models.NetworkSummary, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Driver = strings.TrimSpace(req.Driver)
	req.Subnet = strings.TrimSpace(req.Subnet)
	req.Gateway = strings.TrimSpace(req.Gateway)
	if req.Name == "" {
		return nil, apperror.New(apperror.Conflict, "Network name is required")
	}
	if !dockerNamePattern.MatchString(req.Name) {
		return nil, apperror.New(apperror.Conflict, "Network name must start with a letter or number and use only letters, numbers, '.', '_' or '-'")
	}
	if req.Driver == "" {
		req.Driver = "bridge"
	}
	ipam, err := createIPAM(req.Subnet, req.Gateway)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	created, err := api.NetworkCreate(callCtx, req.Name, network.CreateOptions{
		Driver:     req.Driver,
		IPAM:       ipam,
		Internal:   req.Internal,
		Attachable: req.Attachable,
		Labels:     req.Labels,
	})
	if err != nil {
		return nil, mapDockerError("create network", err)
	}
	raw, _, err := api.NetworkInspectWithRaw(callCtx, created.ID, network.InspectOptions{})
	if err != nil {
		return nil, mapDockerError("inspect created network", err)
	}
	summary := mapNetworkSummary(raw)
	subnet, gateway := networkIPAM(raw)
	if err := c.saveNetworks(ctx, []store.NetworkCacheRecord{{
		Summary:    summary,
		Subnet:     subnet,
		Gateway:    gateway,
		Containers: networkContainerIDs(raw),
	}}, false); err != nil {
		return nil, err
	}
	c.publishNetworkChanged(summary.ID)
	return &summary, nil
}

func (c *Client) ensureImagePresent(ctx context.Context, api APIClient, imageRef string, pullIfMissing bool) error {
	callCtx, cancel := c.withTimeout(ctx)
	_, _, err := api.ImageInspectWithRaw(callCtx, imageRef)
	cancel()
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return mapDockerError("inspect image", err)
	}
	if !pullIfMissing {
		return apperror.Wrap(apperror.NotFound, "Image is not present locally", err, apperror.WithDetail(imageRef))
	}
	return c.pullImage(ctx, api, imageRef, newJobID("pull"))
}

func (c *Client) validateContainerNameAvailable(ctx context.Context, api APIClient, name string) error {
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	_, _, err := api.ContainerInspectWithRaw(callCtx, name, false)
	if err == nil {
		return apperror.New(apperror.Conflict, "Container name is already in use", apperror.WithDetail(name))
	}
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return mapDockerError("inspect container name", err)
}

func (c *Client) pullImage(ctx context.Context, api APIClient, imageRef string, streamID string) error {
	c.publishImageProgress(bus.TopicImagePullProgress, streamID, "", "starting", 0, 0)
	reader, err := api.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return mapDockerError("pull image", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	decoder := json.NewDecoder(reader)
	for {
		var message struct {
			ID             string `json:"id"`
			Status         string `json:"status"`
			ErrorMessage   string `json:"error"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
		}
		if err := decoder.Decode(&message); err != nil {
			if err == io.EOF {
				break
			}
			return mapDockerError("read pull progress", err)
		}
		if message.ErrorMessage != "" {
			return apperror.New(apperror.RegistryUnreachable, "Pull image failed", apperror.WithDetail(message.ErrorMessage))
		}
		status := message.Status
		if status == "" {
			status = "progress"
		}
		c.publishImageProgress(bus.TopicImagePullProgress, streamID, message.ID, status, message.ProgressDetail.Current, message.ProgressDetail.Total)
	}
	c.publishImageProgress(bus.TopicImagePullProgress, streamID, "", "done", 0, 0)
	return nil
}

func (c *Client) registryAuthForPush(ctx context.Context, registry string) (string, error) {
	provider, ok := c.provider.(providers.PlatformProvider)
	if !ok {
		return "", nil
	}
	return registrycore.EncodeDockerAuthConfig(ctx, provider, registry)
}

func (c *Client) pushImage(ctx context.Context, api APIClient, imageRef string, registry string, streamID string, auth string) error {
	c.publishImageProgress(bus.TopicImagePushProgress, streamID, "", "starting", 0, 0)
	reader, err := api.ImagePush(ctx, imageRef, image.PushOptions{RegistryAuth: auth})
	if err != nil {
		return mapRegistryPushError(registry, err)
	}
	defer func() {
		_ = reader.Close()
	}()
	decoder := json.NewDecoder(reader)
	for {
		var message struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			ErrorMessage string `json:"error"`
			ErrorDetail  struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
		}
		if err := decoder.Decode(&message); err != nil {
			if err == io.EOF {
				break
			}
			return mapDockerError("read push progress", err)
		}
		if message.ErrorMessage != "" || message.ErrorDetail.Message != "" {
			detail := message.ErrorMessage
			if detail == "" {
				detail = message.ErrorDetail.Message
			}
			return registryPushStreamError(registry, detail)
		}
		status := message.Status
		if status == "" {
			status = "progress"
		}
		c.publishImageProgress(bus.TopicImagePushProgress, streamID, message.ID, status, message.ProgressDetail.Current, message.ProgressDetail.Total)
	}
	c.publishImageProgress(bus.TopicImagePushProgress, streamID, "", "done", 0, 0)
	return nil
}

func runImageConfig(req models.RunImageRequest) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}
	for _, mapping := range req.Ports {
		port, err := nat.NewPort(protocolOrDefault(mapping.Protocol), strings.TrimSpace(mapping.ContainerPort))
		if err != nil {
			return nil, nil, nil, apperror.Wrap(apperror.Conflict, "Invalid container port", err)
		}
		exposedPorts[port] = struct{}{}
		portBindings[port] = append(portBindings[port], nat.PortBinding{
			HostIP:   strings.TrimSpace(mapping.HostIP),
			HostPort: strings.TrimSpace(mapping.HostPort),
		})
	}

	mounts := make([]dockermount.Mount, 0, len(req.Volumes))
	volumes := map[string]struct{}{}
	for _, spec := range req.Volumes {
		mountSpec, err := mountFromSpec(spec)
		if err != nil {
			return nil, nil, nil, err
		}
		mounts = append(mounts, mountSpec)
		volumes[mountSpec.Target] = struct{}{}
	}

	hostConfig := &container.HostConfig{
		PortBindings: portBindings,
		Mounts:       mounts,
	}
	if req.NetworkID != "" {
		hostConfig.NetworkMode = container.NetworkMode(req.NetworkID)
	}
	if policy := restartPolicy(req.RestartPolicy); policy != "" {
		hostConfig.RestartPolicy = container.RestartPolicy{Name: policy}
	}
	networkingConfig := &network.NetworkingConfig{}
	if req.NetworkID != "" {
		networkingConfig.EndpointsConfig = map[string]*network.EndpointSettings{
			req.NetworkID: {},
		}
	}
	config := &container.Config{
		Image:        req.ImageRef,
		Env:          envList(req.Env),
		Cmd:          req.Command,
		User:         req.User,
		ExposedPorts: exposedPorts,
		Volumes:      volumes,
	}
	return config, hostConfig, networkingConfig, nil
}

func mountFromSpec(spec models.MountSpec) (dockermount.Mount, error) {
	target := strings.TrimSpace(spec.Target)
	if target == "" {
		return dockermount.Mount{}, apperror.New(apperror.Conflict, "Mount target is required")
	}
	mountType := strings.TrimSpace(spec.Type)
	if mountType == "" {
		mountType = "volume"
	}
	source := strings.TrimSpace(spec.Source)
	if mountType == "volume" && strings.TrimSpace(spec.VolumeName) != "" {
		source = strings.TrimSpace(spec.VolumeName)
	}
	switch mountType {
	case "volume":
		if source == "" {
			return dockermount.Mount{}, apperror.New(apperror.Conflict, "Volume source is required")
		}
		return dockermount.Mount{Type: dockermount.TypeVolume, Source: source, Target: target, ReadOnly: spec.ReadOnly}, nil
	case "bind":
		if source == "" {
			return dockermount.Mount{}, apperror.New(apperror.Conflict, "Bind source is required")
		}
		return dockermount.Mount{Type: dockermount.TypeBind, Source: source, Target: target, ReadOnly: spec.ReadOnly}, nil
	default:
		return dockermount.Mount{}, apperror.New(apperror.Conflict, "Unsupported mount type", apperror.WithDetail(mountType))
	}
}

func validateRunImagePorts(ports []models.PortMapping) error {
	for _, mapping := range ports {
		protocol := protocolOrDefault(mapping.Protocol)
		if protocol != "tcp" && protocol != "udp" && protocol != "sctp" {
			return apperror.New(apperror.Conflict, "Unsupported port protocol", apperror.WithDetail(protocol))
		}
		if err := validatePort("container port", mapping.ContainerPort, false); err != nil {
			return err
		}
		if err := validatePort("host port", mapping.HostPort, true); err != nil {
			return err
		}
	}
	return nil
}

func validatePort(label string, value string, allowEmpty bool) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if allowEmpty {
			return nil
		}
		return apperror.New(apperror.Conflict, label+" is required")
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 0 || port > 65535 || (!allowEmpty && port == 0) {
		return apperror.New(apperror.Conflict, "Invalid "+label, apperror.WithDetail(value))
	}
	return nil
}

func createIPAM(subnet string, gateway string) (*network.IPAM, error) {
	if subnet == "" && gateway == "" {
		return nil, nil
	}
	cfg := network.IPAMConfig{}
	if subnet != "" {
		if _, _, err := net.ParseCIDR(subnet); err != nil {
			return nil, apperror.Wrap(apperror.Conflict, "Invalid subnet CIDR", err)
		}
		cfg.Subnet = subnet
	}
	if gateway != "" {
		if net.ParseIP(gateway) == nil {
			return nil, apperror.New(apperror.Conflict, "Invalid gateway IP", apperror.WithDetail(gateway))
		}
		cfg.Gateway = gateway
	}
	return &network.IPAM{Config: []network.IPAMConfig{cfg}}, nil
}

func restartPolicy(value string) container.RestartPolicyMode {
	switch strings.TrimSpace(value) {
	case "", restartPolicyNone:
		return ""
	case "on-failure":
		return container.RestartPolicyOnFailure
	case "unless-stopped":
		return container.RestartPolicyUnlessStopped
	case "always":
		return container.RestartPolicyAlways
	default:
		return container.RestartPolicyMode(value)
	}
}

func envList(values []models.EnvVar) []string {
	byName := make(map[string]string, len(values))
	for _, item := range values {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		byName[name] = item.Value
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+byName[name])
	}
	return out
}

func protocolOrDefault(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return "tcp"
	}
	return protocol
}

func cleanImageRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func newJobID(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func (c *Client) publishImageProgress(topic bus.Topic, streamID string, layerID string, status string, current int64, total int64) {
	c.publish(topic, ImageProgressPayload{
		StreamID: streamID,
		LayerID:  layerID,
		Status:   status,
		Current:  current,
		Total:    total,
	})
}

func (c *Client) publishJobProgress(jobID string, phase string, message string, pct *float64) {
	c.publish(bus.TopicJobProgress, JobProgressPayload{
		JobID:   jobID,
		Phase:   phase,
		Message: message,
		Pct:     pct,
	})
}

func (c *Client) publishJobDone(jobID string, result string, actionErr error) {
	payload := JobDonePayload{JobID: jobID, Result: result}
	if actionErr != nil {
		payload.Error = actionErr.Error()
	}
	c.publish(bus.TopicJobDone, payload)
}

func (c *Client) publishImageChanged(id string) {
	c.publish(bus.TopicObjectsChanged, ObjectsChangedPayload{Kind: objectKindImage, IDs: []string{id}})
}

func (c *Client) publishVolumeChanged(name string) {
	c.publish(bus.TopicObjectsChanged, ObjectsChangedPayload{Kind: objectKindVolume, IDs: []string{name}})
}

func (c *Client) publishNetworkChanged(id string) {
	c.publish(bus.TopicObjectsChanged, ObjectsChangedPayload{Kind: objectKindNetwork, IDs: []string{id}})
}

func floatPtr(value float64) *float64 {
	return &value
}

func mapRegistryPushError(registry string, err error) error {
	if err == nil {
		return nil
	}
	if registryAuthMessage(err.Error()) {
		return apperror.Wrap(apperror.RegistryAuth, "Registry authentication failed", err, apperror.WithDetail(registry+": "+err.Error()))
	}
	if registryRateLimitMessage(err.Error()) {
		return apperror.Wrap(apperror.RegistryRateLimit, "Registry rate limit reached", err, apperror.WithDetail(registry+": "+err.Error()))
	}
	return mapDockerError("push image", err)
}

func registryPushStreamError(registry string, detail string) error {
	if registryAuthMessage(detail) {
		return apperror.New(apperror.RegistryAuth, "Registry authentication failed", apperror.WithDetail(registry+": "+detail))
	}
	if registryRateLimitMessage(detail) {
		return apperror.New(apperror.RegistryRateLimit, "Registry rate limit reached", apperror.WithDetail(registry+": "+detail))
	}
	return apperror.New(apperror.RegistryUnreachable, "Push image failed", apperror.WithDetail(detail))
}

func registryAuthMessage(detail string) bool {
	lower := strings.ToLower(detail)
	for _, marker := range []string{
		"authentication required",
		"authorization required",
		"unauthorized",
		"denied",
		"forbidden",
		"no basic auth credentials",
		"invalid username",
		"invalid password",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func registryRateLimitMessage(detail string) bool {
	lower := strings.ToLower(detail)
	return strings.Contains(lower, "too many requests") || strings.Contains(lower, "rate limit")
}

type progressWriter struct {
	written    int64
	last       int64
	every      int64
	onProgress func(int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.written += int64(len(p))
	if w.every <= 0 || w.written-w.last >= w.every {
		w.last = w.written
		w.onProgress(w.written)
	}
	return len(p), nil
}

type progressReader struct {
	reader     io.Reader
	read       int64
	last       int64
	total      int64
	every      int64
	onProgress func(int64, *float64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.read += int64(n)
		if r.every <= 0 || r.read-r.last >= r.every || err == io.EOF {
			r.last = r.read
			var pct *float64
			if r.total > 0 {
				value := float64(r.read) / float64(r.total) * 100
				pct = &value
			}
			r.onProgress(r.read, pct)
		}
	}
	return n, err
}
