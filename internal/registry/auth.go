package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
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
	m.loginMu.Lock()
	defer m.loginMu.Unlock()

	registry, err := registryCLIArg(req.Registry)
	if err != nil {
		return err
	}
	username := strings.TrimSpace(req.Username)
	secret := req.Secret
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
	transactionLock, err := acquireDockerConfigLock(ctx, provider)
	if err != nil {
		return err
	}
	defer releaseDockerConfigLock(provider, transactionLock)
	ctx = withDockerConfigLockHeld(ctx)
	tx, err := m.prepareRegistryLoginStorage(ctx, provider, registry)
	if err != nil {
		return err
	}

	started := m.now()
	result, runErr := runner.RunDockerWithInput(ctx, registryLoginInput(secret), "login", registry, "-u", username, "--password-stdin")
	commandErr := runErr
	if auditErr := m.recordAudit(ctx, "registry.login", registry, provider.ID(), "docker login "+registry+" -u "+username+" --password-stdin", runErr, result, started); auditErr != nil && runErr == nil {
		runErr = auditErr
	}
	if commandErr != nil || result == nil || result.ExitCode != 0 {
		return m.restoreRegistryLogin(tx, registryCommandError("Registry login failed", result, commandErr))
	}
	if runErr != nil {
		return m.restoreRegistryLogin(tx, registryCommandError("Registry login failed", result, runErr))
	}

	status, err := m.testAuthWithCredential(ctx, registry, credential{
		Username: username,
		Password: secret,
	})
	if err != nil {
		return m.restoreRegistryLogin(tx, err)
	}
	if status != nil && !status.LoggedIn {
		return m.restoreRegistryLogin(tx, apperror.New(apperror.RegistryAuth, "Registry login verification failed", apperror.WithDetail(status.Error)))
	}
	if err := m.finalizeRegistryLoginStorage(ctx, tx); err != nil {
		return m.restoreRegistryLogin(tx, err)
	}
	return nil
}

func registryLoginInput(secret string) string {
	// Docker removes one terminal CRLF, LF, or CR from --password-stdin. Add a
	// delimiter that cannot consume an EOL that is part of the opaque secret.
	if strings.HasSuffix(secret, "\r") {
		return secret + "\r\n"
	}
	return secret + "\n"
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
	transactionLock, err := acquireDockerConfigLock(ctx, provider)
	if err != nil {
		return err
	}
	defer releaseDockerConfigLock(provider, transactionLock)
	ctx = withDockerConfigLockHeld(ctx)
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
	creds, err := m.credentialForRegistry(ctx, provider, registry)
	if err != nil {
		return nil, err
	}
	return m.testAuthWithCredential(ctx, registry, creds)
}

func (m *Manager) testAuthWithCredential(ctx context.Context, registry string, creds credential) (*models.RegistryAuthStatus, error) {
	registry = normalizeRegistryHost(registry)
	status := &models.RegistryAuthStatus{
		Registry:   registry,
		Username:   creds.Username,
		VerifiedAt: m.now(),
	}
	if !hasRegistryCredential(creds) {
		status.Error = "registry credentials not found"
		return status, nil
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

func hasRegistryCredential(creds credential) bool {
	return creds.Username != "" || creds.Password != "" || creds.IdentityToken != ""
}

func (m *Manager) credentialForRegistry(ctx context.Context, provider providers.PlatformProvider, registry string) (credential, error) {
	config, err := m.readDockerConfig(ctx, provider)
	if err != nil {
		return credential{}, err
	}
	if helper := helperForRegistry(config, registry); helper != "" {
		return m.credentialFromHelper(ctx, provider, helper, registry, "credHelper")
	}
	if helper := strings.TrimSpace(config.CredsStore); helper != "" {
		return m.credentialFromHelper(ctx, provider, helper, registry, "credsStore")
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
	record, exists, err := m.readCredentialHelper(ctx, provider, helper, registry)
	if err != nil {
		return credential{}, err
	}
	if !exists {
		return credential{}, nil
	}
	if record.Username == "<token>" {
		return credential{IdentityToken: record.Secret, Source: source}, nil
	}
	return credential{Username: record.Username, Password: record.Secret, Source: source}, nil
}

type credentialHelperRecord struct {
	Username string `json:"Username"`
	Secret   string `json:"Secret"`
}

func (m *Manager) readCredentialHelper(ctx context.Context, provider providers.PlatformProvider, helper string, registry string) (credentialHelperRecord, bool, error) {
	runner, ok := provider.(BackendCommandRunner)
	if !ok {
		return credentialHelperRecord{}, false, apperror.New(apperror.ProviderNotReady, "Provider cannot read Docker credential helper")
	}
	server := helperServerURL(registry)
	result, err := runner.RunBackendCommand(ctx, server+"\n", "docker-credential-"+helper, "get")
	if err != nil || result == nil || result.ExitCode != 0 {
		if credentialHelperNotFound(result, err) {
			return credentialHelperRecord{}, false, nil
		}
		return credentialHelperRecord{}, false, apperror.New(
			apperror.RegistryAuth,
			"Docker credential helper failed",
			apperror.WithRepairHints("Check that docker-credential-"+helper+" is installed, initialized, and accessible on the active backend."),
		)
	}
	var out credentialHelperRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &out); err != nil {
		return credentialHelperRecord{}, false, apperror.Wrap(apperror.RegistryAuth, "Docker credential helper returned invalid data", err)
	}
	if out.Username == "" && out.Secret == "" {
		return credentialHelperRecord{}, false, apperror.New(apperror.RegistryAuth, "Docker credential helper returned an empty credential")
	}
	return out, true, nil
}

func credentialHelperNotFound(result *providers.CommandResult, err error) bool {
	const maxHelperErrorBytes = 4 << 10
	const notFoundDiagnostic = "credentials not found in native keychain"
	found := false
	if result != nil {
		for _, output := range []string{result.Stderr, result.Stdout} {
			if strings.TrimSpace(output) == "" {
				continue
			}
			if len(output) > maxHelperErrorBytes || strings.ToLower(strings.TrimSpace(output)) != notFoundDiagnostic {
				return false
			}
			found = true
		}
	}
	if err != nil {
		diagnostic := strings.TrimSpace(err.Error())
		processExitOnly := result != nil && result.ExitCode > 0 && diagnostic == fmt.Sprintf("exit status %d", result.ExitCode)
		if !processExitOnly {
			if len(diagnostic) > maxHelperErrorBytes || strings.ToLower(diagnostic) != notFoundDiagnostic {
				return false
			}
			found = true
		}
	}
	return found
}

func helperServerURL(registry string) string {
	if normalizeRegistryHost(registry) == DefaultRegistry {
		return "https://index.docker.io/v1/"
	}
	return normalizeRegistryHost(registry)
}

func (m *Manager) doAuthenticated(req *http.Request, registry string, scope string, creds credential) (*http.Response, error) {
	registryOrigin, err := canonicalHTTPOrigin(req.URL)
	if err != nil {
		return nil, apperror.Wrap(apperror.RegistryUnreachable, "Registry request URL is invalid", err)
	}
	resp, err := m.doOriginBoundRequest(req, registryOrigin)
	if err != nil {
		if apperror.IsCode(err, apperror.RegistryAuth) {
			return nil, err
		}
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
		if req.GetBody == nil {
			return nil, apperror.New(apperror.Internal, "Registry request body cannot be replayed safely")
		}
		retry.Body, err = req.GetBody()
		if err != nil {
			return nil, apperror.Wrap(apperror.Internal, "Replay registry request body failed", err)
		}
	}
	switch strings.ToLower(challenge.Scheme) {
	case "bearer":
		challengedURL := req.URL
		if resp.Request != nil && resp.Request.URL != nil {
			challengedURL = resp.Request.URL
		}
		token, err := m.fetchBearerToken(req.Context(), registry, challengedURL, challenge, scope, creds)
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
	resp, err = m.doOriginBoundRequest(retry, registryOrigin)
	if err != nil {
		if apperror.IsCode(err, apperror.RegistryAuth) {
			return nil, err
		}
		return nil, apperror.Wrap(apperror.RegistryUnreachable, "Authenticated registry request failed", err)
	}
	return resp, nil
}

func (m *Manager) fetchBearerToken(ctx context.Context, registry string, challengedURL *url.URL, challenge authChallenge, scope string, creds credential) (string, error) {
	if challenge.Params["realm"] == "" {
		return "", apperror.New(apperror.RegistryAuth, "Registry token realm missing")
	}
	tokenURL, err := url.Parse(challenge.Params["realm"])
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryAuth, "Registry token realm invalid", err)
	}
	tokenOrigin, err := m.trustedTokenOrigin(registry, challengedURL, tokenURL)
	if err != nil {
		return "", err
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
	resp, err := m.doOriginBoundRequest(req, tokenOrigin)
	if err != nil {
		if apperror.IsCode(err, apperror.RegistryAuth) {
			return "", err
		}
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
	if err := decodeBoundedJSON(resp.Body, resp.ContentLength, maxTokenResponseBytes, &payload); err != nil {
		return "", apperror.Wrap(apperror.RegistryUnreachable, "Parse registry token failed", err)
	}
	if len(payload.Token) > maxRegistryTokenBytes || len(payload.AccessToken) > maxRegistryTokenBytes {
		return "", apperror.New(apperror.RegistryUnreachable, "Registry token exceeds safe size limit")
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	if payload.AccessToken != "" {
		return payload.AccessToken, nil
	}
	return "", apperror.New(apperror.RegistryAuth, "Registry token response did not include a token")
}

func (m *Manager) doOriginBoundRequest(req *http.Request, allowedOrigin string) (*http.Response, error) {
	client := *m.httpClient()
	previousCheck := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return apperror.New(apperror.RegistryAuth, "Registry redirect limit exceeded")
		}
		nextOrigin, err := canonicalHTTPOrigin(next.URL)
		if err != nil || nextOrigin != allowedOrigin {
			return apperror.New(apperror.RegistryAuth, "Registry redirect left the trusted origin")
		}
		if previousCheck != nil {
			return previousCheck(next, via)
		}
		return nil
	}
	return client.Do(req)
}

func (m *Manager) trustedTokenOrigin(registry string, challengedURL *url.URL, tokenURL *url.URL) (string, error) {
	challengeOrigin, err := canonicalHTTPOrigin(challengedURL)
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryAuth, "Registry challenge origin is invalid", err)
	}
	expectedURL, err := url.Parse(m.registryBaseURL(registry))
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryAuth, "Registry origin is invalid", err)
	}
	expectedOrigin, err := canonicalHTTPOrigin(expectedURL)
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryAuth, "Registry origin is invalid", err)
	}
	if challengeOrigin != expectedOrigin {
		return "", apperror.New(apperror.RegistryAuth, "Registry authentication challenge came from an untrusted origin")
	}
	tokenOrigin, err := canonicalHTTPOrigin(tokenURL)
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryAuth, "Registry token realm is invalid", err)
	}
	if tokenOrigin == expectedOrigin {
		if strings.EqualFold(tokenURL.Scheme, "https") || m.registryAllowsPlainHTTP(registry) {
			return tokenOrigin, nil
		}
		return "", apperror.New(apperror.RegistryAuth, "Registry token realm must use HTTPS")
	}
	if !strings.EqualFold(tokenURL.Scheme, "https") {
		return "", apperror.New(apperror.RegistryAuth, "Cross-origin registry token realms must use HTTPS")
	}
	for _, allowed := range m.authRealmOrigins(registry) {
		allowedURL, parseErr := url.Parse(allowed)
		if parseErr != nil {
			continue
		}
		allowedOrigin, originErr := canonicalHTTPOrigin(allowedURL)
		if originErr == nil && tokenOrigin == allowedOrigin {
			return tokenOrigin, nil
		}
	}
	return "", apperror.New(
		apperror.RegistryAuth,
		"Registry requested credentials for an untrusted token realm",
		apperror.WithRepairHints("Approve the registry's exact HTTPS authentication origin before retrying."),
	)
}

func (m *Manager) authRealmOrigins(registry string) []string {
	if m == nil || m.TrustedAuthRealms == nil {
		return nil
	}
	keys := []string{normalizeRegistryHost(registry), normalizeRegistryHost(registryAPIHost(registry))}
	var origins []string
	for _, key := range keys {
		origins = append(origins, m.TrustedAuthRealms[key]...)
	}
	return origins
}

func (m *Manager) registryAllowsPlainHTTP(registry string) bool {
	return isPlainHTTPRegistry(registry) || (m != nil && m.PlainHTTPRegistries[normalizeRegistryHost(registry)])
}

func canonicalHTTPOrigin(value *url.URL) (string, error) {
	if value == nil || !value.IsAbs() || value.Opaque != "" || value.User != nil || value.Fragment != "" {
		return "", fmt.Errorf("URL must be an absolute HTTP URL without userinfo or fragment")
	}
	scheme := strings.ToLower(value.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q", value.Scheme)
	}
	hostname := strings.ToLower(strings.TrimSuffix(value.Hostname(), "."))
	if hostname == "" {
		return "", fmt.Errorf("URL host is required")
	}
	port := value.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(hostname, port), nil
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
