package docker

import (
	"context"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	dockerclient "github.com/moby/moby/client"
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
	inspect, err := api.ContainerInspect(callCtx, id, dockerclient.ContainerInspectOptions{})
	if err != nil {
		return mapDockerError("inspect container", err)
	}
	if inspect.Container.State != nil && inspect.Container.State.Paused {
		if _, err := api.ContainerUnpause(callCtx, id, dockerclient.ContainerUnpauseOptions{}); err != nil {
			return mapDockerError("unpause container", err)
		}
		c.publishContainerChanged(id)
		return nil
	}
	if _, err := api.ContainerStart(callCtx, id, dockerclient.ContainerStartOptions{}); err != nil {
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
	if _, err := api.ContainerStop(callCtx, id, dockerclient.ContainerStopOptions{Timeout: stopTimeout(timeoutSeconds)}); err != nil {
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
	if _, err := api.ContainerRestart(callCtx, id, dockerclient.ContainerRestartOptions{Timeout: stopTimeout(timeoutSeconds)}); err != nil {
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
	if _, err := api.ContainerKill(callCtx, id, dockerclient.ContainerKillOptions{Signal: "KILL"}); err != nil {
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
	if _, err := api.ContainerRemove(callCtx, id, dockerclient.ContainerRemoveOptions{
		Force:         opts.Force,
		RemoveVolumes: opts.RemoveVolumes,
	}); err != nil {
		return mapDockerError("remove container", err)
	}
	c.publishContainerChanged(id)
	return nil
}

func stopTimeout(timeoutSeconds int) *int {
	if timeoutSeconds <= 0 {
		// Docker interprets nil Timeout as "use the daemon default" (usually 10s).
		return nil
	}
	return &timeoutSeconds
}

func (c *Client) publishContainerChanged(id string) {
	c.publish(bus.TopicObjectsChanged, ObjectsChangedPayload{Kind: objectKindContainer, IDs: []string{id}})
}
