package compose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestParseVersionFixture(t *testing.T) {
	t.Parallel()
	raw := readFixture(t, "compose-outputs", "v2.2x", "version.json")

	version, err := ParseVersionJSON(raw)
	if err != nil {
		t.Fatalf("ParseVersionJSON() error = %v", err)
	}
	if version.Version != "2.29.1" || version.GitCommit != "c123abc" {
		t.Fatalf("version = %#v", version)
	}
	if !VersionAtLeast(version.Version, MinimumVersion) {
		t.Fatalf("%s should satisfy minimum %s", version.Version, MinimumVersion)
	}
}

func TestParseProjectsFixture(t *testing.T) {
	t.Parallel()
	raw := readFixture(t, "compose-outputs", "v2.2x", "ls.json")

	projects, err := ParseProjectsJSON(raw)
	if err != nil {
		t.Fatalf("ParseProjectsJSON() error = %v", err)
	}
	if got, want := len(projects), 2; got != want {
		t.Fatalf("len(projects) = %d, want %d", got, want)
	}
	if projects[0].Name != "app-db" || projects[0].Status != "running(2)" {
		t.Fatalf("first project = %#v", projects[0])
	}
	if got := projects[0].ConfigFiles[0]; got == "" {
		t.Fatalf("config file was not parsed")
	}
}

func TestParsePSFixtureAndAggregate(t *testing.T) {
	t.Parallel()
	raw := readFixture(t, "compose-outputs", "v2.2x", "ps.ndjson")

	containers, err := ParsePSJSON(raw)
	if err != nil {
		t.Fatalf("ParsePSJSON() error = %v", err)
	}
	statuses := ServiceStatuses(containers)
	if got, want := len(statuses), 2; got != want {
		t.Fatalf("len(statuses) = %d, want %d: %#v", got, want, statuses)
	}
	db := findServiceStatus(t, statuses, "db")
	if db.Status != models.ProjectStatusRunning || db.Health != models.HealthStatusHealthy {
		t.Fatalf("db status = %#v", db)
	}
	if got := db.Ports[0].HostPort; got != "15432" {
		t.Fatalf("db host port = %q", got)
	}
	web := findServiceStatus(t, statuses, "web")
	if web.Running != 1 || web.Replicas != 1 {
		t.Fatalf("web replicas = %#v", web)
	}
}

func TestParseConfigFixture(t *testing.T) {
	t.Parallel()
	raw := readFixture(t, "compose-outputs", "v2.2x", "config.yaml")

	config, err := ParseConfigYAML(raw)
	if err != nil {
		t.Fatalf("ParseConfigYAML() error = %v", err)
	}
	if !config.Valid || !config.API.Valid {
		t.Fatalf("config valid = false: %#v", config.Errors)
	}
	if got, want := config.EnvFiles, []string{"./web.env"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("env files = %#v, want %#v", got, want)
	}
	web := findServiceConfig(t, config.Services, "web")
	if web.Image != "nginx:alpine" {
		t.Fatalf("web image = %q", web.Image)
	}
	if web.BuildContext != "./web" || web.DockerfilePath != "Dockerfile" || web.BuildTarget != "runtime" {
		t.Fatalf("web build = %#v", web)
	}
	if got := web.BuildArgs["NODE_VERSION"]; got != "22" {
		t.Fatalf("NODE_VERSION = %q", got)
	}
	if got, want := web.DependsOn, []string{"db"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("depends_on = %#v, want %#v", got, want)
	}
	db := findServiceConfig(t, config.Services, "db")
	if !db.HasHealthcheck {
		t.Fatalf("db healthcheck was not detected")
	}
}

func TestParseAllTestdataProjectYAML(t *testing.T) {
	t.Parallel()
	for _, project := range testdataProjects(t) {
		project := project
		t.Run(project.expected.Project, func(t *testing.T) {
			t.Parallel()
			rawBytes, err := os.ReadFile(filepath.Join(project.dir, "compose.yaml"))
			if err != nil {
				t.Fatalf("read compose.yaml: %v", err)
			}
			config, err := ParseConfigYAML(string(rawBytes))
			if err != nil {
				t.Fatalf("ParseConfigYAML() error = %v", err)
			}
			if !config.Valid {
				t.Fatalf("config invalid: %#v", config.Errors)
			}
			if project.expected.ServiceCount > 0 {
				if got := len(config.Services); got != project.expected.ServiceCount {
					t.Fatalf("service count = %d, want %d", got, project.expected.ServiceCount)
				}
				return
			}
			for _, expected := range project.expected.Services {
				service := findServiceConfig(t, config.Services, expected.Name)
				if expected.Healthcheck && !service.HasHealthcheck {
					t.Fatalf("%s healthcheck was not detected", expected.Name)
				}
			}
		})
	}
}

type expectedProject struct {
	Project      string `json:"project"`
	ServiceCount int    `json:"serviceCount"`
	Services     []struct {
		Name        string   `json:"name"`
		Image       string   `json:"image"`
		Healthcheck bool     `json:"healthcheck"`
		EnvFiles    []string `json:"envFiles"`
	} `json:"services"`
}

type projectFixture struct {
	dir      string
	expected expectedProject
}

func testdataProjects(t *testing.T) []projectFixture {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read testdata projects: %v", err)
	}
	projects := make([]projectFixture, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		rawExpected, err := os.ReadFile(filepath.Join(dir, "expected.json"))
		if err != nil {
			t.Fatalf("read %s expected.json: %v", entry.Name(), err)
		}
		var expected expectedProject
		if err := json.Unmarshal(rawExpected, &expected); err != nil {
			t.Fatalf("parse %s expected.json: %v", entry.Name(), err)
		}
		projects = append(projects, projectFixture{dir: dir, expected: expected})
	}
	return projects
}

func readFixture(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", "..", "testdata"}, parts...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return string(raw)
}

func findServiceConfig(t *testing.T, services []ServiceConfig, name string) ServiceConfig {
	t.Helper()
	for _, service := range services {
		if service.Name == name {
			return service
		}
	}
	t.Fatalf("service %q not found in %#v", name, services)
	return ServiceConfig{}
}

func findServiceStatus(t *testing.T, statuses []models.ComposeServiceStatus, name string) models.ComposeServiceStatus {
	t.Helper()
	for _, status := range statuses {
		if status.Name == name {
			return status
		}
	}
	t.Fatalf("status %q not found in %#v", name, statuses)
	return models.ComposeServiceStatus{}
}
