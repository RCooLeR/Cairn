package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/providers"
)

const (
	registryCredentialModeDockerHelper = "docker_helper"
	registryCredentialModeNone         = "none"
)

type registryLoginTransaction struct {
	provider              providers.PlatformProvider
	registry              string
	mode                  string
	helper                string
	originalHelperRecord  credentialHelperRecord
	originalHelperExists  bool
	originalAuthEntries   map[string]json.RawMessage
	originalHelperEntries map[string]string
	hadAuthsSection       bool
	hadCredHelpersSection bool
	authsSectionWasNull   bool
	helpersSectionWasNull bool
}

func (m *Manager) prepareRegistryLoginStorage(ctx context.Context, provider providers.PlatformProvider, registry string) (*registryLoginTransaction, error) {
	mode, err := m.registryCredentialMode(ctx)
	if err != nil {
		return nil, err
	}
	tx := &registryLoginTransaction{provider: provider, registry: registry, mode: mode}
	switch mode {
	case registryCredentialModeNone:
		return nil, apperror.New(
			apperror.Conflict,
			"Registry login is disabled",
			apperror.WithDetail("Credential mode is set to No Cairn-managed credentials."),
			apperror.WithRepairHints("Switch Settings > Registries > Credential mode to Require Docker credential helper before logging in from Cairn."),
		)
	case registryCredentialModeDockerHelper:
	default:
		return nil, apperror.New(apperror.Internal, "Unknown registry credential mode", apperror.WithDetail(mode))
	}

	config, rawConfig, err := m.readRegistryLoginConfig(ctx, provider)
	if err != nil {
		return nil, err
	}
	tx.hadAuthsSection = rawConfig["auths"] != nil
	tx.hadCredHelpersSection = rawConfig["credHelpers"] != nil
	tx.authsSectionWasNull = rawJSONIsNull(rawConfig["auths"])
	tx.helpersSectionWasNull = rawJSONIsNull(rawConfig["credHelpers"])
	tx.originalAuthEntries = matchingRawAuthEntries(rawConfig["auths"], registry)
	tx.originalHelperEntries = matchingHelperEntries(config.CredHelpers, registry)

	if mode == registryCredentialModeDockerHelper {
		if err := m.ensureCredentialHelper(ctx, provider, registry, false); err != nil {
			cleanupErr := m.restoreRegistryLoginConfig(tx)
			if cleanupErr != nil {
				return nil, apperror.Wrap(apperror.RegistryAuth, "Prepare registry credential storage failed and configuration restoration failed", err)
			}
			return nil, err
		}
		config, _, err = m.readRegistryLoginConfig(ctx, provider)
		if err != nil {
			cleanupErr := m.restoreRegistryLoginConfig(tx)
			if cleanupErr != nil {
				return nil, apperror.Wrap(apperror.RegistryAuth, "Prepare registry credential storage failed and configuration restoration failed", err)
			}
			return nil, err
		}
	}
	tx.helper = helperForRegistry(config, registry)
	if tx.helper == "" {
		tx.helper = strings.TrimSpace(config.CredsStore)
	}
	if tx.helper != "" {
		record, exists, err := m.readCredentialHelper(ctx, provider, tx.helper, registry)
		if err != nil {
			cleanupErr := m.restoreRegistryLoginConfig(tx)
			if cleanupErr != nil {
				return nil, apperror.Wrap(apperror.RegistryAuth, "Snapshot registry credential failed and configuration restoration failed", err)
			}
			return nil, err
		}
		tx.originalHelperRecord = record
		tx.originalHelperExists = exists
	}
	return tx, nil
}

func (m *Manager) finalizeRegistryLoginStorage(ctx context.Context, tx *registryLoginTransaction) error {
	if tx == nil || tx.mode != registryCredentialModeDockerHelper {
		return nil
	}
	return m.ensureCredentialHelper(ctx, tx.provider, tx.registry, true)
}

func (m *Manager) readRegistryLoginConfig(ctx context.Context, provider providers.PlatformProvider) (dockerConfig, map[string]json.RawMessage, error) {
	unlock, err := m.lockDockerConfigState(ctx, provider)
	if err != nil {
		return dockerConfig{}, nil, err
	}
	defer unlock()

	raw, err := m.readDockerConfigRaw(ctx, provider)
	if err != nil {
		return dockerConfig{}, nil, err
	}
	config := dockerConfig{}
	rawConfig := map[string]json.RawMessage{}
	if strings.TrimSpace(raw) == "" {
		return config, rawConfig, nil
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return dockerConfig{}, nil, apperror.Wrap(apperror.Internal, "Parse Docker config failed", err)
	}
	if err := json.Unmarshal([]byte(raw), &rawConfig); err != nil {
		return dockerConfig{}, nil, apperror.Wrap(apperror.Internal, "Parse Docker config failed", err)
	}
	return config, rawConfig, nil
}

func matchingRawAuthEntries(raw json.RawMessage, registry string) map[string]json.RawMessage {
	entries := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &entries)
	matched := map[string]json.RawMessage{}
	for key, entry := range entries {
		if normalizeRegistryHost(key) == normalizeRegistryHost(registry) {
			matched[key] = append(json.RawMessage(nil), entry...)
		}
	}
	return matched
}

func matchingHelperEntries(entries map[string]string, registry string) map[string]string {
	matched := map[string]string{}
	for key, helper := range entries {
		if normalizeRegistryHost(key) == normalizeRegistryHost(registry) {
			matched[key] = helper
		}
	}
	return matched
}

func (m *Manager) restoreRegistryLogin(tx *registryLoginTransaction, primary error) error {
	if tx == nil {
		return primary
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = withDockerConfigLockHeld(ctx)
	helperErr := m.restoreCredentialHelper(ctx, tx)
	configErr := m.restoreRegistryLoginConfigWithContext(ctx, tx)
	if helperErr == nil && configErr == nil {
		return primary
	}
	return apperror.Wrap(
		apperror.RegistryAuth,
		"Registry login failed and the previous credential state could not be fully restored",
		primary,
		apperror.WithRepairHints("Review docker login state for "+tx.registry+" on the active backend before retrying."),
	)
}

func (m *Manager) restoreCredentialHelper(ctx context.Context, tx *registryLoginTransaction) error {
	if tx == nil || tx.helper == "" {
		return nil
	}
	runner, ok := tx.provider.(BackendCommandRunner)
	if !ok {
		return apperror.New(apperror.ProviderNotReady, "Provider cannot restore Docker credential helper")
	}
	if tx.originalHelperExists {
		payload, err := json.Marshal(struct {
			ServerURL string `json:"ServerURL"`
			Username  string `json:"Username"`
			Secret    string `json:"Secret"`
		}{helperServerURL(tx.registry), tx.originalHelperRecord.Username, tx.originalHelperRecord.Secret})
		if err != nil {
			return err
		}
		result, runErr := runner.RunBackendCommand(ctx, string(payload)+"\n", "docker-credential-"+tx.helper, "store")
		if runErr != nil || result == nil || result.ExitCode != 0 {
			return apperror.New(apperror.RegistryAuth, "Restore previous Docker helper credential failed")
		}
		return nil
	}
	result, err := runner.RunBackendCommand(ctx, helperServerURL(tx.registry)+"\n", "docker-credential-"+tx.helper, "erase")
	if err != nil || result == nil || result.ExitCode != 0 {
		if credentialHelperNotFound(result, err) {
			return nil
		}
		return apperror.New(apperror.RegistryAuth, "Remove failed Docker helper credential failed")
	}
	return nil
}

func (m *Manager) restoreRegistryLoginConfig(tx *registryLoginTransaction) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = withDockerConfigLockHeld(ctx)
	return m.restoreRegistryLoginConfigWithContext(ctx, tx)
}

func (m *Manager) restoreRegistryLoginConfigWithContext(ctx context.Context, tx *registryLoginTransaction) error {
	if tx == nil {
		return nil
	}
	unlock, err := m.lockDockerConfigState(ctx, tx.provider)
	if err != nil {
		return err
	}
	defer unlock()

	raw, err := m.readDockerConfigRaw(ctx, tx.provider)
	if err != nil {
		return err
	}
	rawConfig := map[string]json.RawMessage{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &rawConfig); err != nil {
			return apperror.Wrap(apperror.Internal, "Parse Docker config failed during credential restoration", err)
		}
	}
	before, _ := json.Marshal(rawConfig)

	auths := map[string]json.RawMessage{}
	if rawAuths, ok := rawConfig["auths"]; ok && len(rawAuths) > 0 {
		if err := json.Unmarshal(rawAuths, &auths); err != nil {
			return apperror.Wrap(apperror.Internal, "Parse Docker auths failed during credential restoration", err)
		}
		if auths == nil {
			auths = map[string]json.RawMessage{}
		}
	}
	for key := range auths {
		if normalizeRegistryHost(key) == normalizeRegistryHost(tx.registry) {
			delete(auths, key)
		}
	}
	for key, entry := range tx.originalAuthEntries {
		auths[key] = append(json.RawMessage(nil), entry...)
	}
	if len(auths) == 0 {
		switch {
		case !tx.hadAuthsSection:
			delete(rawConfig, "auths")
		case tx.authsSectionWasNull:
			rawConfig["auths"] = json.RawMessage("null")
		default:
			rawConfig["auths"] = json.RawMessage("{}")
		}
	} else {
		encodedAuths, err := json.Marshal(auths)
		if err != nil {
			return apperror.Wrap(apperror.Internal, "Encode Docker auths failed during credential restoration", err)
		}
		rawConfig["auths"] = encodedAuths
	}

	helpers := map[string]string{}
	if rawHelpers, ok := rawConfig["credHelpers"]; ok && len(rawHelpers) > 0 {
		if err := json.Unmarshal(rawHelpers, &helpers); err != nil {
			return apperror.Wrap(apperror.Internal, "Parse Docker credential helpers failed during credential restoration", err)
		}
		if helpers == nil {
			helpers = map[string]string{}
		}
	}
	for key := range helpers {
		if normalizeRegistryHost(key) == normalizeRegistryHost(tx.registry) {
			delete(helpers, key)
		}
	}
	maps.Copy(helpers, tx.originalHelperEntries)
	if len(helpers) == 0 {
		switch {
		case !tx.hadCredHelpersSection:
			delete(rawConfig, "credHelpers")
		case tx.helpersSectionWasNull:
			rawConfig["credHelpers"] = json.RawMessage("null")
		default:
			rawConfig["credHelpers"] = json.RawMessage("{}")
		}
	} else {
		encodedHelpers, err := json.Marshal(helpers)
		if err != nil {
			return apperror.Wrap(apperror.Internal, "Encode Docker credential helpers failed during credential restoration", err)
		}
		rawConfig["credHelpers"] = encodedHelpers
	}

	after, err := json.Marshal(rawConfig)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	latest, err := m.readDockerConfigRaw(ctx, tx.provider)
	if err != nil {
		return err
	}
	if latest != raw {
		return apperror.New(apperror.Conflict, "Docker credential configuration changed during login restoration")
	}
	updated, err := json.MarshalIndent(rawConfig, "", "  ")
	if err != nil {
		return err
	}
	return m.writeDockerConfigRaw(ctx, tx.provider, updated)
}

func (m *Manager) registryCredentialMode(ctx context.Context) (string, error) {
	if m == nil || m.Settings == nil {
		return registryCredentialModeDockerHelper, nil
	}
	mode, err := m.Settings.GetString(ctx, "registry.credentials_mode")
	if err != nil {
		return "", apperror.Wrap(apperror.Internal, "Read registry credential mode failed", err)
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return registryCredentialModeDockerHelper, nil
	}
	return mode, nil
}

func (m *Manager) ensureCredentialHelper(ctx context.Context, provider providers.PlatformProvider, registry string, removeInline bool) error {
	unlock, err := m.lockDockerConfigState(ctx, provider)
	if err != nil {
		return err
	}
	defer unlock()

	raw, err := m.readDockerConfigRaw(ctx, provider)
	if err != nil {
		return err
	}

	config := dockerConfig{}
	rawConfig := map[string]json.RawMessage{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			return apperror.Wrap(apperror.Internal, "Parse Docker config failed", err)
		}
		if err := json.Unmarshal([]byte(raw), &rawConfig); err != nil {
			return apperror.Wrap(apperror.Internal, "Parse Docker config failed", err)
		}
	}

	changed := false
	configuredHelper := helperForRegistry(config, registry)
	if configuredHelper == "" {
		configuredHelper = strings.TrimSpace(config.CredsStore)
	}
	if configuredHelper != "" {
		if err := m.checkCredentialHelper(ctx, provider, configuredHelper); err != nil {
			return err
		}
	} else {
		helper, err := normalizedHelperAlias(config, registry)
		if err != nil {
			return err
		}
		if helper == "" {
			helper, err = m.detectCredentialHelper(ctx, provider)
			if err != nil {
				return err
			}
		} else if err := m.checkCredentialHelper(ctx, provider, helper); err != nil {
			return err
		}
		helperChanged, err := setCredentialHelper(rawConfig, registryCredentialConfigKey(registry), helper)
		if err != nil {
			return err
		}
		changed = changed || helperChanged
	}

	if removeInline {
		authsChanged, err := removeInlineRegistryAuth(rawConfig, registry)
		if err != nil {
			return err
		}
		changed = changed || authsChanged
	}
	if !changed {
		return nil
	}

	updated, err := json.MarshalIndent(rawConfig, "", "  ")
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Encode Docker config failed", err)
	}
	latest, err := m.readDockerConfigRaw(ctx, provider)
	if err != nil {
		return err
	}
	if latest != raw {
		return apperror.New(
			apperror.Conflict,
			"Docker credential configuration changed concurrently",
			apperror.WithRepairHints("Review the active backend's Docker config.json and retry without another login or credential tool running."),
		)
	}
	return m.writeDockerConfigRaw(ctx, provider, updated)
}

func normalizedHelperAlias(config dockerConfig, registry string) (string, error) {
	exactKey := registryCredentialConfigKey(registry)
	normalizedRegistry := normalizeRegistryHost(registry)
	aliases := map[string]struct{}{}
	for key, helper := range config.CredHelpers {
		helper = strings.TrimSpace(helper)
		if key == exactKey || helper == "" || normalizeRegistryHost(key) != normalizedRegistry {
			continue
		}
		aliases[helper] = struct{}{}
	}
	if len(aliases) > 1 {
		return "", apperror.New(
			apperror.Conflict,
			"Docker credential helper aliases conflict",
			apperror.WithDetail("Multiple non-exact credHelpers entries normalize to "+normalizedRegistry+" but select different helpers."),
			apperror.WithRepairHints("Remove conflicting registry aliases from Docker config.json, then retry login."),
		)
	}
	for helper := range aliases {
		return helper, nil
	}
	return "", nil
}

func (m *Manager) checkCredentialHelper(ctx context.Context, provider providers.PlatformProvider, helper string) error {
	runner, ok := provider.(BackendCommandRunner)
	if !ok {
		return apperror.New(apperror.ProviderNotReady, "Provider cannot check Docker credential helpers")
	}
	if err := credentialHelperProbeContextError(ctx); err != nil {
		return err
	}
	result, err := runner.RunBackendCommand(ctx, "", "docker-credential-"+helper, "list")
	if err == nil && result != nil && result.ExitCode == 0 {
		return nil
	}
	if contextErr := credentialHelperProbeContextError(ctx); contextErr != nil {
		return contextErr
	}
	if credentialHelperBackendUnavailable(provider, result, err) {
		return credentialHelperBackendUnavailableError(result, err)
	}
	return apperror.New(
		apperror.RegistryAuth,
		"Configured Docker credential helper is not available",
		apperror.WithRepairHints("Install or initialize docker-credential-"+helper+" on the active backend before logging in."),
	)
}

func (m *Manager) detectCredentialHelper(ctx context.Context, provider providers.PlatformProvider) (string, error) {
	runner, ok := provider.(BackendCommandRunner)
	if !ok {
		return "", apperror.New(
			apperror.ProviderNotReady,
			"Provider cannot check Docker credential helpers",
			apperror.WithRepairHints("Reconnect the Docker provider and try again."),
		)
	}
	if err := credentialHelperProbeContextError(ctx); err != nil {
		return "", err
	}
	candidates := credentialHelperCandidates(provider)
	for _, helper := range candidates {
		result, err := runner.RunBackendCommand(ctx, "", "docker-credential-"+helper, "list")
		if err == nil && result != nil && result.ExitCode == 0 {
			return helper, nil
		}
		if contextErr := credentialHelperProbeContextError(ctx); contextErr != nil {
			return "", contextErr
		}
		if credentialHelperBackendUnavailable(provider, result, err) {
			return "", credentialHelperBackendUnavailableError(result, err)
		}
	}
	return "", apperror.New(
		apperror.RegistryAuth,
		"Docker credential helper is not available",
		apperror.WithDetail("Cairn is set to Docker credential helper mode, but none of these helpers responded: "+strings.Join(candidates, ", ")+"."),
		apperror.WithRepairHints(
			"Install and initialize a Docker credential helper for this backend.",
			"On WSL, install Docker Desktop's credential helper or configure pass/secretservice inside the distro.",
			"Switch Credential mode to No Cairn-managed credentials if you want to manage registry login outside Cairn.",
		),
	)
}

func credentialHelperProbeContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	switch err := ctx.Err(); err {
	case context.Canceled:
		return apperror.Wrap(apperror.Cancelled, "Docker credential helper check was cancelled", err)
	case context.DeadlineExceeded:
		return apperror.Wrap(apperror.Timeout, "Docker credential helper check timed out", err)
	default:
		return nil
	}
}

func credentialHelperBackendUnavailable(provider providers.PlatformProvider, result *providers.CommandResult, err error) bool {
	if provider == nil || provider.Type() != providers.TypeWindowsWSL {
		return false
	}
	if apperror.IsCode(err, apperror.ProviderNotReady) || result == nil || result.ExitCode == -1 {
		return true
	}
	diagnostic := strings.ToLower(strings.ReplaceAll(credentialHelperProbeDiagnostic(result, err), "\x00", ""))
	return strings.Contains(diagnostic, "wsl/service/") ||
		strings.Contains(diagnostic, "lacked sufficient buffer space") ||
		strings.Contains(diagnostic, "queue was full")
}

func credentialHelperBackendUnavailableError(result *providers.CommandResult, err error) error {
	detail := providers.SafeCommandDiagnostic(
		strings.ReplaceAll(credentialHelperProbeDiagnostic(result, err), "\x00", ""),
		8<<10,
	)
	return apperror.New(
		apperror.ProviderNotReady,
		"WSL backend is not available for Docker credential helper checks",
		apperror.WithDetail(detail),
		apperror.WithRepairHints("Restart WSL and reconnect the active Docker provider before retrying registry login."),
	)
}

func credentialHelperProbeDiagnostic(result *providers.CommandResult, err error) string {
	parts := make([]string, 0, 3)
	if err != nil {
		parts = append(parts, err.Error())
	}
	if result != nil {
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			parts = append(parts, stderr)
		}
		if stdout := strings.TrimSpace(result.Stdout); stdout != "" {
			parts = append(parts, stdout)
		}
	}
	return strings.Join(parts, "\n")
}

func credentialHelperCandidates(provider providers.PlatformProvider) []string {
	if provider == nil {
		return []string{"pass", "secretservice"}
	}
	if provider.Type() == providers.TypeWindowsWSL {
		return []string{"wincred.exe", "desktop.exe", "desktop", "pass", "secretservice"}
	}
	switch provider.Platform() {
	case providers.PlatformWindows:
		return []string{"wincred", "desktop"}
	case providers.PlatformMacOS:
		return []string{"osxkeychain", "desktop"}
	default:
		return []string{"pass", "secretservice"}
	}
}

func setCredentialHelper(rawConfig map[string]json.RawMessage, registry string, helper string) (bool, error) {
	helpers := map[string]string{}
	if rawHelpers, ok := rawConfig["credHelpers"]; ok && len(rawHelpers) > 0 {
		if err := json.Unmarshal(rawHelpers, &helpers); err != nil {
			return false, apperror.Wrap(apperror.Internal, "Parse Docker credential helpers failed", err)
		}
		if helpers == nil {
			helpers = map[string]string{}
		}
	}
	if helpers[registry] == helper {
		return false, nil
	}
	helpers[registry] = helper
	raw, err := json.Marshal(helpers)
	if err != nil {
		return false, apperror.Wrap(apperror.Internal, "Encode Docker credential helpers failed", err)
	}
	rawConfig["credHelpers"] = raw
	return true, nil
}

func rawJSONIsNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func removeInlineRegistryAuth(rawConfig map[string]json.RawMessage, registry string) (bool, error) {
	rawAuths, ok := rawConfig["auths"]
	if !ok || len(rawAuths) == 0 {
		return false, nil
	}
	auths := map[string]json.RawMessage{}
	if err := json.Unmarshal(rawAuths, &auths); err != nil {
		return false, apperror.Wrap(apperror.Internal, "Parse Docker auths failed", err)
	}

	changed := false
	for key, rawEntry := range auths {
		if normalizeRegistryHost(key) != normalizeRegistryHost(registry) {
			continue
		}
		var entry dockerAuth
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			return false, apperror.Wrap(apperror.Internal, "Parse Docker auth entry failed", err)
		}
		_, password, identityToken := decodeDockerAuth(entry)
		if strings.TrimSpace(entry.Auth) != "" || password != "" || identityToken != "" {
			delete(auths, key)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	raw, err := json.Marshal(auths)
	if err != nil {
		return false, apperror.Wrap(apperror.Internal, "Encode Docker auths failed", err)
	}
	rawConfig["auths"] = raw
	return true, nil
}

func registryCredentialConfigKey(registry string) string {
	if normalizeRegistryHost(registry) == DefaultRegistry {
		return helperServerURL(registry)
	}
	return normalizeRegistryHost(registry)
}
