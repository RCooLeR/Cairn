package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

type UpdateRepository struct {
	db *sql.DB
}

type UpdateCheckRecord struct {
	ID                int64
	ProviderID        string
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

type IgnoredUpdateRecord struct {
	ID           int64
	ProviderID   string
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

func (r *UpdateRepository) InsertCheck(ctx context.Context, record UpdateCheckRecord) (int64, error) {
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
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO image_update_checks (
			provider_id, project_id, service_id, container_id, kind, image_ref,
			base_image_ref, local_image_id, local_digest, remote_digest,
			lineage_id, base_image_ref_id, confidence, recommended_action,
			status, checked_at, error
		)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?,
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, ''), NULLIF(?, ''),
			?, ?, NULLIF(?, ''))
	`, record.ProviderID, record.ProjectID, record.ServiceID, record.ContainerID,
		string(record.Kind), record.ImageRef, record.BaseImageRef, record.LocalImageID,
		record.LocalDigest, record.RemoteDigest, record.LineageID, record.BaseImageRefID,
		string(record.Confidence), string(record.RecommendedAction), string(record.Status),
		formatTime(record.CheckedAt), record.Error)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *UpdateRepository) InsertChecks(ctx context.Context, records []UpdateCheckRecord) error {
	for _, record := range records {
		if _, err := r.InsertCheck(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (r *UpdateRepository) GetCheck(ctx context.Context, id int64) (UpdateCheckRecord, error) {
	row := r.db.QueryRowContext(ctx, updateCheckSelectSQL()+` WHERE id = ?`, id)
	return scanUpdateCheck(row)
}

func (r *UpdateRepository) ListCurrent(ctx context.Context, filter models.UpdateFilter) ([]UpdateCheckRecord, error) {
	records, err := r.listLatestChecks(ctx, filter.ProjectID)
	if err != nil {
		return nil, err
	}
	ignored, err := r.listIgnored(ctx)
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
	records, err := r.listLatestChecks(ctx, projectID)
	if err != nil {
		return models.UpdateBadges{}, err
	}
	ignored, err := r.listIgnored(ctx)
	if err != nil {
		return models.UpdateBadges{}, err
	}
	var badges models.UpdateBadges
	for _, record := range records {
		if _, ok := matchingIgnore(ignored, record); ok {
			continue
		}
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
	}
	return badges, nil
}

func (r *UpdateRepository) IgnoreCheck(ctx context.Context, id int64, reason string, createdAt time.Time) error {
	check, err := r.GetCheck(ctx, id)
	if err != nil {
		return err
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	ignored, err := r.listIgnored(ctx)
	if err != nil {
		return err
	}
	for _, rule := range ignored {
		if ignoreRuleMatches(rule, check) {
			_, err := r.db.ExecContext(ctx, `
				UPDATE ignored_updates
				SET reason = NULLIF(?, '')
				WHERE id = ?
			`, reason, rule.ID)
			return err
		}
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO ignored_updates (
			provider_id, image_ref, update_kind, base_image_ref, project_id,
			service_id, reason, created_at
		)
		VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), ?)
	`, check.ProviderID, check.ImageRef, string(check.Kind), check.BaseImageRef,
		check.ProjectID, check.ServiceID, reason, formatTime(createdAt))
	return err
}

func (r *UpdateRepository) Unignore(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM ignored_updates WHERE id = ?`, id)
	return err
}

func (r *UpdateRepository) InsertHistory(ctx context.Context, record UpdateHistoryRecord) (int64, error) {
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now().UTC()
	}
	if record.Result == "" {
		record.Result = "started"
	}
	buildArgs := "{}"
	if len(record.BuildArgs) > 0 {
		buildArgs = jsonText(record.BuildArgs, "{}")
	}
	commands := "[]"
	if len(record.Commands) > 0 {
		commands = jsonText(record.Commands, "[]")
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO update_history (
			provider_id, project_id, service_id, update_kind, image_ref, base_image_ref,
			old_image_id, old_digest, old_base_digest, new_image_id, new_digest,
			new_base_digest, dockerfile_hash, build_args_json, commands_json,
			result, health_result, rollback_status, started_at, finished_at, error
		)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, '{}'), NULLIF(?, '[]'),
			?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''))
	`, record.ProviderID, record.ProjectID, record.ServiceID, string(record.UpdateKind),
		record.ImageRef, record.BaseImageRef, record.OldImageID, record.OldDigest,
		record.OldBaseDigest, record.NewImageID, record.NewDigest, record.NewBaseDigest,
		record.DockerfileHash, buildArgs, commands, record.Result, record.HealthResult,
		record.RollbackStatus, formatTime(record.StartedAt), formatTime(record.FinishedAt),
		record.Error)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *UpdateRepository) FinishHistory(ctx context.Context, id int64, record UpdateHistoryRecord) error {
	if record.FinishedAt.IsZero() {
		record.FinishedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE update_history
		SET new_image_id = NULLIF(?, ''),
			new_digest = NULLIF(?, ''),
			new_base_digest = NULLIF(?, ''),
			result = ?,
			health_result = NULLIF(?, ''),
			rollback_status = NULLIF(?, ''),
			finished_at = ?,
			error = NULLIF(?, '')
		WHERE id = ?
	`, record.NewImageID, record.NewDigest, record.NewBaseDigest, record.Result,
		record.HealthResult, record.RollbackStatus, formatTime(record.FinishedAt),
		record.Error, id)
	return err
}

func (r *UpdateRepository) GetHistory(ctx context.Context, id int64) (UpdateHistoryRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, provider_id, COALESCE(project_id, ''), COALESCE(service_id, ''),
			update_kind, image_ref, COALESCE(base_image_ref, ''),
			COALESCE(old_image_id, ''), COALESCE(old_digest, ''),
			COALESCE(old_base_digest, ''), COALESCE(new_image_id, ''),
			COALESCE(new_digest, ''), COALESCE(new_base_digest, ''),
			COALESCE(dockerfile_hash, ''), COALESCE(build_args_json, '{}'),
			COALESCE(commands_json, '[]'), result, COALESCE(health_result, ''),
			started_at, COALESCE(finished_at, ''), COALESCE(rollback_status, ''),
			COALESCE(error, '')
		FROM update_history
		WHERE id = ?
	`, id)
	return scanUpdateHistory(row)
}

func (r *UpdateRepository) ListHistory(ctx context.Context, filter models.UpdateHistoryFilter) ([]UpdateHistoryRecord, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, COALESCE(project_id, ''), COALESCE(service_id, ''),
			update_kind, image_ref, COALESCE(base_image_ref, ''), result,
			started_at, COALESCE(finished_at, ''), COALESCE(rollback_status, ''),
			COALESCE(error, '')
		FROM update_history
		WHERE (? = '' OR project_id = ?)
		  AND (? = '' OR service_id = ?)
		ORDER BY started_at DESC, id DESC
		LIMIT ?
	`, filter.ProjectID, filter.ProjectID, filter.Service, filter.Service, limit)
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
	buildArgsJSON := "{}"
	commandsJSON := "[]"
	if err := scanner.Scan(
		&record.ID,
		&record.ProviderID,
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
		&buildArgsJSON,
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
	if err := json.Unmarshal([]byte(nullJSON(buildArgsJSON, "{}")), &record.BuildArgs); err != nil {
		return UpdateHistoryRecord{}, err
	}
	if err := json.Unmarshal([]byte(nullJSON(commandsJSON, "[]")), &record.Commands); err != nil {
		return UpdateHistoryRecord{}, err
	}
	return record, nil
}

type updateHistoryScanner interface {
	Scan(dest ...any) error
}

func (r *UpdateRepository) listLatestChecks(ctx context.Context, projectID string) ([]UpdateCheckRecord, error) {
	rows, err := r.db.QueryContext(ctx, updateCheckSelectSQL()+`
		JOIN (
			SELECT MAX(id) AS latest_id
			FROM image_update_checks
			WHERE (? = '' OR project_id = ?)
			GROUP BY provider_id, COALESCE(project_id, ''), COALESCE(service_id, ''),
				COALESCE(container_id, ''), kind, image_ref, COALESCE(base_image_ref, '')
		) latest ON latest.latest_id = image_update_checks.id
		ORDER BY COALESCE(project_id, ''), COALESCE(service_id, ''), kind, image_ref, COALESCE(base_image_ref, '')
	`, projectID, projectID)
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

func (r *UpdateRepository) listIgnored(ctx context.Context) ([]IgnoredUpdateRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, image_ref, update_kind, COALESCE(base_image_ref, ''),
			COALESCE(project_id, ''), COALESCE(service_id, ''), COALESCE(reason, ''),
			created_at
		FROM ignored_updates
		ORDER BY created_at DESC, id DESC
	`)
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
		SELECT id, provider_id, COALESCE(project_id, ''), COALESCE(service_id, ''),
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
	if err := scanner.Scan(
		&record.ID,
		&record.ProviderID,
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
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	if status == "" {
		status = models.UpdateStatusUnknown
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE base_image_refs
		SET local_digest = NULLIF(?, ''),
			remote_digest = NULLIF(?, ''),
			status = ?,
			last_checked_at = ?,
			error = NULLIF(?, '')
		WHERE id = ?
	`, localDigest, remoteDigest, string(status), formatTime(checkedAt), checkErr, id)
	return err
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
		Service:           serviceNameFromID(r.ServiceID),
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
		Service:        serviceNameFromID(r.ServiceID),
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
	if rule.ProviderID != check.ProviderID || rule.UpdateKind != check.Kind || rule.ImageRef != check.ImageRef {
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

func serviceNameFromID(serviceID string) string {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ""
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
