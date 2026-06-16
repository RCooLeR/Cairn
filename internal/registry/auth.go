package registry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
	dockerregistry "github.com/docker/docker/api/types/registry"
)

func (m *Manager) Login(ctx context.Context, req models.RegistryLoginRequest) error {
	registry, err := registryCLIArg(req.Registry)
	if err != nil {
		return err
	}
	username := strings.TrimSpace(req.Username)
	secret := strings.TrimSpace(req.Secret)
	if username == "" || secret == "" {
		return apperror.New(apperror.Conflict, "Registry username and secret are required")
	}
	provider, err := m.provider(ctx)
	if err != nil {
		return err
	}
	runner, ok := provider.(DockerInputRunner)
	if !ok {
		return apperror.New(apperror.ProviderNotReady, "Provider cannot pass registry secrets via stdin")
	}

	started := m.now()
	result, runErr := runner.RunDockerWithInput(ctx, secret+"\n", "login", registry, "-u", username, "--password-stdin")
	if auditErr := m.recordAudit(ctx, "registry.login", registry, provider.ID(), "docker login "+registry+" -u "+username+" --password-stdin", runErr, result, started); auditErr != nil && runErr == nil {
		runErr = auditErr
	}
	if runErr != nil || result == nil || result.ExitCode != 0 {
		return registryCommandError("Registry login failed", result, runErr)
	}

	status, err := m.TestAuth(ctx, registry)
	if err != nil {
		return err
	}
	if status != nil && !status.LoggedIn {
		return apperror.New(apperror.RegistryAuth, "Registry login verification failed", apperror.WithDetail(status.Error))
	}
	return nil
}

func (m *Manager) Logout(ctx context.Context, registry string) error {
	registry, err := registryCLIArg(registry)
	if err != nil {
		return err
	}
	provider, err := m.provider(ctx)
	if err != nil {
		return err
	}
	started := m.now()
	result, runErr := provider.RunDocker(ctx, "logout", registry)
	if auditErr := m.recordAudit(ctx, "registry.logout", registry, provider.ID(), "docker logout "+registry, runErr, result, started); auditErr != nil && runErr == nil {
		runErr = auditErr
	}
	if runErr != nil || result == nil || result.ExitCode != 0 {
		return registryCommandError("Registry logout failed", result, runErr)
	}
	return nil
}

func (m *Manager) TestAuth(ctx context.Context, registry string) (*models.RegistryAuthStatus, error) {
	registry = normalizeRegistryHost(registry)
	provider, err := m.provider(ctx)
	if err != nil {
		return nil, err
	}
	creds, _ := m.credentialForRegistry(ctx, provider, registry)
	status := &models.RegistryAuthStatus{
		Registry:   registry,
		Username:   creds.Username,
		VerifiedAt: m.now(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.registryBaseURL(registry)+"/v2/", nil)
	if err != nil {
		return nil, apperror.Wrap(apperror.RegistryUnreachable, "Build registry auth request failed", err)
	}
	resp, err := m.doAuthenticated(req, registry, "", creds)
	if err != nil {
		status.Error = err.Error()
		return status, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusOK:
		status.LoggedIn = true
	case http.StatusUnauthorized, http.StatusForbidden:
		status.Error = "registry rejected credentials"
	case http.StatusTooManyRequests:
		status.Error = "registry rate limit reached"
	default:
		status.Error = resp.Status
	}
	return status, nil
}

func (m *Manager) credentialForRegistry(ctx context.Context, provider providers.PlatformProvider, registry string) (credential, error) {
	config, err := m.readDockerConfig(ctx, provider)
	if err != nil {
		return credential{}, err
	}
	if _, entry, ok := authEntryForRegistry(config, registry); ok {
		username, password, identityToken := decodeDockerAuth(entry)
		if username != "" || password != "" || identityToken != "" {
			return credential{
				Username:      username,
				Password:      password,
				IdentityToken: identityToken,
				Source:        "authsFile",
			}, nil
		}
	}
	if helper := helperForRegistry(config, registry); helper != "" {
		return m.credentialFromHelper(ctx, provider, helper, registry, "credHelper")
	}
	if helper := strings.TrimSpace(config.CredsStore); helper != "" {
		return m.credentialFromHelper(ctx, provider, helper, registry, "credsStore")
	}
	return credential{}, nil
}

func EncodeDockerAuthConfig(ctx context.Context, provider providers.PlatformProvider, registry string) (string, error) {
	if provider == nil {
		return "", apperror.New(apperror.ProviderNotReady, "Provider cannot resolve registry credentials")
	}
	registry = normalizeRegistryHost(registry)
	creds, err := NewManager(nil, nil).credentialForRegistry(ctx, provider, registry)
	if err != nil {
		return "", err
	}
	if creds.Username == "" && creds.Password == "" && creds.IdentityToken == "" {
		return "", nil
	}
	payload, err := json.Marshal(dockerregistry.AuthConfig{
		Username:      creds.Username,
		Password:      creds.Password,
		IdentityToken: creds.IdentityToken,
		ServerAddress: helperServerURL(registry),
	})
	if err != nil {
		return "", apperror.Wrap(apperror.Internal, "Encode registry auth failed", err)
	}
	return base64.URLEncoding.EncodeToString(payload), nil
}

func (m *Manager) credentialFromHelper(ctx context.Context, provider providers.PlatformProvider, helper string, registry string, source string) (credential, error) {
	runner, ok := provider.(BackendCommandRunner)
	if !ok {
		return credential{}, nil
	}
	server := helperServerURL(registry)
	result, err := runner.RunBackendCommand(ctx, server+"\n", "docker-credential-"+helper, "get")
	if err != nil || result == nil || result.ExitCode != 0 {
		return credential{}, nil
	}
	var out struct {
		Username string `json:"Username"`
		Secret   string `json:"Secret"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &out); err != nil {
		return credential{}, nil
	}
	if out.Username == "" && out.Secret == "" {
		return credential{}, nil
	}
	return credential{Username: out.Username, Password: out.Secret, Source: source}, nil
}

func helperServerURL(registry string) string {
	if normalizeRegistryHost(registry) == DefaultRegistry {
		return "https://index.docker.io/v1/"
	}
	return normalizeRegistryHost(registry)
}

func (m *Manager) doAuthenticated(req *http.Request, registry string, scope string, creds credential) (*http.Response, error) {
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return nil, apperror.Wrap(apperror.RegistryUnreachable, "Registry request failed", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	challenge := parseWWWAuthenticate(resp.Header.Get("WWW-Authenticate"))
	_ = resp.Body.Close()
	if challenge.Scheme == "" {
		return resp, nil
	}
	retry := req.Clone(req.Context())
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
		retry.Body = io.NopCloser(bytes.NewReader(body))
	}
	switch strings.ToLower(challenge.Scheme) {
	case "bearer":
		token, err := m.fetchBearerToken(req.Context(), challenge, scope, creds)
		if err != nil {
			return nil, err
		}
		retry.Header.Set("Authorization", "Bearer "+token)
	case "basic":
		if creds.Username == "" || creds.Password == "" {
			return nil, apperror.New(apperror.RegistryAuth, "Registry authentication required")
		}
		retry.SetBasicAuth(creds.Username, creds.Password)
	default:
		return nil, apperror.New(apperror.RegistryAuth, "Unsupported registry authentication challenge")
	}
	return m.httpClient().Do(retry)
}

func (m *Manager) fetchBearerToken(ctx context.Context, challenge authChallenge, scope string, creds credential) (string, error) {
	if challenge.Params["realm"] == "" {
		return "", apperror.New(apperror.RegistryAuth, "Registry token realm missing")
	}
	tokenURL, err := url.Parse(challenge.Params["realm"])
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryAuth, "Registry token realm invalid", err)
	}
	if tokenURL.Scheme != "https" && !isPlainHTTPRegistry(tokenURL.Host) {
		return "", apperror.New(apperror.RegistryAuth, "Registry token realm must use HTTPS")
	}
	query := tokenURL.Query()
	if service := challenge.Params["service"]; service != "" {
		query.Set("service", service)
	}
	if scope == "" {
		scope = challenge.Params["scope"]
	}
	if scope != "" {
		query.Set("scope", scope)
	}
	tokenURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryAuth, "Build registry token request failed", err)
	}
	if creds.IdentityToken != "" {
		req.Header.Set("Authorization", "Bearer "+creds.IdentityToken)
	} else if creds.Username != "" && creds.Password != "" {
		req.SetBasicAuth(creds.Username, creds.Password)
	}
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryUnreachable, "Registry token request failed", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", apperror.New(apperror.RegistryAuth, "Registry credentials were rejected")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", apperror.New(apperror.RegistryRateLimit, "Registry rate limit reached")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", apperror.New(apperror.RegistryUnreachable, "Registry token request failed", apperror.WithDetail(resp.Status))
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", apperror.Wrap(apperror.RegistryUnreachable, "Parse registry token failed", err)
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	if payload.AccessToken != "" {
		return payload.AccessToken, nil
	}
	return "", apperror.New(apperror.RegistryAuth, "Registry token response did not include a token")
}

func (m *Manager) recordAudit(ctx context.Context, action string, target string, providerID string, command string, actionErr error, result *providers.CommandResult, started time.Time) error {
	if m == nil || m.Audit == nil {
		return nil
	}
	status := "success"
	if actionErr != nil || (result != nil && result.ExitCode != 0) {
		status = "failed"
	}
	var exitCode *int
	if result != nil {
		code := result.ExitCode
		exitCode = &code
	}
	message := ""
	if actionErr != nil {
		message = actionErr.Error()
	}
	_, err := m.Audit.Insert(ctx, store.AuditRecord{
		Action:     action,
		TargetType: "registry",
		TargetID:   target,
		ProviderID: providerID,
		Command:    command,
		Risk:       models.RiskNeedsConfirmation,
		Status:     status,
		ExitCode:   exitCode,
		Duration:   m.now().Sub(started),
		Error:      message,
		CreatedAt:  m.now(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record registry audit entry failed", err)
	}
	return nil
}

func registryCommandError(message string, result *providers.CommandResult, err error) error {
	detail := ""
	if result != nil {
		detail = strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
	}
	if err != nil && detail == "" {
		detail = err.Error()
	}
	return apperror.New(apperror.RegistryAuth, message, apperror.WithDetail(detail))
}
