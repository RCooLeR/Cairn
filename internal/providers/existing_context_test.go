package providers

import (
	"context"
	"reflect"
	"testing"
)

func TestExistingContextDetectHealthyWithUnencryptedTCPWarning(t *testing.T) {
	t.Parallel()
	runner := existingContextRunner("remote-prod", "tcp://192.0.2.10:2375")

	status, err := NewExistingContext(ExistingContextOptions{ContextName: "remote-prod", Runner: runner}).Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !status.Healthy {
		t.Fatalf("Healthy = false, problems = %#v", status.Problems)
	}
	if status.DockerHost != "tcp://192.0.2.10:2375" || status.CurrentContext != "remote-prod" {
		t.Fatalf("context/host = %q/%q", status.CurrentContext, status.DockerHost)
	}
	if len(status.Warnings) != 1 || status.Warnings[0].Code != WarningUnencryptedTCP {
		t.Fatalf("warnings = %#v", status.Warnings)
	}
}

func TestExistingContextDetectMissingContext(t *testing.T) {
	t.Parallel()
	runner := existingContextRunner("desktop-linux", "unix:///Users/ada/.docker/run/docker.sock")

	status, err := NewExistingContext(ExistingContextOptions{ContextName: "missing", Runner: runner}).Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	assertProblem(t, status.Problems, ProblemContextMissing)
	if status.Healthy {
		t.Fatalf("status should not be healthy: %#v", status)
	}
}

func TestExistingContextRunComposeUsesContextWorkdirAndEnv(t *testing.T) {
	t.Parallel()
	runner := &composeOptionsRunner{}
	provider := NewExistingContext(ExistingContextOptions{ContextName: "desktop-linux", Runner: runner})

	result, err := provider.RunComposeEnv(context.Background(), "/Users/ada/app", []string{"COMPOSE_PROJECT_NAME=demo"}, "-f", "compose.yaml", "config")
	if err != nil {
		t.Fatalf("RunComposeEnv() error = %v", err)
	}
	wantCommand := []string{"docker", "--context", "desktop-linux", "compose", "-f", "compose.yaml", "config"}
	if !reflect.DeepEqual(result.Command, wantCommand) {
		t.Fatalf("command = %#v, want %#v", result.Command, wantCommand)
	}
	if runner.opts.Workdir != "/Users/ada/app" {
		t.Fatalf("workdir = %q", runner.opts.Workdir)
	}
	if got := runner.opts.Env; len(got) != 1 || got[0] != "COMPOSE_PROJECT_NAME=demo" {
		t.Fatalf("env = %#v", got)
	}
}

func TestManagerListAndSetDockerContextCreatesActiveProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	runner := existingContextRunner("desktop-linux", "unix:///Users/ada/.docker/run/docker.sock")
	manager := NewManager(db.Providers(), db.Settings(), nil)
	manager.runner = runner

	contexts, err := manager.ListDockerContexts(ctx)
	if err != nil {
		t.Fatalf("ListDockerContexts() error = %v", err)
	}
	if len(contexts) != 1 || contexts[0].Name != "desktop-linux" {
		t.Fatalf("contexts = %#v", contexts)
	}
	if err := manager.SetDockerContext(ctx, "desktop-linux"); err != nil {
		t.Fatalf("SetDockerContext() error = %v", err)
	}
	if active := manager.ActiveProviderID(ctx); active != "ctx:desktop-linux" {
		t.Fatalf("active provider = %q", active)
	}
	summaries, err := manager.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "ctx:desktop-linux" || !summaries[0].Healthy {
		t.Fatalf("summaries = %#v", summaries)
	}
	record, err := db.Providers().Get(ctx, "ctx:desktop-linux")
	if err != nil {
		t.Fatalf("provider record missing: %v", err)
	}
	if record.Type != TypeExistingContext || record.LastStatusJSON == "" {
		t.Fatalf("provider record = %#v", record)
	}
}

func existingContextRunner(name, host string) *fakeRunner {
	runner := newFakeRunner()
	runner.paths["docker"] = "/usr/local/bin/docker"
	runner.outputs["docker context ls --format json"] = `{"Name":"` + name + `","Description":"Existing context","DockerEndpoint":"` + host + `","Current":false}` + "\n"
	runner.outputs["docker --context "+name+" compose version --short"] = "v2.40.3\n"
	runner.outputs["docker --context "+name+" buildx version"] = "github.com/docker/buildx v0.34.1 123456\n"
	runner.outputs["docker --context "+name+" info --format {{.ServerVersion}}"] = "29.0.1\n"
	return runner
}
