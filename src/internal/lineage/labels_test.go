package lineage

import (
	"testing"
	"time"
)

func TestCairnBuildLabels(t *testing.T) {
	t.Parallel()
	labels := CairnBuildLabels(CairnBuildLabelInput{
		ProjectID:      "linux_native/apps",
		Service:        "api",
		ComposeFile:    "compose.yaml",
		DockerfilePath: "api/Dockerfile",
		BaseName:       "node:20-alpine",
		BaseDigest:     "sha256:aaa",
		BuildTime:      time.Date(2026, 6, 13, 14, 0, 0, 0, time.FixedZone("EET", 2*60*60)),
		Platform:       "linux/amd64",
	})
	want := map[string]string{
		"io.cairn.project":        "linux_native/apps",
		"io.cairn.service":        "api",
		"io.cairn.compose.file":   "compose.yaml",
		"io.cairn.dockerfile":     "api/Dockerfile",
		"io.cairn.base.name":      "node:20-alpine",
		"io.cairn.base.digest":    "sha256:aaa",
		"io.cairn.build.time":     "2026-06-13T12:00:00Z",
		"io.cairn.build.platform": "linux/amd64",
	}
	for key, value := range want {
		if labels[key] != value {
			t.Fatalf("%s = %q, want %q in %#v", key, labels[key], value, labels)
		}
	}
	if len(labels) != len(want) {
		t.Fatalf("labels = %#v, want %d entries", labels, len(want))
	}
}
