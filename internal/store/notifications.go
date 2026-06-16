package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

type NotificationRepository struct {
	db *sql.DB
}

type NotificationRecord struct {
	Level     string
	Title     string
	Body      string
	Topic     string
	Read      bool
	CreatedAt time.Time
}

func (s *Store) Notifications() *NotificationRepository {
	return &NotificationRepository{db: s.writer}
}

func (r *NotificationRepository) Insert(ctx context.Context, record NotificationRecord) (int64, error) {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO notifications (level, title, body, topic, read, created_at)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)
	`, record.Level, record.Title, record.Body, record.Topic, boolInt(record.Read), formatTime(record.CreatedAt))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *NotificationRepository) List(ctx context.Context, unreadOnly bool, limit int) ([]models.Notification, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, level, title, COALESCE(body, ''), COALESCE(topic, ''), read, created_at
		FROM notifications
		WHERE (? = 0 OR read = 0)
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, boolInt(unreadOnly), limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	notifications := []models.Notification{}
	for rows.Next() {
		var notification models.Notification
		var read int
		var createdAt string
		if err := rows.Scan(
			&notification.ID,
			&notification.Level,
			&notification.Title,
			&notification.Body,
			&notification.Topic,
			&read,
			&createdAt,
		); err != nil {
			return nil, err
		}
		notification.Read = read != 0
		if createdAt != "" {
			notification.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		}
		notifications = append(notifications, notification)
	}
	return notifications, rows.Err()
}

func (r *NotificationRepository) MarkRead(ctx context.Context, ids []int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if len(ids) == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE notifications SET read = 1 WHERE read = 0`); err != nil {
			return err
		}
		return tx.Commit()
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE notifications SET read = 1 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
