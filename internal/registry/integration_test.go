//go:build linux

package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"golang.org/x/crypto/bcrypt"
)

func TestManagerRealDockerRegistryAuthRoundTrip(t *testing.T) {
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

	provider := &realRegistryProvider{dockerConfig: t.TempDir()}
	if result, err := provider.RunDocker(ctx, "image", "inspect", "registry:2"); err != nil || result.ExitCode != 0 {
		if result, err := provider.RunDocker(ctx, "pull", "registry:2"); err != nil || result.ExitCode != 0 {
			t.Fatalf("pull registry:2: result=%#v err=%v", result, err)
		}
	}

	name := "cairn-registry-" + time.Now().UTC().Format("20060102150405")
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

	registryHost := waitForRegistry(t, ctx, provider, name)
	manager := NewManager(realRegistryResolver{provider: provider}, nil)
	if err := manager.Login(ctx, models.RegistryLoginRequest{Registry: registryHost, Username: username, Secret: secret, SecretKind: "password"}); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	accounts, err := manager.ListRegistryAccounts(ctx)
	if err != nil {
		t.Fatalf("ListRegistryAccounts() error = %v", err)
	}
	if !hasRegistryAccount(accounts, registryHost, username) {
		t.Fatalf("account for %s/%s missing: %#v", registryHost, username, accounts)
	}

	imageRef := registryHost + "/cairn/private:1.0"
	if result, err := provider.RunDocker(ctx, "tag", "registry:2", imageRef); err != nil || result.ExitCode != 0 {
		t.Fatalf("tag image: result=%#v err=%v", result, err)
	}
	if result, err := provider.RunDocker(ctx, "push", imageRef); err != nil || result.ExitCode != 0 {
		t.Fatalf("push image: result=%#v err=%v", result, err)
	}
	digest, err := manager.ResolveDigest(ctx, imageRef, ResolveOptions{
		BypassCache: true,
		Platform:    Platform{OS: "linux", Architecture: "amd64"},
	})
	if err != nil {
		t.Fatalf("ResolveDigest(private) error = %v", err)
	}
	if digest.ManifestDigest == "" {
		t.Fatalf("private digest missing: %#v", digest)
	}

	if err := manager.Logout(ctx, registryHost); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	_, err = manager.ResolveDigest(ctx, imageRef, ResolveOptions{BypassCache: true, Platform: Platform{OS: "linux", Architecture: "amd64"}})
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("ResolveDigest after logout error = %v, want %s", err, apperror.RegistryAuth)
	}
}

func waitForRegistry(t *testing.T, ctx context.Context, provider *realRegistryProvider, name string) string {
	t.Helper()
	var registryHost string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		result, err := provider.RunDocker(ctx, "port", name, "5000/tcp")
		if err == nil && result.ExitCode == 0 {
			registryHost = normalizeDockerPort(result.Stdout)
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

func normalizeDockerPort(stdout string) string {
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

func hasRegistryAccount(accounts []models.RegistryAccount, registry string, username string) bool {
	for _, account := range accounts {
		if normalizeRegistryHost(account.Registry) == normalizeRegistryHost(registry) && account.Username == username && account.Source == "authsFile" {
			return true
		}
	}
	return false
}

type realRegistryResolver struct {
	provider providers.PlatformProvider
}

func (r realRegistryResolver) ActiveProvider(context.Context) (providers.PlatformProvider, error) {
	return r.provider, nil
}

type realRegistryProvider struct {
	dockerConfig string
}

func (p *realRegistryProvider) ID() string          { return "real-registry" }
func (p *realRegistryProvider) DisplayName() string { return "Real Registry" }
func (p *realRegistryProvider) Type() string        { return providers.TypeLinuxNative }
func (p *realRegistryProvider) Platform() string    { return providers.PlatformLinux }
func (p *realRegistryProvider) Detect(context.Context) (*models.ProviderStatus, error) {
	return nil, nil
}
func (p *realRegistryProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return nil, nil
}
func (p *realRegistryProvider) ExecuteInstallStep(context.Context, string, int, chan<- providers.InstallProgress) error {
	return nil
}
func (p *realRegistryProvider) Start(context.Context) error   { return nil }
func (p *realRegistryProvider) Stop(context.Context) error    { return nil }
func (p *realRegistryProvider) Restart(context.Context) error { return nil }
func (p *realRegistryProvider) DockerHost(context.Context) (string, error) {
	return "", nil
}
func (p *realRegistryProvider) DockerContext(context.Context) (string, error) {
	return "", nil
}
func (p *realRegistryProvider) RunDocker(ctx context.Context, args ...string) (*providers.CommandResult, error) {
	return p.run(ctx, "", "docker", args...)
}
func (p *realRegistryProvider) RunDockerWithInput(ctx context.Context, input string, args ...string) (*providers.CommandResult, error) {
	return p.run(ctx, input, "docker", args...)
}
func (p *realRegistryProvider) RunBackendCommand(ctx context.Context, input string, args ...string) (*providers.CommandResult, error) {
	if len(args) == 0 {
		return nil, errors.New("backend command is required")
	}
	if len(args) >= 3 && args[0] == "sh" && args[1] == "-lc" && strings.Contains(args[2], "config.json") {
		raw, err := os.ReadFile(filepath.Join(p.dockerConfig, "config.json"))
		if errors.Is(err, os.ErrNotExist) {
			return &providers.CommandResult{Command: args, ExitCode: 0}, nil
		}
		if err != nil {
			return &providers.CommandResult{Command: args, ExitCode: 1, Stderr: err.Error()}, err
		}
		return &providers.CommandResult{Command: args, Stdout: string(raw), ExitCode: 0}, nil
	}
	return p.run(ctx, input, args[0], args[1:]...)
}
func (p *realRegistryProvider) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	return nil, nil
}
func (p *realRegistryProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *realRegistryProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *realRegistryProvider) MapPathToBackend(path string) (string, error) { return path, nil }
func (p *realRegistryProvider) MapPathToHost(path string) (string, error)    { return path, nil }

func (p *realRegistryProvider) run(ctx context.Context, input string, name string, args ...string) (*providers.CommandResult, error) {
	started := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+p.dockerConfig)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := &providers.CommandResult{
		Command:  append([]string{name}, args...),
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
	return result, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}
