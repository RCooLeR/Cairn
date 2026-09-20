//go:build windows && wslintegration

package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
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

func TestClientRealWSLRegistryTagPushRoundTrip(t *testing.T) {
	if os.Getenv("CAIRN_REAL_WSL_DOCKER_REGISTRY") != "1" {
		t.Skip("set CAIRN_REAL_WSL_DOCKER_REGISTRY=1 to run against the local cairn-dev WSL distro")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	base := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: "cairn-dev"})
	status, err := base.Detect(ctx)
	if err != nil {
		t.Fatalf("provider Detect() error = %v", err)
	}
	if !status.Healthy {
		t.Fatalf("cairn-dev WSL provider is not healthy: %#v", status.Problems)
	}
	if status.DockerHost != "wsl+stdio://cairn-dev" {
		t.Fatalf("DockerHost marker = %q, want wsl+stdio://cairn-dev", status.DockerHost)
	}

	dockerConfigBackend := makeWSLTempDir(t, ctx, base, "cairn-docker-config")
	provider := &tempDockerConfigWSLProvider{
		PlatformProvider: base,
		dockerConfig:     dockerConfigBackend,
	}
	t.Cleanup(func() {
		_, _ = base.RunBackendCommand(context.Background(), "", "rm", "-rf", dockerConfigBackend)
	})

	username := "cairn"
	secret := "registry-secret"
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	authBackend := makeWSLTempDir(t, ctx, base, "cairn-registry-auth")
	t.Cleanup(func() {
		_, _ = base.RunBackendCommand(context.Background(), "", "rm", "-rf", authBackend)
	})
	writeWSLFile(t, ctx, base, authBackend+"/htpasswd", username+":"+string(hash)+"\n")

	if result, err := provider.RunDocker(ctx, "image", "inspect", "registry:2"); err != nil || result.ExitCode != 0 {
		if result, err := provider.RunDocker(ctx, "pull", "registry:2"); err != nil || result.ExitCode != 0 {
			t.Fatalf("pull registry:2: result=%#v err=%v", result, err)
		}
	}

	name := "cairn-wsl-registry-" + time.Now().UTC().Format("20060102150405")
	result, err := provider.RunDocker(ctx,
		"run", "-d", "--rm", "--name", name,
		"-p", "127.0.0.1::5000",
		"-v", authBackend+":/auth:ro",
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

	registryHost := waitForWSLRegistry(t, ctx, provider, name)
	imageRef := registryHost + "/test/app:1.0"

	eventBus := bus.New()
	defer eventBus.Close()
	client := New(provider, eventBus)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_, _ = provider.RunDocker(context.Background(), "image", "rm", "-f", imageRef)
		_, _ = provider.RunDocker(context.Background(), "logout", registryHost)
	})

	if err := client.TagImage(ctx, "registry:2", imageRef); err != nil {
		t.Fatalf("TagImage() error = %v", err)
	}
	if _, err := client.PushImage(ctx, imageRef); !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("PushImage without login error = %v, want %s", err, apperror.RegistryAuth)
	}

	manager := registrycore.NewManager(realWSLRegistryResolver{provider: provider}, nil)
	if err := manager.Login(ctx, models.RegistryLoginRequest{
		Registry:   registryHost,
		Username:   username,
		Secret:     secret,
		SecretKind: "password",
	}); err != nil {
		status, _ := manager.TestAuth(ctx, registryHost)
		accounts, accountsErr := manager.ListRegistryAccounts(ctx)
		t.Fatalf("Login() error = %v; auth status=%#v; accounts=%#v accountsErr=%v; docker config=%s", err, status, accounts, accountsErr, redactedWSLDockerConfig(t, ctx, provider))
	}
	accounts, err := manager.ListRegistryAccounts(ctx)
	if err != nil {
		t.Fatalf("ListRegistryAccounts() error = %v", err)
	}
	if !hasWSLRegistryAccount(accounts, registryHost, username) {
		t.Fatalf("account for %s/%s missing: %#v", registryHost, username, accounts)
	}

	streamID, err := client.PushImage(ctx, imageRef)
	if err != nil {
		t.Fatalf("PushImage() error = %v", err)
	}
	if streamID == "" {
		t.Fatal("PushImage() streamID is empty")
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
	_, err = manager.ResolveDigest(ctx, imageRef, registrycore.ResolveOptions{
		BypassCache: true,
		Platform:    registrycore.Platform{OS: "linux", Architecture: "amd64"},
	})
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("ResolveDigest after logout error = %v, want %s", err, apperror.RegistryAuth)
	}
}

type tempDockerConfigWSLProvider struct {
	providers.PlatformProvider
	dockerConfig string
}

func (p *tempDockerConfigWSLProvider) DockerDialContext(ctx context.Context) (func(context.Context, string, string) (net.Conn, error), error) {
	dialer, ok := p.PlatformProvider.(DialerProvider)
	if !ok {
		return nil, nil
	}
	return dialer.DockerDialContext(ctx)
}

func (p *tempDockerConfigWSLProvider) RunDocker(ctx context.Context, args ...string) (*providers.CommandResult, error) {
	return p.RunBackendCommand(ctx, "", append([]string{"docker"}, args...)...)
}

func (p *tempDockerConfigWSLProvider) RunDockerWithInput(ctx context.Context, input string, args ...string) (*providers.CommandResult, error) {
	return p.RunBackendCommand(ctx, input, append([]string{"docker"}, args...)...)
}

func (p *tempDockerConfigWSLProvider) RunBackendCommand(ctx context.Context, input string, args ...string) (*providers.CommandResult, error) {
	if len(args) == 0 {
		return nil, errors.New("backend command is required")
	}
	runner, ok := p.PlatformProvider.(interface {
		RunBackendCommand(context.Context, string, ...string) (*providers.CommandResult, error)
	})
	if !ok {
		return nil, errors.New("wrapped provider cannot run backend commands")
	}
	if len(args) >= 3 && args[0] == "sh" && args[1] == "-lc" {
		script := strings.ReplaceAll(args[2], `"${DOCKER_CONFIG:-$HOME/.docker}/config.json"`, wslShellQuote(p.dockerConfig+"/config.json"))
		command := "export DOCKER_CONFIG=" + wslShellQuote(p.dockerConfig) + "; " + script
		return runner.RunBackendCommand(ctx, input, append([]string{"sh", "-lc", command}, args[3:]...)...)
	}
	command := "export DOCKER_CONFIG=" + wslShellQuote(p.dockerConfig) + "; exec " + wslShellJoin(args)
	return runner.RunBackendCommand(ctx, input, "sh", "-lc", command)
}

type realWSLRegistryResolver struct {
	provider providers.PlatformProvider
}

func (r realWSLRegistryResolver) ActiveProvider(context.Context) (providers.PlatformProvider, error) {
	return r.provider, nil
}

func makeWSLTempDir(t *testing.T, ctx context.Context, provider interface {
	RunBackendCommand(context.Context, string, ...string) (*providers.CommandResult, error)
}, prefix string) string {
	t.Helper()
	result, err := provider.RunBackendCommand(ctx, "", "mktemp", "-d", "/tmp/"+prefix+"-XXXXXX")
	if err != nil || result == nil || result.ExitCode != 0 {
		stderr := ""
		if result != nil {
			stderr = strings.TrimSpace(result.Stderr)
		}
		t.Fatalf("create WSL temp dir: %v %s", err, stderr)
	}
	return strings.TrimSpace(result.Stdout)
}

func writeWSLFile(t *testing.T, ctx context.Context, provider interface {
	RunBackendCommand(context.Context, string, ...string) (*providers.CommandResult, error)
}, path string, content string) {
	t.Helper()
	command := "cat > " + wslShellQuote(path) + " && chmod 600 " + wslShellQuote(path)
	result, err := provider.RunBackendCommand(ctx, content, "sh", "-lc", command)
	if err != nil || result == nil || result.ExitCode != 0 {
		stderr := ""
		if result != nil {
			stderr = strings.TrimSpace(result.Stderr)
		}
		t.Fatalf("write WSL file %s: %v %s", path, err, stderr)
	}
}

func waitForWSLRegistry(t *testing.T, ctx context.Context, provider providers.PlatformProvider, name string) string {
	t.Helper()
	var registryHost string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		result, err := provider.RunDocker(ctx, "port", name, "5000/tcp")
		if err == nil && result.ExitCode == 0 {
			registryHost = normalizeWSLDockerPort(result.Stdout)
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

func normalizeWSLDockerPort(stdout string) string {
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

func hasWSLRegistryAccount(accounts []models.RegistryAccount, registry string, username string) bool {
	for _, account := range accounts {
		if strings.EqualFold(account.Registry, registry) && account.Username == username && account.Source == "authsFile" {
			return true
		}
	}
	return false
}

func redactedWSLDockerConfig(t *testing.T, ctx context.Context, provider *tempDockerConfigWSLProvider) string {
	t.Helper()
	runner, ok := provider.PlatformProvider.(interface {
		RunBackendCommand(context.Context, string, ...string) (*providers.CommandResult, error)
	})
	if !ok {
		return "<unavailable>"
	}
	command := "cat " + wslShellQuote(provider.dockerConfig+"/config.json") + " 2>/dev/null || true"
	result, err := runner.RunBackendCommand(ctx, "", "sh", "-lc", command)
	if err != nil || result == nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) == "" {
		return "<unavailable>"
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return "<invalid-json>"
	}
	if auths, ok := payload["auths"].(map[string]any); ok {
		for key := range auths {
			auths[key] = map[string]any{"auth": "<redacted>"}
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "<invalid-json>"
	}
	return string(raw)
}

func wslShellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = wslShellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func wslShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
