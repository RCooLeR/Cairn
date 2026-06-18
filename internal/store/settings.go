package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
)

type settingDefault struct {
	kind  settingKind
	value any
}

var settingDefaults = map[string]settingDefault{
	"general.theme":                   {kind: kindString, value: "dark"},
	"general.autostart_app":           {kind: kindBool, value: false},
	"general.language":                {kind: kindString, value: "en"},
	"provider.active_id":              {kind: kindString, value: ""},
	"provider.autostart_backend":      {kind: kindBool, value: true},
	"updates.check_interval_hours":    {kind: kindInt, value: 24},
	"updates.notify":                  {kind: kindBool, value: true},
	"metrics.retention_raw_minutes":   {kind: kindInt, value: 60},
	"metrics.sample_interval_seconds": {kind: kindInt, value: 2},
	"terminal.default_shell":          {kind: kindString, value: ""},
	"security.confirm_destructive":    {kind: kindBool, value: true},
	"backups.directory":               {kind: kindString, value: ""},
	"agent.enabled":                   {kind: kindBool, value: true},
	"agent.provider":                  {kind: kindString, value: "ollama"},
	"agent.endpoint":                  {kind: kindString, value: "http://127.0.0.1:11434"},
	"agent.model":                     {kind: kindString, value: "gemma4:12b"},
	"agent.max_context_lines":         {kind: kindInt, value: 400},
	"registry.credentials_mode":       {kind: kindString, value: "docker_helper"},
	"windows.wsl_distro":              {kind: kindString, value: "Ubuntu"},
	"linux.socket_path":               {kind: kindString, value: "/var/run/docker.sock"},
	"linux.sudo_mode":                 {kind: kindString, value: "ask"},
	"macos.colima_profile":            {kind: kindString, value: "default"},
	"macos.colima_cpu":                {kind: kindInt, value: 2},
	"macos.colima_memory_gb":          {kind: kindInt, value: 4},
	"macos.colima_disk_gb":            {kind: kindInt, value: 60},
}

type SettingsRepository struct {
	db *sql.DB
}

func (r *SettingsRepository) EnsureDefaults(ctx context.Context) error {
	now := utcNow()
	for key, spec := range settingDefaults {
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
	return nil
}

func (r *SettingsRepository) GetString(ctx context.Context, key string) (string, error) {
	raw, err := r.getRawWithDefault(ctx, key, kindString)
	if err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "", fmt.Errorf("%w: %s", ErrTypeMismatch, key)
	}
	return value, nil
}

func (r *SettingsRepository) SetString(ctx context.Context, key, value string) error {
	return r.setTyped(ctx, key, kindString, value)
}

func (r *SettingsRepository) GetBool(ctx context.Context, key string) (bool, error) {
	raw, err := r.getRawWithDefault(ctx, key, kindBool)
	if err != nil {
		return false, err
	}
	var value bool
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return false, fmt.Errorf("%w: %s", ErrTypeMismatch, key)
	}
	return value, nil
}

func (r *SettingsRepository) SetBool(ctx context.Context, key string, value bool) error {
	return r.setTyped(ctx, key, kindBool, value)
}

func (r *SettingsRepository) GetInt(ctx context.Context, key string) (int, error) {
	raw, err := r.getRawWithDefault(ctx, key, kindInt)
	if err != nil {
		return 0, err
	}
	var value int
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return 0, fmt.Errorf("%w: %s", ErrTypeMismatch, key)
	}
	return value, nil
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
	spec, ok := settingDefaults[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSetting, key)
	}
	if !json.Valid([]byte(rawJSON)) {
		return fmt.Errorf("%w: %s", ErrInvalidJSON, key)
	}
	if err := validateJSONKind(rawJSON, spec.kind); err != nil {
		return err
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
	spec, ok := settingDefaults[normalizeSettingKey(key)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSetting, key)
	}

	var value any
	if err := json.Unmarshal([]byte(rawJSON), &value); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidJSON, key)
	}
	return normalizeSettingValue(key, spec.kind, value)
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
		return int(typed), true
	case float64:
		asInt := int(typed)
		return asInt, typed == float64(asInt)
	case json.Number:
		asInt, err := typed.Int64()
		return int(asInt), err == nil
	default:
		return 0, false
	}
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

func validateJSONKind(rawJSON string, expected settingKind) error {
	var value any
	if err := json.Unmarshal([]byte(rawJSON), &value); err != nil {
		return err
	}

	switch expected {
	case kindBool:
		if _, ok := value.(bool); !ok {
			return ErrTypeMismatch
		}
	case kindInt:
		number, ok := value.(float64)
		if !ok || number != float64(int(number)) {
			return ErrTypeMismatch
		}
	case kindString:
		if _, ok := value.(string); !ok {
			return ErrTypeMismatch
		}
	default:
		return ErrTypeMismatch
	}
	return nil
}

func utcNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
