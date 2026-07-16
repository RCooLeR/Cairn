package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
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

var errProjectScopeConflict = errors.New("project ID belongs to another runtime scope")

func IsProjectScopeConflict(err error) bool {
	return errors.Is(err, errProjectScopeConflict)
}

func (s *Store) Projects() *ProjectRepository {
	return &ProjectRepository{db: s.writer}
}

// SaveSnapshot atomically updates only one exact runtime scope. It also
// fails closed when a legacy global project ID is already owned by another
// scope; canonical scoped IDs are introduced by the separate BE-005 migration.
func (r *ProjectRepository) SaveSnapshot(ctx context.Context, runtimeScope runtimescope.Scope, projects []ProjectRecord, services []ServiceRecord, seenAt time.Time, staleCutoff time.Time) error {
	if err := validateSnapshotScope(runtimeScope, projects); err != nil {
		return err
	}
	return r.saveSnapshot(ctx, runtimeScope.ProviderID(), runtimeScope.ContextName(), projects, services, seenAt, staleCutoff, false, nil)
}

// SaveDetectedSnapshot preserves availability for detector snapshots while
// keeping legacy global-ID collisions quarantined. Conflicting project IDs and
// their services are skipped inside the same transaction as the remaining
// snapshot writes and stale cleanup.
func (r *ProjectRepository) SaveDetectedSnapshot(ctx context.Context, runtimeScope runtimescope.Scope, projects []ProjectRecord, services []ServiceRecord, seenAt time.Time, staleCutoff time.Time) ([]string, error) {
	if err := validateSnapshotScope(runtimeScope, projects); err != nil {
		return nil, err
	}
	skipped := []string{}
	if err := r.saveSnapshot(ctx, runtimeScope.ProviderID(), runtimeScope.ContextName(), projects, services, seenAt, staleCutoff, true, &skipped); err != nil {
		return nil, err
	}
	return skipped, nil
}

func validateSnapshotScope(runtimeScope runtimescope.Scope, projects []ProjectRecord) error {
	if !runtimeScope.Valid() {
		return errors.New("runtime scope is required")
	}
	for _, project := range projects {
		if !runtimeScope.Matches(project.ProviderID, project.ContextName) {
			return fmt.Errorf("project %q does not belong to the snapshot runtime scope", project.ID)
		}
	}
	return nil
}

func (r *ProjectRepository) saveSnapshot(ctx context.Context, providerID string, contextName string, projects []ProjectRecord, services []ServiceRecord, seenAt time.Time, staleCutoff time.Time, skipScopeConflicts bool, skippedResult *[]string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	forgotten, err := r.forgottenProjectKeys(ctx, tx, providerID, contextName)
	if err != nil {
		return err
	}
	skippedProjectIDs := map[string]struct{}{}
	if len(forgotten) > 0 {
		var skipped map[string]struct{}
		projects, skipped = filterForgottenProjects(projects, forgotten)
		services = filterForgottenServices(services, skipped)
		for projectID := range skipped {
			skippedProjectIDs[projectID] = struct{}{}
		}
	}

	conflictingProjectIDs, err := r.snapshotScopeConflicts(ctx, tx, providerID, contextName, projects, services)
	if err != nil {
		return err
	}
	if len(conflictingProjectIDs) > 0 {
		skipped := sortedProjectIDs(conflictingProjectIDs)
		if !skipScopeConflicts {
			return fmt.Errorf("%w: %s", errProjectScopeConflict, skipped[0])
		}
		projects = filterProjectsByID(projects, conflictingProjectIDs)
		services = filterServicesByProjectID(services, conflictingProjectIDs)
		for projectID := range conflictingProjectIDs {
			skippedProjectIDs[projectID] = struct{}{}
		}
	}
	if skippedResult != nil {
		*skippedResult = sortedProjectIDs(skippedProjectIDs)
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
			query := `
				DELETE FROM projects
				WHERE provider_id = ?
					AND source <> ?
					AND last_seen_at < ?`
			args := []any{providerID, ProjectSourceImported, formatTime(staleCutoff)}
			query += " AND context_name = ?"
			args = append(args, contextName)
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
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
		query := `
			DELETE FROM projects
			WHERE provider_id = ?
				AND source <> ?
				AND last_seen_at < ?`
		args := []any{providerID, ProjectSourceImported, formatTime(staleCutoff)}
		query += " AND context_name = ?"
		args = append(args, contextName)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *ProjectRepository) forgottenProjectKeys(ctx context.Context, tx *sql.Tx, providerID string, contextName string) (map[forgottenProjectKey]struct{}, error) {
	query := `
		SELECT context_name, name, project_id
		FROM forgotten_projects
		WHERE provider_id = ?`
	args := []any{providerID}
	query += " AND context_name = ?"
	args = append(args, contextName)
	rows, err := tx.QueryContext(ctx, query, args...)
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

func runtimeScopeMatches(providerID string, contextName string, expectedProviderID string, expectedContextName string) bool {
	return strings.TrimSpace(providerID) == strings.TrimSpace(expectedProviderID) && strings.TrimSpace(contextName) == strings.TrimSpace(expectedContextName)
}

func (r *ProjectRepository) snapshotScopeConflicts(ctx context.Context, tx *sql.Tx, providerID string, contextName string, projects []ProjectRecord, services []ServiceRecord) (map[string]struct{}, error) {
	conflicts := map[string]struct{}{}
	inputProjectIDs := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		projectID := strings.TrimSpace(project.ID)
		inputProjectIDs[projectID] = struct{}{}

		var existingProviderID string
		var existingContextName string
		err := tx.QueryRowContext(ctx, "SELECT provider_id, context_name FROM projects WHERE id = ?", projectID).Scan(&existingProviderID, &existingContextName)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil && !runtimeScopeMatches(existingProviderID, existingContextName, providerID, contextName) {
			conflicts[projectID] = struct{}{}
		}
	}

	validatedProjectIDs := map[string]struct{}{}
	for _, service := range services {
		projectID := strings.TrimSpace(service.ProjectID)
		if projectID == "" {
			return nil, errors.New("service project ID is required")
		}
		if _, incoming := inputProjectIDs[projectID]; !incoming {
			if _, validated := validatedProjectIDs[projectID]; !validated {
				var existingProviderID string
				var existingContextName string
				err := tx.QueryRowContext(ctx, "SELECT provider_id, context_name FROM projects WHERE id = ?", projectID).Scan(&existingProviderID, &existingContextName)
				if errors.Is(err, sql.ErrNoRows) {
					return nil, fmt.Errorf("%w: %s", errProjectScopeConflict, projectID)
				}
				if err != nil {
					return nil, err
				}
				if !runtimeScopeMatches(existingProviderID, existingContextName, providerID, contextName) {
					conflicts[projectID] = struct{}{}
				}
				validatedProjectIDs[projectID] = struct{}{}
			}
		}
		if _, conflicting := conflicts[projectID]; conflicting {
			continue
		}

		var existingProjectID string
		var existingProviderID string
		var existingContextName string
		err := tx.QueryRowContext(ctx, `
			SELECT services.project_id, projects.provider_id, projects.context_name
			FROM services
			JOIN projects ON projects.id = services.project_id
			WHERE services.id = ?
		`, strings.TrimSpace(service.ID)).Scan(&existingProjectID, &existingProviderID, &existingContextName)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil && (strings.TrimSpace(existingProjectID) != projectID || !runtimeScopeMatches(existingProviderID, existingContextName, providerID, contextName)) {
			conflicts[projectID] = struct{}{}
		}
	}
	return conflicts, nil
}

func sortedProjectIDs(projectIDs map[string]struct{}) []string {
	ids := make([]string, 0, len(projectIDs))
	for projectID := range projectIDs {
		ids = append(ids, projectID)
	}
	sort.Strings(ids)
	return ids
}

func filterProjectsByID(projects []ProjectRecord, skipped map[string]struct{}) []ProjectRecord {
	if len(projects) == 0 || len(skipped) == 0 {
		return projects
	}
	filtered := make([]ProjectRecord, 0, len(projects))
	for _, project := range projects {
		if _, ok := skipped[strings.TrimSpace(project.ID)]; ok {
			continue
		}
		filtered = append(filtered, project)
	}
	return filtered
}

func filterServicesByProjectID(services []ServiceRecord, skipped map[string]struct{}) []ServiceRecord {
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
	runtimeScope, ok := runtimescope.New(record.ProviderID, record.ContextName)
	if !ok {
		return errors.New("imported project runtime scope is required")
	}
	return r.SaveSnapshot(ctx, runtimeScope, []ProjectRecord{record}, nil, record.LastSeenAt, time.Time{})
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

func (r *ProjectRepository) UnforgetInScope(ctx context.Context, runtimeScope runtimescope.Scope, name string, projectID string) error {
	if !runtimeScope.Valid() {
		return errors.New("runtime scope is required")
	}
	name = strings.TrimSpace(name)
	projectID = strings.TrimSpace(projectID)
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM forgotten_projects
		WHERE provider_id = ? AND context_name = ?
			AND (name = ? OR (? <> '' AND project_id = ?))
	`, runtimeScope.ProviderID(), runtimeScope.ContextName(), name, projectID, projectID)
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

func (r *ProjectRepository) DeleteInScope(ctx context.Context, runtimeScope runtimescope.Scope, projectID string) error {
	if !runtimeScope.Valid() {
		return errors.New("runtime scope is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var providerID string
	var contextName string
	if err := tx.QueryRowContext(ctx, "SELECT provider_id, context_name FROM projects WHERE id = ?", projectID).Scan(&providerID, &contextName); err != nil {
		return err
	}
	if !runtimeScope.Matches(providerID, contextName) {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM services WHERE project_id = ?", projectID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM projects WHERE id = ? AND provider_id = ? AND context_name = ?", projectID, runtimeScope.ProviderID(), runtimeScope.ContextName()); err != nil {
		return err
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

func (r *ProjectRepository) ListByScope(ctx context.Context, runtimeScope runtimescope.Scope) ([]ProjectRecord, error) {
	if !runtimeScope.Valid() {
		return nil, errors.New("runtime scope is required")
	}
	return r.ListByProviderContext(ctx, runtimeScope.ProviderID(), runtimeScope.ContextName())
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

func (r *ProjectRepository) GetInScope(ctx context.Context, runtimeScope runtimescope.Scope, projectID string) (ProjectRecord, error) {
	if !runtimeScope.Valid() {
		return ProjectRecord{}, errors.New("runtime scope is required")
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id, provider_id, context_name, name, working_dir, compose_files_json,
			status, health, source, pinned, last_seen_at, metadata_json
		FROM projects
		WHERE id = ? AND provider_id = ? AND context_name = ?
	`, projectID, runtimeScope.ProviderID(), runtimeScope.ContextName())
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

func (r *ProjectRepository) ListImportedByScope(ctx context.Context, runtimeScope runtimescope.Scope) ([]ProjectRecord, error) {
	if !runtimeScope.Valid() {
		return nil, errors.New("runtime scope is required")
	}
	return r.ListImportedByProviderContext(ctx, runtimeScope.ProviderID(), runtimeScope.ContextName())
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
