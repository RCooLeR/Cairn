package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/security"
)

type failingProviderEntropyReader struct{}

func (failingProviderEntropyReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

type installPlanFailureCase struct {
	plan      func() (*models.CommandPlan, error)
	planCount func() int
}

func TestInstallPlanEntropyFailureStoresNoProviderPlan(t *testing.T) {
	ids := security.NewIDSource(failingProviderEntropyReader{})
	linux := NewLinuxNative(LinuxNativeOptions{IDs: ids})
	macRunner := newFakeRunner()
	macRunner.paths[brewCommandName] = "/opt/homebrew/bin/brew"
	macOS := NewMacOSColima(MacOSColimaOptions{Runner: macRunner, IDs: ids})
	windows := NewWindowsWSL(WindowsWSLOptions{IDs: ids})
	tests := map[string]installPlanFailureCase{
		"linux": {
			plan: func() (*models.CommandPlan, error) {
				return linux.PlanInstall(context.Background(), models.InstallOptions{})
			},
			planCount: func() int { return len(linux.plans) },
		},
		"macOS Colima": {
			plan: func() (*models.CommandPlan, error) {
				return macOS.PlanInstall(context.Background(), models.InstallOptions{})
			},
			planCount: func() int { return len(macOS.installPlans) },
		},
		"Windows WSL": {
			plan: func() (*models.CommandPlan, error) {
				return windows.PlanInstall(context.Background(), models.InstallOptions{})
			},
			planCount: func() int { return len(windows.installPlans) },
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			plan, err := test.plan()
			if plan != nil {
				t.Fatalf("PlanInstall() plan = %#v, want nil", plan)
			}
			if !apperror.IsCode(err, apperror.Internal) {
				t.Fatalf("PlanInstall() error = %v, want %s", err, apperror.Internal)
			}
			if count := test.planCount(); count != 0 {
				t.Fatalf("stored install plans = %d, want 0", count)
			}
		})
	}
	if len(macRunner.counts) != 0 {
		t.Fatalf("provider commands ran despite ID failures: %#v", macRunner.counts)
	}
}
