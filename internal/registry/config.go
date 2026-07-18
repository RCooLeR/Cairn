package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/google/uuid"
)

type dockerConfigLockContextKey struct{}

// Built-in providers cap Docker operations at two hours. Recovery must not
// classify a supported live login as stale, including verification and rollback.
const dockerConfigLockStaleMinutes = 180

func withDockerConfigLockHeld(ctx context.Context) context.Context {
	return context.WithValue(ctx, dockerConfigLockContextKey{}, true)
}

func dockerConfigLockHeld(ctx context.Context) bool {
	held, _ := ctx.Value(dockerConfigLockContextKey{}).(bool)
	return held
}

// lockDockerConfigState serializes Cairn Docker config access in one lock order:
// the backend-wide lock first, followed by the in-process mutex. Login and logout
// transactions already hold the backend lock and mark their contexts, so nested
// config operations only take the in-process mutex.
func (m *Manager) lockDockerConfigState(ctx context.Context, provider providers.PlatformProvider) (func(), error) {
	if dockerConfigLockHeld(ctx) {
		m.configMu.Lock()
		return m.configMu.Unlock, nil
	}

	lockToken, err := acquireDockerConfigLock(ctx, provider)
	if err != nil {
		return nil, err
	}
	m.configMu.Lock()
	return func() {
		m.configMu.Unlock()
		releaseDockerConfigLock(provider, lockToken)
	}, nil
}

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
	raw, err := m.readDockerConfigRaw(ctx, provider)
	if err != nil {
		return dockerConfig{}, err
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

func (m *Manager) readDockerConfigRaw(ctx context.Context, provider providers.PlatformProvider) (string, error) {
	runner, ok := provider.(BackendCommandRunner)
	if !ok {
		return "", apperror.New(
			apperror.ProviderNotReady,
			"Provider cannot read backend Docker configuration",
			apperror.WithRepairHints("Reconnect the Docker provider and try again."),
		)
	}

	command := backendConfigCommand(provider)
	result, err := runner.RunBackendCommand(ctx, "", command...)
	if err != nil {
		return "", apperror.Wrap(apperror.ProviderNotReady, "Read Docker config failed", err)
	}
	if result == nil {
		return "", apperror.New(apperror.ProviderNotReady, "Read Docker config failed", apperror.WithDetail("Backend command returned no result."))
	}
	if result.ExitCode != 0 {
		return "", apperror.New(
			apperror.ProviderNotReady,
			"Read Docker config failed",
			apperror.WithDetail(providers.SafeCommandDiagnostic(result.Stderr, 8<<10)),
		)
	}
	if result.StdoutTruncated {
		return "", apperror.New(
			apperror.ProviderNotReady,
			"Read Docker config failed",
			apperror.WithDetail("Backend Docker configuration exceeded the safe output limit."),
		)
	}
	raw := normalizeDockerConfigJSON(result.Stdout)
	if raw == "" {
		return "", nil
	}
	return raw, nil
}

func (m *Manager) writeDockerConfigRaw(ctx context.Context, provider providers.PlatformProvider, raw []byte) error {
	runner, ok := provider.(BackendCommandRunner)
	if !ok {
		return apperror.New(
			apperror.ProviderNotReady,
			"Provider cannot write backend Docker configuration",
			apperror.WithRepairHints("Reconnect the Docker provider and try again."),
		)
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if raw[len(raw)-1] != '\n' {
		raw = append(raw, '\n')
	}
	command := backendWriteConfigCommand(provider)
	result, err := runner.RunBackendCommand(ctx, string(raw), command...)
	if err != nil {
		return apperror.Wrap(apperror.ProviderNotReady, "Write Docker config failed", err)
	}
	if result == nil {
		return apperror.New(apperror.ProviderNotReady, "Write Docker config failed", apperror.WithDetail("Backend command returned no result."))
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		return apperror.New(apperror.ProviderNotReady, "Write Docker config failed", apperror.WithDetail(providers.SafeCommandDiagnostic(detail, 8<<10)))
	}
	return nil
}

func acquireDockerConfigLock(ctx context.Context, provider providers.PlatformProvider) (string, error) {
	return acquireBackendLock(ctx, provider, ".cairn-config.lock", "Docker credential configuration is busy")
}

func acquireBackendLock(ctx context.Context, provider providers.PlatformProvider, name string, busyMessage string) (string, error) {
	runner, ok := provider.(BackendCommandRunner)
	if !ok {
		return "", apperror.New(apperror.ProviderNotReady, "Provider cannot lock backend Docker configuration")
	}
	token := uuid.NewString()
	command := backendNamedLockCommand(provider, true, name)
	result, err := runner.RunBackendCommand(ctx, token, command...)
	if err != nil {
		return "", apperror.Wrap(apperror.ProviderNotReady, "Lock Docker config failed", err)
	}
	if result == nil || result.ExitCode != 0 {
		return "", apperror.New(
			apperror.Conflict,
			busyMessage,
			apperror.WithRepairHints("Close other Cairn registry login operations and retry."),
		)
	}
	return token, nil
}

func releaseDockerConfigLock(provider providers.PlatformProvider, token string) {
	releaseBackendLock(provider, token, ".cairn-config.lock")
}

func releaseBackendLock(provider providers.PlatformProvider, token string, name string) {
	runner, ok := provider.(BackendCommandRunner)
	if !ok || token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = runner.RunBackendCommand(ctx, token, backendNamedLockCommand(provider, false, name)...)
}

func normalizeDockerConfigJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "\ufeff") {
		return strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	}
	bytes := []byte(raw)
	if len(bytes) >= 2 && bytes[0] == 0xff && bytes[1] == 0xfe {
		return strings.TrimSpace(decodeUTF16LE(bytes[2:]))
	}
	if looksUTF16LE(bytes) {
		return strings.TrimSpace(decodeUTF16LE(bytes))
	}
	return raw
}

func looksUTF16LE(bytes []byte) bool {
	if len(bytes) < 4 || len(bytes)%2 != 0 {
		return false
	}
	nulPairs := 0
	limit := min(len(bytes), 80)
	for i := 1; i < limit; i += 2 {
		if bytes[i] == 0 {
			nulPairs++
		}
	}
	return nulPairs >= limit/4
}

func decodeUTF16LE(bytes []byte) string {
	units := make([]uint16, 0, len(bytes)/2)
	for i := 0; i+1 < len(bytes); i += 2 {
		units = append(units, uint16(bytes[i])|uint16(bytes[i+1])<<8)
	}
	return string(utf16.Decode(units))
}

func backendConfigCommand(provider providers.PlatformProvider) []string {
	posixCommand := `set -eu; cfg="${DOCKER_CONFIG:-$HOME/.docker}"; p="$cfg/config.json"; if [ -e "$p" ]; then cat "$p"; fi`
	if provider.Type() == providers.TypeWindowsWSL {
		return []string{"sh", "-lc", escapeWSLCommandDollarsForRegistry(posixCommand)}
	}
	if runtime.GOOS == "windows" && provider.Platform() != providers.PlatformLinux {
		return []string{
			"powershell.exe",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			`$ErrorActionPreference='Stop'; $utf8=New-Object System.Text.UTF8Encoding($false); [Console]::InputEncoding=$utf8; [Console]::OutputEncoding=$utf8; $cfg=$env:DOCKER_CONFIG; if ([string]::IsNullOrWhiteSpace($cfg)) { $cfg=Join-Path $env:USERPROFILE '.docker' }; $p=Join-Path $cfg 'config.json'; if (Test-Path -LiteralPath $p) { [Console]::Out.Write([IO.File]::ReadAllText($p)) }`,
		}
	}
	return []string{"sh", "-lc", posixCommand}
}

func backendWriteConfigCommand(provider providers.PlatformProvider) []string {
	posixCommand := `set -eu; cfg="${DOCKER_CONFIG:-$HOME/.docker}"; mkdir -p "$cfg"; umask 077; tmp="$(mktemp "$cfg/.config.json.cairn.XXXXXX")"; trap 'rm -f "$tmp"' EXIT HUP INT TERM; cat > "$tmp"; chmod 600 "$tmp"; sync_f=0; if command -v sync >/dev/null 2>&1 && sync --help 2>&1 | grep -q -- '-f'; then sync_f=1; sync -f "$tmp"; fi; mv -f "$tmp" "$cfg/config.json"; if [ "$sync_f" -eq 1 ]; then sync -f "$cfg"; fi; trap - EXIT HUP INT TERM`
	if provider.Type() == providers.TypeWindowsWSL {
		return []string{"sh", "-lc", escapeWSLCommandDollarsForRegistry(posixCommand)}
	}
	if runtime.GOOS == "windows" && provider.Platform() != providers.PlatformLinux {
		return []string{
			"powershell.exe",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			`$ErrorActionPreference='Stop'; $utf8=New-Object System.Text.UTF8Encoding($false); [Console]::InputEncoding=$utf8; [Console]::OutputEncoding=$utf8; $cfg=$env:DOCKER_CONFIG; if ([string]::IsNullOrWhiteSpace($cfg)) { $cfg=Join-Path $env:USERPROFILE '.docker' }; New-Item -ItemType Directory -Force -Path $cfg | Out-Null; $p=Join-Path $cfg 'config.json'; $tmp=Join-Path $cfg ('.config.json.cairn.'+[Guid]::NewGuid().ToString('N')+'.tmp'); try { $content=[Console]::In.ReadToEnd(); $bytes=$utf8.GetBytes($content); $stream=[System.IO.File]::Open($tmp,[System.IO.FileMode]::CreateNew,[System.IO.FileAccess]::Write,[System.IO.FileShare]::None); try { $stream.Write($bytes,0,$bytes.Length); $stream.Flush($true) } finally { $stream.Dispose() }; if (Test-Path -LiteralPath $p) { [System.IO.File]::Replace($tmp,$p,$null,$false) } else { [System.IO.File]::Move($tmp,$p) } } finally { if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force } }`,
		}
	}
	return []string{"sh", "-lc", posixCommand}
}

func backendConfigLockCommand(provider providers.PlatformProvider, acquire bool) []string {
	return backendNamedLockCommand(provider, acquire, ".cairn-config.lock")
}

func backendNamedLockCommand(provider providers.PlatformProvider, acquire bool, name string) []string {
	staleMinutes := strconv.Itoa(dockerConfigLockStaleMinutes)
	posixAcquire := `set -eu; cfg="${DOCKER_CONFIG:-$HOME/.docker}"; mkdir -p "$cfg"; lock="$cfg/` + name + `"; token="$(cat)"; i=0; while ! mkdir "$lock" 2>/dev/null; do if [ -d "$lock" ] && [ ! -L "$lock" ] && [ -n "$(find "$lock" -prune -mmin +` + staleMinutes + ` -print 2>/dev/null)" ]; then rm -f "$lock/owner"; rmdir "$lock" 2>/dev/null || true; fi; i=$((i+1)); if [ "$i" -ge 50 ]; then exit 73; fi; sleep 0.1; done; if ! printf '%s' "$token" > "$lock/owner"; then rm -f "$lock/owner"; rmdir "$lock"; exit 74; fi`
	posixRelease := `set -eu; cfg="${DOCKER_CONFIG:-$HOME/.docker}"; lock="$cfg/` + name + `"; token="$(cat)"; if [ -d "$lock" ] && [ ! -L "$lock" ] && [ -f "$lock/owner" ] && [ "$(cat "$lock/owner")" = "$token" ]; then rm -f "$lock/owner"; rmdir "$lock"; fi`
	posixCommand := posixRelease
	if acquire {
		posixCommand = posixAcquire
	}
	if provider.Type() == providers.TypeWindowsWSL {
		return []string{"sh", "-lc", escapeWSLCommandDollarsForRegistry(posixCommand)}
	}
	if runtime.GOOS == "windows" && provider.Platform() != providers.PlatformLinux {
		operation := `$token=[Console]::In.ReadToEnd(); if (Test-Path -LiteralPath $lock -PathType Container) { $item=Get-Item -LiteralPath $lock -Force; $owner=Join-Path $lock 'owner'; if ((($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) -and (Test-Path -LiteralPath $owner -PathType Leaf) -and ([IO.File]::ReadAllText($owner) -eq $token)) { Remove-Item -LiteralPath $owner -Force; Remove-Item -LiteralPath $lock -Force } }; exit 0`
		if acquire {
			operation = `$token=[Console]::In.ReadToEnd(); for ($i=0; $i -lt 50; $i++) { try { New-Item -ItemType Directory -Path $lock -ErrorAction Stop | Out-Null } catch { if (Test-Path -LiteralPath $lock -PathType Container) { $item=Get-Item -LiteralPath $lock -Force; if ((($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) -and ($item.LastWriteTimeUtc -lt [DateTime]::UtcNow.AddMinutes(-` + staleMinutes + `))) { $owner=Join-Path $lock 'owner'; if (Test-Path -LiteralPath $owner -PathType Leaf) { Remove-Item -LiteralPath $owner -Force }; Remove-Item -LiteralPath $lock -Force } }; Start-Sleep -Milliseconds 100; continue }; try { [IO.File]::WriteAllText((Join-Path $lock 'owner'),$token); exit 0 } catch { $owner=Join-Path $lock 'owner'; if (Test-Path -LiteralPath $owner -PathType Leaf) { Remove-Item -LiteralPath $owner -Force }; if (Test-Path -LiteralPath $lock -PathType Container) { Remove-Item -LiteralPath $lock -Force }; exit 74 } }; exit 73`
		}
		return []string{
			"powershell.exe",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			`$ErrorActionPreference='Stop'; $cfg=$env:DOCKER_CONFIG; if ([string]::IsNullOrWhiteSpace($cfg)) { $cfg=Join-Path $env:USERPROFILE '.docker' }; New-Item -ItemType Directory -Force -Path $cfg | Out-Null; $lock=Join-Path $cfg '` + name + `'; ` + operation,
		}
	}
	return []string{"sh", "-lc", posixCommand}
}

func escapeWSLCommandDollarsForRegistry(command string) string {
	return strings.ReplaceAll(command, "$", `\$`)
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
	return strings.TrimSpace(config.CredHelpers[registryCredentialConfigKey(registry)])
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
		if err != nil || result == nil || result.ExitCode != 0 || result.StdoutTruncated || strings.TrimSpace(result.Stdout) == "" {
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
