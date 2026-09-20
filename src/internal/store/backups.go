package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

type BackupRepository struct {
	db *sql.DB
}

type BackupRecord struct {
	ID                  string
	ProviderID          string
	ProjectID           string
	VolumeName          string
	BackupPath          string
	MetadataPath        string
	CompressedSizeBytes int64
	Result              string
	CreatedAt           time.Time
	Error               string
}

func (s *Store) Backups() *BackupRepository {
	return &BackupRepository{db: s.writer}
}

func (r *BackupRepository) Insert(ctx context.Context, record BackupRecord) error {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO backups (
			id, provider_id, project_id, volume_name, backup_path, metadata_path,
			compressed_size_bytes, result, created_at, error
		)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''))
	`, record.ID, record.ProviderID, record.ProjectID, record.VolumeName, record.BackupPath,
		record.MetadataPath, record.CompressedSizeBytes, record.Result, formatTime(record.CreatedAt),
		record.Error)
	return err
}

func (r *BackupRepository) Get(ctx context.Context, id string) (BackupRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, provider_id, COALESCE(project_id, ''), volume_name, backup_path,
		       COALESCE(metadata_path, ''), COALESCE(compressed_size_bytes, 0),
		       result, created_at, COALESCE(error, '')
		FROM backups
		WHERE id = ?
	`, id)
	return scanBackupRecord(row)
}

func (r *BackupRepository) List(ctx context.Context, filter models.BackupFilter) ([]BackupRecord, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, COALESCE(project_id, ''), volume_name, backup_path,
		       COALESCE(metadata_path, ''), COALESCE(compressed_size_bytes, 0),
		       result, created_at, COALESCE(error, '')
		FROM backups
		WHERE (? = '' OR volume_name = ?)
		  AND (? = '' OR project_id = ?)
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, filter.VolumeName, filter.VolumeName, filter.ProjectID, filter.ProjectID, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	records := []BackupRecord{}
	for rows.Next() {
		record, err := scanBackupRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *BackupRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type backupScanner interface {
	Scan(dest ...any) error
}

func scanBackupRecord(scanner backupScanner) (BackupRecord, error) {
	var record BackupRecord
	var createdAt string
	err := scanner.Scan(
		&record.ID,
		&record.ProviderID,
		&record.ProjectID,
		&record.VolumeName,
		&record.BackupPath,
		&record.MetadataPath,
		&record.CompressedSizeBytes,
		&record.Result,
		&createdAt,
		&record.Error,
	)
	if err != nil {
		return BackupRecord{}, err
	}
	record.CreatedAt = parseStoreTime(createdAt)
	return record, nil
}
