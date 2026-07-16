package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
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

func TestLineageRepositoryScopedReplacePreservesForeignRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	repo := db.Lineage()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "unix:///var/run/docker.sock")
	project := ProjectRecord{
		ID:          "linux_native/demo",
		ProviderID:  scope.ProviderID(),
		ContextName: scope.ContextName(),
		Name:        "demo",
		LastSeenAt:  now,
	}
	if err := db.Projects().SaveSnapshot(ctx, scope, []ProjectRecord{project}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	foreign := LineageRecord{
		ProviderID:   "existing_context:foreign",
		ProjectID:    project.ID,
		ServiceName:  "foreign",
		Source:       models.LineageSourceUnknown,
		Confidence:   models.ConfidenceUnknown,
		DiscoveredAt: now,
		UpdatedAt:    now,
	}
	if err := repo.ReplaceProject(ctx, project.ID, []LineageRecord{foreign}); err != nil {
		t.Fatalf("seed foreign ReplaceProject() error = %v", err)
	}
	local := foreign
	local.ProviderID = scope.ProviderID()
	local.ContextName = scope.ContextName()
	local.ServiceName = "local"
	if err := repo.ReplaceProjectInScope(ctx, scope, project.ID, []LineageRecord{local}); err != nil {
		t.Fatalf("ReplaceProjectInScope() error = %v", err)
	}
	all, err := repo.ListProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListProject() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListProject() = %#v, want local and preserved foreign rows", all)
	}
	scoped, err := repo.ListProjectInScope(ctx, scope, project.ID)
	if err != nil {
		t.Fatalf("ListProjectInScope() error = %v", err)
	}
	if len(scoped) != 1 || scoped[0].ProviderID != scope.ProviderID() || scoped[0].ServiceName != "local" {
		t.Fatalf("ListProjectInScope() = %#v", scoped)
	}
	wrongScope := runtimescope.Must(scope.ProviderID(), "unix:///run/user/1000/docker.sock")
	if err := repo.ReplaceProjectInScope(ctx, wrongScope, project.ID, []LineageRecord{local}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ReplaceProjectInScope(wrong scope) error = %v, want sql.ErrNoRows", err)
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

func TestLineageRepositoryRejectsMalformedBuildArgs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO image_lineage (
			provider_id, project_id, service_id, service_name, service_image_ref,
			build_args_json, source, confidence, discovered_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "linux_native", "linux_native/demo", "svc", "web", "demo:web",
		"{malformed", string(models.LineageSourceComposeDockerfile), string(models.ConfidenceLow),
		formatTime(time.Now().UTC()), formatTime(time.Now().UTC())); err != nil {
		t.Fatalf("insert malformed lineage: %v", err)
	}

	records, err := db.Lineage().ListProject(ctx, "linux_native/demo")
	if err == nil {
		t.Fatalf("ListProject() records = %#v, want malformed build args error", records)
	}
	if !strings.Contains(err.Error(), "parse lineage build args") {
		t.Fatalf("ListProject() error = %v, want build args parse context", err)
	}
}
