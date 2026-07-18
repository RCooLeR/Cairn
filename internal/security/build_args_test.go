package security

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildArgsPresentRetainsNoNamesOrValues(t *testing.T) {
	longSecretName := strings.Repeat("N", 64*1024)
	metadata := map[string]any{
		BuildArgsPresentMetadataKey: BuildArgsPresent(map[string]string{
			longSecretName: "literal-secret-value",
			"BASE":         "private.example/base:latest",
		}),
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	for _, forbidden := range []string{longSecretName, "BASE", "literal-secret-value", "private.example"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("argument-controlled data leaked into bounded metadata: %s", raw)
		}
	}
	if got := string(raw); got != `{"hasBuildArgs":true}` {
		t.Fatalf("bounded metadata = %s", got)
	}
}
