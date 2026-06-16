package docker

import (
	"context"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/docker/docker/api/types/container"
)

func (c *Client) ProviderID() string {
	return c.providerID()
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	inspect, _, err := api.ContainerInspectWithRaw(callCtx, id, false)
	if err != nil {
		return mapDockerError("inspect container", err)
	}
	if inspect.State != nil && inspect.State.Paused {
		if err := api.ContainerUnpause(callCtx, id); err != nil {
			return mapDockerError("unpause container", err)
		}
		c.publishContainerChanged(id)
		return nil
	}
	if err := api.ContainerStart(callCtx, id, container.StartOptions{}); err != nil {
		return mapDockerError("start container", err)
	}
	c.publishContainerChanged(id)
	return nil
}

func (c *Client) StopContainer(ctx context.Context, id string, timeoutSeconds int) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := api.ContainerStop(callCtx, id, stopOptions(timeoutSeconds)); err != nil {
		return mapDockerError("stop container", err)
	}
	c.publishContainerChanged(id)
	return nil
}

func (c *Client) RestartContainer(ctx context.Context, id string, timeoutSeconds int) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := api.ContainerRestart(callCtx, id, stopOptions(timeoutSeconds)); err != nil {
		return mapDockerError("restart container", err)
	}
	c.publishContainerChanged(id)
	return nil
}

func (c *Client) KillContainer(ctx context.Context, id string) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := api.ContainerKill(callCtx, id, "KILL"); err != nil {
		return mapDockerError("kill container", err)
	}
	c.publishContainerChanged(id)
	return nil
}

func (c *Client) RemoveContainer(ctx context.Context, id string, opts models.RemoveContainerOptions) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := api.ContainerRemove(callCtx, id, container.RemoveOptions{
		Force:         opts.Force,
		RemoveVolumes: opts.RemoveVolumes,
	}); err != nil {
		return mapDockerError("remove container", err)
	}
	c.publishContainerChanged(id)
	return nil
}

func stopOptions(timeoutSeconds int) container.StopOptions {
	if timeoutSeconds <= 0 {
		// Docker interprets nil Timeout as "use the daemon default" (usually 10s).
		return container.StopOptions{}
	}
	return container.StopOptions{Timeout: &timeoutSeconds}
}

func (c *Client) publishContainerChanged(id string) {
	c.publish(bus.TopicObjectsChanged, ObjectsChangedPayload{Kind: objectKindContainer, IDs: []string{id}})
}
