package store

import (
	"context"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestLineageRepositoryReplaceAndLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Lineage()
	now := time.Date(2026, 6, 13, 13, 0, 0, 0, time.UTC)
	record := LineageRecord{
		ProviderID:      "linux_native",
		ProjectID:       "linux_native/demo",
		ServiceID:       "linux_native/demo/web",
		ServiceName:     "web",
		ContainerID:     "container-1",
		ServiceImageRef: "demo/web:latest",
		ServiceImageID:  "sha256:image",
		BuildContext:    ".",
		DockerfilePath:  "Dockerfile",
		BuildTarget:     "runtime",
		DockerfileHash:  "sha256:dockerfile",
		BuildArgs:       map[string]string{"BASE": "alpine:3.20"},
		Source:          models.LineageSourceComposeDockerfile,
		Confidence:      models.ConfidenceMedium,
		DiscoveredAt:    now,
		UpdatedAt:       now,
		BaseRefs: []BaseImageRefRecord{
			{Name: "alpine", Tag: "3.20", ImageRef: "alpine:3.20", StageName: "builder", StageIndex: 0, Status: models.UpdateStatusUnknown},
			{Name: "nginx", Tag: "alpine", ImageRef: "nginx:alpine", StageName: "runtime", StageIndex: 1, IsFinalStageBase: true, Status: models.UpdateStatusUnknown},
		},
	}
	if err := repo.ReplaceProject(ctx, "linux_native/demo", []LineageRecord{record}); err != nil {
		t.Fatalf("ReplaceProject() error = %v", err)
	}
	list, err := repo.ListProject(ctx, "linux_native/demo")
	if err != nil {
		t.Fatalf("ListProject() error = %v", err)
	}
	if len(list) != 1 || len(list[0].BaseRefs) != 2 {
		t.Fatalf("ListProject() = %#v", list)
	}
	if model := list[0].ToModel(); model.BaseImage != "nginx:alpine" || model.BaseDigest != "" {
		t.Fatalf("model = %#v", model)
	}
	byService, err := repo.GetService(ctx, "linux_native/demo", "web")
	if err != nil {
		t.Fatalf("GetService() error = %v", err)
	}
	if byService.BuildArgs["BASE"] != "alpine:3.20" {
		t.Fatalf("BuildArgs = %#v", byService.BuildArgs)
	}
	byContainer, err := repo.GetContainer(ctx, "container-1")
	if err != nil {
		t.Fatalf("GetContainer() error = %v", err)
	}
	if byContainer.ServiceName != "web" {
		t.Fatalf("GetContainer() = %#v", byContainer)
	}

	replacement := record
	replacement.ContainerID = "container-2"
	replacement.BaseRefs = []BaseImageRefRecord{{Name: "busybox", Tag: "1.36", ImageRef: "busybox:1.36", IsFinalStageBase: true, Status: models.UpdateStatusUnknown}}
	if err := repo.ReplaceService(ctx, "linux_native/demo", "web", replacement); err != nil {
		t.Fatalf("ReplaceService() error = %v", err)
	}
	list, err = repo.ListProject(ctx, "linux_native/demo")
	if err != nil {
		t.Fatalf("ListProject() after replacement error = %v", err)
	}
	if len(list) != 1 || len(list[0].BaseRefs) != 1 || list[0].BaseRefs[0].ImageRef != "busybox:1.36" {
		t.Fatalf("replacement list = %#v", list)
	}
}

func TestLineageRepositoryReasons(t *testing.T) {
	t.Parallel()
	unknown := LineageRecord{Source: models.LineageSourceUnknown, Confidence: models.ConfidenceUnknown}
	if got := unknown.ToModel().Reason; got != "Base image: Unknown — this is a third-party registry image and no base metadata was found." {
		t.Fatalf("unknown reason = %q", got)
	}
	unparsed := LineageRecord{Source: models.LineageSourceComposeDockerfile, Confidence: models.ConfidenceUnknown}
	if got := unparsed.ToModel().Reason; got != "Base tracking unavailable — Dockerfile could not be parsed (see details)." {
		t.Fatalf("unparsed reason = %q", got)
	}
	scratch := LineageRecord{Source: models.LineageSourceComposeDockerfile, Confidence: models.ConfidenceMedium}
	if got := scratch.ToModel().Reason; got != "Dockerfile final stage uses scratch; no external base image is tracked." {
		t.Fatalf("scratch reason = %q", got)
	}
}
