package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

var ErrProviderNotFound = errors.New("provider not found")

type ProviderRecord struct {
	ID             string
	Type           string
	Platform       string
	DisplayName    string
	Enabled        bool
	ConfigJSON     string
	LastStatusJSON string
	LastCheckedAt  time.Time
}

type ProviderRepository struct {
	db *sql.DB
}

func (s *Store) Providers() *ProviderRepository {
	return &ProviderRepository{db: s.writer}
}

func (r *ProviderRepository) Upsert(ctx context.Context, record ProviderRecord) error {
	enabled := 0
	if record.Enabled {
		enabled = 1
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO providers (id, type, platform, display_name, enabled, config_json, last_status_json, last_checked_at)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))
		ON CONFLICT(id) DO UPDATE SET
			type = excluded.type,
			platform = excluded.platform,
			display_name = excluded.display_name,
			enabled = excluded.enabled,
			config_json = COALESCE(excluded.config_json, providers.config_json)
	`, record.ID, record.Type, record.Platform, record.DisplayName, enabled, record.ConfigJSON, record.LastStatusJSON, formatTime(record.LastCheckedAt))
	return err
}

func (r *ProviderRepository) SaveStatus(ctx context.Context, providerID string, status *models.ProviderStatus, checkedAt time.Time) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE providers
		SET last_status_json = ?, last_checked_at = ?
		WHERE id = ?
	`, string(raw), formatTime(checkedAt), providerID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrProviderNotFound
	}
	return nil
}

func (r *ProviderRepository) List(ctx context.Context) ([]ProviderRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, type, platform, display_name, enabled, COALESCE(config_json, ''),
			COALESCE(last_status_json, ''), COALESCE(last_checked_at, '')
		FROM providers
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var records []ProviderRecord
	for rows.Next() {
		record, err := scanProviderRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *ProviderRepository) Get(ctx context.Context, providerID string) (*ProviderRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, type, platform, display_name, enabled, COALESCE(config_json, ''),
			COALESCE(last_status_json, ''), COALESCE(last_checked_at, '')
		FROM providers
		WHERE id = ?
	`, providerID)
	record, err := scanProviderRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrProviderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

type providerScanner interface {
	Scan(dest ...any) error
}

func scanProviderRecord(scanner providerScanner) (ProviderRecord, error) {
	var record ProviderRecord
	var enabled int
	var lastChecked string
	if err := scanner.Scan(
		&record.ID,
		&record.Type,
		&record.Platform,
		&record.DisplayName,
		&enabled,
		&record.ConfigJSON,
		&record.LastStatusJSON,
		&lastChecked,
	); err != nil {
		return ProviderRecord{}, err
	}
	record.Enabled = enabled != 0
	if lastChecked != "" {
		parsed, err := time.Parse(time.RFC3339Nano, lastChecked)
		if err != nil {
			return ProviderRecord{}, err
		}
		record.LastCheckedAt = parsed
	}
	return record, nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
