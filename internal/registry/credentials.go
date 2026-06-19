package registry

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/providers"
)

const (
	registryCredentialModeDockerHelper = "docker_helper"
	registryCredentialModeNone         = "none"
)

func (m *Manager) prepareRegistryLoginStorage(ctx context.Context, provider providers.PlatformProvider, registry string) error {
	mode, err := m.registryCredentialMode(ctx)
	if err != nil {
		return err
	}
	switch mode {
	case "":
		return nil
	case registryCredentialModeDockerHelper:
		return m.ensureCredentialHelper(ctx, provider, registry)
	case registryCredentialModeNone:
		return apperror.New(
			apperror.Conflict,
			"Registry login is disabled",
			apperror.WithDetail("Credential mode is set to No Cairn-managed credentials."),
			apperror.WithRepairHints("Switch Settings > Registries > Credential mode to Prefer Docker credential helper before logging in from Cairn."),
		)
	default:
		return apperror.New(apperror.Internal, "Unknown registry credential mode", apperror.WithDetail(mode))
	}
}

func (m *Manager) registryCredentialMode(ctx context.Context) (string, error) {
	if m == nil || m.Settings == nil {
		return "", nil
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

func (m *Manager) ensureCredentialHelper(ctx context.Context, provider providers.PlatformProvider, registry string) error {
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
	if helperForRegistry(config, registry) == "" && strings.TrimSpace(config.CredsStore) == "" {
		helper, err := m.detectCredentialHelper(ctx, provider)
		if err != nil {
			if apperror.IsCode(err, apperror.ProviderNotReady) {
				return nil
			}
			return err
		}
		helperChanged, err := setCredentialHelper(rawConfig, registryCredentialConfigKey(registry), helper)
		if err != nil {
			return err
		}
		changed = changed || helperChanged
	}

	authsChanged, err := removeInlineRegistryAuth(rawConfig, registry)
	if err != nil {
		return err
	}
	changed = changed || authsChanged
	if !changed {
		return nil
	}

	updated, err := json.MarshalIndent(rawConfig, "", "  ")
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Encode Docker config failed", err)
	}
	return m.writeDockerConfigRaw(ctx, provider, updated)
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
	candidates := credentialHelperCandidates(provider)
	for _, helper := range candidates {
		result, err := runner.RunBackendCommand(ctx, "", "docker-credential-"+helper, "list")
		if err == nil && result != nil && result.ExitCode == 0 {
			return helper, nil
		}
	}
	return "", apperror.New(
		apperror.ProviderNotReady,
		"Docker credential helper is not available",
		apperror.WithDetail("Cairn is set to Docker credential helper mode, but none of these helpers responded: "+strings.Join(candidates, ", ")+"."),
		apperror.WithRepairHints(
			"Install and initialize a Docker credential helper for this backend.",
			"On WSL, install Docker Desktop's credential helper or configure pass/secretservice inside the distro.",
			"Switch Credential mode to No Cairn-managed credentials if you want to manage registry login outside Cairn.",
		),
	)
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
