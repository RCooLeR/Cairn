package providers

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

type blockingDetectionProvider struct {
	fakeProvider
	gate  chan struct{}
	calls int
	done  chan struct{}
}

func (p *blockingDetectionProvider) Detect(ctx context.Context) (*models.ProviderStatus, error) {
	p.calls++
	defer func() {
		if p.done != nil {
			p.done <- struct{}{}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.gate:
		return &models.ProviderStatus{Problems: []models.ProviderProblem{{Message: "shared probe"}}}, nil
	}
}

func TestManagerCoalescesDetectionWithoutCachingCompletedResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &blockingDetectionProvider{fakeProvider: fakeProvider{id: "test"}, gate: make(chan struct{})}
		manager := NewManager(nil, nil, []PlatformProvider{provider})
		const callers = 24
		results := make(chan *models.ProviderStatus, callers)
		errs := make(chan error, callers)
		for range callers {
			go func() {
				status, err := manager.detectShared(context.Background(), provider)
				results <- status
				errs <- err
			}()
		}
		synctest.Wait()
		close(provider.gate)
		for range callers {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			result := <-results
			if result.Problems[0].Message != "shared probe" {
				t.Fatal("returned status was shared with another caller")
			}
			result.Problems[0].Message = "caller edited status"
		}
		if provider.calls != 1 {
			t.Fatalf("provider detections = %d, want 1 for %d concurrent callers", provider.calls, callers)
		}
		if _, err := manager.detectShared(context.Background(), provider); err != nil {
			t.Fatal(err)
		}
		if provider.calls != 2 {
			t.Fatalf("explicit refresh did not detect again: %d probes", provider.calls)
		}
	})
}

func TestManagerDetectionInitiatorCancellationPreservesOtherWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &blockingDetectionProvider{fakeProvider: fakeProvider{id: "test"}, gate: make(chan struct{})}
		manager := NewManager(nil, nil, []PlatformProvider{provider})
		ctx, cancel := context.WithCancel(context.Background())
		first := make(chan error, 1)
		go func() { _, err := manager.detectShared(ctx, provider); first <- err }()
		synctest.Wait()
		second := make(chan error, 1)
		go func() { _, err := manager.detectShared(context.Background(), provider); second <- err }()
		synctest.Wait()
		cancel()
		if err := <-first; !errors.Is(err, context.Canceled) {
			t.Fatalf("initiator error = %v", err)
		}
		close(provider.gate)
		if err := <-second; err != nil {
			t.Fatalf("another caller's cancellation interrupted active probe: %v", err)
		}
		if provider.calls != 1 {
			t.Fatalf("provider detections = %d, want 1", provider.calls)
		}
	})
}

func TestManagerDetectionCancelsProbeWhenLastWaiterLeaves(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &blockingDetectionProvider{
			fakeProvider: fakeProvider{id: "test"}, gate: make(chan struct{}), done: make(chan struct{}, 1),
		}
		manager := NewManager(nil, nil, []PlatformProvider{provider})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { _, err := manager.detectShared(ctx, provider); result <- err }()
		synctest.Wait()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled detection error = %v", err)
		}
		<-provider.done
		synctest.Wait()
		if len(manager.detections) != 0 {
			t.Fatal("cancelled probe remained in flight")
		}
	})
}

func TestManagerWSLDetectionRejectsResultsAfterDistroChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &blockedIPRunner{fakeRunner: newFakeRunner(), gate: make(chan struct{})}
		seedWSLDetectThroughDockerProbe(runner.fakeRunner)
		provider := NewWindowsWSL(WindowsWSLOptions{Distro: "Ubuntu", Runner: runner})
		manager := NewManager(nil, nil, []PlatformProvider{provider})
		result := make(chan error, 1)
		go func() {
			status, err := manager.detectShared(context.Background(), provider)
			if status != nil {
				err = errors.New("returned old distro health after retarget")
			}
			result <- err
		}()
		synctest.Wait()
		provider.SetDistro("Other")
		close(runner.gate)
		if err := <-result; !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("stale detection error = %v, want settings-changed conflict", err)
		}
		if got := provider.configuredDistro(); got != "Other" {
			t.Fatalf("old detection reset selected distro to %q", got)
		}
		runner.outputs[wslCommandName+" -l -v"] = "  NAME STATE VERSION\n  Other Running 1\n"
		status, err := manager.detectShared(context.Background(), provider)
		if err != nil {
			t.Fatal(err)
		}
		assertProblem(t, status.Problems, ProblemWSL2Required)
	})
}
