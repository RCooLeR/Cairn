package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
)

type healthyDetectionProvider struct {
	fakeInstallProvider
}

func (p *healthyDetectionProvider) Detect(context.Context) (*models.ProviderStatus, error) {
	return &models.ProviderStatus{Healthy: true, Running: true}, nil
}

func TestProviderDetectionBindsInitialSelectionExactlyOnce(t *testing.T) {
	for _, all := range []bool{false, true} {
		name := "one"
		if all {
			name = "all"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db := openServiceTestStore(t)
			provider := &healthyDetectionProvider{}
			manager := providers.NewManager(db.Providers(), db.Settings(), []providers.PlatformProvider{provider})
			runtime := &fakeProviderRuntime{}
			service := &ProviderService{Manager: manager, Runtime: runtime}
			for range 2 {
				var err error
				if all {
					_, err = service.DetectAll(ctx)
				} else {
					_, err = service.Detect(ctx, provider.ID())
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if runtime.rebindCalls != 1 || runtime.lastProvider != provider {
				t.Fatalf("initial detection rebinds = %d, provider = %v; want one bind", runtime.rebindCalls, runtime.lastProvider)
			}
		})
	}
}

type completionAuditLifecycleRunner struct {
	fakeLifecycleRunner
	afterMutation func()
}

func (r *completionAuditLifecycleRunner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (*providers.CommandResult, error) {
	result, err := r.fakeLifecycleRunner.Run(ctx, timeout, name, args...)
	r.afterMutation()
	return result, err
}

type failingCompletionRuntime struct {
	fakeProviderRuntime
	err error
}

func (r *failingCompletionRuntime) RebindProvider(ctx context.Context, provider providers.PlatformProvider) (*models.ProviderSummary, error) {
	_, _ = r.fakeProviderRuntime.RebindProvider(ctx, provider)
	return nil, r.err
}

func TestProviderLifecycleSynchronizesRuntimeWhenCompletionAuditFails(t *testing.T) {
	for _, action := range []string{"start", "restart", "stop"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			db := openServiceTestStore(t)
			auditDB := openServiceTestStore(t)
			runner := &completionAuditLifecycleRunner{afterMutation: func() {
				if err := auditDB.Close(); err != nil {
					t.Fatal(err)
				}
			}}
			provider := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: "Ubuntu", Runner: runner})
			manager := providers.NewManager(db.Providers(), db.Settings(), []providers.PlatformProvider{provider})
			if err := manager.SetActiveProvider(ctx, provider.ID()); err != nil {
				t.Fatal(err)
			}
			runtimeErr := errors.New("runtime synchronization failed")
			runtime := &failingCompletionRuntime{err: runtimeErr}
			plans := security.NewProviderPlanStore(nil)
			t.Cleanup(plans.Close)
			service := &ProviderService{Manager: manager, Runtime: runtime, Audit: auditDB.Audit(), Plans: plans}
			var err error
			if action == "start" {
				err = service.Start(ctx, provider.ID())
			} else {
				plan, planErr := service.planProviderLifecycle(ctx, action, provider.ID())
				if planErr != nil {
					t.Fatal(planErr)
				}
				err = service.ApplyProviderPlan(ctx, plan.PlanID, "")
			}
			if !apperror.IsCode(err, apperror.Internal) || !errors.Is(err, runtimeErr) {
				t.Fatalf("action error = %v, want both audit and runtime errors", err)
			}
			if runtime.rebindCalls != 1 {
				t.Fatalf("runtime rebind calls = %d, want 1 despite audit failure", runtime.rebindCalls)
			}
			if action == "stop" && runtime.lastProvider != nil {
				t.Fatal("successful stop did not clear runtime")
			}
			if action != "stop" && runtime.lastProvider != provider {
				t.Fatal("successful start/restart did not bind provider")
			}
		})
	}
}

func TestStartingInactiveProviderDoesNotInterruptCurrentRuntime(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	runner := &fakeLifecycleRunner{}
	active := providers.NewLinuxNative(providers.LinuxNativeOptions{Runner: runner})
	inactive := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: "Ubuntu", Runner: runner})
	manager := providers.NewManager(db.Providers(), db.Settings(), []providers.PlatformProvider{active, inactive})
	if err := manager.SetActiveProvider(ctx, active.ID()); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeProviderRuntime{}
	service := &ProviderService{Manager: manager, Runtime: runtime}
	if err := service.Start(ctx, inactive.ID()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || runtime.rebindCalls != 0 || manager.ActiveProviderID(ctx) != active.ID() {
		t.Fatalf("inactive start affected current runtime: commands=%v rebinds=%d active=%q", runner.commands, runtime.rebindCalls, manager.ActiveProviderID(ctx))
	}
}
