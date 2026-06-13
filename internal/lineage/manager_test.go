package lineage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

type expectedProjectFile struct {
	Project  string                 `json:"project"`
	Services []expectedServiceEntry `json:"services"`
	Lineage  []expectedLineageEntry `json:"lineage"`
}

type expectedServiceEntry struct {
	Name string `json:"name"`
}

type expectedLineageEntry struct {
	Service   string `json:"service"`
	BaseImage string `json:"baseImage"`
}

func TestManagerDiscoversGoldenProjectLineage(t *testing.T) {
	t.Parallel()
	for _, projectName := range []string{"build-simple", "build-multistage", "mixed-updates"} {
		projectName := projectName
		t.Run(projectName, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := openLineageStore(t)
			projectDir := filepath.Join("..", "..", "testdata", "projects", projectName)
			expected := readExpectedProject(t, projectDir)
			projectID := seedProjectFromCompose(t, ctx, db, projectDir, expected.Project)
			manager := NewManager(db.Projects(), db.Lineage(), db.Objects(), nil)
			got, err := manager.DiscoverProjectLineage(ctx, projectID)
			if err != nil {
				t.Fatalf("DiscoverProjectLineage() error = %v", err)
			}
			if len(got) != len(expected.Services) {
				t.Fatalf("lineage models = %d, want service count %d: %#v", len(got), len(expected.Services), got)
			}
			records, err := db.Lineage().ListProject(ctx, projectID)
			if err != nil {
				t.Fatalf("ListProject() error = %v", err)
			}
			recordsByService := map[string]store.LineageRecord{}
			modelsByService := map[string]models.ImageLineage{}
			for _, record := range records {
				recordsByService[record.ServiceName] = record
			}
			for _, model := range got {
				modelsByService[model.Service] = model
			}

			expectedByService := expectedBasesByService(expected.Lineage)
			for service, expectedBases := range expectedByService {
				record, ok := recordsByService[service]
				if !ok {
					t.Fatalf("missing lineage record for service %s", service)
				}
				actualBases := make([]string, 0, len(record.BaseRefs))
				finalBase := ""
				for _, ref := range record.BaseRefs {
					actualBases = append(actualBases, ref.ImageRef)
					if ref.IsFinalStageBase {
						finalBase = ref.ImageRef
					}
				}
				if !sameStringSet(actualBases, expectedBases) {
					t.Fatalf("%s bases = %v, want %v", service, actualBases, expectedBases)
				}
				if finalBase != expectedBases[0] {
					t.Fatalf("%s final base = %q, want %q", service, finalBase, expectedBases[0])
				}
				if modelsByService[service].BaseImage != expectedBases[0] {
					t.Fatalf("%s model base = %q, want %q", service, modelsByService[service].BaseImage, expectedBases[0])
				}
				if record.Confidence != models.ConfidenceMedium {
					t.Fatalf("%s confidence = %s, want medium", service, record.Confidence)
				}
				if record.DockerfileHash == "" {
					t.Fatalf("%s dockerfile hash is empty", service)
				}
			}

			if projectName == "mixed-updates" {
				for _, service := range []string{"image-a", "image-b"} {
					model := modelsByService[service]
					if model.Confidence != models.ConfidenceUnknown || model.Source != models.LineageSourceUnknown {
						t.Fatalf("%s unknown model = %#v", service, model)
					}
					wantReason := "Base image: Unknown — this is a third-party registry image and no base metadata was found."
					if model.Reason != wantReason {
						t.Fatalf("%s reason = %q, want %q", service, model.Reason, wantReason)
					}
				}
			}
		})
	}
}

func TestManagerConfidenceAssignments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openLineageStore(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "high", "Dockerfile"), "FROM alpine:3.20\n")
	writeFile(t, filepath.Join(root, "medium", "Dockerfile"), "FROM nginx:alpine\n")
	writeFile(t, filepath.Join(root, "unresolved", "Dockerfile"), "ARG BASE\nFROM ${BASE}:latest\n")
	projectID := "linux_native/confidence"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if err := db.Projects().SaveSnapshot(ctx, "linux_native", []store.ProjectRecord{{
		ID:         projectID,
		ProviderID: "linux_native",
		Name:       "confidence",
		WorkingDir: root,
		Source:     store.ProjectSourceImported,
		LastSeenAt: now,
	}}, []store.ServiceRecord{
		{ID: projectID + "/high", ProjectID: projectID, Name: "high", ImageRef: "local/high:latest", BuildContext: "high", LastSeenAt: now},
		{ID: projectID + "/medium", ProjectID: projectID, Name: "medium", ImageRef: "local/medium:latest", BuildContext: "medium", LastSeenAt: now},
		{ID: projectID + "/low", ProjectID: projectID, Name: "low", ImageRef: "local/low:latest", LastSeenAt: now},
		{ID: projectID + "/unresolved", ProjectID: projectID, Name: "unresolved", ImageRef: "local/unresolved:latest", BuildContext: "unresolved", LastSeenAt: now},
		{ID: projectID + "/unknown", ProjectID: projectID, Name: "unknown", ImageRef: "local/unknown:latest", LastSeenAt: now},
	}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}

	images := fakeImageInspector{
		"local/high:latest": {Labels: map[string]string{
			"io.cairn.base.name":      "alpine:3.20",
			"io.cairn.base.digest":    "sha256:abc",
			"io.cairn.build.platform": "linux/amd64",
		}},
		"local/low:latest": {Labels: map[string]string{
			"org.opencontainers.image.base.name":   "debian:12",
			"org.opencontainers.image.base.digest": "sha256:def",
		}},
	}
	manager := NewManager(db.Projects(), db.Lineage(), nil, images)
	got, err := manager.DiscoverProjectLineage(ctx, projectID)
	if err != nil {
		t.Fatalf("DiscoverProjectLineage() error = %v", err)
	}
	byService := map[string]models.ImageLineage{}
	for _, item := range got {
		byService[item.Service] = item
	}
	assertLineage(t, byService["high"], models.LineageSourceCairnLabel, models.ConfidenceHigh, "alpine:3.20")
	assertLineage(t, byService["medium"], models.LineageSourceComposeDockerfile, models.ConfidenceMedium, "nginx:alpine")
	assertLineage(t, byService["low"], models.LineageSourceOCIAnnotation, models.ConfidenceLow, "debian:12")
	assertLineage(t, byService["unresolved"], models.LineageSourceComposeDockerfile, models.ConfidenceLow, "${BASE}:latest")
	assertLineage(t, byService["unknown"], models.LineageSourceUnknown, models.ConfidenceUnknown, "")
}

func TestManagerGetAndRefreshLineage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openLineageStore(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM nginx:alpine\n")
	projectID := "linux_native/refresh"
	now := time.Date(2026, 6, 13, 12, 30, 0, 0, time.UTC)
	if err := db.Projects().SaveSnapshot(ctx, "linux_native", []store.ProjectRecord{{
		ID:         projectID,
		ProviderID: "linux_native",
		Name:       "refresh",
		WorkingDir: root,
		Source:     store.ProjectSourceImported,
		LastSeenAt: now,
	}}, []store.ServiceRecord{{
		ID:           projectID + "/web",
		ProjectID:    projectID,
		Name:         "web",
		ImageRef:     "local/refresh:latest",
		BuildContext: ".",
		LastSeenAt:   now,
	}}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	manager := NewManager(db.Projects(), db.Lineage(), db.Objects(), nil)
	refreshed, err := manager.RefreshServiceLineage(ctx, projectID, "web")
	if err != nil {
		t.Fatalf("RefreshServiceLineage() error = %v", err)
	}
	if refreshed.BaseImage != "nginx:alpine" {
		t.Fatalf("refreshed base = %#v", refreshed)
	}
	got, err := manager.GetServiceLineage(ctx, projectID, "web")
	if err != nil {
		t.Fatalf("GetServiceLineage() error = %v", err)
	}
	if got == nil || got.BaseImage != "nginx:alpine" {
		t.Fatalf("GetServiceLineage() = %#v", got)
	}
	missing, err := manager.GetServiceLineage(ctx, projectID, "missing")
	if err != nil {
		t.Fatalf("missing GetServiceLineage() error = %v", err)
	}
	if missing != nil {
		t.Fatalf("missing lineage = %#v", missing)
	}
}

type fakeImageInspector map[string]*models.ImageDetail

func (f fakeImageInspector) GetImage(_ context.Context, ref string) (*models.ImageDetail, error) {
	if detail, ok := f[ref]; ok {
		return detail, nil
	}
	return nil, errors.New("not found")
}

func openLineageStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
		ID:          "linux_native",
		Type:        "linux_native",
		Platform:    "linux",
		DisplayName: "Linux Native",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	return db
}

func seedProjectFromCompose(t *testing.T, ctx context.Context, db *store.Store, projectDir string, projectName string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(projectDir, "compose.yaml"))
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	config, err := composecore.ParseConfigYAML(string(raw))
	if err != nil {
		t.Fatalf("ParseConfigYAML() error = %v", err)
	}
	projectID := "linux_native/" + projectName
	now := time.Date(2026, 6, 13, 11, 0, 0, 0, time.UTC)
	services := make([]store.ServiceRecord, 0, len(config.Services))
	for _, service := range config.Services {
		metadata := map[string]any{}
		if len(service.BuildArgs) > 0 {
			metadata["buildArgs"] = service.BuildArgs
		}
		services = append(services, store.ServiceRecord{
			ID:             projectID + "/" + service.Name,
			ProjectID:      projectID,
			Name:           service.Name,
			ImageRef:       service.Image,
			BuildContext:   service.BuildContext,
			DockerfilePath: service.DockerfilePath,
			BuildTarget:    service.BuildTarget,
			Metadata:       metadata,
			LastSeenAt:     now,
		})
	}
	if err := db.Projects().SaveSnapshot(ctx, "linux_native", []store.ProjectRecord{{
		ID:           projectID,
		ProviderID:   "linux_native",
		Name:         projectName,
		WorkingDir:   projectDir,
		ComposeFiles: []string{filepath.Join(projectDir, "compose.yaml")},
		Source:       store.ProjectSourceImported,
		LastSeenAt:   now,
	}}, services, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	return projectID
}

func readExpectedProject(t *testing.T, projectDir string) expectedProjectFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(projectDir, "expected.json"))
	if err != nil {
		t.Fatalf("read expected.json: %v", err)
	}
	var expected expectedProjectFile
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatalf("parse expected.json: %v", err)
	}
	return expected
}

func expectedBasesByService(entries []expectedLineageEntry) map[string][]string {
	result := map[string][]string{}
	for _, entry := range entries {
		result[entry.Service] = append(result[entry.Service], entry.BaseImage)
	}
	return result
}

func sameStringSet(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func assertLineage(t *testing.T, got models.ImageLineage, source models.LineageSource, confidence models.Confidence, base string) {
	t.Helper()
	if got.Source != source || got.Confidence != confidence || got.BaseImage != base {
		t.Fatalf("lineage = %#v, want source=%s confidence=%s base=%q", got, source, confidence, base)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestManagerMissingProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openLineageStore(t)
	manager := NewManager(db.Projects(), db.Lineage(), nil, nil)
	_, err := manager.DiscoverProjectLineage(ctx, "missing")
	if err == nil {
		t.Fatal("DiscoverProjectLineage() error = nil, want not found")
	}
	if !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("DiscoverProjectLineage() error = %v, want E_NOT_FOUND", err)
	}
}
