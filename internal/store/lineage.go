package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

type LineageRepository struct {
	db *sql.DB
}

type LineageRecord struct {
	ID              int64
	ProviderID      string
	ProjectID       string
	ServiceID       string
	ServiceName     string
	ContainerID     string
	ServiceImageRef string
	ServiceImageID  string
	ServiceDigest   string
	BuildContext    string
	DockerfilePath  string
	BuildTarget     string
	DockerfileHash  string
	BuildArgs       map[string]string
	Source          models.LineageSource
	Confidence      models.Confidence
	DiscoveredAt    time.Time
	UpdatedAt       time.Time
	BaseRefs        []BaseImageRefRecord
}

type BaseImageRefRecord struct {
	ID               int64
	LineageID        int64
	Name             string
	Tag              string
	ImageRef         string
	Platform         string
	StageName        string
	StageIndex       int
	IsFinalStageBase bool
	BuildTimeDigest  string
	LocalDigest      string
	RemoteDigest     string
	Status           models.UpdateStatus
	LastCheckedAt    time.Time
	Error            string
}

func (s *Store) Lineage() *LineageRepository {
	return &LineageRepository{db: s.writer}
}

func (r *LineageRepository) ReplaceProject(ctx context.Context, projectID string, records []LineageRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM image_lineage WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	for _, record := range records {
		record.ProjectID = projectID
		if err := insertLineageRecord(ctx, tx, record); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *LineageRepository) ReplaceService(ctx context.Context, projectID string, service string, record LineageRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM image_lineage
		WHERE project_id = ? AND service_name = ?
	`, projectID, service); err != nil {
		return err
	}
	record.ProjectID = projectID
	record.ServiceName = service
	if err := insertLineageRecord(ctx, tx, record); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *LineageRepository) ListProject(ctx context.Context, projectID string) ([]LineageRecord, error) {
	rows, err := r.db.QueryContext(ctx, lineageSelectSQL()+`
		WHERE project_id = ?
		ORDER BY service_name ASC, id ASC
	`, projectID)
	if err != nil {
		return nil, err
	}
	records, err := scanLineageRows(rows)
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for i := range records {
		records[i].BaseRefs, err = r.listBaseRefs(ctx, records[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return records, nil
}

func (r *LineageRepository) GetService(ctx context.Context, projectID string, service string) (LineageRecord, error) {
	row := r.db.QueryRowContext(ctx, lineageSelectSQL()+`
		WHERE project_id = ? AND service_name = ?
		ORDER BY id DESC
		LIMIT 1
	`, projectID, service)
	record, err := scanLineageRecord(row)
	if err != nil {
		return LineageRecord{}, err
	}
	record.BaseRefs, err = r.listBaseRefs(ctx, record.ID)
	if err != nil {
		return LineageRecord{}, err
	}
	return record, nil
}

func (r *LineageRepository) GetContainer(ctx context.Context, containerID string) (LineageRecord, error) {
	row := r.db.QueryRowContext(ctx, lineageSelectSQL()+`
		WHERE container_id = ?
		ORDER BY id DESC
		LIMIT 1
	`, containerID)
	record, err := scanLineageRecord(row)
	if err != nil {
		return LineageRecord{}, err
	}
	record.BaseRefs, err = r.listBaseRefs(ctx, record.ID)
	if err != nil {
		return LineageRecord{}, err
	}
	return record, nil
}

func scanLineageRows(rows *sql.Rows) ([]LineageRecord, error) {
	records := []LineageRecord{}
	for rows.Next() {
		record, err := scanLineageRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func insertLineageRecord(ctx context.Context, tx *sql.Tx, record LineageRecord) error {
	if record.DiscoveredAt.IsZero() {
		record.DiscoveredAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.DiscoveredAt
	}
	if record.Source == "" {
		record.Source = models.LineageSourceUnknown
	}
	if record.Confidence == "" {
		record.Confidence = models.ConfidenceUnknown
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO image_lineage (
			provider_id, project_id, service_id, service_name, container_id,
			service_image_ref, service_image_id, service_digest, build_context,
			dockerfile_path, build_target, dockerfile_hash, build_args_json,
			source, confidence, discovered_at, updated_at
		)
		VALUES (?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?)
	`, record.ProviderID, record.ProjectID, record.ServiceID, record.ServiceName,
		record.ContainerID, record.ServiceImageRef, record.ServiceImageID, record.ServiceDigest,
		record.BuildContext, record.DockerfilePath, record.BuildTarget, record.DockerfileHash,
		jsonText(record.BuildArgs, "{}"), string(record.Source), string(record.Confidence),
		formatTime(record.DiscoveredAt), formatTime(record.UpdatedAt))
	if err != nil {
		return err
	}
	lineageID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for _, base := range record.BaseRefs {
		base.LineageID = lineageID
		if base.Status == "" {
			base.Status = models.UpdateStatusUnknown
		}
		final := 0
		if base.IsFinalStageBase {
			final = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO base_image_refs (
				lineage_id, name, tag, image_ref, platform, stage_name, stage_index,
				is_final_stage_base, build_time_digest, local_digest, remote_digest,
				status, last_checked_at, error
			)
			VALUES (?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), ?,
				?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?,
				NULLIF(?, ''), NULLIF(?, ''))
		`, base.LineageID, base.Name, base.Tag, base.ImageRef, base.Platform,
			base.StageName, base.StageIndex, final, base.BuildTimeDigest, base.LocalDigest,
			base.RemoteDigest, string(base.Status), formatTime(base.LastCheckedAt), base.Error); err != nil {
			return err
		}
	}
	return nil
}

func (r *LineageRepository) listBaseRefs(ctx context.Context, lineageID int64) ([]BaseImageRefRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, lineage_id, name, COALESCE(tag, ''), image_ref,
			COALESCE(platform, ''), COALESCE(stage_name, ''), stage_index,
			is_final_stage_base, COALESCE(build_time_digest, ''),
			COALESCE(local_digest, ''), COALESCE(remote_digest, ''), status,
			COALESCE(last_checked_at, ''), COALESCE(error, '')
		FROM base_image_refs
		WHERE lineage_id = ?
		ORDER BY stage_index ASC, id ASC
	`, lineageID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	refs := []BaseImageRefRecord{}
	for rows.Next() {
		var ref BaseImageRefRecord
		var final int
		var checkedAt string
		if err := rows.Scan(
			&ref.ID,
			&ref.LineageID,
			&ref.Name,
			&ref.Tag,
			&ref.ImageRef,
			&ref.Platform,
			&ref.StageName,
			&ref.StageIndex,
			&final,
			&ref.BuildTimeDigest,
			&ref.LocalDigest,
			&ref.RemoteDigest,
			&ref.Status,
			&checkedAt,
			&ref.Error,
		); err != nil {
			return nil, err
		}
		ref.IsFinalStageBase = final != 0
		ref.LastCheckedAt = parseStoreTime(checkedAt)
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func lineageSelectSQL() string {
	return `
		SELECT id, provider_id, project_id, COALESCE(service_id, ''), service_name,
			COALESCE(container_id, ''), COALESCE(service_image_ref, ''),
			COALESCE(service_image_id, ''), COALESCE(service_digest, ''),
			COALESCE(build_context, ''), COALESCE(dockerfile_path, ''),
			COALESCE(build_target, ''), COALESCE(dockerfile_hash, ''),
			COALESCE(build_args_json, '{}'), source, confidence, discovered_at, updated_at
		FROM image_lineage
	`
}

type lineageScanner interface {
	Scan(dest ...any) error
}

func scanLineageRecord(scanner lineageScanner) (LineageRecord, error) {
	var record LineageRecord
	var argsJSON string
	var source string
	var confidence string
	var discoveredAt string
	var updatedAt string
	if err := scanner.Scan(
		&record.ID,
		&record.ProviderID,
		&record.ProjectID,
		&record.ServiceID,
		&record.ServiceName,
		&record.ContainerID,
		&record.ServiceImageRef,
		&record.ServiceImageID,
		&record.ServiceDigest,
		&record.BuildContext,
		&record.DockerfilePath,
		&record.BuildTarget,
		&record.DockerfileHash,
		&argsJSON,
		&source,
		&confidence,
		&discoveredAt,
		&updatedAt,
	); err != nil {
		return LineageRecord{}, err
	}
	if err := json.Unmarshal([]byte(nullJSON(argsJSON, "{}")), &record.BuildArgs); err != nil {
		record.BuildArgs = map[string]string{}
	}
	record.Source = models.LineageSource(source)
	record.Confidence = models.Confidence(confidence)
	record.DiscoveredAt = parseStoreTime(discoveredAt)
	record.UpdatedAt = parseStoreTime(updatedAt)
	return record, nil
}

func LineageRecordsToModels(records []LineageRecord) []models.ImageLineage {
	result := make([]models.ImageLineage, 0, len(records))
	for _, record := range records {
		result = append(result, record.ToModel())
	}
	return result
}

func (r LineageRecord) ToModel() models.ImageLineage {
	base := selectDisplayedBase(r.BaseRefs)
	model := models.ImageLineage{
		ProjectID:   r.ProjectID,
		Service:     r.ServiceName,
		ContainerID: r.ContainerID,
		ImageRef:    r.ServiceImageRef,
		ImageID:     r.ServiceImageID,
		Source:      r.Source,
		Confidence:  r.Confidence,
		Reason:      lineageReason(r, base),
	}
	if base != nil {
		model.BaseImage = base.ImageRef
		model.BaseDigest = base.BuildTimeDigest
	}
	return model
}

func selectDisplayedBase(refs []BaseImageRefRecord) *BaseImageRefRecord {
	if len(refs) == 0 {
		return nil
	}
	for i := range refs {
		if refs[i].IsFinalStageBase {
			return &refs[i]
		}
	}
	return &refs[0]
}

func lineageReason(record LineageRecord, base *BaseImageRefRecord) string {
	if base == nil {
		if record.Source == models.LineageSourceUnknown {
			return "Base image: Unknown — this is a third-party registry image and no base metadata was found."
		}
		if record.Confidence == models.ConfidenceUnknown {
			return "Base tracking unavailable — Dockerfile could not be parsed (see details)."
		}
		return "Dockerfile final stage uses scratch; no external base image is tracked."
	}
	if base.Status == models.UpdateStatusUnknownBaseImage || record.Confidence == models.ConfidenceLow {
		return "Dockerfile FROM contains unresolved ARG values; base tracking confidence is low."
	}
	switch record.Source {
	case models.LineageSourceCairnLabel:
		return "Derived from Cairn build labels."
	case models.LineageSourceOCIAnnotation:
		return "Derived from OCI base image annotations."
	case models.LineageSourceComposeDockerfile:
		if record.Confidence == models.ConfidenceHigh {
			return "Derived from Compose build config, Dockerfile, and build-time base digest."
		}
		return "Derived from Compose build config and Dockerfile."
	default:
		return "Base image metadata was discovered with unknown provenance."
	}
}

func IsStoreNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
