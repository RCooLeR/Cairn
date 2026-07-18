package services

import (
	"encoding/json"
	"strings"
	"testing"

	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/security"
)

func TestImportedServiceMetadataRetainsOnlyBuildArgNames(t *testing.T) {
	metadata := serviceConfigMetadata(composecore.ServiceConfig{BuildArgs: map[string]string{
		"TOKEN": "literal-secret-value",
		"BASE":  "private.example/base:latest",
	}})
	if _, exists := metadata["buildArgs"]; exists {
		t.Fatalf("legacy secret-valued buildArgs metadata remains: %#v", metadata)
	}
	if present, ok := metadata[security.BuildArgsPresentMetadataKey].(bool); !ok || !present {
		t.Fatalf("bounded build-argument presence metadata = %#v", metadata)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if strings.Contains(string(raw), "literal-secret-value") || strings.Contains(string(raw), "private.example") || strings.Contains(string(raw), "TOKEN") || strings.Contains(string(raw), "BASE") {
		t.Fatalf("build argument value leaked into metadata: %s", raw)
	}
}
