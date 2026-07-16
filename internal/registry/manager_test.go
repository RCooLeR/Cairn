package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

func TestEncodeDockerAuthConfigMapsHelperTokenUsernameToIdentityToken(t *testing.T) {
	t.Parallel()
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{"credHelpers":{"ghcr.io":"gh"}}`,
			"docker-credential-gh get": `{"Username":"<token>","Secret":"identity-token"}`,
		},
	}
	encoded, err := EncodeDockerAuthConfig(context.Background(), provider, "ghcr.io")
	if err != nil {
		t.Fatalf("EncodeDockerAuthConfig() error = %v", err)
	}
	raw, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var auth dockerregistry.AuthConfig
	if err := json.Unmarshal(raw, &auth); err != nil {
		t.Fatal(err)
	}
	if auth.IdentityToken != "identity-token" || auth.Username != "" || auth.Password != "" {
		t.Fatalf("auth config = %#v", auth)
	}
}

func TestCredentialHelperNotFoundRecognizesOfficialStdoutMarkerOnly(t *testing.T) {
	t.Parallel()
	if !credentialHelperNotFound(&providers.CommandResult{ExitCode: 1, Stdout: "credentials not found in native keychain\n"}, errors.New("exit status 1")) {
		t.Fatal("official helper stdout marker was not recognized")
	}
	if credentialHelperNotFound(&providers.CommandResult{ExitCode: 1, Stdout: "keychain service unavailable\n"}, errors.New("exit status 1")) {
		t.Fatal("unknown helper failure was treated as a missing credential")
	}
	if credentialHelperNotFound(&providers.CommandResult{ExitCode: 1, Stderr: "no credentials store is configured"}, nil) {
		t.Fatal("generic no-credentials diagnostic was treated as a missing registry credential")
	}
	if credentialHelperNotFound(&providers.CommandResult{ExitCode: 1, Stdout: "credentials not found in native keychain\nfatal backend corruption\n"}, errors.New("exit status 1")) {
		t.Fatal("mixed helper stdout was treated as a missing credential")
	}
	if credentialHelperNotFound(&providers.CommandResult{ExitCode: 1, Stdout: "credentials not found in native keychain\n", Stderr: "fatal backend corruption\n"}, errors.New("exit status 1")) {
		t.Fatal("mixed helper streams were treated as a missing credential")
	}
	if credentialHelperNotFound(&providers.CommandResult{ExitCode: 1, Stdout: "credentials not found in native keychain\n"}, errors.New("backend transport failed")) {
		t.Fatal("additional runner error was treated as a missing credential")
	}
	if credentialHelperNotFound(&providers.CommandResult{ExitCode: 1, Stdout: strings.Repeat("x", (4<<10)+1) + " credentials not found"}, errors.New("exit status 1")) {
		t.Fatal("oversized helper output was used to authorize missing-credential handling")
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

func TestEncodeDockerAuthConfigFailsClosedOnBadCredentialHelperJSON(t *testing.T) {
	t.Parallel()
	config := `{"auths":{"ghcr.io":{}},"credHelpers":{"ghcr.io":"gh"}}`
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: config,
			"docker-credential-gh get": `{bad`,
		},
	}

	_, err := EncodeDockerAuthConfig(context.Background(), provider, "ghcr.io")
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("EncodeDockerAuthConfig() error = %v, want registry auth", err)
	}
}

func TestEncodeDockerAuthConfigDistinguishesMissingCredentialFromHelperFailure(t *testing.T) {
	t.Parallel()
	config := `{"credHelpers":{"ghcr.io":"gh"}}`
	configCommand := `sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`

	missing := &fakeRegistryProvider{
		backendResults:         map[string]string{configCommand: config},
		backendDefaultExitCode: 1,
		backendDefaultStderr:   "credentials not found in native keychain",
	}
	encoded, err := EncodeDockerAuthConfig(context.Background(), missing, "ghcr.io")
	if err != nil || encoded != "" {
		t.Fatalf("missing credential encoded=%q error=%v", encoded, err)
	}

	broken := &fakeRegistryProvider{
		backendResults:         map[string]string{configCommand: config},
		backendDefaultExitCode: 2,
		backendDefaultStderr:   "helper process crashed",
	}
	_, err = EncodeDockerAuthConfig(context.Background(), broken, "ghcr.io")
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("broken helper error = %v, want registry auth", err)
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
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{"auths":{"` + registryHost + `":{"auth":"` + auth + `"}}}`,
			"docker-credential-pass list": `{}`,
		},
		backendDefaultExitCode: 1,
		backendDefaultStderr:   "credentials not found in native keychain",
		dockerResult:           &providers.CommandResult{ExitCode: 0},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	secret := " \ttoken\n"
	err := manager.Login(context.Background(), models.RegistryLoginRequest{
		Registry: registryHost,
		Username: "ada",
		Secret:   secret,
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if provider.dockerInput != registryLoginInput(secret) {
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

func TestRegistryLoginInputPreservesOpaqueSecretAfterDockerDelimiterRemoval(t *testing.T) {
	t.Parallel()
	secrets := []string{
		" token ",
		"\ttoken\t",
		"token\n",
		"token\r",
		"token\r\n",
		"token\u00a0",
	}
	for _, secret := range secrets {
		input := registryLoginInput(secret)
		decoded := input
		for _, eol := range []string{"\r\n", "\n", "\r"} {
			if value, ok := strings.CutSuffix(decoded, eol); ok {
				decoded = value
				break
			}
		}
		if decoded != secret {
			t.Fatalf("secret %q became %q through password stdin", secret, decoded)
		}
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

func TestLoginPreservesInlineAuthWhenDockerLoginFails(t *testing.T) {
	registryHost := "registry.example.test"
	auth := base64.StdEncoding.EncodeToString([]byte("ada:old-token"))
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{"auths":{"` + registryHost + `":{"auth":"` + auth + `"}}}`,
			"docker-credential-pass list": `{}`,
		},
		backendDefaultExitCode: 1,
		backendDefaultStderr:   "credentials not found in native keychain",
		dockerResult:           &providers.CommandResult{ExitCode: 1, Stderr: "denied"},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	manager.Settings = testRegistrySettings(t, registryCredentialModeDockerHelper)

	err := manager.Login(context.Background(), models.RegistryLoginRequest{
		Registry: registryHost,
		Username: "ada",
		Secret:   "bad-token",
	})
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("Login() error = %v, want registry auth", err)
	}
	var written dockerConfig
	if err := json.Unmarshal([]byte(provider.backendConfig), &written); err != nil {
		t.Fatalf("written Docker config is not JSON: %v\n%s", err, provider.backendConfig)
	}
	if _, _, ok := authEntryForRegistry(written, registryHost); !ok {
		t.Fatalf("inline auth for %s was removed after failed login: %s", registryHost, provider.backendConfig)
	}
	if helperForRegistry(written, registryHost) != "" {
		t.Fatalf("preparatory helper mapping still masks old inline auth: %s", provider.backendConfig)
	}
}

func TestLoginRollsBackCredentialAfterPostLoginVerificationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	registryHost := strings.TrimPrefix(server.URL, "http://")
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{}`,
			"docker-credential-pass list": `{}`,
		},
		backendDefaultExitCode: 1,
		backendDefaultStderr:   "credentials not found in native keychain",
		dockerResult:           &providers.CommandResult{ExitCode: 0},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)

	err := manager.Login(context.Background(), models.RegistryLoginRequest{
		Registry: registryHost,
		Username: "ada",
		Secret:   "new-token",
	})
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("Login() error = %v, want registry auth", err)
	}
	if len(provider.dockerCalls) != 0 {
		t.Fatalf("rollback destroyed credentials through docker logout: %#v", provider.dockerCalls)
	}
}

func TestLoginRestoresPreexistingHelperCredentialAfterVerificationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	registryHost := strings.TrimPrefix(server.URL, "http://")
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{"credHelpers":{"` + registryHost + `":"pass"}}`,
			"docker-credential-pass list": `{}`,
			"docker-credential-pass get":  `{"Username":"old-user","Secret":"old-secret"}`,
		},
		dockerResult: &providers.CommandResult{ExitCode: 0},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	manager.Settings = testRegistrySettings(t, registryCredentialModeDockerHelper)

	err := manager.Login(context.Background(), models.RegistryLoginRequest{
		Registry: registryHost,
		Username: "new-user",
		Secret:   "new-secret",
	})
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("Login() error = %v, want registry auth", err)
	}
	storeInput := ""
	for i, call := range provider.backendCalls {
		if strings.Contains(call, "docker-credential-pass store") {
			storeInput = provider.backendInputs[i]
		}
	}
	if !strings.Contains(storeInput, `"Username":"old-user"`) || !strings.Contains(storeInput, `"Secret":"old-secret"`) {
		t.Fatalf("restored helper payload = %q", storeInput)
	}
	if len(provider.dockerCalls) != 0 {
		t.Fatalf("rollback used destructive docker logout: %#v", provider.dockerCalls)
	}
}

func TestRegistryTransactionLockSerializesDifferentManagersAndRegistries(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{}`,
			"docker-credential-pass list": `{}`,
		},
		backendDefaultExitCode: 1,
		backendDefaultStderr:   "credentials not found in native keychain",
		dockerResult:           &providers.CommandResult{ExitCode: 1, Stderr: "denied"},
		dockerInputStarted:     started,
		dockerInputRelease:     release,
	}
	first := NewManager(fakeResolver{provider: provider}, nil)
	second := NewManager(fakeResolver{provider: provider}, nil)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.Login(context.Background(), models.RegistryLoginRequest{Registry: "first.example.test", Username: "ada", Secret: "one"})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first login did not reach Docker")
	}
	secondErr := second.Login(context.Background(), models.RegistryLoginRequest{Registry: "second.example.test", Username: "grace", Secret: "two"})
	if !apperror.IsCode(secondErr, apperror.Conflict) {
		t.Fatalf("concurrent login error = %v, want config-wide conflict", secondErr)
	}
	close(release)
	if err := <-firstDone; !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("first login error = %v, want registry auth", err)
	}
}

func TestLoginDefaultsToFailClosedCredentialHelperModeWhenSettingsUnavailable(t *testing.T) {
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

	provider := &fakeRegistryProvider{
		backendResults: map[string]string{
			`sh -lc cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`: `{}`,
		},
		backendDefaultExitCode: 127,
		backendDefaultStderr:   "not found",
		dockerResult:           &providers.CommandResult{ExitCode: 0},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)

	err := manager.Login(context.Background(), models.RegistryLoginRequest{
		Registry: registryHost,
		Username: "ada",
		Secret:   "token",
	})
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("Login() error = %v, want provider not ready", err)
	}
	if provider.dockerInput != "" || len(provider.dockerArgs) != 0 {
		t.Fatalf("docker login ran despite missing helper: input=%q args=%#v", provider.dockerInput, provider.dockerArgs)
	}
	if provider.backendConfig != "" {
		t.Fatalf("backend config was rewritten despite missing helper: %s", provider.backendConfig)
	}
}

func TestBackendDockerConfigCommandsEscapeWSLDollars(t *testing.T) {
	provider := &fakeRegistryProvider{providerType: providers.TypeWindowsWSL}
	readCommand := strings.Join(backendConfigCommand(provider), " ")
	if !strings.Contains(readCommand, `\${DOCKER_CONFIG:-\$HOME/.docker}`) {
		t.Fatalf("WSL read command does not escape Docker config variables: %q", readCommand)
	}
	if strings.Contains(readCommand, "|| true") || !strings.Contains(readCommand, "set -eu") {
		t.Fatalf("WSL config read can hide permission or I/O errors: %q", readCommand)
	}
	writeCommand := strings.Join(backendWriteConfigCommand(provider), " ")
	if !strings.Contains(writeCommand, `\${DOCKER_CONFIG:-\$HOME/.docker}`) || !strings.Contains(writeCommand, `"\$cfg/config.json"`) {
		t.Fatalf("WSL write command does not escape Docker config variables: %q", writeCommand)
	}
	if !strings.Contains(writeCommand, "mktemp") || !strings.Contains(writeCommand, "mv -f") || !strings.Contains(writeCommand, "chmod 600") {
		t.Fatalf("WSL write command is not an atomic restrictive replacement: %q", writeCommand)
	}
	if !strings.Contains(writeCommand, `sync -f "\$tmp"`) || !strings.Contains(writeCommand, `sync -f "\$cfg"`) || strings.Contains(writeCommand, "sync -f \"\\$tmp\" 2>/dev/null || true") {
		t.Fatalf("WSL atomic write does not fail closed on supported file/directory sync: %q", writeCommand)
	}
	acquireCommand := strings.Join(backendConfigLockCommand(provider, true), " ")
	releaseCommand := strings.Join(backendConfigLockCommand(provider, false), " ")
	if !strings.Contains(acquireCommand, `.cairn-config.lock`) || !strings.Contains(acquireCommand, "mkdir") || !strings.Contains(releaseCommand, "owner") {
		t.Fatalf("WSL Docker config lock commands are incomplete: acquire=%q release=%q", acquireCommand, releaseCommand)
	}
	if dockerConfigLockStaleMinutes <= 120 || !strings.Contains(acquireCommand, `-mmin +180`) {
		t.Fatalf("WSL config lock can steal a supported two-hour Docker transaction: %q", acquireCommand)
	}
	if !strings.Contains(acquireCommand, `if ! printf`) || !strings.Contains(acquireCommand, `rmdir "\$lock"; exit 74`) {
		t.Fatalf("WSL config lock can orphan a newly-created lock after owner write failure: %q", acquireCommand)
	}
}

func TestWindowsDockerConfigCommandsUseDurableUTF8IO(t *testing.T) {
	provider := &fakeRegistryProvider{providerPlatform: providers.PlatformWindows}
	readArgs := backendConfigCommand(provider)
	if len(readArgs) == 0 || readArgs[0] != "powershell.exe" {
		t.Skip("Windows PowerShell command shape is only selected by a Windows build")
	}
	readCommand := strings.Join(readArgs, " ")
	if !strings.Contains(readCommand, `[Console]::InputEncoding=$utf8`) || !strings.Contains(readCommand, `[Console]::OutputEncoding=$utf8`) || !strings.Contains(readCommand, "ReadAllText") {
		t.Fatalf("Windows read command does not preserve UTF-8 JSON: %q", readCommand)
	}

	writeCommand := strings.Join(backendWriteConfigCommand(provider), " ")
	for _, required := range []string{`[Console]::InputEncoding=$utf8`, `[Console]::OutputEncoding=$utf8`, `[System.IO.File]::Open`, `$stream.Flush($true)`, `[System.IO.File]::Replace($tmp,$p,$null,$false)`} {
		if !strings.Contains(writeCommand, required) {
			t.Fatalf("Windows write command is missing %q: %q", required, writeCommand)
		}
	}
	if strings.Contains(writeCommand, "WriteAllText") || strings.Contains(writeCommand, `Replace($tmp,$p,$null,$true)`) {
		t.Fatalf("Windows write command can skip durable flush or metadata errors: %q", writeCommand)
	}

	lockCommand := strings.Join(backendConfigLockCommand(provider, true), " ")
	for _, required := range []string{`AddMinutes(-180)`, `[IO.File]::WriteAllText`, `ReparsePoint`, `Remove-Item -LiteralPath $lock -Force`, `exit 74`} {
		if !strings.Contains(lockCommand, required) {
			t.Fatalf("Windows config lock command is missing %q: %q", required, lockCommand)
		}
	}
}

func TestDockerConfigCommandsFailClosedOnNilResult(t *testing.T) {
	t.Parallel()
	provider := &fakeRegistryProvider{backendNilResult: true}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	if _, err := manager.readDockerConfigRaw(context.Background(), provider); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("readDockerConfigRaw() error = %v, want provider not ready", err)
	}
	if err := manager.writeDockerConfigRaw(context.Background(), provider, []byte(`{"label":"Привіт"}`)); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("writeDockerConfigRaw() error = %v, want provider not ready", err)
	}
}

func TestCredentialHelperConfigAbortsOnConcurrentDockerConfigChange(t *testing.T) {
	t.Parallel()
	provider := &fakeRegistryProvider{
		backendReadSequence: []string{`{}`, `{"experimental":true}`},
		backendResults: map[string]string{
			"docker-credential-pass list": `{}`,
		},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	err := manager.ensureCredentialHelper(context.Background(), provider, "ghcr.io", false)
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ensureCredentialHelper() error = %v, want conflict", err)
	}
	if provider.backendConfig != "" {
		t.Fatalf("concurrent Docker config was overwritten: %s", provider.backendConfig)
	}
}

func TestCredentialHelperConfigHandlesNullSectionsAndRejectsWrongTypes(t *testing.T) {
	t.Parallel()
	rawConfig := map[string]json.RawMessage{"credHelpers": json.RawMessage("null")}
	changed, err := setCredentialHelper(rawConfig, "ghcr.io", "pass")
	if err != nil || !changed {
		t.Fatalf("setCredentialHelper(null) = %v, %v", changed, err)
	}
	var helpers map[string]string
	if err := json.Unmarshal(rawConfig["credHelpers"], &helpers); err != nil || helpers["ghcr.io"] != "pass" {
		t.Fatalf("credHelpers = %s, %v", rawConfig["credHelpers"], err)
	}

	for _, malformed := range []string{`[]`, `"invalid"`, `{"ghcr.io":42}`} {
		config := map[string]json.RawMessage{"credHelpers": json.RawMessage(malformed)}
		if _, err := setCredentialHelper(config, "ghcr.io", "pass"); !apperror.IsCode(err, apperror.Internal) {
			t.Fatalf("setCredentialHelper(%s) error = %v, want internal", malformed, err)
		}
	}
}

func TestCredentialHelperConfigAddsDockerExactKeyForNormalizedAlias(t *testing.T) {
	t.Parallel()
	provider := &fakeRegistryProvider{
		backendConfig: `{"credHelpers":{"https://ghcr.io":"pass"}}`,
		backendResults: map[string]string{
			"docker-credential-pass list": `{}`,
		},
	}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	if err := manager.ensureCredentialHelper(context.Background(), provider, "ghcr.io", false); err != nil {
		t.Fatalf("ensureCredentialHelper() error = %v", err)
	}
	var config dockerConfig
	if err := json.Unmarshal([]byte(provider.backendConfig), &config); err != nil {
		t.Fatalf("written config is invalid: %v\n%s", err, provider.backendConfig)
	}
	if got := config.CredHelpers["ghcr.io"]; got != "pass" {
		t.Fatalf("Docker exact helper key = %q, config=%s", got, provider.backendConfig)
	}
	if got := config.CredHelpers["https://ghcr.io"]; got != "pass" {
		t.Fatalf("pre-existing alias was not preserved: %q, config=%s", got, provider.backendConfig)
	}
}

func TestCredentialHelperConfigRejectsConflictingNormalizedAliasesWithoutExactKey(t *testing.T) {
	t.Parallel()
	original := `{"credHelpers":{"https://ghcr.io":"pass","ghcr.io/":"secretservice"}}`
	provider := &fakeRegistryProvider{backendConfig: original}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	if err := manager.ensureCredentialHelper(context.Background(), provider, "ghcr.io", false); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ensureCredentialHelper() error = %v, want conflict", err)
	}
	if provider.backendConfig != original {
		t.Fatalf("conflicting config was overwritten: %s", provider.backendConfig)
	}
}

func TestHelperForRegistryUsesDockerExactKeyDeterministically(t *testing.T) {
	t.Parallel()
	config := dockerConfig{CredHelpers: map[string]string{
		"ghcr.io":         "wincred",
		"https://ghcr.io": "pass",
		"ghcr.io/":        "secretservice",
	}}
	for i := 0; i < 100; i++ {
		if got := helperForRegistry(config, "ghcr.io"); got != "wincred" {
			t.Fatalf("helperForRegistry() = %q, want exact helper", got)
		}
	}
}

func TestRegistryLoginConfigRestorationHandlesNullCurrentSections(t *testing.T) {
	t.Parallel()
	registryHost := "registry.example.test"
	oldAuth := json.RawMessage(`{"auth":"b2xkOnNlY3JldA=="}`)
	provider := &fakeRegistryProvider{backendConfig: `{"auths":null,"credHelpers":null}`}
	manager := NewManager(fakeResolver{provider: provider}, nil)
	tx := &registryLoginTransaction{
		provider:              provider,
		registry:              registryHost,
		originalAuthEntries:   map[string]json.RawMessage{registryHost: oldAuth},
		originalHelperEntries: map[string]string{registryHost: "pass"},
		hadAuthsSection:       true,
		hadCredHelpersSection: true,
	}
	if err := manager.restoreRegistryLoginConfigWithContext(context.Background(), tx); err != nil {
		t.Fatalf("restoreRegistryLoginConfigWithContext() error = %v", err)
	}
	var restored dockerConfig
	if err := json.Unmarshal([]byte(provider.backendConfig), &restored); err != nil {
		t.Fatalf("restored config is invalid: %v\n%s", err, provider.backendConfig)
	}
	if _, _, ok := authEntryForRegistry(restored, registryHost); !ok || helperForRegistry(restored, registryHost) != "pass" {
		t.Fatalf("restored config = %s", provider.backendConfig)
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
	auth := base64.StdEncoding.EncodeToString([]byte("ada:token"))
	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{
		backendStdout: `{"auths":{"` + registryHost + `":{"auth":"` + auth + `"}}}`,
	}}, nil)

	status, err := manager.TestAuth(context.Background(), registryHost)
	if err != nil {
		t.Fatalf("TestAuth() error = %v", err)
	}
	if status.LoggedIn || !strings.Contains(status.Error, "404") {
		t.Fatalf("status = %#v, want logged out 404 error", status)
	}
}

func TestAuthDoesNotTreatAnonymousBearerTokenAsLoggedIn(t *testing.T) {
	t.Parallel()
	var serverURL string
	tokenRequested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequested = true
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "anonymous"})
		case "/v2/":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+serverURL+`/token",service="registry.test"`)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL
	registryHost := strings.TrimPrefix(server.URL, "http://")
	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{}}, nil)

	status, err := manager.TestAuth(context.Background(), registryHost)
	if err != nil {
		t.Fatalf("TestAuth() error = %v", err)
	}
	if status.LoggedIn || !strings.Contains(status.Error, "credentials") {
		t.Fatalf("status = %#v, want logged out credential error", status)
	}
	if tokenRequested {
		t.Fatal("anonymous token endpoint was requested without stored credentials")
	}
}

func TestBearerTokenRealmRejectsPlainHTTPRemote(t *testing.T) {
	t.Parallel()
	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{}}, nil)
	challengedURL, parseErr := url.Parse("https://registry.example/v2/")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	_, err := manager.fetchBearerToken(context.Background(), "registry.example", challengedURL, authChallenge{
		Scheme: "Bearer",
		Params: map[string]string{"realm": "http://registry-token.example/token"},
	}, "repository:library/nginx:pull", credential{Username: "ada", Password: "secret"})
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("fetchBearerToken() error = %v, want registry auth", err)
	}
}

func TestDockerHubBearerTokenRealmIsExplicitlyTrusted(t *testing.T) {
	t.Parallel()
	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{}}, nil)
	challengedURL, _ := url.Parse("https://registry-1.docker.io/v2/")
	tokenURL, _ := url.Parse("https://auth.docker.io/token")
	got, err := manager.trustedTokenOrigin(DefaultRegistry, challengedURL, tokenURL)
	if err != nil {
		t.Fatalf("trustedTokenOrigin() error = %v", err)
	}
	if got != "https://auth.docker.io:443" {
		t.Fatalf("trusted token origin = %q", got)
	}
}

func TestBearerTokenRealmRejectsUntrustedCrossOriginWithoutSendingCredentials(t *testing.T) {
	attackerRequested := make(chan struct{}, 1)
	attacker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case attackerRequested <- struct{}{}:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "stolen"})
	}))
	defer attacker.Close()

	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+attacker.URL+`/token"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer registryServer.Close()
	registryHost := strings.TrimPrefix(registryServer.URL, "http://")
	auth := base64.StdEncoding.EncodeToString([]byte("ada:secret"))
	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{
		backendStdout: `{"auths":{"` + registryHost + `":{"auth":"` + auth + `"}}}`,
	}}, nil)

	_, err := manager.ResolveDigest(context.Background(), registryHost+"/team/app:1", ResolveOptions{BypassCache: true})
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("ResolveDigest() error = %v, want registry auth", err)
	}
	select {
	case <-attackerRequested:
		t.Fatal("untrusted token realm received a request")
	default:
	}
}

func TestBearerTokenRedirectCannotLeaveTrustedOrigin(t *testing.T) {
	attackerRequested := make(chan struct{}, 1)
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case attackerRequested <- struct{}{}:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "stolen"})
	}))
	defer attacker.Close()

	var registryURL string
	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			http.Redirect(w, r, attacker.URL+"/steal", http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+registryURL+`/token"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer registryServer.Close()
	registryURL = registryServer.URL
	registryHost := strings.TrimPrefix(registryURL, "http://")
	auth := base64.StdEncoding.EncodeToString([]byte("ada:secret"))
	manager := NewManager(fakeResolver{provider: &fakeRegistryProvider{
		backendStdout: `{"auths":{"` + registryHost + `":{"auth":"` + auth + `"}}}`,
	}}, nil)

	_, err := manager.ResolveDigest(context.Background(), registryHost+"/team/app:1", ResolveOptions{BypassCache: true})
	if !apperror.IsCode(err, apperror.RegistryAuth) {
		t.Fatalf("ResolveDigest() error = %v, want registry auth", err)
	}
	select {
	case <-attackerRequested:
		t.Fatal("redirect target received a credential-bearing request")
	default:
	}
}

func TestBearerTokenResponseBodyAndTokenAreBounded(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "response body", body: `{"token":"` + strings.Repeat("x", maxTokenResponseBytes) + `"}`},
		{name: "token field", body: `{"token":"` + strings.Repeat("x", maxRegistryTokenBytes+1) + `"}`},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			registryHost := strings.TrimPrefix(server.URL, "http://")
			challengedURL, _ := url.Parse(server.URL + "/v2/")
			manager := NewManager(nil, nil)
			_, err := manager.fetchBearerToken(context.Background(), registryHost, challengedURL, authChallenge{
				Scheme: "Bearer",
				Params: map[string]string{"realm": server.URL + "/token"},
			}, "repository:team/app:pull", credential{Username: "ada", Password: "secret"})
			if !apperror.IsCode(err, apperror.RegistryUnreachable) {
				t.Fatalf("fetchBearerToken() error = %v, want registry unreachable", err)
			}
		})
	}
}

func TestDecodeBoundedJSONRejectsOversizeAndTrailingValues(t *testing.T) {
	t.Parallel()
	var payload map[string]any
	err := decodeBoundedJSON(strings.NewReader(strings.Repeat("x", 17)), -1, 16, &payload)
	var tooLarge responseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("oversize error = %v, want responseTooLargeError", err)
	}
	err = decodeBoundedJSON(strings.NewReader(`{"token":"ok"} {"second":true}`), -1, 1024, &payload)
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("trailing JSON error = %v", err)
	}
}

func TestDecodePlatformManifestRejectsEntryCountDuringStreaming(t *testing.T) {
	t.Parallel()
	body := `{"manifests":[` + strings.Repeat("null,", maxManifestIndexEntries) + `null]}`
	_, err := decodePlatformManifest(strings.NewReader(body), int64(len(body)), Platform{OS: "linux", Architecture: "amd64"})
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("decodePlatformManifest() error = %v, want entry limit", err)
	}
}

func TestRegistryHeadMetadataIsBoundedAndValidated(t *testing.T) {
	t.Parallel()
	validDigest := "sha256:" + strings.Repeat("a", 64)
	if got, err := validatedRegistryDigest(validDigest); err != nil || got != validDigest {
		t.Fatalf("valid digest = %q, %v", got, err)
	}
	for _, value := range []string{"not-a-digest", "sha256:short", strings.Repeat("a", maxRegistryDigestHeaderBytes+1)} {
		if _, err := validatedRegistryDigest(value); !apperror.IsCode(err, apperror.RegistryUnreachable) {
			t.Fatalf("validatedRegistryDigest(%q) error = %v", value, err)
		}
	}
	if got, err := validatedManifestMediaType("application/vnd.oci.image.index.v1+json; charset=utf-8"); err != nil || got != "application/vnd.oci.image.index.v1+json" {
		t.Fatalf("valid media type = %q, %v", got, err)
	}
	for _, value := range []string{"not a media type", strings.Repeat("a", maxRegistryContentTypeHeaderBytes+1)} {
		if _, err := validatedManifestMediaType(value); !apperror.IsCode(err, apperror.RegistryUnreachable) {
			t.Fatalf("validatedManifestMediaType(%q) error = %v", value, err)
		}
	}
}

func TestRegistryStateMapsRemainBoundedAndLimitersAreReleased(t *testing.T) {
	t.Parallel()
	manager := NewManager(nil, nil)
	manager.cacheEntryLimit = 4
	manager.circuitEntryLimit = 3
	now := time.Now().UTC()
	manager.Now = func() time.Time { return now }
	for i := 0; i < 20; i++ {
		key := string(rune('a'+i)) + "/image:1"
		manager.storeCache(key, DigestResult{}, now.Add(time.Duration(i+1)*time.Minute))
		host := string(rune('a'+i)) + ".example.test"
		manager.recordRegistryFailure(host, apperror.New(apperror.RegistryUnreachable, "down"), now.Add(time.Duration(i)*time.Second), 0)
		if err := manager.withLimits(context.Background(), host, func() error { return nil }); err != nil {
			t.Fatalf("withLimits(%q) error = %v", host, err)
		}
	}
	if len(manager.cache) != manager.cacheEntryLimit {
		t.Fatalf("cache entries = %d, want %d", len(manager.cache), manager.cacheEntryLimit)
	}
	if len(manager.circuit) != manager.circuitEntryLimit {
		t.Fatalf("circuit entries = %d, want %d", len(manager.circuit), manager.circuitEntryLimit)
	}
	if len(manager.registryGate) != 0 {
		t.Fatalf("idle registry limiters retained = %d", len(manager.registryGate))
	}
}

func TestManagerCloneBoundToCopiesPolicyAndResetsProviderSensitiveState(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	sourceProvider := &fakeRegistryProvider{}
	boundProvider := &fakeRegistryProvider{}
	source := NewManager(fakeResolver{provider: sourceProvider}, nil)
	source.Audit = &store.AuditRepository{}
	source.Settings = &store.SettingsRepository{}
	source.HTTPClient = &http.Client{Timeout: 17 * time.Second}
	source.Now = func() time.Time { return fixedNow }
	source.CacheTTL = 23 * time.Minute
	source.PlainHTTPRegistries = map[string]bool{"registry.internal": true}
	source.TrustedAuthRealms = map[string][]string{"registry.internal": {"https://auth.internal", "https://backup-auth.internal"}}
	source.globalLimit = make(chan struct{}, 7)
	source.globalLimit <- struct{}{}
	source.perRegistryLimit = 2
	source.cacheEntryLimit = 3
	source.circuitEntryLimit = 4
	source.cache["source-cache"] = cacheEntry{ExpiresAt: fixedNow.Add(time.Hour)}
	source.circuit["registry.internal"] = circuitState{Failures: 2, LastTouched: fixedNow}
	source.registryGate["registry.internal"] = &registryGateState{gate: make(chan struct{}, 1), refs: 1}

	bound := source.CloneBoundTo(fakeResolver{provider: boundProvider})
	if bound == source {
		t.Fatal("CloneBoundTo() returned the source manager")
	}
	resolvedProvider, err := bound.provider(context.Background())
	if err != nil || resolvedProvider != boundProvider {
		t.Fatalf("bound provider = %#v, %v; want supplied provider", resolvedProvider, err)
	}
	if bound.Audit != source.Audit || bound.Settings != source.Settings || bound.HTTPClient != source.HTTPClient {
		t.Fatal("CloneBoundTo() did not preserve shared repositories/HTTP client")
	}
	if got := bound.now(); !got.Equal(fixedNow) {
		t.Fatalf("bound Now() = %s, want %s", got, fixedNow)
	}
	if bound.CacheTTL != source.CacheTTL || bound.perRegistryLimit != 2 || bound.cacheEntryLimit != 3 || bound.circuitEntryLimit != 4 {
		t.Fatalf("bound policy limits = ttl:%s per:%d cache:%d circuit:%d", bound.CacheTTL, bound.perRegistryLimit, bound.cacheEntryLimit, bound.circuitEntryLimit)
	}
	if bound.globalLimit == source.globalLimit || cap(bound.globalLimit) != 7 || len(bound.globalLimit) != 0 {
		t.Fatalf("bound global limiter = same:%t cap:%d len:%d", bound.globalLimit == source.globalLimit, cap(bound.globalLimit), len(bound.globalLimit))
	}
	if len(bound.cache) != 0 || len(bound.circuit) != 0 || len(bound.registryGate) != 0 {
		t.Fatalf("provider-sensitive state was copied: cache=%d circuit=%d gates=%d", len(bound.cache), len(bound.circuit), len(bound.registryGate))
	}

	source.PlainHTTPRegistries["registry.internal"] = false
	source.TrustedAuthRealms["registry.internal"][0] = "https://changed.invalid"
	if !bound.PlainHTTPRegistries["registry.internal"] {
		t.Fatal("bound plain-HTTP policy aliases the source map")
	}
	if got := bound.TrustedAuthRealms["registry.internal"][0]; got != "https://auth.internal" {
		t.Fatalf("bound trusted realm = %q; nested slice aliases source", got)
	}
	bound.cache["bound-cache"] = cacheEntry{ExpiresAt: fixedNow.Add(time.Hour)}
	if _, exists := source.cache["bound-cache"]; exists {
		t.Fatal("bound digest cache aliases source cache")
	}
	<-source.globalLimit
}

func TestManagerCloneBoundToIsolatesAuthSensitiveDigestCaches(t *testing.T) {
	t.Parallel()
	digestByUser := map[string]string{
		"alice": "sha256:" + strings.Repeat("a", 64),
		"bob":   "sha256:" + strings.Repeat("b", 64),
	}
	var authMu sync.Mutex
	authenticatedUsers := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/v2/team/app/manifests/1" {
			http.NotFound(w, r)
			return
		}
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="registry.test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		digest, exists := digestByUser[username]
		if !exists || password != username+"-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		authMu.Lock()
		authenticatedUsers = append(authenticatedUsers, username)
		authMu.Unlock()
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	registryHost := strings.TrimPrefix(server.URL, "http://")

	providerFor := func(username string) *fakeRegistryProvider {
		config, err := json.Marshal(dockerConfig{Auths: map[string]dockerAuth{
			registryHost: {Auth: base64.StdEncoding.EncodeToString([]byte(username + ":" + username + "-secret"))},
		}})
		if err != nil {
			t.Fatalf("marshal Docker config: %v", err)
		}
		return &fakeRegistryProvider{backendConfig: string(config)}
	}

	template := NewManager(fakeResolver{provider: providerFor("alice")}, nil)
	managerA := template.CloneBoundTo(fakeResolver{provider: providerFor("alice")})
	managerB := template.CloneBoundTo(fakeResolver{provider: providerFor("bob")})
	image := registryHost + "/team/app:1"
	platform := Platform{OS: "linux", Architecture: "amd64"}

	resultA, err := managerA.ResolveDigest(context.Background(), image, ResolveOptions{Platform: platform})
	if err != nil {
		t.Fatalf("ResolveDigest(A) error = %v", err)
	}
	resultB, err := managerB.ResolveDigest(context.Background(), image, ResolveOptions{Platform: platform})
	if err != nil {
		t.Fatalf("ResolveDigest(B) error = %v", err)
	}
	if resultA.ManifestDigest != digestByUser["alice"] || resultB.ManifestDigest != digestByUser["bob"] {
		t.Fatalf("auth-sensitive digests crossed providers: A=%q B=%q", resultA.ManifestDigest, resultB.ManifestDigest)
	}
	if resultA.FromCache || resultB.FromCache {
		t.Fatalf("first bound resolutions unexpectedly used cache: A=%t B=%t", resultA.FromCache, resultB.FromCache)
	}

	cachedA, err := managerA.ResolveDigest(context.Background(), image, ResolveOptions{Platform: platform})
	if err != nil || !cachedA.FromCache || cachedA.ManifestDigest != digestByUser["alice"] {
		t.Fatalf("cached A = %#v, %v", cachedA, err)
	}
	cachedB, err := managerB.ResolveDigest(context.Background(), image, ResolveOptions{Platform: platform})
	if err != nil || !cachedB.FromCache || cachedB.ManifestDigest != digestByUser["bob"] {
		t.Fatalf("cached B = %#v, %v", cachedB, err)
	}
	authMu.Lock()
	users := append([]string(nil), authenticatedUsers...)
	authMu.Unlock()
	if !reflect.DeepEqual(users, []string{"alice", "bob"}) {
		t.Fatalf("authenticated registry requests = %#v, want one per bound provider", users)
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
		(strings.Contains(command, "cat \"") || strings.Contains(command, "Get-Content") || strings.Contains(command, "ReadAllText"))
}

func isBackendConfigWriteForTest(command string) bool {
	return strings.Contains(command, "config.json") &&
		(strings.Contains(command, "cat >") || strings.Contains(command, "Set-Content"))
}

type fakeRegistryProvider struct {
	mu                     sync.Mutex
	backendStdout          string
	backendResults         map[string]string
	backendReadSequence    []string
	backendReadIndex       int
	backendDefaultExitCode int
	backendDefaultStderr   string
	backendConfig          string
	backendInput           string
	backendInputs          []string
	backendArgs            []string
	backendCalls           []string
	dockerInput            string
	dockerArgs             []string
	dockerCalls            [][]string
	dockerResult           *providers.CommandResult
	dockerInputStarted     chan struct{}
	dockerInputRelease     chan struct{}
	dockerStartOnce        sync.Once
	backendLocks           map[string]string
	backendNilResult       bool
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
func (p *fakeRegistryProvider) RunDocker(_ context.Context, args ...string) (*providers.CommandResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dockerCalls = append(p.dockerCalls, append([]string(nil), args...))
	if p.dockerResult != nil {
		return p.dockerResult, nil
	}
	return &providers.CommandResult{ExitCode: 0}, nil
}
func (p *fakeRegistryProvider) RunDockerWithInput(_ context.Context, input string, args ...string) (*providers.CommandResult, error) {
	p.mu.Lock()
	p.dockerInput = input
	p.dockerArgs = append([]string(nil), args...)
	started := p.dockerInputStarted
	release := p.dockerInputRelease
	result := p.dockerResult
	if started != nil {
		p.dockerStartOnce.Do(func() { close(started) })
	}
	p.mu.Unlock()
	if release != nil {
		<-release
	}
	if result != nil {
		result.Command = append([]string{"docker"}, args...)
		return result, nil
	}
	return &providers.CommandResult{Command: append([]string{"docker"}, args...), ExitCode: 0}, nil
}
func (p *fakeRegistryProvider) RunBackendCommand(_ context.Context, input string, args ...string) (*providers.CommandResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.backendInput = input
	p.backendInputs = append(p.backendInputs, input)
	p.backendArgs = append([]string(nil), args...)
	joined := strings.Join(args, " ")
	p.backendCalls = append(p.backendCalls, joined)
	if lockName := backendLockNameForTest(joined); lockName != "" {
		if p.backendLocks == nil {
			p.backendLocks = map[string]string{}
		}
		acquire := strings.Contains(joined, "while ! mkdir") || strings.Contains(joined, "for ($i=0")
		if acquire {
			if _, exists := p.backendLocks[lockName]; exists {
				return &providers.CommandResult{Command: args, ExitCode: 73}, nil
			}
			p.backendLocks[lockName] = input
		} else if p.backendLocks[lockName] == input {
			delete(p.backendLocks, lockName)
		}
		return &providers.CommandResult{Command: args, ExitCode: 0}, nil
	}
	if p.backendNilResult {
		return nil, nil
	}
	if isBackendConfigReadForTest(joined) && p.backendReadIndex < len(p.backendReadSequence) {
		stdout := p.backendReadSequence[p.backendReadIndex]
		p.backendReadIndex++
		return &providers.CommandResult{Command: args, Stdout: stdout, ExitCode: 0}, nil
	}
	if isBackendConfigWriteForTest(joined) {
		p.backendConfig = input
		return &providers.CommandResult{Command: args, ExitCode: 0}, nil
	}
	if isBackendConfigReadForTest(joined) && p.backendConfig != "" {
		return &providers.CommandResult{Command: args, Stdout: p.backendConfig, ExitCode: 0}, nil
	}
	if isBackendConfigReadForTest(joined) {
		for configuredCommand, stdout := range p.backendResults {
			if isBackendConfigReadForTest(configuredCommand) {
				return &providers.CommandResult{Command: args, Stdout: stdout, ExitCode: 0}, nil
			}
		}
	}
	if p.backendResults != nil {
		if stdout, ok := p.backendResults[joined]; ok {
			return &providers.CommandResult{Command: args, Stdout: stdout, ExitCode: 0}, nil
		}
	}
	return &providers.CommandResult{Command: args, Stdout: p.backendStdout, Stderr: p.backendDefaultStderr, ExitCode: p.backendDefaultExitCode}, nil
}

func backendLockNameForTest(command string) string {
	start := strings.Index(command, ".cairn-")
	if start < 0 {
		return ""
	}
	end := strings.Index(command[start:], ".lock")
	if end < 0 {
		return ""
	}
	return command[start : start+end+len(".lock")]
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
