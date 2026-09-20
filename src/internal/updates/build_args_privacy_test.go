package updates

import (
	"testing"

	"github.com/RCooLeR/Cairn/internal/store"
)

func TestUpdateSnapshotDoesNotReviveLegacyBuildArgValues(t *testing.T) {
	manager := &Manager{}
	snapshot := manager.snapshotForCheck(
		store.UpdateCheckRecord{},
		store.ServiceRecord{Metadata: map[string]any{
			"buildArgs": map[string]any{"TOKEN": "literal-secret-value"},
		}},
		store.LineageRecord{},
		nil,
	)
	if len(snapshot.BuildArgs) != 0 {
		t.Fatalf("legacy build argument values reached update snapshot: %#v", snapshot.BuildArgs)
	}
}
