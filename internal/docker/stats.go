package docker

import (
	"context"
	"io"
)

type StatsOptions struct {
	Stream  bool
	OneShot bool
}

type StatsReader struct {
	Body   io.ReadCloser
	OSType string
}

func (c *Client) ContainerStats(ctx context.Context, id string, opts StatsOptions) (*StatsReader, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}

	if opts.Stream {
		reader, err := api.ContainerStats(ctx, id, true)
		if err != nil {
			return nil, mapDockerError("stream container stats", err)
		}
		return &StatsReader{Body: reader.Body, OSType: reader.OSType}, nil
	}

	callCtx, cancel := c.withTimeout(ctx)
	reader, err := statsOnce(callCtx, api, id, opts.OneShot)
	if err != nil {
		cancel()
		return nil, mapDockerError("read container stats", err)
	}
	return &StatsReader{Body: cancelReadCloser{ReadCloser: reader.Body, cancel: cancel}, OSType: reader.OSType}, nil
}

func statsOnce(ctx context.Context, api APIClient, id string, oneShot bool) (statsResponseReader, error) {
	if oneShot {
		reader, err := api.ContainerStatsOneShot(ctx, id)
		return statsResponseReader{Body: reader.Body, OSType: reader.OSType}, err
	}
	reader, err := api.ContainerStats(ctx, id, false)
	return statsResponseReader{Body: reader.Body, OSType: reader.OSType}, err
}

type statsResponseReader struct {
	Body   io.ReadCloser
	OSType string
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
