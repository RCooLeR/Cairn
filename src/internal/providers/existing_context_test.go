package providers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
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

func TestExistingContextRejectsTruncatedInspectOutput(t *testing.T) {
	runner := existingContextRunner("remote-prod", "tcp://192.0.2.10:2375")
	runner.truncated["docker context inspect remote-prod"] = true
	provider := NewExistingContext(ExistingContextOptions{ContextName: "remote-prod", Runner: runner})

	if _, err := provider.inspectContextTarget(context.Background(), false); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("inspectContextTarget() error = %v, want provider-not-ready truncation rejection", err)
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

	if runner.opts.Timeout != composeCommandTimeout {
		t.Fatalf("config timeout = %s, want %s", runner.opts.Timeout, composeCommandTimeout)
	}
	if _, err := provider.RunComposeEnv(context.Background(), "/Users/ada/app", nil, "-f", "compose.yaml", "up", "-d"); err != nil {
		t.Fatalf("RunComposeEnv(up) error = %v", err)
	}
	if runner.opts.Timeout != dockerOperationTimeout {
		t.Fatalf("up timeout = %s, want %s", runner.opts.Timeout, dockerOperationTimeout)
	}
}

func TestExistingContextIdentityTracksTargetAndIgnoresDescription(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runner := existingContextRunner("remote-prod", "tcp://192.0.2.10:2375")
	provider := NewExistingContext(ExistingContextOptions{ContextName: "remote-prod", Runner: runner})

	initial, err := ResolveRuntimeScope(ctx, provider)
	if err != nil {
		t.Fatalf("initial ResolveRuntimeScope() error = %v", err)
	}
	runner.outputs["docker context inspect remote-prod"] = existingContextInspectJSON("remote-prod", "tcp://192.0.2.10:2375", "renamed description", "/tmp/docker-tls")
	metadataOnly, err := ResolveRuntimeScope(ctx, provider)
	if err != nil {
		t.Fatalf("metadata-only ResolveRuntimeScope() error = %v", err)
	}
	if !initial.Equal(metadataOnly) {
		t.Fatalf("metadata-only edit changed scope from %q to %q", initial.ContextName(), metadataOnly.ContextName())
	}

	runner.outputs["docker context inspect remote-prod"] = existingContextInspectJSON("remote-prod", "tcp://192.0.2.11:2375", "renamed description", "/tmp/docker-tls")
	retargeted, err := ResolveRuntimeScope(ctx, provider)
	if err != nil {
		t.Fatalf("retargeted ResolveRuntimeScope() error = %v", err)
	}
	if initial.Equal(retargeted) {
		t.Fatalf("same-name endpoint retarget kept scope %q", retargeted.ContextName())
	}
}

func TestFrozenExistingContextRejectsSameNameRetarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runner := existingContextRunner("remote-prod", "tcp://192.0.2.10:2375")
	provider := NewExistingContext(ExistingContextOptions{ContextName: "remote-prod", Runner: runner})
	frozen, err := SnapshotRuntimeProvider(ctx, provider)
	if err != nil {
		t.Fatalf("SnapshotRuntimeProvider() error = %v", err)
	}
	if _, err := ResolveRuntimeScope(ctx, frozen); err != nil {
		t.Fatalf("frozen ResolveRuntimeScope() error = %v", err)
	}

	runner.outputs["docker context inspect remote-prod"] = existingContextInspectJSON("remote-prod", "tcp://192.0.2.11:2375", "Existing context", "/tmp/docker-tls")
	if _, err := frozen.RunDocker(ctx, "ps"); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("RunDocker() error = %v, want provider not ready after retarget", err)
	}
	if _, err := frozen.RunCompose(ctx, "/tmp/app", "config"); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("RunCompose() error = %v, want provider not ready after retarget", err)
	}
}

func TestFrozenExistingContextRejectsSameHostTLSRetarget(t *testing.T) {
	const (
		name = "remote-prod"
		host = "tcp://192.0.2.10:2376"
	)
	files := []string{"ca.pem", "cert.pem", "key.pem"}
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, *fakeRunner)
	}{
		{
			name: "storage path",
			mutate: func(t *testing.T, _ string, runner *fakeRunner) {
				other := filepath.Join(t.TempDir(), "other-tls")
				writeExistingContextTLSFiles(t, other, map[string]string{"ca.pem": "ca", "cert.pem": "cert", "key.pem": "key"})
				runner.outputs["docker context inspect "+name] = existingContextTLSInspectJSON(name, host, "Existing context", other, files)
			},
		},
		{
			name: "material list",
			mutate: func(t *testing.T, tlsPath string, runner *fakeRunner) {
				writeExistingContextTLSFiles(t, tlsPath, map[string]string{"extra.pem": "extra"})
				runner.outputs["docker context inspect "+name] = existingContextTLSInspectJSON(name, host, "Existing context", tlsPath, append(files, "extra.pem"))
			},
		},
		{
			name: "material content",
			mutate: func(t *testing.T, tlsPath string, _ *fakeRunner) {
				writeExistingContextTLSFiles(t, tlsPath, map[string]string{"key.pem": "different-key"})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			tlsPath := filepath.Join(t.TempDir(), "tls")
			writeExistingContextTLSFiles(t, tlsPath, map[string]string{"ca.pem": "ca", "cert.pem": "cert", "key.pem": "key"})
			runner := existingContextRunner(name, host)
			runner.outputs["docker context inspect "+name] = existingContextTLSInspectJSON(name, host, "Existing context", tlsPath, files)
			frozen, err := SnapshotRuntimeProvider(ctx, NewExistingContext(ExistingContextOptions{ContextName: name, Runner: runner}))
			if err != nil {
				t.Fatalf("SnapshotRuntimeProvider() error = %v", err)
			}
			test.mutate(t, tlsPath, runner)
			if _, err := frozen.RunDocker(ctx, "ps"); !apperror.IsCode(err, apperror.ProviderNotReady) {
				t.Fatalf("RunDocker() error = %v, want provider not ready after TLS retarget", err)
			}
		})
	}
}

func TestFrozenExistingContextPinsTLSCommandArguments(t *testing.T) {
	ctx := context.Background()
	const (
		name = "remote-prod"
		host = "tcp://192.0.2.10:2376"
	)
	tlsPath := filepath.Join(t.TempDir(), "tls")
	files := []string{"key.pem", "ca.pem", "cert.pem"}
	writeExistingContextTLSFiles(t, tlsPath, map[string]string{"ca.pem": "ca", "cert.pem": "cert", "key.pem": "key"})
	runner := existingContextRunner(name, host)
	runner.outputs["docker context inspect "+name] = existingContextTLSInspectJSON(name, host, "before", tlsPath, files)
	frozen, err := SnapshotRuntimeProvider(ctx, NewExistingContext(ExistingContextOptions{ContextName: name, Runner: runner}))
	if err != nil {
		t.Fatalf("SnapshotRuntimeProvider() error = %v", err)
	}
	frozenContext := frozen.(*ExistingContextProvider)
	defer func() {
		if err := frozenContext.CloseRuntime(); err != nil {
			t.Errorf("CloseRuntime() error = %v", err)
		}
	}()
	runner.outputs["docker context inspect "+name] = existingContextTLSInspectJSON(name, host, "description changed", tlsPath, []string{"cert.pem", "key.pem", "ca.pem"})
	want := append([]string{"docker"}, frozenContext.expected.dockerArgs...)
	want = append(want, "ps")
	runner.outputs[strings.Join(want, " ")] = "CONTAINER ID\n"
	result, err := frozen.RunDocker(ctx, "ps")
	if err != nil {
		t.Fatalf("RunDocker() error = %v", err)
	}
	if !reflect.DeepEqual(result.Command, want) {
		t.Fatalf("RunDocker() command = %#v, want %#v", result.Command, want)
	}
	for _, flag := range []string{"--tlscacert", "--tlscert", "--tlskey"} {
		path := commandFlagValue(t, result.Command, flag)
		if strings.HasPrefix(filepath.Clean(path), filepath.Clean(tlsPath)+string(filepath.Separator)) {
			t.Fatalf("%s still points at mutable context storage: %q", flag, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", flag, err)
		}
		if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", flag, got)
		}
	}
}

func TestExistingContextDockerArgsOverrideAmbientTLSDefaults(t *testing.T) {
	t.Parallel()
	host := "tcp://192.0.2.10:2376"
	if got, want := existingContextDockerArgs(host, false, nil), []string{
		"--host", host, "--tls=false", "--tlsverify=false",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plain Docker args = %#v, want %#v", got, want)
	}
	files := []existingContextTLSFileFingerprint{{Name: "ca.pem", Path: "/frozen/ca.pem"}}
	if got, want := existingContextDockerArgs(host, false, files), []string{
		"--host", host,
		"--tls=true", "--tlsverify=true",
		"--tlscacert", "/frozen/ca.pem",
		"--tlscert", "",
		"--tlskey", "",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("TLS Docker args = %#v, want %#v", got, want)
	}
}

func TestFrozenExistingContextUsesPrivateTLSCopyWhenSourceMutatesAtExecution(t *testing.T) {
	ctx := context.Background()
	const (
		name = "remote-prod"
		host = "tcp://192.0.2.10:2376"
	)
	tlsPath := filepath.Join(t.TempDir(), "tls")
	files := []string{"ca.pem", "cert.pem", "key.pem"}
	writeExistingContextTLSFiles(t, tlsPath, map[string]string{"ca.pem": "original-ca", "cert.pem": "original-cert", "key.pem": "original-key"})
	base := existingContextRunner(name, host)
	base.outputs["docker context inspect "+name] = existingContextTLSInspectJSON(name, host, "before", tlsPath, files)
	runner := &existingContextMutatingTLSRunner{fakeRunner: base, sourceKey: filepath.Join(tlsPath, "docker", "key.pem")}
	frozen, err := SnapshotRuntimeProvider(ctx, NewExistingContext(ExistingContextOptions{ContextName: name, Runner: runner}))
	if err != nil {
		t.Fatalf("SnapshotRuntimeProvider() error = %v", err)
	}
	frozenContext := frozen.(*ExistingContextProvider)
	frozenDir := frozenContext.frozenTLSDir
	defer func() { _ = frozenContext.CloseRuntime() }()

	if _, err := frozen.RunDocker(ctx, "ps"); err != nil {
		t.Fatalf("RunDocker() error = %v", err)
	}
	if runner.executedKeyPath == runner.sourceKey {
		t.Fatalf("execution used mutable source key %q", runner.executedKeyPath)
	}
	if runner.executedKeyContent != "original-key" {
		t.Fatalf("execution key content = %q, want frozen original", runner.executedKeyContent)
	}
	if source, err := os.ReadFile(runner.sourceKey); err != nil || string(source) != "mutated-key" {
		t.Fatalf("source key after execution = %q, %v", source, err)
	}
	if _, err := frozen.RunDocker(ctx, "ps"); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("second RunDocker() error = %v, want source retarget rejection", err)
	}
	if err := frozenContext.CloseRuntime(); err != nil {
		t.Fatalf("CloseRuntime() error = %v", err)
	}
	if _, err := os.Stat(frozenDir); !os.IsNotExist(err) {
		t.Fatalf("frozen TLS directory still exists after close: %v", err)
	}
	if _, err := frozen.RunDocker(ctx, "ps"); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("RunDocker() after close error = %v, want ProviderNotReady", err)
	}
}

func TestExistingContextRejectsUnsafeTLSMaterialNames(t *testing.T) {
	const (
		name = "remote-prod"
		host = "tcp://192.0.2.10:2376"
	)
	for testName, files := range map[string][]string{
		"parent traversal":    {"../key.pem"},
		"forward slash":       {"nested/key.pem"},
		"backslash":           {`nested\key.pem`},
		"absolute":            {filepath.Join(string(filepath.Separator), "tmp", "key.pem")},
		"drive relative":      {`C:key.pem`},
		"duplicate":           {"ca.pem", "ca.pem"},
		"case-fold duplicate": {"ca.pem", "CA.PEM"},
	} {
		t.Run(testName, func(t *testing.T) {
			tlsPath := filepath.Join(t.TempDir(), "tls")
			runner := existingContextRunner(name, host)
			runner.outputs["docker context inspect "+name] = existingContextTLSInspectJSON(name, host, "unsafe", tlsPath, files)
			_, err := SnapshotRuntimeProvider(context.Background(), NewExistingContext(ExistingContextOptions{ContextName: name, Runner: runner}))
			if !apperror.IsCode(err, apperror.ProviderNotReady) {
				t.Fatalf("SnapshotRuntimeProvider() error = %v, want ProviderNotReady", err)
			}
		})
	}
}

type existingContextMutatingTLSRunner struct {
	*fakeRunner
	sourceKey          string
	executedKeyPath    string
	executedKeyContent string
}

func (r *existingContextMutatingTLSRunner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (*CommandResult, error) {
	if name == "docker" && len(args) > 0 && args[len(args)-1] == "ps" && commandFlagValueRaw(args, "--tlskey") != "" {
		if err := os.WriteFile(r.sourceKey, []byte("mutated-key"), 0o600); err != nil {
			return nil, err
		}
		r.executedKeyPath = commandFlagValueRaw(args, "--tlskey")
		content, err := os.ReadFile(r.executedKeyPath)
		if err != nil {
			return nil, err
		}
		r.executedKeyContent = string(content)
		return &CommandResult{Command: append([]string{name}, args...), ExitCode: 0}, nil
	}
	return r.fakeRunner.Run(ctx, timeout, name, args...)
}

func commandFlagValue(t *testing.T, command []string, flag string) string {
	t.Helper()
	value := commandFlagValueRaw(command, flag)
	if value == "" {
		t.Fatalf("command %#v does not contain %s", command, flag)
	}
	return value
}

func commandFlagValueRaw(command []string, flag string) string {
	for i := 0; i+1 < len(command); i++ {
		if command[i] == flag {
			return command[i+1]
		}
	}
	return ""
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
	runner.outputs["docker context inspect "+name] = existingContextInspectJSON(name, host, "Existing context", "/tmp/docker-tls")
	runner.outputs["docker --context "+name+" compose version --short"] = "v2.40.3\n"
	runner.outputs["docker --context "+name+" buildx version"] = "github.com/docker/buildx v0.34.1 123456\n"
	runner.outputs["docker --context "+name+" info --format {{.ServerVersion}}"] = "29.0.1\n"
	return runner
}

func existingContextInspectJSON(name string, host string, description string, tlsPath string) string {
	return existingContextTLSInspectJSON(name, host, description, tlsPath, nil)
}

func existingContextTLSInspectJSON(name string, host string, description string, tlsPath string, files []string) string {
	payload := []map[string]any{{
		"Name":        name,
		"Metadata":    map[string]any{"Description": description},
		"Endpoints":   map[string]any{"docker": map[string]any{"Host": host, "SkipTLSVerify": false}},
		"TLSMaterial": map[string]any{"docker": files},
		"Storage":     map[string]any{"TLSPath": tlsPath},
	}}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func writeExistingContextTLSFiles(t *testing.T, tlsPath string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(tlsPath, "docker")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
	}
}
