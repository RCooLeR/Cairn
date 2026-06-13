package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

func (m *Manager) ListRegistryAccounts(ctx context.Context) ([]models.RegistryAccount, error) {
	provider, err := m.provider(ctx)
	if err != nil {
		return nil, err
	}
	config, err := m.readDockerConfig(ctx, provider)
	if err != nil {
		return nil, err
	}
	accounts := accountsFromDockerConfig(config, m.now())
	helperAccounts := m.accountsFromHelpers(ctx, provider, config)
	return mergeRegistryAccounts(accounts, helperAccounts), nil
}

func (m *Manager) readDockerConfig(ctx context.Context, provider providers.PlatformProvider) (dockerConfig, error) {
	runner, ok := provider.(BackendCommandRunner)
	if !ok {
		return dockerConfig{}, apperror.New(
			apperror.ProviderNotReady,
			"Provider cannot read backend Docker configuration",
			apperror.WithRepairHints("Reconnect the Docker provider and try again."),
		)
	}

	command := backendConfigCommand(provider)
	result, err := runner.RunBackendCommand(ctx, "", command...)
	if err != nil && result == nil {
		return dockerConfig{}, apperror.Wrap(apperror.ProviderNotReady, "Read Docker config failed", err)
	}
	if result != nil && result.ExitCode != 0 {
		return dockerConfig{}, apperror.New(
			apperror.ProviderNotReady,
			"Read Docker config failed",
			apperror.WithDetail(strings.TrimSpace(result.Stderr)),
		)
	}
	raw := ""
	if result != nil {
		raw = strings.TrimSpace(result.Stdout)
	}
	if raw == "" {
		return dockerConfig{}, nil
	}
	var config dockerConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return dockerConfig{}, apperror.Wrap(apperror.Internal, "Parse Docker config failed", err)
	}
	return config, nil
}

func backendConfigCommand(provider providers.PlatformProvider) []string {
	if provider.Type() == providers.TypeWindowsWSL || provider.Platform() != providers.PlatformWindows {
		return []string{"sh", "-lc", `cat "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null || true`}
	}
	return []string{
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		`$cfg=$env:DOCKER_CONFIG; if ([string]::IsNullOrWhiteSpace($cfg)) { $cfg=Join-Path $env:USERPROFILE '.docker' }; $p=Join-Path $cfg 'config.json'; if (Test-Path -LiteralPath $p) { Get-Content -LiteralPath $p -Raw }`,
	}
}

func accountsFromDockerConfig(config dockerConfig, verified time.Time) []models.RegistryAccount {
	accountsByRegistry := map[string]models.RegistryAccount{}
	for registry, helper := range config.CredHelpers {
		host := normalizeRegistryHost(registry)
		if strings.TrimSpace(helper) == "" {
			continue
		}
		accountsByRegistry[host] = account(host, "", "credHelper", verified)
	}
	for registry := range config.Auths {
		host := normalizeRegistryHost(registry)
		entry := config.Auths[registry]
		username, password, identityToken := decodeDockerAuth(entry)
		source := "authsFile"
		if password == "" && identityToken == "" {
			if helper := helperForRegistry(config, host); helper != "" {
				source = "credHelper"
				_ = helper
			} else if strings.TrimSpace(config.CredsStore) != "" {
				source = "credsStore"
			}
		}
		accountsByRegistry[host] = account(host, username, source, verified)
	}
	if strings.TrimSpace(config.CredsStore) != "" {
		for registry, current := range accountsByRegistry {
			if current.Source == "authsFile" {
				current.Source = "credsStore"
				accountsByRegistry[registry] = current
			}
		}
	}

	accounts := make([]models.RegistryAccount, 0, len(accountsByRegistry))
	for _, item := range accountsByRegistry {
		accounts = append(accounts, item)
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Registry == accounts[j].Registry {
			return accounts[i].Username < accounts[j].Username
		}
		return accounts[i].Registry < accounts[j].Registry
	})
	return accounts
}

func decodeDockerAuth(entry dockerAuth) (string, string, string) {
	if entry.Username != "" || entry.Password != "" || entry.IdentityToken != "" {
		return entry.Username, entry.Password, entry.IdentityToken
	}
	if strings.TrimSpace(entry.Auth) == "" {
		return "", "", ""
	}
	decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
	if err != nil {
		return "", "", ""
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return "", "", ""
	}
	return username, password, ""
}

func helperForRegistry(config dockerConfig, registry string) string {
	for key, helper := range config.CredHelpers {
		if normalizeRegistryHost(key) == normalizeRegistryHost(registry) {
			return strings.TrimSpace(helper)
		}
	}
	return ""
}

func authEntryForRegistry(config dockerConfig, registry string) (string, dockerAuth, bool) {
	normalized := normalizeRegistryHost(registry)
	for key, entry := range config.Auths {
		if normalizeRegistryHost(key) == normalized {
			return key, entry, true
		}
	}
	return "", dockerAuth{}, false
}

func (m *Manager) accountsFromHelpers(ctx context.Context, provider providers.PlatformProvider, config dockerConfig) []models.RegistryAccount {
	runner, ok := provider.(BackendCommandRunner)
	if !ok {
		return nil
	}
	helpers := map[string]string{}
	for _, helper := range config.CredHelpers {
		helper = strings.TrimSpace(helper)
		if helper != "" {
			helpers[helper] = "credHelper"
		}
	}
	if helper := strings.TrimSpace(config.CredsStore); helper != "" {
		helpers[helper] = "credsStore"
	}
	accounts := []models.RegistryAccount{}
	for helper, source := range helpers {
		result, err := runner.RunBackendCommand(ctx, "", "docker-credential-"+helper, "list")
		if err != nil || result == nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) == "" {
			continue
		}
		var listed map[string]string
		if err := json.Unmarshal([]byte(result.Stdout), &listed); err != nil {
			continue
		}
		for registry, username := range listed {
			host := normalizeRegistryHost(registry)
			if host == "" {
				continue
			}
			accounts = append(accounts, account(host, username, source, m.now()))
		}
	}
	return accounts
}

func mergeRegistryAccounts(base []models.RegistryAccount, extra []models.RegistryAccount) []models.RegistryAccount {
	if len(extra) == 0 {
		return base
	}
	byRegistry := map[string]models.RegistryAccount{}
	for _, item := range base {
		byRegistry[normalizeRegistryHost(item.Registry)] = item
	}
	for _, item := range extra {
		key := normalizeRegistryHost(item.Registry)
		current, exists := byRegistry[key]
		if !exists || current.Username == "" || (current.Source == "credsStore" && item.Source == "credHelper") {
			item.Registry = key
			byRegistry[key] = item
		}
	}
	accounts := make([]models.RegistryAccount, 0, len(byRegistry))
	for _, item := range byRegistry {
		accounts = append(accounts, item)
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Registry == accounts[j].Registry {
			return accounts[i].Username < accounts[j].Username
		}
		return accounts[i].Registry < accounts[j].Registry
	})
	return accounts
}
