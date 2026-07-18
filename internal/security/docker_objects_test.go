package security

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestQuotePlanArgUsesShellQuoting(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "simple", value: "nginx:latest", want: "nginx:latest"},
		{name: "trim simple", value: "  alpine:3.20  ", want: "alpine:3.20"},
		{name: "empty", value: "  ", want: "''"},
		{name: "space", value: "my volume", want: "'my volume'"},
		{name: "single quote", value: "team's volume", want: `'team'"'"'s volume'`},
		{name: "shell metachar", value: "repo/$tag", want: "'repo/$tag'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quotePlanArg(tt.value); got != tt.want {
				t.Fatalf("quotePlanArg(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRemoveVolumePlanPreviewShellQuotesTarget(t *testing.T) {
	createdAt := time.Date(2026, 6, 16, 11, 30, 0, 123, time.UTC)
	plan, err := NewRemoveVolumePlan(models.VolumeDetail{
		Summary:   models.VolumeSummary{Name: "team's volume", Driver: "local"},
		CreatedAt: createdAt,
	}, runtimescope.Must("linux_native", "default"), false, time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewRemoveVolumePlan() error = %v", err)
	}
	if got, want := plan.Plan.Commands[0].Command, `docker volume rm 'team'"'"'s volume'`; got != want {
		t.Fatalf("preview command = %q, want %q", got, want)
	}
}

func TestVolumeIncarnationFingerprintIsCanonicalAndFailClosed(t *testing.T) {
	createdAt := time.Date(2026, 6, 16, 11, 30, 0, 123, time.UTC)
	first := models.VolumeDetail{
		Summary: models.VolumeSummary{
			Name:       "data",
			Driver:     "local",
			Mountpoint: "/var/lib/docker/volumes/data/_data",
			Labels:     map[string]string{"z": "last", "a": "first"},
		},
		Options:   map[string]string{"password": "not-user-visible", "type": "nfs"},
		CreatedAt: createdAt,
	}
	second := first
	second.Summary.Labels = map[string]string{"a": "first", "z": "last"}
	second.Options = map[string]string{"type": "nfs", "password": "not-user-visible"}

	firstFingerprint, err := VolumeIncarnationFingerprint(first)
	if err != nil {
		t.Fatalf("VolumeIncarnationFingerprint(first) error = %v", err)
	}
	secondFingerprint, err := VolumeIncarnationFingerprint(second)
	if err != nil {
		t.Fatalf("VolumeIncarnationFingerprint(second) error = %v", err)
	}
	if firstFingerprint != secondFingerprint {
		t.Fatalf("map insertion order changed fingerprint: %q != %q", firstFingerprint, secondFingerprint)
	}
	mutations := []struct {
		name   string
		mutate func(*models.VolumeDetail)
	}{
		{name: "name", mutate: func(volume *models.VolumeDetail) { volume.Summary.Name = "replacement" }},
		{name: "driver", mutate: func(volume *models.VolumeDetail) { volume.Summary.Driver = "other" }},
		{name: "mountpoint", mutate: func(volume *models.VolumeDetail) { volume.Summary.Mountpoint += "-other" }},
		{name: "labels", mutate: func(volume *models.VolumeDetail) {
			volume.Summary.Labels = map[string]string{"a": "changed", "z": "last"}
		}},
		{name: "options", mutate: func(volume *models.VolumeDetail) {
			volume.Options = map[string]string{"password": "changed", "type": "nfs"}
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			candidate := first
			test.mutate(&candidate)
			fingerprint, err := VolumeIncarnationFingerprint(candidate)
			if err != nil {
				t.Fatalf("VolumeIncarnationFingerprint(%s) error = %v", test.name, err)
			}
			if fingerprint == firstFingerprint {
				t.Fatalf("%s mutation did not change fingerprint", test.name)
			}
		})
	}

	second.CreatedAt = createdAt.Add(time.Nanosecond)
	replacementFingerprint, err := VolumeIncarnationFingerprint(second)
	if err != nil {
		t.Fatalf("VolumeIncarnationFingerprint(replacement) error = %v", err)
	}
	if replacementFingerprint == firstFingerprint {
		t.Fatalf("replacement fingerprint = %q, want a new incarnation", replacementFingerprint)
	}

	first.CreatedAt = time.Time{}
	if _, err := VolumeIncarnationFingerprint(first); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("VolumeIncarnationFingerprint(missing createdAt) error = %v, want conflict", err)
	}
	first.CreatedAt = createdAt
	if _, err := NewRemoveVolumePlan(first, runtimescope.Scope{}, false, createdAt); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("NewRemoveVolumePlan(missing scope) error = %v, want conflict", err)
	}
	plan, err := NewRemoveVolumePlan(first, runtimescope.Must("linux_native", "private-context"), false, createdAt)
	if err != nil {
		t.Fatalf("NewRemoveVolumePlan() error = %v", err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal(DockerObjectPlan) error = %v", err)
	}
	for _, privateValue := range []string{plan.TargetFingerprint, "private-context", "not-user-visible"} {
		if strings.Contains(string(encoded), privateValue) {
			t.Fatalf("serialized Docker object plan leaked private value %q: %s", privateValue, encoded)
		}
	}
}

func TestImageTargetUsesTagOnlyWhenUnambiguous(t *testing.T) {
	tests := []struct {
		name  string
		image models.ImageSummary
		want  string
	}{
		{
			name:  "single tag",
			image: models.ImageSummary{ID: "sha256:abc", RepoTags: []string{"app:latest"}},
			want:  "app:latest",
		},
		{
			name:  "multiple tags",
			image: models.ImageSummary{ID: "sha256:abc", RepoTags: []string{"app:latest", "app:stable"}},
			want:  "sha256:abc",
		},
		{
			name:  "dangling tag ignored",
			image: models.ImageSummary{ID: "sha256:abc", RepoTags: []string{"<none>:<none>"}},
			want:  "sha256:abc",
		},
		{
			name:  "blank tag ignored",
			image: models.ImageSummary{ID: "sha256:abc", RepoTags: []string{"  "}},
			want:  "sha256:abc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageTarget(tt.image); got != tt.want {
				t.Fatalf("imageTarget() = %q, want %q", got, tt.want)
			}
		})
	}
}
