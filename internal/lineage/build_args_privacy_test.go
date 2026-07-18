package lineage

import (
	"context"
	"testing"

	"github.com/RCooLeR/Cairn/internal/store"
)

func TestDiscoverServiceDoesNotReviveLegacyBuildArgValues(t *testing.T) {
	manager := &Manager{}
	record := manager.discoverService(
		context.Background(),
		store.ProjectRecord{ID: "project", ProviderID: "provider"},
		store.ServiceRecord{
			ID:   "service",
			Name: "app",
			Metadata: map[string]any{
				"buildArgs": map[string]any{"TOKEN": "literal-secret-value"},
			},
		},
		nil,
	)
	if len(record.BuildArgs) != 0 {
		t.Fatalf("legacy build argument values reached lineage: %#v", record.BuildArgs)
	}
}
