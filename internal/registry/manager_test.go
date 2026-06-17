package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
	dockerregistry "github.com/docker/docker/api/types/registry"
)

func TestNormalizeImageRefCorpus(t *testing.T) {
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digest512 := "sha512:" + strings.Repeat("b", 128)
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
		{"example.com/ns/app:tag@" + digest512, "example.com", "ns/app", "", true},
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

func TestEncodeDockerAuthConfigUsesCredentialHelper(t *testing.T) {
	config := `{"auths":{"ghcr.io":{}},"credHelpers":{"ghcr.io":"gh"}}`
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: config,
			"docker-credential-gh get": `{"Username":"octo","Secret":"token"}`,
		},
	}
	encoded, err := EncodeDockerAuthConfig(context.Background(), provider, "ghcr.io")
	if err != nil {
		t.Fatalf("EncodeDockerAuthConfig() error = %v", err)
	}
	raw, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode auth config: %v", err)
	}
	var auth dockerregistry.AuthConfig
	if err := json.Unmarshal(raw, &auth); err != nil {
		t.Fatalf("unmarshal auth config: %v", err)
	}
	if auth.Username != "octo" || auth.Password != "token" || auth.ServerAddress != "ghcr.io" {
		t.Fatalf("auth config = %#v", auth)
	}
	if provider.backendInput != "ghcr.io\n" {
		t.Fatalf("helper input = %q", provider.backendInput)
	}
}

func TestEncodeDockerAuthConfigHandlesBadDockerConfigJSON(t *testing.T) {
	t.Parallel()
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{bad`,
		},
	}

	_, err := EncodeDockerAuthConfig(context.Background(), provider, "ghcr.io")
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("EncodeDockerAuthConfig() error = %v, want internal parse error", err)
	}
}

func TestEncodeDockerAuthConfigIgnoresBadCredentialHelperJSON(t *testing.T) {
	t.Parallel()
	config := `{"auths":{"ghcr.io":{}},"credHelpers":{"ghcr.io":"gh"}}`
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: config,
			"docker-credential-gh get": `{bad`,
		},
	}

	encoded, err := EncodeDockerAuthConfig(context.Background(), provider, "ghcr.io")
	if err != nil {
		t.Fatalf("EncodeDockerAuthConfig() error = %v", err)
	}
	if encoded != "" {
		t.Fatalf("encoded auth = %q, want empty", encoded)
	}
}

func TestParseWWWAuthenticateMalformedChallenges(t *testing.T) {
	t.Parallel()

	empty := parseWWWAuthenticate("")
	if empty.Scheme != "" || len(empty.Params) != 0 {
		t.Fatalf("empty challenge = %#v", empty)
	}

	basic := parseWWWAuthenticate("Basic")
	if basic.Scheme != "Basic" || len(basic.Params) != 0 {
		t.Fatalf("basic challenge = %#v", basic)
	}

	bearer := parseWWWAuthenticate(`Bearer realm="https://registry.example/token",broken,service=registry.example,scope="repository:library/nginx:pull"`)
	if bearer.Scheme != "Bearer" {
		t.Fatalf("bearer scheme = %q", bearer.Scheme)
	}
	if bearer.Params["realm"] != "https://registry.example/token" ||
		bearer.Params["service"] != "registry.example" ||
		bearer.Params["scope"] != "repository:library/nginx:pull" {
		t.Fatalf("bearer params = %#v", bearer.Params)
	}
	if _, ok := bearer.Params["broken"]; ok {
		t.Fatalf("malformed param was preserved: %#v", bearer.Params)
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

func TestLoginConfiguresCredentialHelperBeforeDockerLogin(t *testing.T) {
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

	auth := base64.StdEncoding.EncodeToString([]byte("ada:old-token"))
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{"auths":{"` + registryHost + `":{"auth":"` + auth + `"}},"experimental":"enabled"}`,
			"docker-credential-pass list": `{}`,
			"docker-credential-pass get":  `{"Username":"ada","Secret":"token"}`,
		},
		dockerResult: &providers.CommandResult{ExitCode: 0},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	manager.Settings = testRegistrySettings(t, registryCredentialModeDockerHelper)

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

	var written map[string]json.RawMessage
	if err := json.Unmarshal([]byte(provider.backendConfig), &written); err != nil {
		t.Fatalf("written Docker config is not JSON: %v\n%s", err, provider.backendConfig)
	}
	var helpers map[string]string
	if err := json.Unmarshal(written["credHelpers"], &helpers); err != nil {
		t.Fatalf("credHelpers = %s: %v", string(written["credHelpers"]), err)
	}
	if helpers[registryHost] != "pass" {
		t.Fatalf("cred helper for %s = %q, want pass", registryHost, helpers[registryHost])
	}
	var auths map[string]json.RawMessage
	if err := json.Unmarshal(written["auths"], &auths); err != nil {
		t.Fatalf("auths = %s: %v", string(written["auths"]), err)
	}
	if _, ok := auths[registryHost]; ok {
		t.Fatalf("inline auth for %s was not removed: %s", registryHost, string(written["auths"]))
	}
	if string(written["experimental"]) != `"enabled"` {
		t.Fatalf("unrelated Docker config key was not preserved: %#v", written)
	}
}

func TestLoginFailsWhenCredentialHelperUnavailable(t *testing.T) {
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{}`,
		},
		backendDefaultExitCode: 127,
		backendDefaultStderr:   "not found",
		dockerResult:           &providers.CommandResult{ExitCode: 0},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	manager.Settings = testRegistrySettings(t, registryCredentialModeDockerHelper)

	err := manager.Login(context.Background(), models.RegistryLoginRequest{
		Registry: "ghcr.io",
		Username: "ada",
		Secret:   "token",
	})
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("Login() error = %v, want provider-not-ready", err)
	}
	if provider.dockerInput != "" {
		t.Fatalf("docker login received secret despite missing helper: %q", provider.dockerInput)
	}
}

func TestLoginRespectsDisabledCredentialMode(t *testing.T) {
	provider := &fakeRegistryProvider{dockerResult: &providers.CommandResult{ExitCode: 0}}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	manager.Settings = testRegistrySettings(t, registryCredentialModeNone)

	err := manager.Login(context.Background(), models.RegistryLoginRequest{
		Registry: "ghcr.io",
		Username: "ada",
		Secret:   "token",
	})
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("Login() error = %v, want conflict", err)
	}
	if provider.dockerInput != "" {
		t.Fatalf("docker login received secret while login is disabled: %q", provider.dockerInput)
	}
}

func TestReadDockerConfigNormalizesUTF16LE(t *testing.T) {
	t.Parallel()
	raw := `{"auths":{"ghcr.io":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("ada:token")) + `"}}}`
	provider := &fakeRegistryProvider{backendStdout: utf16LEWithBOM(raw)}
	manager := NewManager(fakeResolver{provider: provider}, nil)

	accounts, err := manager.ListRegistryAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListRegistryAccounts() error = %v", err)
	}
	if !hasTestRegistryAccount(accounts, "ghcr.io", "ada") {
		t.Fatalf("accounts = %#v", accounts)
	}
}

func TestRegistryCLIArgRejectsFlagLikeHosts(t *testing.T) {
	t.Parallel()
	for _, registry := range []string{"--config=/tmp/evil", "-u", "registry.example.com --debug", "registry.example.com\n--debug"} {
		registry := registry
		t.Run(registry, func(t *testing.T) {
			t.Parallel()
			if _, err := registryCLIArg(registry); !apperror.IsCode(err, apperror.Conflict) {
				t.Fatalf("registryCLIArg(%q) error = %v, want conflict", registry, err)
			}
		})
	}
}

func utf16LEWithBOM(value string) string {
	out := []byte{0xff, 0xfe}
	for _, r := range value {
		if r > 0xffff {
			continue
		}
		out = append(out, byte(r), byte(r>>8))
	}
	return string(out)
}

func hasTestRegistryAccount(accounts []models.RegistryAccount, registry string, username string) bool {
	for _, account := range accounts {
		if account.Registry == registry && account.Username == username {
			return true
		}
	}
	return false
}

func TestPlainHTTPRegistryRequiresExactLoopbackHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		registry string
		want     bool
	}{
		{"localhost", true},
		{"localhost:5000", true},
		{"127.0.0.1", true},
		{"127.0.0.1:5000", true},
		{"[::1]:5000", true},
		{"127.0.0.1.attacker.test", false},
		{"[::1].attacker.test", false},
		{"example.com:5000", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.registry, func(t *testing.T) {
			t.Parallel()
			if got := isPlainHTTPRegistry(tt.registry); got != tt.want {
				t.Fatalf("isPlainHTTPRegistry(%q) = %v, want %v", tt.registry, got, tt.want)
			}
		})
	}
}

func TestAuthDoesNotTreatUnexpectedClientStatusAsLoggedIn(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	registryHost := strings.TrimPrefix(server.URL, "http://")
	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{}}, nil)

	status, err := manager.TestAuth(context.Background(), registryHost)
	if err != nil {
		t.Fatalf("TestAuth() error = %v", err)
	}
	if status.LoggedIn || !strings.Contains(status.Error, "404") {
		t.Fatalf("status = %#v, want logged out 404 error", status)
	}
}

func TestBearerTokenRealmRejectsPlainHTTPRemote(t *testing.T) {
	t.Parallel()
	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{}}, nil)
	_, err := manager.fetchBearerToken(context.Background(), authChallenge{
		Scheme: "Bearer",
		Params: map[string]string{"realm": "http://registry-token.example/token"},
	}, "repository:library/nginx:pull", credential{Username: "ada", Password: "secret"})
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("fetchBearerToken() error = %v, want registry auth", err)
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

func TestRetryAfterFromErrorUsesTypedDuration(t *testing.T) {
	err := retryAfterError{
		error:      apperror.New(apperror.RegistryRateLimit, "Registry rate limit reached", apperror.WithDetail("retry-after=999h")),
		retryAfter: 2 * time.Second,
	}

	if got := retryAfterFromError(err); got != 2*time.Second {
		t.Fatalf("retryAfterFromError() = %s, want 2s", got)
	}
}

type fakeResolver struct {
	provider providers.PlatformProvider
}

func (r fakeResolver) ActiveProvider(context.Context) (providers.PlatformProvider, error) {
	return r.provider, nil
}

func testRegistrySettings(t *testing.T, mode string) *store.SettingsRepository {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	settings := db.Settings()
	if mode != "" {
		if err := settings.SetString(ctx, "registry.credentials_mode", mode); err != nil {
			t.Fatalf("SetString() error = %v", err)
		}
	}
	return settings
}

func isBackendConfigReadForTest(command string) bool {
	return strings.Contains(command, "config.json") &&
		(strings.Contains(command, "cat \"${DOCKER_CONFIG") || strings.Contains(command, "Get-Content"))
}

func isBackendConfigWriteForTest(command string) bool {
	return strings.Contains(command, "config.json") &&
		(strings.Contains(command, "cat >") || strings.Contains(command, "Set-Content"))
}

type fakeRegistryProvider struct {
	backendStdout          string
	backendResults         map[string]string
	backendDefaultExitCode int
	backendDefaultStderr   string
	backendConfig          string
	backendInput           string
	backendInputs          []string
	backendArgs            []string
	backendCalls           []string
	dockerInput            string
	dockerArgs             []string
	dockerResult           *providers.CommandResult
	providerType           string
	providerPlatform       string
}

func (p *fakeRegistryProvider) ID() string          { return "fake" }
func (p *fakeRegistryProvider) DisplayName() string { return "Fake" }
func (p *fakeRegistryProvider) Type() string {
	if p.providerType != "" {
		return p.providerType
	}
	return providers.TypeLinuxNative
}
func (p *fakeRegistryProvider) Platform() string {
	if p.providerPlatform != "" {
		return p.providerPlatform
	}
	return providers.PlatformLinux
}
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
	p.backendInputs = append(p.backendInputs, input)
	p.backendArgs = append([]string(nil), args...)
	joined := strings.Join(args, " ")
	p.backendCalls = append(p.backendCalls, joined)
	if isBackendConfigWriteForTest(joined) {
		p.backendConfig = input
		return &providers.CommandResult{Command: args, ExitCode: 0}, nil
	}
	if isBackendConfigReadForTest(joined) && p.backendConfig != "" {
		return &providers.CommandResult{Command: args, Stdout: p.backendConfig, ExitCode: 0}, nil
	}
	if p.backendResults != nil {
		if stdout, ok := p.backendResults[joined]; ok {
			return &providers.CommandResult{Command: args, Stdout: stdout, ExitCode: 0}, nil
		}
	}
	return &providers.CommandResult{Command: args, Stdout: p.backendStdout, Stderr: p.backendDefaultStderr, ExitCode: p.backendDefaultExitCode}, nil
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
