package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

func TestNormalizeImageRefCorpus(t *testing.T) {
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []struct {
		raw        string
		registry   string
		repository string
		tag        string
		pinned     bool
	}{
		{"nginx", "docker.io", "library/nginx", "latest", false},
		{"redis:7", "docker.io", "library/redis", "7", false},
		{"library/postgres:16", "docker.io", "library/postgres", "16", false},
		{"docker.io/library/busybox:1.36", "docker.io", "library/busybox", "1.36", false},
		{"index.docker.io/library/alpine:3.20", "docker.io", "library/alpine", "3.20", false},
		{"registry-1.docker.io/library/httpd:2", "docker.io", "library/httpd", "2", false},
		{"ghcr.io/org/app:main", "ghcr.io", "org/app", "main", false},
		{"registry.gitlab.com/group/project/image:v1", "registry.gitlab.com", "group/project/image", "v1", false},
		{"quay.io/coreos/etcd:v3.5", "quay.io", "coreos/etcd", "v3.5", false},
		{"us-docker.pkg.dev/project/repo/app:prod", "us-docker.pkg.dev", "project/repo/app", "prod", false},
		{"localhost:5000/team/app:dev", "localhost:5000", "team/app", "dev", false},
		{"127.0.0.1:5001/team/app:dev", "127.0.0.1:5001", "team/app", "dev", false},
		{"example.com/ns/app", "example.com", "ns/app", "latest", false},
		{"example.com:5443/ns/app:release-2026.06", "example.com:5443", "ns/app", "release-2026.06", false},
		{"example.com/ns/app@" + digest, "example.com", "ns/app", "", true},
		{"ubuntu", "docker.io", "library/ubuntu", "latest", false},
		{"debian:bookworm-slim", "docker.io", "library/debian", "bookworm-slim", false},
		{"mcr.microsoft.com/dotnet/runtime:8.0", "mcr.microsoft.com", "dotnet/runtime", "8.0", false},
		{"public.ecr.aws/nginx/nginx:stable", "public.ecr.aws", "nginx/nginx", "stable", false},
		{"registry.k8s.io/pause:3.10", "registry.k8s.io", "pause", "3.10", false},
		{"gcr.io/distroless/static:nonroot", "gcr.io", "distroless/static", "nonroot", false},
		{"lscr.io/linuxserver/swag:latest", "lscr.io", "linuxserver/swag", "latest", false},
		{"docker.io/rcooler/cairn:test", "docker.io", "rcooler/cairn", "test", false},
		{"example.net/a/b/c:d", "example.net", "a/b/c", "d", false},
		{"example.net:5000/a_b/c.d:e-f", "example.net:5000", "a_b/c.d", "e-f", false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.raw, func(t *testing.T) {
			got, err := NormalizeImageRef(tt.raw)
			if err != nil {
				t.Fatalf("NormalizeImageRef() error = %v", err)
			}
			if got.Registry != tt.registry || got.Repository != tt.repository || got.Tag != tt.tag || got.Pinned != tt.pinned {
				t.Fatalf("NormalizeImageRef() = %#v", got)
			}
		})
	}
}

func TestAccountsFromDockerConfig(t *testing.T) {
	config := dockerConfig{
		Auths: map[string]dockerAuth{
			"https://index.docker.io/v1/": {Auth: base64.StdEncoding.EncodeToString([]byte("ada:secret"))},
			"ghcr.io":                     {Username: "octo"},
			"registry.gitlab.com":         {},
		},
		CredHelpers: map[string]string{"ghcr.io": "gh"},
		CredsStore:  "pass",
	}
	got := accountsFromDockerConfig(config, time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC))
	want := []models.RegistryAccount{
		{Registry: "docker.io", Username: "ada", Source: "authsFile", LoggedIn: true, LastVerifiedAt: got[0].LastVerifiedAt},
		{Registry: "ghcr.io", Username: "octo", Source: "credHelper", LoggedIn: true, LastVerifiedAt: got[0].LastVerifiedAt},
		{Registry: "registry.gitlab.com", Source: "credsStore", LoggedIn: true, LastVerifiedAt: got[0].LastVerifiedAt},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("accounts = %#v, want %#v", got, want)
	}
}

func TestListRegistryAccountsIncludesCredentialHelperList(t *testing.T) {
	config := `{"auths":{"ghcr.io":{}},"credHelpers":{"ghcr.io":"gh"},"credsStore":"pass"}`
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: config,
			"docker-credential-gh list":   `{"ghcr.io":"octo"}`,
			"docker-credential-pass list": `{"https://index.docker.io/v1/":"ada"}`,
		},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	got, err := manager.ListRegistryAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListRegistryAccounts() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("accounts = %#v", got)
	}
	if got[0].Registry != "docker.io" || got[0].Username != "ada" || got[0].Source != "credsStore" {
		t.Fatalf("docker account = %#v", got[0])
	}
	if got[1].Registry != "ghcr.io" || got[1].Username != "octo" || got[1].Source != "credHelper" {
		t.Fatalf("ghcr account = %#v", got[1])
	}
}

func TestLoginPipesSecretThroughStdin(t *testing.T) {
	var registryHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	registryHost = strings.TrimPrefix(server.URL, "http://")

	auth := base64.StdEncoding.EncodeToString([]byte("ada:token"))
	provider := &fakeRegistryProvider{
		backendStdout: `{"auths":{"` + registryHost + `":{"auth":"` + auth + `"}}}`,
		dockerResult:  &providers.CommandResult{ExitCode: 0},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	err := manager.Login(context.Background(), models.RegistryLoginRequest{
		Registry: registryHost,
		Username: "ada",
		Secret:   "token",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if provider.dockerInput != "token\n" {
		t.Fatalf("docker input = %q", provider.dockerInput)
	}
	got := strings.Join(provider.dockerArgs, " ")
	if strings.Contains(got, "token") {
		t.Fatalf("secret leaked into argv: %q", got)
	}
	if want := []string{"login", registryHost, "-u", "ada", "--password-stdin"}; !reflect.DeepEqual(provider.dockerArgs, want) {
		t.Fatalf("docker args = %#v, want %#v", provider.dockerArgs, want)
	}
}

func TestResolveDigestHandlesBearerAuthAndIndexSelection(t *testing.T) {
	var serverURL string
	tokenRequested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			tokenRequested = true
			if got := r.URL.Query().Get("scope"); got != "repository:library/nginx:pull" {
				t.Fatalf("scope = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "ok"})
		case r.URL.Path == "/v2/library/nginx/manifests/1.25" && r.Method == http.MethodHead:
			if r.Header.Get("Authorization") == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+serverURL+`/token",service="registry.test",scope="repository:library/nginx:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Docker-Content-Digest", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v2/library/nginx/manifests/1.25" && r.Method == http.MethodGet:
			if r.Header.Get("Authorization") == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+serverURL+`/token",service="registry.test",scope="repository:library/nginx:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") != "Bearer ok" {
				t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"manifests": []map[string]any{
					{"digest": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "platform": map[string]string{"os": "linux", "architecture": "arm64"}},
					{"digest": "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", "platform": map[string]string{"os": "linux", "architecture": "amd64"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL
	registryHost := strings.TrimPrefix(server.URL, "http://")

	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{}}, nil)
	got, err := manager.ResolveDigest(context.Background(), registryHost+"/library/nginx:1.25", ResolveOptions{
		Platform: Platform{OS: "linux", Architecture: "amd64"},
	})
	if err != nil {
		t.Fatalf("ResolveDigest() error = %v", err)
	}
	if !tokenRequested {
		t.Fatalf("token endpoint was not requested")
	}
	if got.IndexDigest != "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ||
		got.ManifestDigest != "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" {
		t.Fatalf("digest result = %#v", got)
	}
	cached, err := manager.ResolveDigest(context.Background(), registryHost+"/library/nginx:1.25", ResolveOptions{
		Platform: Platform{OS: "linux", Architecture: "amd64"},
	})
	if err != nil {
		t.Fatalf("ResolveDigest(cached) error = %v", err)
	}
	if !cached.FromCache {
		t.Fatalf("cached result did not set FromCache: %#v", cached)
	}
}

func TestResolveDigestRateLimitOpensCircuit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	registryHost := strings.TrimPrefix(server.URL, "http://")
	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{}}, nil)

	_, err := manager.ResolveDigest(context.Background(), registryHost+"/team/app:latest", ResolveOptions{BypassCache: true})
	if !apperror.IsCode(err, apperror.RegistryRateLimit) {
		t.Fatalf("first error = %v, want rate limit", err)
	}
	got, err := manager.ResolveDigest(context.Background(), registryHost+"/team/app:latest", ResolveOptions{BypassCache: true})
	if !apperror.IsCode(err, apperror.RegistryRateLimit) {
		t.Fatalf("circuit error = %v, want rate limit", err)
	}
	if got == nil || !got.RateLimited || got.RetryAfter <= 0 {
		t.Fatalf("circuit result = %#v", got)
	}
}

type fakeResolver struct {
	provider providers.PlatformProvider
}

func (r fakeResolver) ActiveProvider(context.Context) (providers.PlatformProvider, error) {
	return r.provider, nil
}

type fakeRegistryProvider struct {
	backendStdout  string
	backendResults map[string]string
	backendInput   string
	backendArgs    []string
	dockerInput    string
	dockerArgs     []string
	dockerResult   *providers.CommandResult
}

func (p *fakeRegistryProvider) ID() string          { return "fake" }
func (p *fakeRegistryProvider) DisplayName() string { return "Fake" }
func (p *fakeRegistryProvider) Type() string        { return providers.TypeLinuxNative }
func (p *fakeRegistryProvider) Platform() string    { return providers.PlatformLinux }
func (p *fakeRegistryProvider) Detect(context.Context) (*models.ProviderStatus, error) {
	return nil, nil
}
func (p *fakeRegistryProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return nil, nil
}
func (p *fakeRegistryProvider) ExecuteInstallStep(context.Context, string, int, chan<- providers.InstallProgress) error {
	return nil
}
func (p *fakeRegistryProvider) Start(context.Context) error   { return nil }
func (p *fakeRegistryProvider) Stop(context.Context) error    { return nil }
func (p *fakeRegistryProvider) Restart(context.Context) error { return nil }
func (p *fakeRegistryProvider) DockerHost(context.Context) (string, error) {
	return "", nil
}
func (p *fakeRegistryProvider) DockerContext(context.Context) (string, error) {
	return "", nil
}
func (p *fakeRegistryProvider) RunDocker(context.Context, ...string) (*providers.CommandResult, error) {
	if p.dockerResult != nil {
		return p.dockerResult, nil
	}
	return &providers.CommandResult{ExitCode: 0}, nil
}
func (p *fakeRegistryProvider) RunDockerWithInput(_ context.Context, input string, args ...string) (*providers.CommandResult, error) {
	p.dockerInput = input
	p.dockerArgs = append([]string(nil), args...)
	if p.dockerResult != nil {
		p.dockerResult.Command = append([]string{"docker"}, args...)
		return p.dockerResult, nil
	}
	return &providers.CommandResult{Command: append([]string{"docker"}, args...), ExitCode: 0}, nil
}
func (p *fakeRegistryProvider) RunBackendCommand(_ context.Context, input string, args ...string) (*providers.CommandResult, error) {
	p.backendInput = input
	p.backendArgs = append([]string(nil), args...)
	if p.backendResults != nil {
		if stdout, ok := p.backendResults[strings.Join(args, " ")]; ok {
			return &providers.CommandResult{Command: args, Stdout: stdout, ExitCode: 0}, nil
		}
	}
	return &providers.CommandResult{Command: args, Stdout: p.backendStdout, ExitCode: 0}, nil
}
func (p *fakeRegistryProvider) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	return nil, nil
}
func (p *fakeRegistryProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeRegistryProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeRegistryProvider) MapPathToBackend(path string) (string, error) { return path, nil }
func (p *fakeRegistryProvider) MapPathToHost(path string) (string, error)    { return path, nil }
