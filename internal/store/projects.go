package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	ProjectSourceLabels    = "labels"
	ProjectSourceComposeLS = "compose_ls"
	ProjectSourceImported  = "imported"
)

type ProjectRepository struct {
	db *sql.DB
}

type ProjectRecord struct {
	ID           string
	ProviderID   string
	ContextName  string
	Name         string
	WorkingDir   string
	ComposeFiles []string
	Status       models.ProjectStatus
	Health       models.HealthStatus
	Source       string
	Pinned       bool
	LastSeenAt   time.Time
	Metadata     map[string]any
}

type ServiceRecord struct {
	ID              string
	ProjectID       string
	Name            string
	ImageRef        string
	BuildContext    string
	DockerfilePath  string
	BuildTarget     string
	Status          models.ProjectStatus
	Health          models.HealthStatus
	ReplicasRunning int
	ReplicasTotal   int
	Metadata        map[string]any
	LastSeenAt      time.Time
}

type forgottenProjectKey struct {
	contextName string
	name        string
	projectID   string
}

func (s *Store) Projects() *ProjectRepository {
	return &ProjectRepository{db: s.writer}
}

func (r *ProjectRepository) SaveSnapshot(ctx context.Context, providerID string, projects []ProjectRecord, services []ServiceRecord, seenAt time.Time, staleCutoff time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	forgotten, err := r.forgottenProjectKeys(ctx, tx, providerID)
	if err != nil {
		return err
	}
	if len(forgotten) > 0 {
		var skipped map[string]struct{}
		projects, skipped = filterForgottenProjects(projects, forgotten)
		services = filterForgottenServices(services, skipped)
	}

	replaceServices := services != nil
	serviceReplacementIDs := serviceReplacementProjectIDs(projects, services)
	for _, project := range projects {
		if project.LastSeenAt.IsZero() {
			project.LastSeenAt = seenAt
		}
		if project.Status == "" {
			project.Status = models.ProjectStatusUnknown
		}
		if project.Health == "" {
			project.Health = models.HealthStatusUnknown
		}
		if project.Source == "" {
			project.Source = ProjectSourceLabels
		}
		pinned := 0
		if project.Pinned {
			pinned = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO projects (
				id, provider_id, context_name, name, working_dir, compose_files_json,
				status, health, source, pinned, last_seen_at, metadata_json
			)
			VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				provider_id = excluded.provider_id,
				context_name = excluded.context_name,
				name = excluded.name,
				working_dir = excluded.working_dir,
				compose_files_json = excluded.compose_files_json,
				status = excluded.status,
				health = excluded.health,
				source = excluded.source,
				pinned = projects.pinned,
				last_seen_at = excluded.last_seen_at,
				metadata_json = excluded.metadata_json
		`, project.ID, project.ProviderID, project.ContextName, project.Name, project.WorkingDir,
			jsonText(project.ComposeFiles, "[]"), string(project.Status), string(project.Health),
			project.Source, pinned, formatTime(project.LastSeenAt), jsonText(project.Metadata, "{}")); err != nil {
			return err
		}
	}

	for _, projectID := range serviceReplacementIDs {
		if _, err := tx.ExecContext(ctx, "DELETE FROM services WHERE project_id = ?", projectID); err != nil {
			return err
		}
	}

	if !replaceServices {
		if !staleCutoff.IsZero() {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM projects
				WHERE provider_id = ?
					AND source <> ?
					AND last_seen_at < ?
			`, providerID, ProjectSourceImported, formatTime(staleCutoff)); err != nil {
				return err
			}
		}
		return tx.Commit()
	}

	for _, service := range services {
		if service.LastSeenAt.IsZero() {
			service.LastSeenAt = seenAt
		}
		if service.Status == "" {
			service.Status = models.ProjectStatusUnknown
		}
		if service.Health == "" {
			service.Health = models.HealthStatusUnknown
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO services (
				id, project_id, name, image_ref, build_context, dockerfile_path,
				build_target, status, health, replicas_running, replicas_total,
				metadata_json, last_seen_at
			)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
				NULLIF(?, ''), ?, ?, ?, ?, ?, ?)
		`, service.ID, service.ProjectID, service.Name, service.ImageRef, service.BuildContext,
			service.DockerfilePath, service.BuildTarget, string(service.Status), string(service.Health),
			service.ReplicasRunning, service.ReplicasTotal, jsonText(service.Metadata, "{}"),
			formatTime(service.LastSeenAt)); err != nil {
			return err
		}
	}

	if !staleCutoff.IsZero() {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM projects
			WHERE provider_id = ?
				AND source <> ?
				AND last_seen_at < ?
		`, providerID, ProjectSourceImported, formatTime(staleCutoff)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *ProjectRepository) forgottenProjectKeys(ctx context.Context, tx *sql.Tx, providerID string) (map[forgottenProjectKey]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT context_name, name, project_id
		FROM forgotten_projects
		WHERE provider_id = ?
	`, providerID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	keys := map[forgottenProjectKey]struct{}{}
	for rows.Next() {
		var key forgottenProjectKey
		if err := rows.Scan(&key.contextName, &key.name, &key.projectID); err != nil {
			return nil, err
		}
		key.contextName = strings.TrimSpace(key.contextName)
		key.name = strings.TrimSpace(key.name)
		key.projectID = strings.TrimSpace(key.projectID)
		keys[key] = struct{}{}
	}
	return keys, rows.Err()
}

func filterForgottenProjects(projects []ProjectRecord, forgotten map[forgottenProjectKey]struct{}) ([]ProjectRecord, map[string]struct{}) {
	if len(projects) == 0 || len(forgotten) == 0 {
		return projects, nil
	}
	filtered := make([]ProjectRecord, 0, len(projects))
	skipped := map[string]struct{}{}
	for _, project := range projects {
		if project.Source != ProjectSourceImported && isForgottenProject(project, forgotten) {
			if id := strings.TrimSpace(project.ID); id != "" {
				skipped[id] = struct{}{}
			}
			continue
		}
		filtered = append(filtered, project)
	}
	return filtered, skipped
}

func isForgottenProject(project ProjectRecord, forgotten map[forgottenProjectKey]struct{}) bool {
	key := forgottenProjectKey{
		contextName: strings.TrimSpace(project.ContextName),
		name:        strings.TrimSpace(project.Name),
		projectID:   strings.TrimSpace(project.ID),
	}
	if _, ok := forgotten[key]; ok {
		return true
	}
	if key.projectID == "" {
		return false
	}
	for forgottenKey := range forgotten {
		if forgottenKey.projectID == key.projectID {
			return true
		}
	}
	return false
}

func filterForgottenServices(services []ServiceRecord, skipped map[string]struct{}) []ServiceRecord {
	if len(services) == 0 || len(skipped) == 0 {
		return services
	}
	filtered := make([]ServiceRecord, 0, len(services))
	for _, service := range services {
		if _, ok := skipped[strings.TrimSpace(service.ProjectID)]; ok {
			continue
		}
		filtered = append(filtered, service)
	}
	return filtered
}

func serviceReplacementProjectIDs(projects []ProjectRecord, services []ServiceRecord) []string {
	if services == nil {
		return nil
	}
	if len(services) == 0 {
		ids := make([]string, 0, len(projects))
		for _, project := range projects {
			if id := strings.TrimSpace(project.ID); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(services))
	for _, service := range services {
		projectID := strings.TrimSpace(service.ProjectID)
		if projectID == "" {
			continue
		}
		if _, ok := seen[projectID]; ok {
			continue
		}
		seen[projectID] = struct{}{}
		ids = append(ids, projectID)
	}
	return ids
}

func (r *ProjectRepository) UpsertImported(ctx context.Context, record ProjectRecord) error {
	if record.Source == "" {
		record.Source = ProjectSourceImported
	}
	return r.SaveSnapshot(ctx, record.ProviderID, []ProjectRecord{record}, nil, record.LastSeenAt, time.Time{})
}

func (r *ProjectRepository) Forget(ctx context.Context, project ProjectRecord, forgottenAt time.Time) error {
	providerID := strings.TrimSpace(project.ProviderID)
	contextName := strings.TrimSpace(project.ContextName)
	name := strings.TrimSpace(project.Name)
	projectID := strings.TrimSpace(project.ID)
	if providerID == "" || name == "" {
		return nil
	}
	if forgottenAt.IsZero() {
		forgottenAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO forgotten_projects (
			provider_id, context_name, name, project_id, forgotten_at
		)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(provider_id, context_name, name) DO UPDATE SET
			project_id = excluded.project_id,
			forgotten_at = excluded.forgotten_at
	`, providerID, contextName, name, projectID, formatTime(forgottenAt))
	return err
}

func (r *ProjectRepository) Unforget(ctx context.Context, providerID string, contextName string, name string, projectID string) error {
	providerID = strings.TrimSpace(providerID)
	contextName = strings.TrimSpace(contextName)
	name = strings.TrimSpace(name)
	projectID = strings.TrimSpace(projectID)
	if providerID == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM forgotten_projects
		WHERE provider_id = ?
			AND (
				(context_name = ? AND name = ?)
				OR (? <> '' AND project_id = ?)
			)
	`, providerID, contextName, name, projectID, projectID)
	return err
}

func (r *ProjectRepository) Delete(ctx context.Context, projectID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, "DELETE FROM services WHERE project_id = ?", projectID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", projectID)
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
	return tx.Commit()
}

func (r *ProjectRepository) List(ctx context.Context) ([]ProjectRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, context_name, name, working_dir, compose_files_json,
			status, health, source, pinned, last_seen_at, metadata_json
		FROM projects
		ORDER BY pinned DESC, name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanProjectRows(rows)
}

func (r *ProjectRepository) ListByProvider(ctx context.Context, providerID string) ([]ProjectRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, context_name, name, working_dir, compose_files_json,
			status, health, source, pinned, last_seen_at, metadata_json
		FROM projects
		WHERE provider_id = ?
		ORDER BY pinned DESC, name ASC
	`, providerID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanProjectRows(rows)
}

func (r *ProjectRepository) ListByProviderContext(ctx context.Context, providerID string, contextName string) ([]ProjectRecord, error) {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return r.ListByProvider(ctx, providerID)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, context_name, name, working_dir, compose_files_json,
			status, health, source, pinned, last_seen_at, metadata_json
		FROM projects
		WHERE provider_id = ? AND context_name = ?
		ORDER BY pinned DESC, name ASC
	`, providerID, contextName)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanProjectRows(rows)
}

func (r *ProjectRepository) Get(ctx context.Context, projectID string) (ProjectRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, provider_id, context_name, name, working_dir, compose_files_json,
			status, health, source, pinned, last_seen_at, metadata_json
		FROM projects
		WHERE id = ?
	`, projectID)
	return scanProject(row)
}

func (r *ProjectRepository) ListImported(ctx context.Context, providerID string) ([]ProjectRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, context_name, name, working_dir, compose_files_json,
			status, health, source, pinned, last_seen_at, metadata_json
		FROM projects
		WHERE provider_id = ? AND source = ?
		ORDER BY name ASC
	`, providerID, ProjectSourceImported)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanProjectRows(rows)
}

func (r *ProjectRepository) ListImportedByProviderContext(ctx context.Context, providerID string, contextName string) ([]ProjectRecord, error) {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return r.ListImported(ctx, providerID)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, context_name, name, working_dir, compose_files_json,
			status, health, source, pinned, last_seen_at, metadata_json
		FROM projects
		WHERE provider_id = ? AND context_name = ? AND source = ?
		ORDER BY name ASC
	`, providerID, contextName, ProjectSourceImported)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanProjectRows(rows)
}

func (r *ProjectRepository) ListServices(ctx context.Context, projectID string) ([]ServiceRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, project_id, name, image_ref, build_context, dockerfile_path,
			build_target, status, health, replicas_running, replicas_total,
			metadata_json, last_seen_at
		FROM services
		WHERE project_id = ?
		ORDER BY name ASC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var services []ServiceRecord
	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (r *ProjectRepository) ListServicesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]ServiceRecord, error) {
	servicesByProject := make(map[string][]ServiceRecord, len(projectIDs))
	if len(projectIDs) == 0 {
		return servicesByProject, nil
	}
	args := make([]any, 0, len(projectIDs))
	placeholders := make([]string, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		projectID = strings.TrimSpace(projectID)
		if projectID == "" {
			continue
		}
		if _, ok := servicesByProject[projectID]; ok {
			continue
		}
		servicesByProject[projectID] = nil
		placeholders = append(placeholders, "?")
		args = append(args, projectID)
	}
	if len(args) == 0 {
		return servicesByProject, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, project_id, name, image_ref, build_context, dockerfile_path,
			build_target, status, health, replicas_running, replicas_total,
			metadata_json, last_seen_at
		FROM services
		WHERE project_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY project_id ASC, name ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		servicesByProject[service.ProjectID] = append(servicesByProject[service.ProjectID], service)
	}
	return servicesByProject, rows.Err()
}

type projectScanner interface {
	Scan(dest ...any) error
}

func scanProjectRows(rows *sql.Rows) ([]ProjectRecord, error) {
	var projects []ProjectRecord
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func scanProject(scanner projectScanner) (ProjectRecord, error) {
	var (
		project      ProjectRecord
		workingDir   sql.NullString
		filesJSON    string
		status       sql.NullString
		health       sql.NullString
		lastSeen     sql.NullString
		metadataJSON string
		pinned       int
	)
	if err := scanner.Scan(
		&project.ID,
		&project.ProviderID,
		&project.ContextName,
		&project.Name,
		&workingDir,
		&filesJSON,
		&status,
		&health,
		&project.Source,
		&pinned,
		&lastSeen,
		&metadataJSON,
	); err != nil {
		return ProjectRecord{}, err
	}
	project.WorkingDir = workingDir.String
	project.Status = models.ProjectStatus(status.String)
	project.Health = models.HealthStatus(health.String)
	project.Pinned = pinned != 0
	project.LastSeenAt = parseStoreTime(lastSeen.String)
	if project.Status == "" {
		project.Status = models.ProjectStatusUnknown
	}
	if project.Health == "" {
		project.Health = models.HealthStatusUnknown
	}
	if err := json.Unmarshal([]byte(nullJSON(filesJSON, "[]")), &project.ComposeFiles); err != nil {
		return ProjectRecord{}, err
	}
	if err := json.Unmarshal([]byte(nullJSON(metadataJSON, "{}")), &project.Metadata); err != nil {
		return ProjectRecord{}, err
	}
	return project, nil
}

func scanService(scanner projectScanner) (ServiceRecord, error) {
	var (
		service      ServiceRecord
		imageRef     sql.NullString
		buildContext sql.NullString
		dockerfile   sql.NullString
		buildTarget  sql.NullString
		status       sql.NullString
		health       sql.NullString
		metadataJSON string
		lastSeen     sql.NullString
	)
	if err := scanner.Scan(
		&service.ID,
		&service.ProjectID,
		&service.Name,
		&imageRef,
		&buildContext,
		&dockerfile,
		&buildTarget,
		&status,
		&health,
		&service.ReplicasRunning,
		&service.ReplicasTotal,
		&metadataJSON,
		&lastSeen,
	); err != nil {
		return ServiceRecord{}, err
	}
	service.ImageRef = imageRef.String
	service.BuildContext = buildContext.String
	service.DockerfilePath = dockerfile.String
	service.BuildTarget = buildTarget.String
	service.Status = models.ProjectStatus(status.String)
	service.Health = models.HealthStatus(health.String)
	service.LastSeenAt = parseStoreTime(lastSeen.String)
	if service.Status == "" {
		service.Status = models.ProjectStatusUnknown
	}
	if service.Health == "" {
		service.Health = models.HealthStatusUnknown
	}
	if err := json.Unmarshal([]byte(nullJSON(metadataJSON, "{}")), &service.Metadata); err != nil {
		return ServiceRecord{}, err
	}
	return service, nil
}
