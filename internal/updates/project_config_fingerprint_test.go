package updates

import (
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestProjectConfigurationFingerprintIsDeterministicAndIgnoresVolatileState(t *testing.T) {
	t.Parallel()
	project, services := fingerprintFixture()
	first, err := projectConfigurationFingerprint(project, services)
	if err != nil {
		t.Fatalf("projectConfigurationFingerprint(first) error = %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("fingerprint length = %d, want 64", len(first))
	}

	project.Status = models.ProjectStatusRunning
	project.Health = models.HealthStatusHealthy
	project.Pinned = !project.Pinned
	project.LastSeenAt = project.LastSeenAt.Add(time.Hour)
	services[0].Status = models.ProjectStatusStopped
	services[0].Health = models.HealthStatusUnhealthy
	services[0].ReplicasRunning = 17
	services[0].ReplicasTotal = 19
	services[0].LastSeenAt = services[0].LastSeenAt.Add(2 * time.Hour)
	services[0].Metadata = map[string]any{
		"profiles":       []any{"worker", "default"},
		"hasHealthcheck": true,
	}
	services[0], services[1] = services[1], services[0]

	second, err := projectConfigurationFingerprint(project, services)
	if err != nil {
		t.Fatalf("projectConfigurationFingerprint(second) error = %v", err)
	}
	if second != first {
		t.Fatalf("volatile/reordered fingerprint = %q, want %q", second, first)
	}
}

func TestProjectConfigurationFingerprintChangesForUpdateRelevantConfiguration(t *testing.T) {
	t.Parallel()
	project, services := fingerprintFixture()
	baseline, err := projectConfigurationFingerprint(project, services)
	if err != nil {
		t.Fatalf("projectConfigurationFingerprint(baseline) error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*store.ProjectRecord, []store.ServiceRecord)
	}{
		{
			name: "working directory",
			mutate: func(project *store.ProjectRecord, _ []store.ServiceRecord) {
				project.WorkingDir = `D:\different`
			},
		},
		{
			name: "compose file order",
			mutate: func(project *store.ProjectRecord, _ []store.ServiceRecord) {
				project.ComposeFiles = []string{"override.yaml", "compose.yaml"}
			},
		},
		{
			name: "service image",
			mutate: func(_ *store.ProjectRecord, services []store.ServiceRecord) {
				services[0].ImageRef = "example/web:2"
			},
		},
		{
			name: "build context",
			mutate: func(_ *store.ProjectRecord, services []store.ServiceRecord) {
				services[0].BuildContext = "./other"
			},
		},
		{
			name: "dockerfile",
			mutate: func(_ *store.ProjectRecord, services []store.ServiceRecord) {
				services[0].DockerfilePath = "Dockerfile.other"
			},
		},
		{
			name: "build target",
			mutate: func(_ *store.ProjectRecord, services []store.ServiceRecord) {
				services[0].BuildTarget = "debug"
			},
		},
		{
			name: "healthcheck metadata",
			mutate: func(_ *store.ProjectRecord, services []store.ServiceRecord) {
				services[0].Metadata["hasHealthcheck"] = false
			},
		},
		{
			name: "service set",
			mutate: func(_ *store.ProjectRecord, services []store.ServiceRecord) {
				services[1].Name = "renamed"
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			changedProject, changedServices := fingerprintFixture()
			testCase.mutate(&changedProject, changedServices)
			got, err := projectConfigurationFingerprint(changedProject, changedServices)
			if err != nil {
				t.Fatalf("projectConfigurationFingerprint() error = %v", err)
			}
			if got == baseline {
				t.Fatalf("fingerprint did not change from %q", baseline)
			}
		})
	}
}

func fingerprintFixture() (store.ProjectRecord, []store.ServiceRecord) {
	seenAt := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	projectID := "linux_native/fingerprint"
	project := store.ProjectRecord{
		ID:           projectID,
		ProviderID:   "linux_native",
		ContextName:  "default",
		Name:         "fingerprint",
		WorkingDir:   `D:\project`,
		ComposeFiles: []string{"compose.yaml", "override.yaml"},
		Status:       models.ProjectStatusStopped,
		Health:       models.HealthStatusUnknown,
		Source:       store.ProjectSourceImported,
		LastSeenAt:   seenAt,
	}
	services := []store.ServiceRecord{
		{
			ID:              projectID + "/web",
			ProjectID:       projectID,
			Name:            "web",
			ImageRef:        "example/web:1",
			BuildContext:    ".",
			DockerfilePath:  "Dockerfile",
			BuildTarget:     "runtime",
			Status:          models.ProjectStatusRunning,
			Health:          models.HealthStatusHealthy,
			ReplicasRunning: 1,
			ReplicasTotal:   1,
			Metadata: map[string]any{
				"hasHealthcheck": true,
				"profiles":       []any{"worker", "default"},
			},
			LastSeenAt: seenAt,
		},
		{
			ID:         projectID + "/db",
			ProjectID:  projectID,
			Name:       "db",
			ImageRef:   "postgres:17",
			Status:     models.ProjectStatusRunning,
			Health:     models.HealthStatusHealthy,
			LastSeenAt: seenAt,
		},
	}
	return project, services
}
