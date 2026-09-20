package docker

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestLazyConnectionCoalescesConcurrentRequests(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := New(fakeDockerProvider{}, nil)
				gate := make(chan struct{})
				calls := 0
				api := newFakeAPI()
				failure := errors.New("WSL is starting")
				client.factory = func(string) (APIClient, error) {
					calls++
					<-gate
					if fail {
						return nil, failure
					}
					return api, nil
				}
				const requests = 24
				errs := make(chan error, requests)
				for range requests {
					go func() {
						_, err := client.ensureConnected(context.Background())
						errs <- err
					}()
				}
				synctest.Wait()
				close(gate)
				for range requests {
					err := <-errs
					if fail && !errors.Is(err, failure) {
						t.Fatalf("ensureConnected() error = %v, want shared failure", err)
					}
					if !fail && err != nil {
						t.Fatalf("ensureConnected() error = %v", err)
					}
				}
				if calls != 1 {
					t.Fatalf("SDK clients created = %d for %d concurrent callers, want 1", calls, requests)
				}
				if !fail && api.closed {
					t.Fatal("shared client was replaced/closed during concurrent startup")
				}
			})
		})
	}
}

func TestLazyConnectionWaitCanBeCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := New(fakeDockerProvider{}, nil)
		gate := make(chan struct{})
		client.factory = func(string) (APIClient, error) {
			<-gate
			return newFakeAPI(), nil
		}
		first := make(chan error, 1)
		go func() {
			_, err := client.ensureConnected(context.Background())
			first <- err
		}()
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		second := make(chan error, 1)
		go func() {
			_, err := client.ensureConnected(ctx)
			second <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-second; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter error = %v", err)
		}
		close(gate)
		if err := <-first; err != nil {
			t.Fatalf("initial connection interrupted by waiter: %v", err)
		}
	})
}

func TestLazyConnectionInitiatorCancellationPreservesWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := New(fakeDockerProvider{}, nil)
		gate := make(chan struct{})
		calls := 0
		client.factory = func(string) (APIClient, error) {
			calls++
			<-gate
			return newFakeAPI(), nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		first := make(chan error, 1)
		go func() { _, err := client.ensureConnected(ctx); first <- err }()
		synctest.Wait()
		second := make(chan error, 1)
		go func() { _, err := client.ensureConnected(context.Background()); second <- err }()
		synctest.Wait()
		cancel()
		if err := <-first; !errors.Is(err, context.Canceled) {
			t.Fatalf("initiator error = %v", err)
		}
		close(gate)
		if err := <-second; err != nil {
			t.Fatalf("initiator cancellation interrupted surviving caller: %v", err)
		}
		if calls != 1 {
			t.Fatalf("client creations = %d, want original shared attempt", calls)
		}
	})
}

type waitingConnectionProvider struct {
	fakeDockerProvider
	started chan struct{}
}

func (p waitingConnectionProvider) DockerHost(ctx context.Context) (string, error) {
	close(p.started)
	<-ctx.Done()
	return "", ctx.Err()
}

func TestCloseCancelsPendingLazyConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := waitingConnectionProvider{started: make(chan struct{})}
		client := New(provider, nil)
		factoryCalls := 0
		client.factory = func(string) (APIClient, error) {
			factoryCalls++
			return newFakeAPI(), nil
		}
		result := make(chan error, 1)
		go func() { _, err := client.ensureConnected(context.Background()); result <- err }()
		<-provider.started
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("pending connection error = %v, want shutdown cancellation", err)
		}
		if factoryCalls != 0 || client.api != nil {
			t.Fatal("pending setup published an API client after Close")
		}
	})
}

func TestLastLazyConnectionWaiterCancelsSetup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := waitingConnectionProvider{started: make(chan struct{})}
		client := New(provider, nil)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { _, err := client.ensureConnected(ctx); result <- err }()
		<-provider.started
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled connection error = %v", err)
		}
		synctest.Wait()
		if client.connecting != nil || client.api != nil {
			t.Fatal("orphaned connection attempt remained after last caller cancelled")
		}
	})
}
