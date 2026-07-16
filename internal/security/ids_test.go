package security

import (
	"errors"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

type failingEntropyReader struct {
	err error
}

func (r failingEntropyReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestIDSourceEntropyFailureIsTypedAndRecoverable(t *testing.T) {
	cause := errors.New("entropy unavailable")
	ids := NewIDSource(failingEntropyReader{err: cause})

	for name, generate := range map[string]func() (string, error){
		"plan":       ids.NewPlanID,
		"typed plan": func() (string, error) { return ids.NewTypedPlanID("object") },
		"job":        func() (string, error) { return ids.NewJobID("job") },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := generate()
			if got != "" {
				t.Fatalf("identifier = %q, want empty", got)
			}
			if !apperror.IsCode(err, apperror.Internal) {
				t.Fatalf("error = %v, want %s", err, apperror.Internal)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("error = %v, want wrapped entropy cause", err)
			}
		})
	}
}

func TestPlanConstructorEntropyFailureReturnsBeforePlanCreation(t *testing.T) {
	ids := NewIDSource(failingEntropyReader{err: errors.New("entropy unavailable")})
	now := time.Now().UTC()
	tests := map[string]func() error{
		"container": func() error {
			_, err := NewContainerActionPlan(
				ContainerActionKill,
				[]models.ContainerSummary{{ID: "container-id", Name: "api", State: "running"}},
				0,
				models.RemoveContainerOptions{},
				now,
				ids,
			)
			return err
		},
		"remove image": func() error {
			_, err := NewRemoveImagePlan(models.ImageSummary{ID: "sha256:image"}, false, now, ids)
			return err
		},
		"push image": func() error {
			_, err := NewPushImagePlan("registry.example/app:latest", now, ids)
			return err
		},
		"run image": func() error {
			_, err := NewRunImagePlan(models.RunImageRequest{ImageRef: "app:latest"}, models.RiskSafe, "docker run app:latest", "app", now, ids)
			return err
		},
		"remove volume": func() error {
			_, err := NewRemoveVolumePlan(models.VolumeSummary{Name: "data"}, false, now, ids)
			return err
		},
		"remove network": func() error {
			_, err := NewRemoveNetworkPlan(models.NetworkSummary{ID: "network-id", Name: "app"}, now, ids)
			return err
		},
		"prune": func() error {
			_, err := NewPrunePlan("images", now, ids)
			return err
		},
		"provider lifecycle": func() error {
			_, err := NewProviderLifecyclePlan(
				"restart",
				"linux_native",
				"Linux Native",
				"systemctl restart docker",
				models.RiskNeedsConfirmation,
				runtimescope.Must("linux_native", "unix:///var/run/docker.sock"),
				now,
				ids,
			)
			return err
		},
	}
	for name, construct := range tests {
		t.Run(name, func(t *testing.T) {
			if err := construct(); !apperror.IsCode(err, apperror.Internal) {
				t.Fatalf("constructor error = %v, want %s", err, apperror.Internal)
			}
		})
	}
}
