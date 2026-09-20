package providers

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

type blockedIPRunner struct {
	*fakeRunner
	gate chan struct{}
}

func (r *blockedIPRunner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (*CommandResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.gate:
		return r.fakeRunner.Run(ctx, timeout, name, args...)
	}
}

func TestWindowsWSLConcurrentForwardResolutionsShareProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &blockedIPRunner{fakeRunner: newFakeRunner(), gate: make(chan struct{})}
		key := wslCommandName + " -d cairn-dev -- sh -lc " + wslDefaultRouteIPCommand
		runner.outputs[key] = "172.19.124.14\n"
		provider := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-dev", Runner: runner})
		const clients = 24
		errs := make(chan error, clients)
		for range clients {
			go func() {
				ip, err := provider.backendIP(context.Background())
				if err == nil && ip != "172.19.124.14" {
					err = errors.New("unexpected backend IP " + ip)
				}
				errs <- err
			}()
		}
		synctest.Wait()
		close(runner.gate)
		for range clients {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		if calls := runner.counts[key]; calls != 1 {
			t.Fatalf("WSL IP probes = %d for %d concurrent forwarded connections, want 1", calls, clients)
		}
		provider.SetDistro("cairn-dev")
		if _, err := provider.backendIP(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls := runner.counts[key]; calls != 1 {
			t.Fatalf("reapplying unchanged settings discarded cached IP: %d probes", calls)
		}
	})
}

func TestWindowsWSLForwardResolutionWaitRespectsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &blockedIPRunner{fakeRunner: newFakeRunner(), gate: make(chan struct{})}
		provider := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-dev", Runner: runner})
		firstCtx, firstCancel := context.WithCancel(context.Background())
		first := make(chan error, 1)
		go func() {
			_, err := provider.backendIP(firstCtx)
			first <- err
		}()
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		second := make(chan error, 1)
		go func() {
			_, err := provider.backendIP(ctx)
			second <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-second; !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting resolver error = %v", err)
		}
		firstCancel()
		if err := <-first; !errors.Is(err, context.Canceled) {
			t.Fatalf("active resolver error = %v", err)
		}
	})
}

func TestWindowsWSLForwardDialBoundsHungWSLResolution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &blockedIPRunner{fakeRunner: newFakeRunner(), gate: make(chan struct{})}
		provider := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-dev", Runner: runner})
		started := time.Now()
		_, err := provider.DialStream(context.Background(), 8080)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("DialStream() error = %v, want bounded resolution timeout", err)
		}
		if elapsed := time.Since(started); elapsed > wslBackendPortDialTimeout {
			t.Fatalf("hung resolution lasted %v", elapsed)
		}
	})
}

func TestWindowsWSLBackendIPCacheDoesNotCrossDistros(t *testing.T) {
	runner := newFakeRunner()
	runner.outputs[wslCommandName+" -d first -- sh -lc "+wslDefaultRouteIPCommand] = "172.19.124.14\n"
	runner.outputs[wslCommandName+" -d second -- sh -lc "+wslDefaultRouteIPCommand] = "172.19.124.15\n"
	provider := NewWindowsWSL(WindowsWSLOptions{Distro: "first", Runner: runner})
	if _, err := provider.backendIP(context.Background()); err != nil {
		t.Fatal(err)
	}
	provider.SetDistro("second")
	if ip, err := provider.backendIP(context.Background()); err != nil || ip != "172.19.124.15" {
		t.Fatalf("new distro backend IP = %q, %v", ip, err)
	}
}
