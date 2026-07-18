//go:build linux

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"golang.org/x/crypto/bcrypt"
)

func TestClientRealRegistryTagPushRoundTrip(t *testing.T) {
	if os.Getenv("CAIRN_REAL_DOCKER_REGISTRY") != "1" {
		t.Skip("set CAIRN_REAL_DOCKER_REGISTRY=1 to run real registry integration")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	username := "cairn"
	secret := "registry-secret"
	authDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "htpasswd"), []byte(username+":"+string(hash)+"\n"), 0o600); err != nil {
		t.Fatalf("write htpasswd: %v", err)
	}

	provider := &realPushProvider{dockerConfig: t.TempDir()}
	if result, err := provider.RunDocker(ctx, "image", "inspect", "registry:2"); err != nil || result.ExitCode != 0 {
		if result, err := provider.RunDocker(ctx, "pull", "registry:2"); err != nil || result.ExitCode != 0 {
			t.Fatalf("pull registry:2: result=%#v err=%v", result, err)
		}
	}

	name := "cairn-push-registry-" + time.Now().UTC().Format("20060102150405")
	result, err := provider.RunDocker(ctx,
		"run", "-d", "--rm", "--name", name,
		"-p", "127.0.0.1::5000",
		"-v", authDir+":/auth:ro",
		"-e", "REGISTRY_AUTH=htpasswd",
		"-e", "REGISTRY_AUTH_HTPASSWD_REALM=Cairn Registry",
		"-e", "REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd",
		"registry:2",
	)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("start registry: result=%#v err=%v", result, err)
	}
	t.Cleanup(func() {
		_, _ = provider.RunDocker(context.Background(), "rm", "-f", name)
	})

	registryHost := waitForPushRegistry(t, ctx, provider, name)
	imageRef := registryHost + "/test/app:1.0"

	eventBus := bus.New()
	defer eventBus.Close()
	client := New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	if err := client.TagImage(ctx, "registry:2", imageRef); err != nil {
		t.Fatalf("TagImage() error = %v", err)
	}
	if _, err := client.PushImage(ctx, imageRef); !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("PushImage without login error = %v, want %s", err, apperror.RegistryAuth)
	}

	installRealPushCredentialHelper(t, provider, registryHost, username)
	manager := registrycore.NewManager(realPushResolver{provider: provider}, nil)
	if err := manager.Login(ctx, models.RegistryLoginRequest{
		Registry:   registryHost,
		Username:   username,
		Secret:     secret,
		SecretKind: "password",
	}); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	assertRealPushCredentialStorage(t, provider, registryHost, secret)

	progressEvents := eventBus.Subscribe(ctx, bus.TopicImagePushProgress, 16)
	streamID, err := client.PushImage(ctx, imageRef)
	if err != nil {
		t.Fatalf("PushImage() error = %v", err)
	}
	if streamID == "" {
		t.Fatal("PushImage() streamID is empty")
	}
	if progress := waitImageProgress(t, ctx, progressEvents, time.Second); progress.StreamID != streamID {
		t.Fatalf("push progress = %#v, want stream %q", progress, streamID)
	}

	digest, err := manager.ResolveDigest(ctx, imageRef, registrycore.ResolveOptions{
		BypassCache: true,
		Platform: registrycore.Platform{
			OS:           "linux",
			Architecture: "amd64",
		},
	})
	if err != nil {
		t.Fatalf("ResolveDigest() error = %v", err)
	}
	if digest.ManifestDigest == "" {
		t.Fatalf("manifest digest missing: %#v", digest)
	}

	_, _ = provider.RunDocker(ctx, "image", "rm", imageRef)
	if result, err := provider.RunDocker(ctx, "pull", imageRef); err != nil || result.ExitCode != 0 {
		t.Fatalf("pull back image: result=%#v err=%v", result, err)
	}
	result, err = provider.RunDocker(ctx, "image", "inspect", "--format", "{{index .RepoDigests 0}}", imageRef)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("inspect pulled digest: result=%#v err=%v", result, err)
	}
	if !strings.Contains(result.Stdout, digest.ManifestDigest) {
		t.Fatalf("pulled digest %q does not contain %q", strings.TrimSpace(result.Stdout), digest.ManifestDigest)
	}

	if err := manager.Logout(ctx, registryHost); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := os.Stat(provider.credentialState); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential helper state after logout error = %v, want not exist", err)
	}
}

func waitForPushRegistry(t *testing.T, ctx context.Context, provider *realPushProvider, name string) string {
	t.Helper()
	var registryHost string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		result, err := provider.RunDocker(ctx, "port", name, "5000/tcp")
		if err == nil && result.ExitCode == 0 {
			registryHost = normalizePushRegistryPort(result.Stdout)
			if registryHost != "" {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+registryHost+"/v2/", nil)
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusOK {
						return registryHost
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("registry %s did not become reachable at %s", name, registryHost)
	return ""
}

func normalizePushRegistryPort(stdout string) string {
	lines := strings.Fields(strings.TrimSpace(stdout))
	if len(lines) == 0 {
		return ""
	}
	host := lines[len(lines)-1]
	if !strings.Contains(host, ":") {
		return ""
	}
	if strings.HasPrefix(host, "0.0.0.0") {
		host = strings.Replace(host, "0.0.0.0", "127.0.0.1", 1)
	}
	return host
}

const realPushCredentialHelperName = "cairn-test"

const realPushCredentialHelperScript = `#!/bin/sh
set -eu

state=${CAIRN_TEST_CREDENTIAL_STATE:?}
registry=${CAIRN_TEST_CREDENTIAL_REGISTRY:?}
username=${CAIRN_TEST_CREDENTIAL_USERNAME:?}

case "${1:-}" in
store)
	umask 077
	tmp="${state}.tmp.$$"
	trap 'rm -f "$tmp"' 0 1 2 15
	cat > "$tmp"
	chmod 0600 "$tmp"
	mv "$tmp" "$state"
	trap - 0 1 2 15
	;;
get)
	cat >/dev/null
	if [ ! -s "$state" ]; then
		printf '%s\n' 'credentials not found in native keychain' >&2
		exit 1
	fi
	cat "$state"
	;;
erase)
	cat >/dev/null
	rm -f "$state"
	;;
list)
	if [ -s "$state" ]; then
		printf '{"%s":"%s"}\n' "$registry" "$username"
	else
		printf '{}\n'
	fi
	;;
*)
	printf '%s\n' 'unsupported credential helper command' >&2
	exit 64
	;;
esac
`

func installRealPushCredentialHelper(t *testing.T, provider *realPushProvider, registry string, username string) {
	t.Helper()
	helpersDir := t.TempDir()
	if err := os.Chmod(helpersDir, 0o700); err != nil {
		t.Fatalf("secure credential helper directory: %v", err)
	}
	helperPath := filepath.Join(helpersDir, "docker-credential-"+realPushCredentialHelperName)
	if err := os.WriteFile(helperPath, []byte(realPushCredentialHelperScript), 0o700); err != nil {
		t.Fatalf("write credential helper: %v", err)
	}

	provider.credentialHelperDir = helpersDir
	provider.credentialState = filepath.Join(helpersDir, "credentials.json")
	provider.credentialRegistry = registry
	provider.credentialUsername = username

	config := map[string]map[string]string{
		"credHelpers": {
			registry: realPushCredentialHelperName,
		},
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("encode Docker credential config: %v", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(provider.dockerConfig, "config.json"), raw, 0o600); err != nil {
		t.Fatalf("write Docker credential config: %v", err)
	}
}

type realPushDockerConfig struct {
	Auths map[string]struct {
		Auth          string `json:"auth"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		IdentityToken string `json:"identitytoken"`
	} `json:"auths"`
	CredHelpers map[string]string `json:"credHelpers"`
}

func assertRealPushCredentialStorage(t *testing.T, provider *realPushProvider, registry string, secret string) {
	t.Helper()
	info, err := os.Stat(provider.credentialState)
	if err != nil {
		t.Fatalf("stat credential helper state: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("credential helper state mode = %o, want %o", got, want)
	}

	raw, err := os.ReadFile(filepath.Join(provider.dockerConfig, "config.json"))
	if err != nil {
		t.Fatalf("read Docker credential config: %v", err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("Docker credential config contains the registry secret")
	}
	var config realPushDockerConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("parse Docker credential config: %v", err)
	}
	if got, want := strings.TrimSpace(config.CredHelpers[registry]), realPushCredentialHelperName; got != want {
		t.Fatalf("credential helper = %q, want %q", got, want)
	}
	if entry, ok := config.Auths[registry]; ok {
		if strings.TrimSpace(entry.Auth) != "" || entry.Username != "" || entry.Password != "" || entry.IdentityToken != "" {
			t.Fatalf("inline credential remained for %q", registry)
		}
	}
}

type realPushResolver struct {
	provider providers.PlatformProvider
}

func (r realPushResolver) ActiveProvider(context.Context) (providers.PlatformProvider, error) {
	return r.provider, nil
}

type realPushProvider struct {
	dockerConfig        string
	credentialHelperDir string
	credentialState     string
	credentialRegistry  string
	credentialUsername  string
}

func (p *realPushProvider) ID() string          { return "real-push" }
func (p *realPushProvider) DisplayName() string { return "Real Push" }
func (p *realPushProvider) Type() string        { return providers.TypeLinuxNative }
func (p *realPushProvider) Platform() string    { return providers.PlatformLinux }
func (p *realPushProvider) Detect(context.Context) (*models.ProviderStatus, error) {
	return nil, nil
}
func (p *realPushProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return nil, nil
}
func (p *realPushProvider) ExecuteInstallStep(context.Context, string, int, chan<- providers.InstallProgress) error {
	return nil
}
func (p *realPushProvider) Start(context.Context) error   { return nil }
func (p *realPushProvider) Stop(context.Context) error    { return nil }
func (p *realPushProvider) Restart(context.Context) error { return nil }
func (p *realPushProvider) DockerHost(context.Context) (string, error) {
	return "unix:///var/run/docker.sock", nil
}
func (p *realPushProvider) DockerContext(context.Context) (string, error) {
	return "default", nil
}
func (p *realPushProvider) RunDocker(ctx context.Context, args ...string) (*providers.CommandResult, error) {
	return p.run(ctx, "", "docker", args...)
}
func (p *realPushProvider) RunDockerWithInput(ctx context.Context, input string, args ...string) (*providers.CommandResult, error) {
	return p.run(ctx, input, "docker", args...)
}
func (p *realPushProvider) RunBackendCommand(ctx context.Context, input string, args ...string) (*providers.CommandResult, error) {
	if len(args) == 0 {
		return nil, errors.New("backend command is required")
	}
	return p.run(ctx, input, args[0], args[1:]...)
}
func (p *realPushProvider) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	return nil, nil
}
func (p *realPushProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *realPushProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *realPushProvider) MapPathToBackend(path string) (string, error) { return path, nil }
func (p *realPushProvider) MapPathToHost(path string) (string, error)    { return path, nil }

func (p *realPushProvider) run(ctx context.Context, input string, name string, args ...string) (*providers.CommandResult, error) {
	started := time.Now()
	commandName := name
	if p.credentialHelperDir != "" && name == "docker-credential-"+realPushCredentialHelperName {
		name = filepath.Join(p.credentialHelperDir, name)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+p.dockerConfig)
	if p.credentialHelperDir != "" {
		cmd.Env = append(cmd.Env,
			"PATH="+p.credentialHelperDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"CAIRN_TEST_CREDENTIAL_STATE="+p.credentialState,
			"CAIRN_TEST_CREDENTIAL_REGISTRY="+p.credentialRegistry,
			"CAIRN_TEST_CREDENTIAL_USERNAME="+p.credentialUsername,
		)
	}
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := &providers.CommandResult{
		Command:  append([]string{commandName}, args...),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: time.Since(started),
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, err
}
