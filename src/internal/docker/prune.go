package docker

import (
	"context"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	dockerclient "github.com/moby/moby/client"
)

func (c *Client) RemoveImage(ctx context.Context, id string, force bool) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return apperror.New(apperror.Conflict, "Image ID is required")
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if _, err := api.ImageRemove(callCtx, id, dockerclient.ImageRemoveOptions{Force: force, PruneChildren: true}); err != nil {
		return mapDockerError("remove image", err)
	}
	c.publishImageChanged(id)
	return nil
}

func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return apperror.New(apperror.Conflict, "Volume name is required")
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if _, err := api.VolumeRemove(callCtx, name, dockerclient.VolumeRemoveOptions{Force: force}); err != nil {
		return mapDockerError("remove volume", err)
	}
	c.publishVolumeChanged(name)
	return nil
}

func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return apperror.New(apperror.Conflict, "Network ID is required")
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if _, err := api.NetworkRemove(callCtx, id, dockerclient.NetworkRemoveOptions{}); err != nil {
		return mapDockerError("remove network", err)
	}
	c.publishNetworkChanged(id)
	return nil
}

func (c *Client) Prune(ctx context.Context, kind string) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	kind = normalizePruneKind(kind)
	if kind == "" {
		return apperror.New(apperror.Conflict, "Prune kind is required")
	}

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	switch kind {
	case "images":
		pruneFilters := dockerclient.Filters{}.Add("dangling", "false")
		if _, err := api.ImagePrune(callCtx, dockerclient.ImagePruneOptions{Filters: pruneFilters}); err != nil {
			return mapDockerError("prune images", err)
		}
		c.publishImageChanged("")
	case "containers":
		if _, err := api.ContainerPrune(callCtx, dockerclient.ContainerPruneOptions{}); err != nil {
			return mapDockerError("prune containers", err)
		}
		c.publishContainerChanged("")
	case "volumes":
		if _, err := api.VolumePrune(callCtx, dockerclient.VolumePruneOptions{}); err != nil {
			return mapDockerError("prune volumes", err)
		}
		c.publishVolumeChanged("")
	case "networks":
		if _, err := api.NetworkPrune(callCtx, dockerclient.NetworkPruneOptions{}); err != nil {
			return mapDockerError("prune networks", err)
		}
		c.publishNetworkChanged("")
	case "build-cache":
		if _, err := api.BuildCachePrune(callCtx, dockerclient.BuildCachePruneOptions{}); err != nil {
			return mapDockerError("prune build cache", err)
		}
		c.publishImageChanged("")
	case "system":
		for _, nested := range []string{"containers", "networks", "images", "build-cache"} {
			if err := c.Prune(callCtx, nested); err != nil {
				return err
			}
		}
	default:
		return apperror.New(apperror.Conflict, "Unsupported prune kind", apperror.WithDetail(kind))
	}
	return nil
}

func normalizePruneKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "image", "images":
		return "images"
	case "container", "containers":
		return "containers"
	case "volume", "volumes":
		return "volumes"
	case "network", "networks":
		return "networks"
	case "builder", "build", "build-cache", "build_cache":
		return "build-cache"
	case "system":
		return "system"
	default:
		return kind
	}
}
