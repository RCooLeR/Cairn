package docker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestClientReconcilePublishesHealthOnlyChanges(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	events := bus.New()
	t.Cleanup(events.Close)
	api := newFakeAPI()
	seedFakeObjects(api)
	api.containers[0].State = "running"
	api.containers[0].Status = "Up 1 minute (health: starting)"
	client := New(fakeDockerProvider{}, events)
	client.SetObjectCache(db.Objects())
	client.factory = func(string) (APIClient, error) { return api, nil }
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	changed := events.Subscribe(ctx, bus.TopicObjectsChanged, 8)
	for _, status := range []string{"Up 2 minutes (healthy)", "Up 3 minutes (unhealthy)", "Up 4 minutes"} {
		api.containers[0].Status = status
		if err := client.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		payload := waitObjectsChangedKind(t, ctx, changed, objectKindContainer, fakeContainerID, time.Second)
		if len(payload.IDs) != 1 || payload.IDs[0] != fakeContainerID {
			t.Fatalf("health-only transition %q published %#v", status, payload)
		}
		if err := client.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case unexpected := <-changed:
			t.Fatalf("unchanged health published another event: %#v", unexpected)
		default:
		}
	}
}

func TestContainerCacheWritersPublishHealthBeforeReconcile(t *testing.T) {
	for _, method := range []string{"list", "inspect", "unrelated-image-error", "failed-cache-write"} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			events := bus.New()
			t.Cleanup(events.Close)
			api := newFakeAPI()
			seedFakeObjects(api)
			api.containers[0].State = "running"
			api.containers[0].Status = "Up 1 minute (health: starting)"
			client := New(fakeDockerProvider{}, events)
			client.SetObjectCache(db.Objects())
			client.factory = func(string) (APIClient, error) { return api, nil }
			t.Cleanup(func() { _ = client.Close() })
			if err := client.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			changed := events.Subscribe(ctx, bus.TopicObjectsChanged, 16)
			api.containers[0].Status = "Up 2 minutes (healthy)"
			read := func() error {
				if method == "inspect" {
					_, err := client.GetContainer(ctx, fakeContainerID)
					return err
				}
				_, err := client.ListContainers(ctx, models.ContainerListOptions{All: true})
				return err
			}
			if method == "failed-cache-write" {
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if method == "unrelated-image-error" {
				api.imageListErr = errors.New("image inventory unavailable")
				if err := client.Reconcile(ctx); err == nil {
					t.Fatal("missing image error")
				}
			} else if err := read(); err != nil {
				t.Fatal(err)
			}
			if method != "failed-cache-write" {
				waitObjectsChangedKind(t, ctx, changed, objectKindContainer, fakeContainerID, time.Second)
				if method != "unrelated-image-error" {
					if err := read(); err != nil {
						t.Fatal(err)
					}
					if method == "list" {
						if err := client.Reconcile(ctx); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			select {
			case unexpected := <-changed:
				t.Fatalf("unchanged/failed write or reconcile duplicated event: %#v", unexpected)
			default:
			}
		})
	}
}
