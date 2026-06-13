package docker

import (
	"context"
	"io"
	"strconv"

	"github.com/docker/docker/api/types/container"
)

type LogOptions struct {
	Follow     bool
	Tail       int
	Since      string
	Until      string
	Timestamps bool
}

func (c *Client) ContainerLogs(ctx context.Context, id string, opts LogOptions) (io.ReadCloser, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	tail := "all"
	if opts.Tail >= 0 {
		tail = strconv.Itoa(opts.Tail)
	}
	reader, err := api.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Since:      opts.Since,
		Until:      opts.Until,
		Timestamps: opts.Timestamps,
		Follow:     opts.Follow,
		Tail:       tail,
	})
	if err != nil {
		return nil, mapDockerError("read container logs", err)
	}
	return reader, nil
}
