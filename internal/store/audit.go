package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

type AuditRepository struct {
	db *sql.DB
}

type AuditRecord struct {
	Action     string
	TargetType string
	TargetID   string
	ProviderID string
	ProjectID  string
	Command    string
	Risk       models.Risk
	Status     string
	ExitCode   *int
	Duration   time.Duration
	Error      string
	CreatedAt  time.Time
}

func (s *Store) Audit() *AuditRepository {
	return &AuditRepository{db: s.writer}
}

func (r *AuditRepository) Insert(ctx context.Context, record AuditRecord) (int64, error) {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	var durationMS any
	if record.Duration > 0 {
		durationMS = record.Duration.Milliseconds()
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO audit_log (
			action, target_type, target_id, provider_id, project_id,
			command, risk, status, exit_code, duration_ms, error, created_at
		)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), ?)
	`, record.Action, record.TargetType, record.TargetID, record.ProviderID, record.ProjectID,
		record.Command, string(record.Risk), record.Status, record.ExitCode, durationMS, record.Error,
		formatTime(record.CreatedAt))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *AuditRepository) List(ctx context.Context, filter models.AuditFilter) ([]models.AuditEntry, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, action, COALESCE(target_id, ''), status, COALESCE(error, ''), created_at
		FROM audit_log
		WHERE (? = '' OR action LIKE ? || '%')
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, filter.Topic, filter.Topic, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	entries := []models.AuditEntry{}
	for rows.Next() {
		var entry models.AuditEntry
		var createdAt string
		if err := rows.Scan(&entry.ID, &entry.Action, &entry.Target, &entry.Result, &entry.Error, &createdAt); err != nil {
			return nil, err
		}
		if createdAt != "" {
			entry.TS, _ = time.Parse(time.RFC3339Nano, createdAt)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
