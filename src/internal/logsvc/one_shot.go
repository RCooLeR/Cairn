package logsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
)

type oneShotOperation struct {
	manager        *Manager
	ctx            context.Context
	cancel         context.CancelFunc
	stopRootCancel func() bool

	mu               sync.Mutex
	activeLifecycles int
	finished         bool
	released         bool
}

func (m *Manager) beginOneShotOperation(ctx context.Context, timeout time.Duration) (*oneShotOperation, error) {
	m.ensureReady()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, wrapOperationContextError("Log operation canceled", err)
	}

	m.mu.Lock()
	if m.closed || m.rootCtx == nil || m.rootCtx.Err() != nil {
		m.mu.Unlock()
		return nil, apperror.New(apperror.ProviderNotReady, "Log runtime is stopping")
	}
	if m.activeOperations >= m.maxOperations {
		m.mu.Unlock()
		return nil, apperror.New(
			apperror.Conflict,
			"Log operation capacity has been reached",
			apperror.WithDetail(fmt.Sprintf("At most %d log fetch or export operations can run at once.", m.maxOperations)),
		)
	}
	rootCtx := m.rootCtx
	m.activeOperations++
	m.operations.Add(1)
	m.mu.Unlock()

	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	return &oneShotOperation{
		manager:        m,
		ctx:            operationCtx,
		cancel:         cancel,
		stopRootCancel: context.AfterFunc(rootCtx, cancel),
	}, nil
}

func (o *oneShotOperation) beginLifecycle() func() {
	o.mu.Lock()
	o.activeLifecycles++
	o.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			if o.activeLifecycles > 0 {
				o.activeLifecycles--
			}
			shouldRelease := o.finished && o.activeLifecycles == 0 && !o.released
			if shouldRelease {
				o.released = true
			}
			o.mu.Unlock()
			if shouldRelease {
				o.release()
			}
		})
	}
}

func (o *oneShotOperation) finish() {
	if o == nil {
		return
	}
	o.stopRootCancel()
	o.cancel()
	o.mu.Lock()
	o.finished = true
	shouldRelease := o.activeLifecycles == 0 && !o.released
	if shouldRelease {
		o.released = true
	}
	o.mu.Unlock()
	if shouldRelease {
		o.release()
	}
}

func (o *oneShotOperation) release() {
	o.manager.mu.Lock()
	if o.manager.activeOperations > 0 {
		o.manager.activeOperations--
	}
	o.manager.mu.Unlock()
	o.manager.operations.Done()
}

func wrapOperationContextError(message string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return apperror.Wrap(apperror.Timeout, message, err)
	}
	return apperror.Wrap(apperror.Cancelled, message, err)
}

func (m *Manager) reserveOneShotReader() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.rootCtx == nil || m.rootCtx.Err() != nil {
		return apperror.New(apperror.ProviderNotReady, "Log runtime is stopping")
	}
	if m.reservedReaders >= m.maxReaders {
		return logReaderCapacityError(m.maxReaders)
	}
	m.reservedReaders++
	return nil
}

func (m *Manager) releaseOneShotReader() {
	m.mu.Lock()
	if m.reservedReaders > 0 {
		m.reservedReaders--
	}
	m.mu.Unlock()
}

func (m *Manager) collectLogsBounded(
	operation *oneShotOperation,
	req models.LogStreamRequest,
	maxLines int,
	maxBytes int64,
) ([]models.LogLine, int64, bool, error) {
	containers, err := m.resolveContainersForOperation(operation, req)
	if err != nil {
		return nil, 0, false, err
	}
	if len(containers) > m.maxReadersPerStream {
		return nil, 0, false, apperror.New(
			apperror.Conflict,
			"Log scope matches too many containers",
			apperror.WithDetail(fmt.Sprintf("Select a narrower scope containing at most %d containers.", m.maxReadersPerStream)),
		)
	}
	lines := make([]models.LogLine, 0, min(maxLines, 1024))
	var retainedBytes int64
	truncated := false

	for _, container := range containers {
		if err := operation.ctx.Err(); err != nil {
			return nil, 0, false, wrapOperationContextError("Read logs canceled", err)
		}
		result, err := m.readContainerLogsBounded(
			operation,
			container,
			req,
			maxLines-len(lines),
			maxBytes-retainedBytes,
		)
		if err != nil {
			return nil, 0, false, err
		}
		lines = append(lines, result.lines...)
		retainedBytes += result.bytes
		if result.truncated {
			truncated = true
			break
		}
	}

	SortLines(lines)
	for index := range lines {
		lines[index].Sequence = uint64(index + 1)
	}
	return lines, retainedBytes, truncated, nil
}

type resolvedContainersResult struct {
	containers []models.ContainerSummary
	err        error
}

func (m *Manager) resolveContainersForOperation(
	operation *oneShotOperation,
	req models.LogStreamRequest,
) ([]models.ContainerSummary, error) {
	lifecycleDone := operation.beginLifecycle()
	resultCh := make(chan resolvedContainersResult, 1)
	go func() {
		containers, err := m.resolveContainers(operation.ctx, req, false)
		lifecycleDone()
		resultCh <- resolvedContainersResult{containers: containers, err: err}
	}()

	select {
	case result := <-resultCh:
		if err := operation.ctx.Err(); err != nil {
			return nil, wrapOperationContextError("Resolve log scope canceled", err)
		}
		return result.containers, result.err
	case <-operation.ctx.Done():
		return nil, wrapOperationContextError("Resolve log scope canceled", operation.ctx.Err())
	}
}

type boundedContainerLogResult struct {
	lines     []models.LogLine
	bytes     int64
	truncated bool
	err       error
}

func (m *Manager) readContainerLogsBounded(
	operation *oneShotOperation,
	container models.ContainerSummary,
	req models.LogStreamRequest,
	maxLines int,
	maxBytes int64,
) (boundedContainerLogResult, error) {
	if err := m.reserveOneShotReader(); err != nil {
		return boundedContainerLogResult{}, err
	}
	lifecycleDone := operation.beginLifecycle()
	resultCh := make(chan boundedContainerLogResult, 1)
	go func() {
		result := m.readContainerLogsWorker(operation, container, req, maxLines, maxBytes)
		m.releaseOneShotReader()
		lifecycleDone()
		resultCh <- result
	}()

	select {
	case result := <-resultCh:
		return result, result.err
	case <-operation.ctx.Done():
		return boundedContainerLogResult{}, wrapOperationContextError("Read logs canceled", operation.ctx.Err())
	}
}

func (m *Manager) readContainerLogsWorker(
	operation *oneShotOperation,
	container models.ContainerSummary,
	req models.LogStreamRequest,
	maxLines int,
	maxBytes int64,
) boundedContainerLogResult {
	reference := strings.TrimSpace(container.ID)
	if reference == "" {
		reference = strings.TrimSpace(container.Name)
	}
	if reference == "" {
		return boundedContainerLogResult{err: apperror.New(apperror.Internal, "Resolved log container has no identifier")}
	}
	reader, err := m.Docker.ContainerLogs(operation.ctx, reference, dockercore.LogOptions{
		Follow:     false,
		Tail:       req.Tail,
		Since:      req.Since,
		Timestamps: true,
	})
	if err != nil {
		if ctxErr := operation.ctx.Err(); ctxErr != nil {
			return boundedContainerLogResult{err: wrapOperationContextError("Read logs canceled", ctxErr)}
		}
		return boundedContainerLogResult{err: err}
	}
	if reader == nil {
		return boundedContainerLogResult{err: apperror.New(apperror.Internal, "Docker returned an empty log reader")}
	}

	managed := &managedLogReader{closer: reader, closeDone: make(chan struct{})}
	stopCancellationClose := context.AfterFunc(operation.ctx, managed.requestClose)
	result := boundedContainerLogResult{
		lines: make([]models.LogLine, 0, min(maxLines, 1024)),
	}
	readErr := ReadDockerLogStream(operation.ctx, reader, sourceFromContainer(container), m.now, func(line models.LogLine) bool {
		if operation.ctx.Err() != nil {
			return false
		}
		size := retainedLogLineBytes(line)
		if len(result.lines) >= maxLines || size > maxBytes-result.bytes {
			result.truncated = true
			return false
		}
		result.lines = append(result.lines, line)
		result.bytes += size
		return true
	})
	stopCancellationClose()
	closeErr := managed.waitClose()
	if ctxErr := operation.ctx.Err(); ctxErr != nil {
		result.err = wrapOperationContextError("Read logs canceled", ctxErr)
	} else if readErr != nil {
		result.err = readErr
	} else if closeErr != nil {
		result.err = apperror.Wrap(apperror.Internal, "Close log stream failed", closeErr)
	}
	return result
}
