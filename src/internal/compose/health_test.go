package compose

import (
	"testing"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestHealthAggregationUsesSeverityNotContainerOrder(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []models.HealthStatus
		want   models.HealthStatus
	}{
		{"starting before unhealthy", []models.HealthStatus{models.HealthStatusStarting, models.HealthStatusUnhealthy}, models.HealthStatusUnhealthy},
		{"unhealthy before starting", []models.HealthStatus{models.HealthStatusUnhealthy, models.HealthStatusStarting}, models.HealthStatusUnhealthy},
		{"healthy before starting", []models.HealthStatus{models.HealthStatusHealthy, models.HealthStatusStarting}, models.HealthStatusStarting},
		{"starting before healthy", []models.HealthStatus{models.HealthStatusStarting, models.HealthStatusHealthy}, models.HealthStatusStarting},
		{"unknown with healthy", []models.HealthStatus{models.HealthStatusUnknown, models.HealthStatusHealthy}, models.HealthStatusHealthy},
		{"empty", nil, models.HealthStatusUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := healthFromValues(test.values); got != test.want {
				t.Fatalf("healthFromValues() = %q, want %q", got, test.want)
			}
			strings := make([]string, len(test.values))
			for i, value := range test.values {
				strings[i] = string(value)
			}
			if got := aggregateHealth(strings); got != test.want {
				t.Fatalf("aggregateHealth() = %q, want %q", got, test.want)
			}
		})
	}
}
