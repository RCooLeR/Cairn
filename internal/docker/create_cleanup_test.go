package docker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"
)

type startFailingAPI struct {
	APIClient
	startErr       error
	removeErr      error
	inspectErr     error
	startFailures  int
	stateOnFailure container.ContainerState
	onStartFailure func()
	removed        []dockerclient.ContainerRemoveOptions
	removedID      []string
	removedSet     map[string]bool
}

func (a *startFailingAPI) ContainerStart(ctx context.Context, id string, opts dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error) {
	if a.startFailures > 0 {
		a.startFailures--
		if a.stateOnFailure != "" {
			result, err := a.APIClient.ContainerInspect(ctx, id, dockerclient.ContainerInspectOptions{})
			if err == nil && result.Container.State != nil {
				result.Container.State.Status = a.stateOnFailure
				result.Container.State.Running = a.stateOnFailure == container.StateRunning
			}
		}
		if a.onStartFailure != nil {
			a.onStartFailure()
		}
		return dockerclient.ContainerStartResult{}, a.startErr
	}
	delete(a.removedSet, id)
	return a.APIClient.ContainerStart(ctx, id, opts)
}

func (a *startFailingAPI) ContainerRemove(_ context.Context, id string, opts dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error) {
	a.removedID = append(a.removedID, id)
	a.removed = append(a.removed, opts)
	if a.removeErr != nil {
		if cerrdefs.IsNotFound(a.removeErr) {
			a.removedSet[id] = true
		}
		return dockerclient.ContainerRemoveResult{}, a.removeErr
	}
	a.removedSet[id] = true
	return dockerclient.ContainerRemoveResult{}, nil
}

func (a *startFailingAPI) ContainerInspect(ctx context.Context, id string, options dockerclient.ContainerInspectOptions) (dockerclient.ContainerInspectResult, error) {
	if a.removedSet[id] {
		return dockerclient.ContainerInspectResult{}, cerrdefs.ErrNotFound.WithMessage("container already removed")
	}
	if a.inspectErr != nil {
		return dockerclient.ContainerInspectResult{}, a.inspectErr
	}
	return a.APIClient.ContainerInspect(ctx, id, options)
}

func (a *startFailingAPI) ContainerList(ctx context.Context, opts dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error) {
	result, err := a.APIClient.ContainerList(ctx, opts)
	if err != nil {
		return dockerclient.ContainerListResult{}, err
	}
	filtered := result.Items[:0]
	for _, item := range result.Items {
		if !a.removedSet[item.ID] {
			filtered = append(filtered, item)
		}
	}
	result.Items = filtered
	return result, nil
}

func TestRunImageRemovesNewContainerWhenStartFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := newFakeAPI()
	seedFakeObjects(base)
	api := &startFailingAPI{
		APIClient:     base,
		startErr:      errors.New("start rejected"),
		startFailures: 1,
		removedSet:    map[string]bool{},
	}
	eventBus := bus.New()
	defer eventBus.Close()
	changed := eventBus.Subscribe(ctx, bus.TopicObjectsChanged, 4)
	client := New(fakeDockerProvider{}, eventBus)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	id, err := client.RunImage(ctx, models.RunImageRequest{ImageRef: "example/web:latest", Name: "cleanup-me"})
	if err == nil || id != "" {
		t.Fatalf("RunImage() = id %q, error %v; want start failure", id, err)
	}
	if len(api.removedID) != 1 || api.removedID[0] != "created-cleanup-me" {
		t.Fatalf("removed IDs = %#v", api.removedID)
	}
	if len(api.removed) != 1 || api.removed[0].Force || !api.removed[0].RemoveVolumes {
		t.Fatalf("remove options = %#v, want non-force cleanup with anonymous volumes", api.removed)
	}
	waitObjectsChangedKind(t, ctx, changed, objectKindContainer, "created-cleanup-me", time.Second)
	if _, inspectErr := api.ContainerInspect(ctx, "created-cleanup-me", dockerclient.ContainerInspectOptions{}); !cerrdefs.IsNotFound(inspectErr) {
		t.Fatalf("inspect after cleanup error = %v, want not found", inspectErr)
	}

	// The compensating removal must make an idempotent same-name retry possible.
	retryID, retryErr := client.RunImage(ctx, models.RunImageRequest{ImageRef: "example/web:latest", Name: "cleanup-me"})
	if retryErr != nil || retryID != "created-cleanup-me" {
		t.Fatalf("RunImage() retry = id %q, error %v", retryID, retryErr)
	}
}

func TestRunImagePreservesAmbiguousStartedContainer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := newFakeAPI()
	seedFakeObjects(base)
	api := &startFailingAPI{
		APIClient:      base,
		startErr:       context.DeadlineExceeded,
		startFailures:  1,
		stateOnFailure: container.StateRunning,
		removedSet:     map[string]bool{},
	}
	eventBus := bus.New()
	defer eventBus.Close()
	changed := eventBus.Subscribe(ctx, bus.TopicObjectsChanged, 4)
	client := New(fakeDockerProvider{}, eventBus)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	id, err := client.RunImage(ctx, models.RunImageRequest{ImageRef: "example/web:latest", Name: "ambiguous"})
	if !apperror.IsCode(err, apperror.Timeout) || id != "created-ambiguous" {
		t.Fatalf("RunImage() = id %q, error %v; want retained partial and %s", id, err, apperror.Timeout)
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Partial == nil || appErr.Partial.ID != id || appErr.Partial.State != "running" || !appErr.Partial.CleanupRequired {
		t.Fatalf("RunImage() error = %#v, want structured running partial", appErr)
	}
	if len(api.removedID) != 0 {
		t.Fatalf("ambiguous running container was removed: %#v", api.removedID)
	}
	waitObjectsChangedKind(t, ctx, changed, objectKindContainer, id, time.Second)
}

func TestRunImageReportsPartialWhenCreatedContainerCleanupFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := newFakeAPI()
	seedFakeObjects(base)
	api := &startFailingAPI{
		APIClient:     base,
		startErr:      errors.New("start rejected"),
		startFailures: 1,
		removeErr:     errors.New("cleanup unavailable"),
		removedSet:    map[string]bool{},
	}
	client := New(fakeDockerProvider{}, nil)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	id, err := client.RunImage(ctx, models.RunImageRequest{ImageRef: "example/web:latest", Name: "partial"})
	var appErr *apperror.AppError
	if id != "created-partial" || !errors.As(err, &appErr) || appErr.Partial == nil || appErr.Partial.ID != id || appErr.Partial.State != "created" {
		t.Fatalf("RunImage() = id %q, error %#v; want created partial", id, appErr)
	}
}

func TestRunImageTreatsCleanupNotFoundAsRolledBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := newFakeAPI()
	seedFakeObjects(base)
	api := &startFailingAPI{
		APIClient:     base,
		startErr:      errors.New("start rejected"),
		startFailures: 1,
		removeErr:     cerrdefs.ErrNotFound,
		removedSet:    map[string]bool{},
	}
	client := New(fakeDockerProvider{}, nil)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	id, err := client.RunImage(ctx, models.RunImageRequest{ImageRef: "example/web:latest", Name: "already-gone"})
	var appErr *apperror.AppError
	if id != "" || err == nil {
		t.Fatalf("RunImage() = id %q, error %v; want original start failure", id, err)
	}
	if errors.As(err, &appErr) && appErr.Partial != nil {
		t.Fatalf("RunImage() reported removed container as partial: %#v", appErr.Partial)
	}
}

func TestRunImageCleanupOutlivesCancelledCaller(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	base := newFakeAPI()
	seedFakeObjects(base)
	api := &startFailingAPI{
		APIClient:      base,
		startErr:       context.Canceled,
		startFailures:  1,
		onStartFailure: cancel,
		removedSet:     map[string]bool{},
	}
	client := New(fakeDockerProvider{}, nil)
	client.factory = func(string) (APIClient, error) { return api, nil }
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	id, err := client.RunImage(ctx, models.RunImageRequest{ImageRef: "example/web:latest", Name: "cancel-cleanup"})
	if id != "" || !apperror.IsCode(err, apperror.Cancelled) || len(api.removedID) != 1 {
		t.Fatalf("RunImage() = id %q, error %v, removed %#v", id, err, api.removedID)
	}
}
