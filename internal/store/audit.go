package store

import (
	"context"
	"database/sql"
	"strings"
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
	if millis := record.Duration.Milliseconds(); millis > 0 || (record.Status != "" && record.Status != "started") {
		durationMS = millis
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
	topic := escapeAuditLike(filter.Topic)
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, action, COALESCE(target_type, ''), COALESCE(target_id, ''), status,
		       COALESCE(error, ''), created_at, COALESCE(command, ''),
		       COALESCE(risk, ''), COALESCE(provider_id, ''), COALESCE(project_id, ''),
		       duration_ms
		FROM audit_log
		WHERE (? = '' OR action LIKE ? || '%' ESCAPE '\')
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, topic, topic, limit)
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
		var command string
		var risk string
		var providerID string
		var projectID string
		var targetType string
		var durationMS sql.NullInt64
		if err := rows.Scan(
			&entry.ID,
			&entry.Action,
			&targetType,
			&entry.Target,
			&entry.Result,
			&entry.Error,
			&createdAt,
			&command,
			&risk,
			&providerID,
			&projectID,
			&durationMS,
		); err != nil {
			return nil, err
		}
		if createdAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, createdAt)
			if err != nil {
				return nil, err
			}
			entry.TS = parsed
		}
		entry.Metadata = auditMetadata(command, risk, providerID, projectID, targetType, durationMS)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func escapeAuditLike(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func auditMetadata(command string, risk string, providerID string, projectID string, targetType string, durationMS sql.NullInt64) map[string]any {
	values := map[string]any{}
	if command != "" {
		values["command"] = command
	}
	if risk != "" {
		values["risk"] = risk
	}
	if providerID != "" {
		values["providerID"] = providerID
	}
	if projectID != "" {
		values["projectID"] = projectID
	}
	if targetType != "" {
		values["targetType"] = targetType
	}
	if durationMS.Valid {
		values["durationMS"] = durationMS.Int64
	}
	if len(values) == 0 {
		return nil
	}
	return values
}
