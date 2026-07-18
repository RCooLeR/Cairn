package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

type UpdateRepository struct {
	db *sql.DB
}

const (
	updateCheckHistoryRetention       = 30 * 24 * time.Hour
	updateCheckHistoryKeepGenerations = 20
	updateCheckRunningLease           = 24 * time.Hour
	updateCheckLegacyKeepRows         = 1000
)

type updateCheckExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type UpdateCheckRecord struct {
	ID                int64
	GenerationID      int64
	IsCurrent         bool
	ProviderID        string
	ContextName       string
	ProjectID         string
	ServiceID         string
	ContainerID       string
	Kind              models.UpdateKind
	ImageRef          string
	BaseImageRef      string
	LocalImageID      string
	LocalDigest       string
	RemoteDigest      string
	LineageID         int64
	BaseImageRefID    int64
	Confidence        models.Confidence
	RecommendedAction models.RecommendedAction
	Status            models.UpdateStatus
	CheckedAt         time.Time
	Error             string
}

type UpdateCheckRun struct {
	ID          int64
	ProviderID  string
	ContextName string
	ProjectID   string
	ServiceID   string
	StartedAt   time.Time
	FinishedAt  time.Time
	Status      string
}

type IgnoredUpdateRecord struct {
	ID           int64
	ProviderID   string
	ContextName  string
	ImageRef     string
	UpdateKind   models.UpdateKind
	BaseImageRef string
	ProjectID    string
	ServiceID    string
	Reason       string
	CreatedAt    time.Time
}

type UpdateHistoryRecord struct {
	ID             int64
	ProviderID     string
	ContextName    string
	ProjectID      string
	ServiceID      string
	UpdateKind     models.UpdateKind
	ImageRef       string
	BaseImageRef   string
	OldImageID     string
	OldDigest      string
	OldBaseDigest  string
	NewImageID     string
	NewDigest      string
	NewBaseDigest  string
	DockerfileHash string
	BuildArgs      map[string]string
	Commands       []models.PlannedCommand
	Result         string
	HealthResult   string
	StartedAt      time.Time
	FinishedAt     time.Time
	RollbackStatus string
	Error          string
}

func (s *Store) Updates() *UpdateRepository {
	return &UpdateRepository{db: s.writer}
}

func normalizeUpdateCheck(record UpdateCheckRecord) UpdateCheckRecord {
	if record.CheckedAt.IsZero() {
		record.CheckedAt = time.Now().UTC()
	}
	if record.Kind == "" {
		record.Kind = models.UpdateKindServiceImage
	}
	if record.Status == "" {
		record.Status = models.UpdateStatusUnknown
	}
	if record.Confidence == "" {
		record.Confidence = models.ConfidenceUnknown
	}
	if record.RecommendedAction == "" {
		record.RecommendedAction = models.RecommendedActionNone
	}
	return record
}

func insertCheck(ctx context.Context, exec updateCheckExecutor, record UpdateCheckRecord, generationID int64) (int64, error) {
	record = normalizeUpdateCheck(record)
	result, err := exec.ExecContext(ctx, `
		INSERT INTO image_update_checks (
			generation_id, is_current, provider_id, context_name, project_id, service_id, container_id, kind, image_ref,
			base_image_ref, local_image_id, local_digest, remote_digest,
			lineage_id, base_image_ref_id, confidence, recommended_action,
			status, checked_at, error
		)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?,
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, ''), NULLIF(?, ''),
			?, ?, NULLIF(?, ''))
	`, generationID, true, record.ProviderID, record.ContextName, record.ProjectID, record.ServiceID, record.ContainerID,
		string(record.Kind), record.ImageRef, record.BaseImageRef, record.LocalImageID,
		record.LocalDigest, record.RemoteDigest, record.LineageID, record.BaseImageRefID,
		string(record.Confidence), string(record.RecommendedAction), string(record.Status),
		formatTime(record.CheckedAt), record.Error)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// BeginProjectCheckRunInScope reserves the ordering token before a project
// snapshot is evaluated. A later run ID can supersede this run even when the
// later publication contains no check rows.
func (r *UpdateRepository) BeginProjectCheckRunInScope(ctx context.Context, scope runtimescope.Scope, projectID string, startedAt time.Time) (UpdateCheckRun, error) {
	return r.beginCheckRunInScope(ctx, scope, projectID, "", startedAt)
}

func (r *UpdateRepository) BeginServiceCheckRunInScope(ctx context.Context, scope runtimescope.Scope, projectID string, serviceID string, startedAt time.Time) (UpdateCheckRun, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return UpdateCheckRun{}, errors.New("service ID is required")
	}
	return r.beginCheckRunInScope(ctx, scope, projectID, serviceID, startedAt)
}

func (r *UpdateRepository) beginCheckRunInScope(ctx context.Context, scope runtimescope.Scope, projectID string, serviceID string, startedAt time.Time) (UpdateCheckRun, error) {
	if !scope.Valid() {
		return UpdateCheckRun{}, errors.New("runtime scope is required")
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return UpdateCheckRun{}, errors.New("project ID is required")
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdateCheckRun{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM projects
		WHERE id = ? AND provider_id = ? AND context_name = ?
	`, projectID, scope.ProviderID(), scope.ContextName()).Scan(&exists); err != nil {
		return UpdateCheckRun{}, err
	}
	if serviceID != "" {
		if err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM services WHERE id = ? AND project_id = ?
		`, serviceID, projectID).Scan(&exists); err != nil {
			return UpdateCheckRun{}, err
		}
	}
	leaseNow := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE image_update_check_runs
		SET status = 'abandoned', finished_at = ?
		WHERE provider_id = ? AND context_name = ? AND project_id = ?
			AND status = 'running' AND julianday(reserved_at) < julianday(?)
	`, formatTime(leaseNow), scope.ProviderID(), scope.ContextName(), projectID,
		formatTime(leaseNow.Add(-updateCheckRunningLease))); err != nil {
		return UpdateCheckRun{}, err
	}
	if err := pruneUpdateCheckRuns(ctx, tx, scope, projectID); err != nil {
		return UpdateCheckRun{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO image_update_check_runs (
			provider_id, context_name, project_id, service_id, started_at, status
		) VALUES (?, ?, ?, ?, ?, 'running')
	`, scope.ProviderID(), scope.ContextName(), projectID, serviceID, formatTime(startedAt))
	if err != nil {
		return UpdateCheckRun{}, err
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return UpdateCheckRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return UpdateCheckRun{}, err
	}
	return UpdateCheckRun{
		ID: runID, ProviderID: scope.ProviderID(), ContextName: scope.ContextName(),
		ProjectID: projectID, ServiceID: serviceID, StartedAt: startedAt, Status: "running",
	}, nil
}

// PublishCheckRunInScope atomically publishes a complete generation if no
// equal-or-newer project/service head has already committed. accepted=false is
// a successful supersession outcome; callers must read and return current state.
func (r *UpdateRepository) PublishCheckRunInScope(ctx context.Context, scope runtimescope.Scope, runID int64, records []UpdateCheckRecord, completedAt time.Time) ([]UpdateCheckRecord, bool, error) {
	if !scope.Valid() {
		return nil, false, errors.New("runtime scope is required")
	}
	if runID <= 0 {
		return nil, false, errors.New("update check run ID is required")
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	} else {
		completedAt = completedAt.UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	run, err := scanUpdateCheckRun(tx.QueryRowContext(ctx, `
		SELECT id, provider_id, context_name, project_id, service_id,
			started_at, COALESCE(finished_at, ''), status
		FROM image_update_check_runs
		WHERE id = ? AND provider_id = ? AND context_name = ?
	`, runID, scope.ProviderID(), scope.ContextName()))
	if err != nil {
		return nil, false, err
	}
	if run.Status != "running" {
		return nil, false, nil
	}
	if err := validatePublishedChecks(scope, run, records); err != nil {
		return nil, false, err
	}
	headQuery := `
		SELECT COALESCE(MAX(last_run_id), 0)
		FROM image_update_check_heads
		WHERE provider_id = ? AND context_name = ? AND project_id = ?
	`
	headArgs := []any{scope.ProviderID(), scope.ContextName(), run.ProjectID}
	if run.ServiceID != "" {
		headQuery += ` AND service_id IN ('', ?)`
		headArgs = append(headArgs, run.ServiceID)
	}
	var lastPublishedRunID int64
	if err := tx.QueryRowContext(ctx, headQuery, headArgs...).Scan(&lastPublishedRunID); err != nil {
		return nil, false, err
	}
	if lastPublishedRunID >= run.ID {
		if _, err := tx.ExecContext(ctx, `
			UPDATE image_update_check_runs
			SET status = 'superseded', finished_at = ?
			WHERE id = ? AND status = 'running'
		`, formatTime(completedAt), run.ID); err != nil {
			return nil, false, err
		}
		if err := pruneUpdateCheckRuns(ctx, tx, scope, run.ProjectID); err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	demoteQuery := `
		UPDATE image_update_checks
		SET is_current = 0
		WHERE provider_id = ? AND context_name = ? AND COALESCE(project_id, '') = ? AND is_current = 1
	`
	demoteArgs := []any{scope.ProviderID(), scope.ContextName(), run.ProjectID}
	if run.ServiceID != "" {
		demoteQuery += ` AND COALESCE(service_id, '') = ?`
		demoteArgs = append(demoteArgs, run.ServiceID)
	}
	if _, err := tx.ExecContext(ctx, demoteQuery, demoteArgs...); err != nil {
		return nil, false, err
	}
	published := make([]UpdateCheckRecord, len(records))
	for i, record := range records {
		record.CheckedAt = completedAt
		record = normalizeUpdateCheck(record)
		id, err := insertCheck(ctx, tx, record, run.ID)
		if err != nil {
			return nil, false, err
		}
		record.ID = id
		record.GenerationID = run.ID
		record.IsCurrent = true
		published[i] = record
	}
	if run.ServiceID == "" {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM image_update_check_heads
			WHERE provider_id = ? AND context_name = ? AND project_id = ? AND service_id <> ''
		`, scope.ProviderID(), scope.ContextName(), run.ProjectID); err != nil {
			return nil, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO image_update_check_heads (
			provider_id, context_name, project_id, service_id, last_run_id
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(provider_id, context_name, project_id, service_id) DO UPDATE SET
			last_run_id = excluded.last_run_id
	`, scope.ProviderID(), scope.ContextName(), run.ProjectID, run.ServiceID, run.ID); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE image_update_check_runs
		SET status = 'published', finished_at = ?
		WHERE id = ? AND status = 'running'
	`, formatTime(completedAt), run.ID); err != nil {
		return nil, false, err
	}
	if err := pruneUpdateCheckHistory(ctx, tx, scope, run.ProjectID, completedAt); err != nil {
		return nil, false, err
	}
	if err := pruneUpdateCheckRuns(ctx, tx, scope, run.ProjectID); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return published, true, nil
}

func validatePublishedChecks(scope runtimescope.Scope, run UpdateCheckRun, records []UpdateCheckRecord) error {
	for _, record := range records {
		if !scope.Matches(record.ProviderID, record.ContextName) {
			return errors.New("update check does not belong to the runtime scope")
		}
		if record.ProjectID != run.ProjectID {
			return errors.New("update check does not belong to the project")
		}
		if strings.TrimSpace(record.ServiceID) == "" {
			return errors.New("update check service ID is required")
		}
		if run.ServiceID != "" && record.ServiceID != run.ServiceID {
			return errors.New("update check does not belong to the service")
		}
	}
	return nil
}

func (r *UpdateRepository) AbandonCheckRunInScope(ctx context.Context, scope runtimescope.Scope, runID int64, finishedAt time.Time) error {
	if !scope.Valid() {
		return errors.New("runtime scope is required")
	}
	if runID <= 0 {
		return errors.New("update check run ID is required")
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var projectID string
	err = tx.QueryRowContext(ctx, `
		SELECT project_id FROM image_update_check_runs
		WHERE id = ? AND provider_id = ? AND context_name = ?
	`, runID, scope.ProviderID(), scope.ContextName()).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE image_update_check_runs
		SET status = 'abandoned', finished_at = ?
		WHERE id = ? AND status = 'running'
	`, formatTime(finishedAt), runID); err != nil {
		return err
	}
	if err := pruneUpdateCheckRuns(ctx, tx, scope, projectID); err != nil {
		return err
	}
	return tx.Commit()
}

func scanUpdateCheckRun(scanner interface{ Scan(...any) error }) (UpdateCheckRun, error) {
	var run UpdateCheckRun
	var startedAt string
	var finishedAt string
	if err := scanner.Scan(
		&run.ID, &run.ProviderID, &run.ContextName, &run.ProjectID, &run.ServiceID,
		&startedAt, &finishedAt, &run.Status,
	); err != nil {
		return UpdateCheckRun{}, err
	}
	run.StartedAt = parseStoreTime(startedAt)
	run.FinishedAt = parseStoreTime(finishedAt)
	return run, nil
}

func pruneUpdateCheckHistory(ctx context.Context, tx *sql.Tx, scope runtimescope.Scope, projectID string, completedAt time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM image_update_checks
		WHERE provider_id = ? AND context_name = ? AND COALESCE(project_id, '') = ?
			AND is_current = 0 AND julianday(checked_at) < julianday(?)
	`, scope.ProviderID(), scope.ContextName(), projectID,
		formatTime(completedAt.Add(-updateCheckHistoryRetention))); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		DELETE FROM image_update_checks
		WHERE provider_id = ? AND context_name = ? AND COALESCE(project_id, '') = ?
			AND is_current = 0
			AND generation_id NOT IN (
				SELECT generation_id
				FROM image_update_checks
				WHERE provider_id = ? AND context_name = ? AND COALESCE(project_id, '') = ?
					AND is_current = 0
				GROUP BY generation_id
				ORDER BY generation_id DESC
				LIMIT ?
			)
	`, scope.ProviderID(), scope.ContextName(), projectID,
		scope.ProviderID(), scope.ContextName(), projectID, updateCheckHistoryKeepGenerations)
	return err
}

func pruneUpdateCheckRuns(ctx context.Context, tx *sql.Tx, scope runtimescope.Scope, projectID string) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM image_update_check_runs AS candidate
		WHERE candidate.provider_id = ? AND candidate.context_name = ? AND candidate.project_id = ?
			AND candidate.status <> 'running'
			AND NOT EXISTS (
				SELECT 1 FROM image_update_check_heads
				WHERE last_run_id = candidate.id
			)
			AND NOT EXISTS (
				SELECT 1 FROM image_update_checks
				WHERE generation_id = candidate.id
					AND provider_id = candidate.provider_id
					AND context_name = candidate.context_name
					AND COALESCE(project_id, '') = candidate.project_id
			)
	`, scope.ProviderID(), scope.ContextName(), projectID)
	return err
}

func (r *UpdateRepository) GetCheck(ctx context.Context, id int64) (UpdateCheckRecord, error) {
	row := r.db.QueryRowContext(ctx, updateCheckSelectSQL()+` WHERE id = ?`, id)
	return scanUpdateCheck(row)
}

func (r *UpdateRepository) GetCheckInScope(ctx context.Context, scope runtimescope.Scope, id int64) (UpdateCheckRecord, error) {
	if !scope.Valid() {
		return UpdateCheckRecord{}, errors.New("runtime scope is required")
	}
	row := r.db.QueryRowContext(ctx, updateCheckSelectSQL()+` WHERE id = ? AND provider_id = ? AND context_name = ?`, id, scope.ProviderID(), scope.ContextName())
	return scanUpdateCheck(row)
}

func (r *UpdateRepository) ListCurrent(ctx context.Context, filter models.UpdateFilter) ([]UpdateCheckRecord, error) {
	return r.listCurrent(ctx, runtimescope.Scope{}, filter)
}

func (r *UpdateRepository) ListCurrentInScope(ctx context.Context, scope runtimescope.Scope, filter models.UpdateFilter) ([]UpdateCheckRecord, error) {
	if !scope.Valid() {
		return nil, errors.New("runtime scope is required")
	}
	return r.listCurrent(ctx, scope, filter)
}

func (r *UpdateRepository) ListCurrentServiceChecksInScope(ctx context.Context, scope runtimescope.Scope, projectID string, serviceID string) ([]UpdateCheckRecord, error) {
	if !scope.Valid() {
		return nil, errors.New("runtime scope is required")
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return nil, errors.New("service ID is required")
	}
	records, err := r.listLatestChecks(ctx, scope, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	result := make([]UpdateCheckRecord, 0, len(records))
	for _, record := range records {
		if record.ServiceID == serviceID {
			result = append(result, record)
		}
	}
	return result, nil
}

func (r *UpdateRepository) listCurrent(ctx context.Context, scope runtimescope.Scope, filter models.UpdateFilter) ([]UpdateCheckRecord, error) {
	records, err := r.listLatestChecks(ctx, scope, filter.ProjectID)
	if err != nil {
		return nil, err
	}
	ignored, err := r.listIgnored(ctx, scope)
	if err != nil {
		return nil, err
	}
	statuses := updateStatusSet(filter.Status)
	kinds := updateKindSet(filter.Kind)
	filteringIgnored := len(statuses) > 0 && statuses[models.UpdateStatusIgnored]
	result := make([]UpdateCheckRecord, 0, len(records))
	for _, record := range records {
		if len(kinds) > 0 && !kinds[record.Kind] {
			continue
		}
		if ignoreID, ok := matchingIgnore(ignored, record); ok {
			record.ID = ignoreID
			record.Status = models.UpdateStatusIgnored
			record.RecommendedAction = models.RecommendedActionNone
		}
		if len(statuses) == 0 {
			if record.Status == models.UpdateStatusIgnored {
				continue
			}
		} else if !statuses[record.Status] {
			continue
		}
		if record.Status == models.UpdateStatusIgnored && !filteringIgnored && len(statuses) > 0 {
			continue
		}
		result = append(result, record)
	}
	return result, nil
}

func (r *UpdateRepository) Badges(ctx context.Context, projectID string) (models.UpdateBadges, error) {
	badgesByProject, err := r.BadgesByProjectIDs(ctx, []string{projectID})
	if err != nil {
		return models.UpdateBadges{}, err
	}
	return badgesByProject[projectID], nil
}

func (r *UpdateRepository) BadgesByProjectIDs(ctx context.Context, projectIDs []string) (map[string]models.UpdateBadges, error) {
	return r.badgesByProjectIDs(ctx, runtimescope.Scope{}, projectIDs)
}

func (r *UpdateRepository) BadgesByProjectIDsInScope(ctx context.Context, scope runtimescope.Scope, projectIDs []string) (map[string]models.UpdateBadges, error) {
	if !scope.Valid() {
		return nil, errors.New("runtime scope is required")
	}
	return r.badgesByProjectIDs(ctx, scope, projectIDs)
}

func (r *UpdateRepository) badgesByProjectIDs(ctx context.Context, scope runtimescope.Scope, projectIDs []string) (map[string]models.UpdateBadges, error) {
	badgesByProject := make(map[string]models.UpdateBadges, len(projectIDs))
	if len(projectIDs) == 0 {
		return badgesByProject, nil
	}
	wanted := make(map[string]struct{}, len(projectIDs))
	for _, projectID := range projectIDs {
		projectID = strings.TrimSpace(projectID)
		if projectID == "" {
			continue
		}
		wanted[projectID] = struct{}{}
		badgesByProject[projectID] = models.UpdateBadges{}
	}
	if len(wanted) == 0 {
		return badgesByProject, nil
	}
	allowed := wanted
	if scope.Valid() {
		allowed = make(map[string]struct{}, len(wanted))
		args := []any{scope.ProviderID(), scope.ContextName()}
		placeholders := make([]string, 0, len(wanted))
		for projectID := range wanted {
			placeholders = append(placeholders, "?")
			args = append(args, projectID)
		}
		rows, err := r.db.QueryContext(ctx, `
			SELECT id FROM projects
			WHERE provider_id = ? AND context_name = ?
				AND id IN (`+strings.Join(placeholders, ",")+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var projectID string
			if err := rows.Scan(&projectID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			allowed[projectID] = struct{}{}
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if rowsErr != nil {
			return nil, rowsErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	records, err := r.listLatestChecks(ctx, scope, "")
	if err != nil {
		return nil, err
	}
	ignored, err := r.listIgnored(ctx, scope)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if scope.Valid() && !scope.Matches(record.ProviderID, record.ContextName) {
			continue
		}
		if _, ok := allowed[record.ProjectID]; !ok {
			continue
		}
		if _, ok := matchingIgnore(ignored, record); ok {
			continue
		}
		badges := badgesByProject[record.ProjectID]
		switch record.Status {
		case models.UpdateStatusServiceImageUpdateAvailable:
			badges.ImageUpdates++
		case models.UpdateStatusBaseImageUpdateAvailable:
			badges.BaseUpdates++
		case models.UpdateStatusRebuildRequired:
			badges.RebuildNeeded++
		case models.UpdateStatusPinnedDigest:
			badges.Pinned++
		case models.UpdateStatusUnknownBaseImage:
			badges.UnknownBase++
		}
		badgesByProject[record.ProjectID] = badges
	}
	return badgesByProject, nil
}

func (r *UpdateRepository) IgnoreCheck(ctx context.Context, id int64, reason string, createdAt time.Time) error {
	return r.ignoreCheck(ctx, runtimescope.Scope{}, id, reason, createdAt)
}

func (r *UpdateRepository) IgnoreCheckInScope(ctx context.Context, scope runtimescope.Scope, id int64, reason string, createdAt time.Time) error {
	if !scope.Valid() {
		return errors.New("runtime scope is required")
	}
	return r.ignoreCheck(ctx, scope, id, reason, createdAt)
}

func (r *UpdateRepository) ignoreCheck(ctx context.Context, scope runtimescope.Scope, id int64, reason string, createdAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	query := updateCheckSelectSQL() + ` WHERE id = ?`
	args := []any{id}
	if scope.Valid() {
		query += ` AND provider_id = ? AND context_name = ?`
		args = append(args, scope.ProviderID(), scope.ContextName())
	}
	check, err := scanUpdateCheck(tx.QueryRowContext(ctx, query, args...))
	if err != nil {
		return err
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if err := upsertIgnoredUpdate(ctx, tx, check, reason, createdAt); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertIgnoredUpdate(ctx context.Context, tx *sql.Tx, check UpdateCheckRecord, reason string, createdAt time.Time) error {
	args := []any{
		check.ProviderID,
		check.ContextName,
		check.ImageRef,
		string(check.Kind),
		check.BaseImageRef,
		check.ProjectID,
		check.ServiceID,
	}
	update := func() (int64, error) {
		result, err := tx.ExecContext(ctx, `
			UPDATE ignored_updates
			SET reason = NULLIF(?, ''), created_at = ?
			WHERE provider_id = ?
				AND context_name = ?
				AND image_ref = ?
				AND update_kind = ?
				AND COALESCE(base_image_ref, '') = ?
				AND COALESCE(project_id, '') = ?
				AND COALESCE(service_id, '') = ?
		`, append([]any{reason, formatTime(createdAt)}, args...)...)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}
	if rows, err := update(); err != nil || rows > 0 {
		return err
	}
	_, insertErr := tx.ExecContext(ctx, `
		INSERT INTO ignored_updates (
			provider_id, context_name, image_ref, update_kind, base_image_ref, project_id,
			service_id, reason, created_at
		)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), ?)
	`, append(args, reason, formatTime(createdAt))...)
	if insertErr == nil {
		return nil
	}
	if rows, err := update(); err == nil && rows > 0 {
		return nil
	}
	return insertErr
}

func (r *UpdateRepository) Unignore(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM ignored_updates WHERE id = ?`, id)
	return err
}

func (r *UpdateRepository) UnignoreInScope(ctx context.Context, scope runtimescope.Scope, id int64) error {
	if !scope.Valid() {
		return errors.New("runtime scope is required")
	}
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM ignored_updates
		WHERE id = ? AND provider_id = ? AND context_name = ?
	`, id, scope.ProviderID(), scope.ContextName())
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

func (r *UpdateRepository) GetIgnored(ctx context.Context, id int64) (IgnoredUpdateRecord, error) {
	var record IgnoredUpdateRecord
	var createdAt string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, provider_id, context_name, image_ref, update_kind, COALESCE(base_image_ref, ''),
			COALESCE(project_id, ''), COALESCE(service_id, ''), COALESCE(reason, ''), created_at
		FROM ignored_updates
		WHERE id = ?
	`, id).Scan(&record.ID, &record.ProviderID, &record.ContextName, &record.ImageRef, &record.UpdateKind, &record.BaseImageRef, &record.ProjectID, &record.ServiceID, &record.Reason, &createdAt)
	if err != nil {
		return IgnoredUpdateRecord{}, err
	}
	record.CreatedAt = parseStoreTime(createdAt)
	return record, nil
}

func (r *UpdateRepository) GetIgnoredInScope(ctx context.Context, scope runtimescope.Scope, id int64) (IgnoredUpdateRecord, error) {
	if !scope.Valid() {
		return IgnoredUpdateRecord{}, errors.New("runtime scope is required")
	}
	var record IgnoredUpdateRecord
	var createdAt string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, provider_id, context_name, image_ref, update_kind, COALESCE(base_image_ref, ''),
			COALESCE(project_id, ''), COALESCE(service_id, ''), COALESCE(reason, ''), created_at
		FROM ignored_updates
		WHERE id = ? AND provider_id = ? AND context_name = ?
	`, id, scope.ProviderID(), scope.ContextName()).Scan(&record.ID, &record.ProviderID, &record.ContextName, &record.ImageRef, &record.UpdateKind, &record.BaseImageRef, &record.ProjectID, &record.ServiceID, &record.Reason, &createdAt)
	if err != nil {
		return IgnoredUpdateRecord{}, err
	}
	record.CreatedAt = parseStoreTime(createdAt)
	return record, nil
}

func (r *UpdateRepository) InsertHistory(ctx context.Context, record UpdateHistoryRecord) (int64, error) {
	return r.insertHistory(ctx, record)
}

func (r *UpdateRepository) InsertHistoryInScope(ctx context.Context, scope runtimescope.Scope, record UpdateHistoryRecord) (int64, error) {
	if !scope.Valid() {
		return 0, errors.New("runtime scope is required")
	}
	if !scope.Matches(record.ProviderID, record.ContextName) {
		return 0, errors.New("update history does not belong to the runtime scope")
	}
	return r.insertHistory(ctx, record)
}

func (r *UpdateRepository) insertHistory(ctx context.Context, record UpdateHistoryRecord) (int64, error) {
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now().UTC()
	}
	if record.Result == "" {
		record.Result = "started"
	}
	commands := "[]"
	if len(record.Commands) > 0 {
		commands = jsonText(record.Commands, "[]")
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO update_history (
			provider_id, context_name, project_id, service_id, update_kind, image_ref, base_image_ref,
			old_image_id, old_digest, old_base_digest, new_image_id, new_digest,
			new_base_digest, dockerfile_hash, build_args_json, commands_json,
			result, health_result, rollback_status, started_at, finished_at, error
		)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), '{}', NULLIF(?, '[]'),
			?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''))
	`, record.ProviderID, record.ContextName, record.ProjectID, record.ServiceID, string(record.UpdateKind),
		record.ImageRef, record.BaseImageRef, record.OldImageID, record.OldDigest,
		record.OldBaseDigest, record.NewImageID, record.NewDigest, record.NewBaseDigest,
		record.DockerfileHash, commands, record.Result, record.HealthResult,
		record.RollbackStatus, formatTime(record.StartedAt), formatTime(record.FinishedAt),
		record.Error)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *UpdateRepository) FinishHistory(ctx context.Context, id int64, record UpdateHistoryRecord) error {
	return r.finishHistory(ctx, runtimescope.Scope{}, id, record)
}

func (r *UpdateRepository) FinishHistoryInScope(ctx context.Context, scope runtimescope.Scope, id int64, record UpdateHistoryRecord) error {
	if !scope.Valid() {
		return errors.New("runtime scope is required")
	}
	return r.finishHistory(ctx, scope, id, record)
}

func (r *UpdateRepository) finishHistory(ctx context.Context, scope runtimescope.Scope, id int64, record UpdateHistoryRecord) error {
	if record.FinishedAt.IsZero() {
		record.FinishedAt = time.Now().UTC()
	}
	query := `
		UPDATE update_history
		SET new_image_id = COALESCE(NULLIF(?, ''), new_image_id),
			new_digest = COALESCE(NULLIF(?, ''), new_digest),
			new_base_digest = COALESCE(NULLIF(?, ''), new_base_digest),
			result = ?,
			health_result = NULLIF(?, ''),
			rollback_status = NULLIF(?, ''),
			finished_at = ?,
			error = NULLIF(?, '')
		WHERE id = ?`
	args := []any{record.NewImageID, record.NewDigest, record.NewBaseDigest, record.Result,
		record.HealthResult, record.RollbackStatus, formatTime(record.FinishedAt),
		record.Error, id}
	if scope.Valid() {
		query += ` AND provider_id = ? AND context_name = ?`
		args = append(args, scope.ProviderID(), scope.ContextName())
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if scope.Valid() {
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return sql.ErrNoRows
		}
	}
	return nil
}

func (r *UpdateRepository) GetHistory(ctx context.Context, id int64) (UpdateHistoryRecord, error) {
	return r.getHistory(ctx, runtimescope.Scope{}, id)
}

func (r *UpdateRepository) GetHistoryInScope(ctx context.Context, scope runtimescope.Scope, id int64) (UpdateHistoryRecord, error) {
	if !scope.Valid() {
		return UpdateHistoryRecord{}, errors.New("runtime scope is required")
	}
	return r.getHistory(ctx, scope, id)
}

func (r *UpdateRepository) getHistory(ctx context.Context, scope runtimescope.Scope, id int64) (UpdateHistoryRecord, error) {
	query := `
		SELECT id, provider_id, context_name, COALESCE(project_id, ''), COALESCE(service_id, ''),
			update_kind, image_ref, COALESCE(base_image_ref, ''),
			COALESCE(old_image_id, ''), COALESCE(old_digest, ''),
			COALESCE(old_base_digest, ''), COALESCE(new_image_id, ''),
			COALESCE(new_digest, ''), COALESCE(new_base_digest, ''),
			COALESCE(dockerfile_hash, ''), '{}' AS build_args_json,
			COALESCE(commands_json, '[]'), result, COALESCE(health_result, ''),
			started_at, COALESCE(finished_at, ''), COALESCE(rollback_status, ''),
			COALESCE(error, '')
		FROM update_history
		WHERE id = ?`
	args := []any{id}
	if scope.Valid() {
		query += ` AND provider_id = ? AND context_name = ?`
		args = append(args, scope.ProviderID(), scope.ContextName())
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	return scanUpdateHistory(row)
}

func (r *UpdateRepository) ListHistory(ctx context.Context, filter models.UpdateHistoryFilter) ([]UpdateHistoryRecord, error) {
	return r.listHistory(ctx, runtimescope.Scope{}, filter)
}

func (r *UpdateRepository) ListHistoryInScope(ctx context.Context, scope runtimescope.Scope, filter models.UpdateHistoryFilter) ([]UpdateHistoryRecord, error) {
	if !scope.Valid() {
		return nil, errors.New("runtime scope is required")
	}
	return r.listHistory(ctx, scope, filter)
}

func (r *UpdateRepository) listHistory(ctx context.Context, scope runtimescope.Scope, filter models.UpdateHistoryFilter) ([]UpdateHistoryRecord, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `
		SELECT id, provider_id, context_name, COALESCE(project_id, ''), COALESCE(service_id, ''),
			update_kind, image_ref, COALESCE(base_image_ref, ''), result,
			started_at, COALESCE(finished_at, ''), COALESCE(rollback_status, ''),
			COALESCE(error, '')
		FROM update_history
		WHERE `
	args := make([]any, 0, 9)
	if scope.Valid() {
		query += `provider_id = ? AND context_name = ?
		  AND project_id IN (
			SELECT id FROM projects WHERE provider_id = ? AND context_name = ?
		  )
		  AND `
		args = append(args, scope.ProviderID(), scope.ContextName(), scope.ProviderID(), scope.ContextName())
	}
	query += `(? = '' OR project_id = ?)
		  AND (? = '' OR service_id = ?)
		ORDER BY started_at DESC, id DESC
		LIMIT ?
	`
	args = append(args, filter.ProjectID, filter.ProjectID, filter.Service, filter.Service, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	records := []UpdateHistoryRecord{}
	for rows.Next() {
		var record UpdateHistoryRecord
		var startedAt string
		var finishedAt string
		if err := rows.Scan(
			&record.ID,
			&record.ProviderID,
			&record.ContextName,
			&record.ProjectID,
			&record.ServiceID,
			&record.UpdateKind,
			&record.ImageRef,
			&record.BaseImageRef,
			&record.Result,
			&startedAt,
			&finishedAt,
			&record.RollbackStatus,
			&record.Error,
		); err != nil {
			return nil, err
		}
		record.StartedAt = parseStoreTime(startedAt)
		record.FinishedAt = parseStoreTime(finishedAt)
		records = append(records, record)
	}
	return records, rows.Err()
}

func scanUpdateHistory(scanner updateHistoryScanner) (UpdateHistoryRecord, error) {
	var record UpdateHistoryRecord
	var startedAt string
	var finishedAt string
	ignoredBuildArgsJSON := "{}"
	commandsJSON := "[]"
	if err := scanner.Scan(
		&record.ID,
		&record.ProviderID,
		&record.ContextName,
		&record.ProjectID,
		&record.ServiceID,
		&record.UpdateKind,
		&record.ImageRef,
		&record.BaseImageRef,
		&record.OldImageID,
		&record.OldDigest,
		&record.OldBaseDigest,
		&record.NewImageID,
		&record.NewDigest,
		&record.NewBaseDigest,
		&record.DockerfileHash,
		&ignoredBuildArgsJSON,
		&commandsJSON,
		&record.Result,
		&record.HealthResult,
		&startedAt,
		&finishedAt,
		&record.RollbackStatus,
		&record.Error,
	); err != nil {
		return UpdateHistoryRecord{}, err
	}
	record.StartedAt = parseStoreTime(startedAt)
	record.FinishedAt = parseStoreTime(finishedAt)
	// Legacy build-argument data is never restored into rollback/history state.
	record.BuildArgs = nil
	if err := json.Unmarshal([]byte(nullJSON(commandsJSON, "[]")), &record.Commands); err != nil {
		return UpdateHistoryRecord{}, err
	}
	return record, nil
}

type updateHistoryScanner interface {
	Scan(dest ...any) error
}

func (r *UpdateRepository) listLatestChecks(ctx context.Context, scope runtimescope.Scope, projectID string) ([]UpdateCheckRecord, error) {
	args := []any{}
	query := updateCheckSelectSQL()
	conditions := []string{`is_current = 1`}
	if scope.Valid() {
		conditions = append(conditions, `provider_id = ? AND context_name = ?`)
		args = append(args, scope.ProviderID(), scope.ContextName())
	}
	if projectID != "" {
		conditions = append(conditions, `COALESCE(project_id, '') = ?`)
		args = append(args, projectID)
	}
	query += `
		WHERE ` + strings.Join(conditions, ` AND `) + `
		ORDER BY COALESCE(project_id, ''), COALESCE(service_id, ''), kind, image_ref, COALESCE(base_image_ref, '')
	`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	records := []UpdateCheckRecord{}
	for rows.Next() {
		record, err := scanUpdateCheck(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *UpdateRepository) listIgnored(ctx context.Context, scope runtimescope.Scope) ([]IgnoredUpdateRecord, error) {
	query := `
		SELECT id, provider_id, context_name, image_ref, update_kind, COALESCE(base_image_ref, ''),
			COALESCE(project_id, ''), COALESCE(service_id, ''), COALESCE(reason, ''),
			created_at
		FROM ignored_updates
	`
	args := []any{}
	if scope.Valid() {
		query += ` WHERE provider_id = ? AND context_name = ?`
		args = append(args, scope.ProviderID(), scope.ContextName())
	}
	query += `
		ORDER BY created_at DESC, id DESC
	`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	records := []IgnoredUpdateRecord{}
	for rows.Next() {
		var record IgnoredUpdateRecord
		var createdAt string
		if err := rows.Scan(
			&record.ID,
			&record.ProviderID,
			&record.ContextName,
			&record.ImageRef,
			&record.UpdateKind,
			&record.BaseImageRef,
			&record.ProjectID,
			&record.ServiceID,
			&record.Reason,
			&createdAt,
		); err != nil {
			return nil, err
		}
		record.CreatedAt = parseStoreTime(createdAt)
		records = append(records, record)
	}
	return records, rows.Err()
}

func updateCheckSelectSQL() string {
	return `
		SELECT id, generation_id, is_current, provider_id, context_name, COALESCE(project_id, ''), COALESCE(service_id, ''),
			COALESCE(container_id, ''), kind, image_ref, COALESCE(base_image_ref, ''),
			COALESCE(local_image_id, ''), COALESCE(local_digest, ''),
			COALESCE(remote_digest, ''), COALESCE(lineage_id, 0),
			COALESCE(base_image_ref_id, 0), COALESCE(confidence, ''),
			COALESCE(recommended_action, ''), status, checked_at, COALESCE(error, '')
		FROM image_update_checks
	`
}

type updateCheckScanner interface {
	Scan(dest ...any) error
}

func scanUpdateCheck(scanner updateCheckScanner) (UpdateCheckRecord, error) {
	var record UpdateCheckRecord
	var checkedAt string
	var isCurrent int
	if err := scanner.Scan(
		&record.ID,
		&record.GenerationID,
		&isCurrent,
		&record.ProviderID,
		&record.ContextName,
		&record.ProjectID,
		&record.ServiceID,
		&record.ContainerID,
		&record.Kind,
		&record.ImageRef,
		&record.BaseImageRef,
		&record.LocalImageID,
		&record.LocalDigest,
		&record.RemoteDigest,
		&record.LineageID,
		&record.BaseImageRefID,
		&record.Confidence,
		&record.RecommendedAction,
		&record.Status,
		&checkedAt,
		&record.Error,
	); err != nil {
		return UpdateCheckRecord{}, err
	}
	record.IsCurrent = isCurrent == 1
	record.CheckedAt = parseStoreTime(checkedAt)
	if record.Confidence == "" {
		record.Confidence = models.ConfidenceUnknown
	}
	if record.RecommendedAction == "" {
		record.RecommendedAction = models.RecommendedActionNone
	}
	return record, nil
}

func (r *LineageRepository) UpdateBaseRefCheck(ctx context.Context, id int64, localDigest string, remoteDigest string, status models.UpdateStatus, checkedAt time.Time, checkErr string) error {
	return r.updateBaseRefCheck(ctx, runtimescope.Scope{}, id, localDigest, remoteDigest, status, checkedAt, checkErr)
}

func (r *LineageRepository) UpdateBaseRefCheckInScope(ctx context.Context, scope runtimescope.Scope, id int64, localDigest string, remoteDigest string, status models.UpdateStatus, checkedAt time.Time, checkErr string) error {
	if !scope.Valid() {
		return errors.New("runtime scope is required")
	}
	return r.updateBaseRefCheck(ctx, scope, id, localDigest, remoteDigest, status, checkedAt, checkErr)
}

func (r *LineageRepository) updateBaseRefCheck(ctx context.Context, scope runtimescope.Scope, id int64, localDigest string, remoteDigest string, status models.UpdateStatus, checkedAt time.Time, checkErr string) error {
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	if status == "" {
		status = models.UpdateStatusUnknown
	}
	query := `
		UPDATE base_image_refs
		SET local_digest = NULLIF(?, ''),
			remote_digest = NULLIF(?, ''),
			status = ?,
			last_checked_at = ?,
			error = NULLIF(?, '')
		WHERE id = ?`
	args := []any{localDigest, remoteDigest, string(status), formatTime(checkedAt), checkErr, id}
	if scope.Valid() {
		query += ` AND lineage_id IN (
			SELECT id FROM image_lineage WHERE provider_id = ? AND context_name = ?
		)`
		args = append(args, scope.ProviderID(), scope.ContextName())
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if scope.Valid() {
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return sql.ErrNoRows
		}
	}
	return nil
}

func (r UpdateCheckRecord) ToModel() models.ImageUpdate {
	notes := []string{}
	if r.Error != "" {
		notes = append(notes, r.Error)
	}
	if isLatestTag(r.ImageRef) {
		notes = append(notes, "Mutable tag 'latest' can change without a versioned release; review before applying.")
	}
	return models.ImageUpdate{
		ID:                r.ID,
		ProjectID:         r.ProjectID,
		Service:           serviceNameFromID(r.ServiceID, r.ProjectID),
		ContainerID:       r.ContainerID,
		Kind:              r.Kind,
		Status:            r.Status,
		CurrentImage:      r.ImageRef,
		BaseImage:         r.BaseImageRef,
		LocalDigest:       r.LocalDigest,
		RemoteDigest:      r.RemoteDigest,
		Confidence:        r.Confidence,
		RecommendedAction: r.RecommendedAction,
		CheckedAt:         r.CheckedAt,
		Notes:             notes,
	}
}

func (r UpdateHistoryRecord) ToModel() models.UpdateHistoryItem {
	return models.UpdateHistoryItem{
		ID:             r.ID,
		ProjectID:      r.ProjectID,
		Service:        serviceNameFromID(r.ServiceID, r.ProjectID),
		Kind:           r.UpdateKind,
		Result:         r.Result,
		StartedAt:      r.StartedAt,
		FinishedAt:     r.FinishedAt,
		RollbackStatus: r.RollbackStatus,
		Error:          r.Error,
	}
}

func updateStatusSet(values []models.UpdateStatus) map[models.UpdateStatus]bool {
	result := map[models.UpdateStatus]bool{}
	for _, value := range values {
		if value != "" {
			result[value] = true
		}
	}
	return result
}

func updateKindSet(values []models.UpdateKind) map[models.UpdateKind]bool {
	result := map[models.UpdateKind]bool{}
	for _, value := range values {
		if value != "" {
			result[value] = true
		}
	}
	return result
}

func matchingIgnore(rules []IgnoredUpdateRecord, check UpdateCheckRecord) (int64, bool) {
	for _, rule := range rules {
		if ignoreRuleMatches(rule, check) {
			return rule.ID, true
		}
	}
	return 0, false
}

func ignoreRuleMatches(rule IgnoredUpdateRecord, check UpdateCheckRecord) bool {
	if rule.ProviderID != check.ProviderID || rule.ContextName != check.ContextName || rule.UpdateKind != check.Kind || rule.ImageRef != check.ImageRef {
		return false
	}
	if rule.BaseImageRef != check.BaseImageRef {
		return false
	}
	if rule.ProjectID != "" && rule.ProjectID != check.ProjectID {
		return false
	}
	if rule.ServiceID != "" && rule.ServiceID != check.ServiceID {
		return false
	}
	return true
}

func serviceNameFromID(serviceID string, projectID string) string {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ""
	}
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		if service, ok := strings.CutPrefix(serviceID, projectID+"/"); ok {
			return service
		}
	}
	if idx := strings.LastIndex(serviceID, "/"); idx >= 0 && idx < len(serviceID)-1 {
		return serviceID[idx+1:]
	}
	return serviceID
}

func isLatestTag(imageRef string) bool {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" || strings.Contains(imageRef, "@") {
		return false
	}
	lastSlash := strings.LastIndex(imageRef, "/")
	lastColon := strings.LastIndex(imageRef, ":")
	if lastColon <= lastSlash {
		return true
	}
	return strings.EqualFold(imageRef[lastColon+1:], "latest")
}
