package docker

import (
	"context"
	"io"
	"strconv"
	"strings"
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

func (c *Client) ContainerProcessPIDs(ctx context.Context, id string) ([]int, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	top, err := api.ContainerTop(callCtx, id, []string{"-eo", "pid"})
	if err != nil {
		return nil, mapDockerError("list container processes", err)
	}
	pidIndex := topPIDColumn(top.Titles)
	if pidIndex < 0 {
		return nil, nil
	}
	pids := make([]int, 0, len(top.Processes))
	for _, process := range top.Processes {
		if pidIndex >= len(process) {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(process[pidIndex]))
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func topPIDColumn(titles []string) int {
	for i, title := range titles {
		if strings.EqualFold(strings.TrimSpace(title), "PID") {
			return i
		}
	}
	return -1
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
