package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"time"
)

var (
	ErrUnknownSetting = errors.New("unknown setting")
	ErrInvalidJSON    = errors.New("invalid setting JSON")
	ErrTypeMismatch   = errors.New("setting type mismatch")
	ErrInvalidValue   = errors.New("invalid setting value")
)

type settingKind string

const (
	kindBool   settingKind = "bool"
	kindInt    settingKind = "int"
	kindString settingKind = "string"

	defaultAgentModel       = "gemma4:12b-it-q8_0"
	legacyDefaultAgentModel = "gemma4:12b"
	maxSettingNumberLength  = 64
)

type settingDefault struct {
	kind     settingKind
	value    any
	intRange *settingIntRange
}

type settingIntRange struct {
	min int
	max int
}

// Integer limits match explicit UI bounds where they exist. For fixed-choice
// or open-ended controls, the backend limits are deliberately broader so
// existing power-user configurations remain valid while nonsensical values
// cannot reach schedulers, allocators, or provider command construction.
var (
	updateCheckIntervalHoursRange = &settingIntRange{min: 0, max: 24 * 365}
	metricsRetentionMinutesRange  = &settingIntRange{min: 1, max: 24 * 60}
	metricsSampleSecondsRange     = &settingIntRange{min: 1, max: 10}
	agentContextLinesRange        = &settingIntRange{min: 100, max: 2_000}
	colimaCPURange                = &settingIntRange{min: 1, max: 128}
	colimaMemoryGBRange           = &settingIntRange{min: 1, max: 512}
	colimaDiskGBRange             = &settingIntRange{min: 1, max: 2_048}
)

var settingDefaults = map[string]settingDefault{
	"general.theme":                   {kind: kindString, value: "dark"},
	"general.autostart_app":           {kind: kindBool, value: false},
	"general.language":                {kind: kindString, value: "en"},
	"provider.active_id":              {kind: kindString, value: ""},
	"provider.autostart_backend":      {kind: kindBool, value: true},
	"portforward.enabled":             {kind: kindBool, value: true},
	"updates.check_interval_hours":    {kind: kindInt, value: 24, intRange: updateCheckIntervalHoursRange},
	"updates.notify":                  {kind: kindBool, value: true},
	"metrics.retention_raw_minutes":   {kind: kindInt, value: 60, intRange: metricsRetentionMinutesRange},
	"metrics.sample_interval_seconds": {kind: kindInt, value: 2, intRange: metricsSampleSecondsRange},
	"terminal.default_shell":          {kind: kindString, value: ""},
	"security.confirm_destructive":    {kind: kindBool, value: true},
	"backups.directory":               {kind: kindString, value: ""},
	"agent.enabled":                   {kind: kindBool, value: true},
	"agent.provider":                  {kind: kindString, value: "ollama"},
	"agent.endpoint":                  {kind: kindString, value: "http://127.0.0.1:11434"},
	"agent.model":                     {kind: kindString, value: defaultAgentModel},
	"agent.max_context_lines":         {kind: kindInt, value: 400, intRange: agentContextLinesRange},
	"registry.credentials_mode":       {kind: kindString, value: "docker_helper"},
	"windows.wsl_distro":              {kind: kindString, value: "Ubuntu"},
	"linux.socket_path":               {kind: kindString, value: "/var/run/docker.sock"},
	"linux.sudo_mode":                 {kind: kindString, value: "ask"},
	"macos.colima_profile":            {kind: kindString, value: "default"},
	"macos.colima_cpu":                {kind: kindInt, value: 2, intRange: colimaCPURange},
	"macos.colima_memory_gb":          {kind: kindInt, value: 4, intRange: colimaMemoryGBRange},
	"macos.colima_disk_gb":            {kind: kindInt, value: 60, intRange: colimaDiskGBRange},
}

type SettingsRepository struct {
	db *sql.DB
}

func (r *SettingsRepository) EnsureDefaults(ctx context.Context) error {
	if err := validateSettingDefinitions(); err != nil {
		return err
	}
	now := utcNow()
	for _, key := range sortedSettingKeys() {
		spec := settingDefaults[key]
		value, err := json.Marshal(spec.value)
		if err != nil {
			return err
		}
		if _, err := r.db.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO NOTHING
		`, key, string(value), now); err != nil {
			return err
		}
	}
	if err := r.upgradeLegacySettingDefaults(ctx, now); err != nil {
		return err
	}
	return r.repairInvalidSettingValues(ctx, now)
}

func validateSettingDefinitions() error {
	for _, key := range sortedSettingKeys() {
		spec := settingDefaults[key]
		if _, err := normalizeSettingValue(key, spec.kind, spec.value); err != nil {
			return fmt.Errorf("invalid setting definition %s: %w", key, err)
		}
	}
	return nil
}

func sortedSettingKeys() []string {
	keys := make([]string, 0, len(settingDefaults))
	for key := range settingDefaults {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// repairInvalidSettingValues is the compatibility path for databases written
// by older versions that had type/enum/range checks only on some call paths.
// Store.Migrate calls EnsureDefaults on every startup, so invalid known values
// are reset to their stable defaults before services can consume them. The
// compare-and-swap predicate avoids overwriting a concurrent valid update.
func (r *SettingsRepository) repairInvalidSettingValues(ctx context.Context, now string) error {
	for _, key := range sortedSettingKeys() {
		spec := settingDefaults[key]
		var raw string
		if err := r.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&raw); err != nil {
			return err
		}
		if _, err := decodeSettingValue(key, raw); err == nil {
			continue
		}

		defaultValue, err := json.Marshal(spec.value)
		if err != nil {
			return err
		}
		if _, err := r.db.ExecContext(ctx, `
			UPDATE settings
			SET value = ?, updated_at = ?
			WHERE key = ? AND value = ?
		`, string(defaultValue), now, key, raw); err != nil {
			return err
		}
	}
	return nil
}

func (r *SettingsRepository) upgradeLegacySettingDefaults(ctx context.Context, now string) error {
	currentAgentModel, err := json.Marshal(defaultAgentModel)
	if err != nil {
		return err
	}
	legacyAgentModel, err := json.Marshal(legacyDefaultAgentModel)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE settings
		SET value = ?, updated_at = ?
		WHERE key = ? AND value = ?
	`, string(currentAgentModel), now, "agent.model", string(legacyAgentModel))
	return err
}

func (r *SettingsRepository) GetString(ctx context.Context, key string) (string, error) {
	raw, err := r.getRawWithDefault(ctx, key, kindString)
	if err != nil {
		return "", err
	}
	value, err := decodeSettingValue(key, raw)
	if err != nil {
		return "", err
	}
	stringValue, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrTypeMismatch, key)
	}
	return stringValue, nil
}

func (r *SettingsRepository) SetString(ctx context.Context, key, value string) error {
	return r.setTyped(ctx, key, kindString, value)
}

func (r *SettingsRepository) GetBool(ctx context.Context, key string) (bool, error) {
	raw, err := r.getRawWithDefault(ctx, key, kindBool)
	if err != nil {
		return false, err
	}
	value, err := decodeSettingValue(key, raw)
	if err != nil {
		return false, err
	}
	boolValue, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrTypeMismatch, key)
	}
	return boolValue, nil
}

func (r *SettingsRepository) SetBool(ctx context.Context, key string, value bool) error {
	return r.setTyped(ctx, key, kindBool, value)
}

func (r *SettingsRepository) GetInt(ctx context.Context, key string) (int, error) {
	raw, err := r.getRawWithDefault(ctx, key, kindInt)
	if err != nil {
		return 0, err
	}
	value, err := decodeSettingValue(key, raw)
	if err != nil {
		return 0, err
	}
	intValue, ok := value.(int)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrTypeMismatch, key)
	}
	return intValue, nil
}

func (r *SettingsRepository) SetInt(ctx context.Context, key string, value int) error {
	return r.setTyped(ctx, key, kindInt, value)
}

func (r *SettingsRepository) All(ctx context.Context) (map[string]any, error) {
	if err := r.EnsureDefaults(ctx); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT key, value
		FROM settings
		ORDER BY key
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	settings := make(map[string]any, len(settingDefaults))
	for rows.Next() {
		var key string
		var raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		value, err := decodeSettingValue(key, raw)
		if err != nil {
			return nil, err
		}
		settings[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return settings, nil
}

func (r *SettingsRepository) SetValue(ctx context.Context, key string, value any) error {
	key = normalizeSettingKey(key)
	spec, ok := settingDefaults[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSetting, key)
	}

	normalized, err := normalizeSettingValue(key, spec.kind, value)
	if err != nil {
		return err
	}
	return r.setTyped(ctx, key, spec.kind, normalized)
}

func (r *SettingsRepository) SetRaw(ctx context.Context, key, rawJSON string) error {
	key = normalizeSettingKey(key)
	if _, ok := settingDefaults[key]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSetting, key)
	}
	if _, err := decodeSettingValue(key, rawJSON); err != nil {
		return err
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, rawJSON, utcNow())
	return err
}

func (r *SettingsRepository) getRawWithDefault(ctx context.Context, key string, expected settingKind) (string, error) {
	key = normalizeSettingKey(key)
	spec, ok := settingDefaults[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownSetting, key)
	}
	if spec.kind != expected {
		return "", fmt.Errorf("%w: %s", ErrTypeMismatch, key)
	}

	var raw string
	err := r.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		value, marshalErr := json.Marshal(spec.value)
		if marshalErr != nil {
			return "", marshalErr
		}
		raw = string(value)
		err = r.SetRaw(ctx, key, raw)
	}
	if err != nil {
		return "", err
	}

	return raw, nil
}

func (r *SettingsRepository) setTyped(ctx context.Context, key string, expected settingKind, value any) error {
	key = normalizeSettingKey(key)
	spec, ok := settingDefaults[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSetting, key)
	}
	if spec.kind != expected {
		return fmt.Errorf("%w: %s", ErrTypeMismatch, key)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, key, string(raw))
}

func decodeSettingValue(key, rawJSON string) (any, error) {
	normalizedKey := normalizeSettingKey(key)
	spec, ok := settingDefaults[normalizedKey]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSetting, key)
	}
	if !json.Valid([]byte(rawJSON)) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidJSON, key)
	}

	var value any
	decoder := json.NewDecoder(bytes.NewReader([]byte(rawJSON)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidJSON, key)
	}
	return normalizeSettingValue(normalizedKey, spec.kind, value)
}

func normalizeSettingValue(key string, expected settingKind, value any) (any, error) {
	var normalized any
	switch expected {
	case kindBool:
		boolValue, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrTypeMismatch, key)
		}
		if normalizeSettingKey(key) == "security.confirm_destructive" && !boolValue {
			return nil, fmt.Errorf("%w: %s=false", ErrInvalidValue, key)
		}
		normalized = boolValue
	case kindInt:
		intValue, ok := asSettingInt(value)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrTypeMismatch, key)
		}
		spec := settingDefaults[normalizeSettingKey(key)]
		if spec.intRange == nil {
			return nil, fmt.Errorf("%w: %s has no integer range", ErrInvalidValue, key)
		}
		if intValue < spec.intRange.min || intValue > spec.intRange.max {
			return nil, fmt.Errorf(
				"%w: %s=%d (expected %d..%d)",
				ErrInvalidValue,
				key,
				intValue,
				spec.intRange.min,
				spec.intRange.max,
			)
		}
		normalized = intValue
	case kindString:
		stringValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrTypeMismatch, key)
		}
		normalized = stringValue
	default:
		return nil, fmt.Errorf("%w: %s", ErrTypeMismatch, key)
	}
	if err := validateSettingEnum(key, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func asSettingInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return settingIntFromInt64(typed)
	case float64:
		return settingIntFromFloat64(typed)
	case json.Number:
		if len(typed.String()) > maxSettingNumberLength {
			return 0, false
		}
		rational, ok := new(big.Rat).SetString(typed.String())
		if !ok || !rational.IsInt() || !rational.Num().IsInt64() {
			return 0, false
		}
		return settingIntFromInt64(rational.Num().Int64())
	default:
		return 0, false
	}
}

func settingIntFromInt64(value int64) (int, bool) {
	if strconv.IntSize == 32 && (value < -1<<31 || value > 1<<31-1) {
		return 0, false
	}
	return int(value), true
}

func settingIntFromFloat64(value float64) (int, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
		return 0, false
	}
	// Use an exclusive power-of-two upper bound so conversion is checked before
	// it happens even when float64 cannot represent the platform's MaxInt.
	limit := math.Ldexp(1, strconv.IntSize-1)
	if value < -limit || value >= limit {
		return 0, false
	}
	return int(value), true
}

func validateSettingEnum(key string, value any) error {
	stringValue, ok := value.(string)
	if !ok {
		return nil
	}
	allowedValues := map[string]map[string]struct{}{
		"general.theme":             {"dark": {}, "light": {}, "system": {}},
		"general.language":          {"en": {}},
		"agent.provider":            {"ollama": {}, "openai_compatible": {}},
		"registry.credentials_mode": {"docker_helper": {}, "none": {}},
		"linux.sudo_mode":           {"ask": {}, "group": {}, "rootless": {}},
	}
	allowed, ok := allowedValues[normalizeSettingKey(key)]
	if !ok {
		return nil
	}
	if _, ok := allowed[stringValue]; ok {
		return nil
	}
	return fmt.Errorf("%w: %s=%s", ErrInvalidValue, key, stringValue)
}

func utcNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
