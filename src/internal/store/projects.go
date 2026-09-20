package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	db         *sql.DB
	operations *projectOperationGate
}

type projectExecContext interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
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
	return &ProjectRepository{db: s.writer, operations: s.projectOperations}
}

// ProjectOperationGeneration returns the current in-process incarnation fence
// for a project. Plans capture this value and background work must present it
// again before touching Compose or mutable project state.
func (r *ProjectRepository) ProjectOperationGeneration(runtimeScope runtimescope.Scope, projectID string) (uint64, error) {
	key, ok := projectOperationKeyFromScope(runtimeScope, projectID)
	if !ok {
		return 0, errors.New("runtime scope and project ID are required")
	}
	return r.operations.generation(key)
}

// BeginProjectOperation registers cancellable background work for one project
// incarnation. Project deletion cancels and joins all registered work before
// it removes the snapshot.
func (r *ProjectRepository) BeginProjectOperation(
	ctx context.Context,
	runtimeScope runtimescope.Scope,
	projectID string,
	expectedGeneration uint64,
) (context.Context, func(), error) {
	key, ok := projectOperationKeyFromScope(runtimeScope, projectID)
	if !ok {
		return nil, nil, errors.New("runtime scope and project ID are required")
	}
	return r.operations.beginOperation(ctx, key, expectedGeneration)
}

type saveSnapshotOptions struct {
	replaceAllProjectServices bool
	skipScopeConflicts        bool
	skippedResult             *[]string
	unforgetName              string
	unforgetProjectID         string
}

// SaveSnapshot atomically updates only one exact runtime scope. It also
// fails closed when a legacy global project ID is already owned by another
// scope; canonical scoped IDs are introduced by the separate BE-005 migration.
func (r *ProjectRepository) SaveSnapshot(ctx context.Context, runtimeScope runtimescope.Scope, projects []ProjectRecord, services []ServiceRecord, seenAt time.Time, staleCutoff time.Time) error {
	if err := validateSnapshotScope(runtimeScope, projects); err != nil {
		return err
	}
	return r.saveSnapshot(ctx, runtimeScope.ProviderID(), runtimeScope.ContextName(), projects, services, seenAt, staleCutoff, saveSnapshotOptions{})
}

// SaveDetectedSnapshot preserves availability for detector snapshots while
// keeping legacy global-ID collisions quarantined. Conflicting project IDs and
// their services are skipped inside the same transaction as the remaining
// snapshot writes and stale cleanup. The service slice is authoritative for
// every accepted incoming project, including projects with zero services.
func (r *ProjectRepository) SaveDetectedSnapshot(ctx context.Context, runtimeScope runtimescope.Scope, projects []ProjectRecord, services []ServiceRecord, seenAt time.Time, staleCutoff time.Time) ([]string, error) {
	if err := validateSnapshotScope(runtimeScope, projects); err != nil {
		return nil, err
	}
	skipped := []string{}
	if err := r.saveSnapshot(ctx, runtimeScope.ProviderID(), runtimeScope.ContextName(), projects, services, seenAt, staleCutoff, saveSnapshotOptions{
		replaceAllProjectServices: true,
		skipScopeConflicts:        true,
		skippedResult:             &skipped,
	}); err != nil {
		return nil, err
	}
	return skipped, nil
}

// SaveImportedSnapshot derives the runtime scope from project and atomically
// clears its matching forgotten-project tombstone with the imported snapshot.
func (r *ProjectRepository) SaveImportedSnapshot(ctx context.Context, project ProjectRecord, services []ServiceRecord, seenAt time.Time) error {
	runtimeScope, ok := runtimescope.New(project.ProviderID, project.ContextName)
	if !ok {
		return errors.New("imported project runtime scope is required")
	}
	return r.SaveImportedSnapshotInScope(ctx, runtimeScope, project, services, seenAt)
}

// SaveImportedSnapshotInScope prevents a failed import from silently
// resurrecting a previously forgotten project. Tombstone removal, project
// ownership validation, project upsert, and service replacement share one
// transaction.
func (r *ProjectRepository) SaveImportedSnapshotInScope(ctx context.Context, runtimeScope runtimescope.Scope, project ProjectRecord, services []ServiceRecord, seenAt time.Time) error {
	if err := validateSnapshotScope(runtimeScope, []ProjectRecord{project}); err != nil {
		return err
	}
	name := strings.TrimSpace(project.Name)
	projectID := strings.TrimSpace(project.ID)
	if name == "" || projectID == "" {
		return errors.New("imported project name and ID are required")
	}
	if project.Source == "" {
		project.Source = ProjectSourceImported
	}
	return r.saveSnapshot(
		ctx,
		runtimeScope.ProviderID(),
		runtimeScope.ContextName(),
		[]ProjectRecord{project},
		services,
		seenAt,
		time.Time{},
		saveSnapshotOptions{
			unforgetName:      name,
			unforgetProjectID: projectID,
		},
	)
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

func (r *ProjectRepository) saveSnapshot(ctx context.Context, providerID string, contextName string, projects []ProjectRecord, services []ServiceRecord, seenAt time.Time, staleCutoff time.Time, options saveSnapshotOptions) error {
	staleProjectIDs, releaseDeletions, err := r.beginStaleProjectDeletions(ctx, providerID, contextName, projects, seenAt, staleCutoff)
	if err != nil {
		return err
	}
	defer releaseProjectDeletions(releaseDeletions)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if options.unforgetName != "" || options.unforgetProjectID != "" {
		if err := unforgetProjectInScope(ctx, tx, providerID, contextName, options.unforgetName, options.unforgetProjectID); err != nil {
			return err
		}
	}
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
		if !options.skipScopeConflicts {
			return fmt.Errorf("%w: %s", errProjectScopeConflict, skipped[0])
		}
		projects = filterProjectsByID(projects, conflictingProjectIDs)
		services = filterServicesByProjectID(services, conflictingProjectIDs)
		for projectID := range conflictingProjectIDs {
			skippedProjectIDs[projectID] = struct{}{}
		}
	}
	if options.skippedResult != nil {
		*options.skippedResult = sortedProjectIDs(skippedProjectIDs)
	}

	replaceServices := services != nil || options.replaceAllProjectServices
	serviceReplacementIDs := serviceReplacementProjectIDs(projects, services)
	if options.replaceAllProjectServices {
		serviceReplacementIDs = snapshotProjectIDs(projects)
	}
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
			if err := deleteStaleDetectedProjects(ctx, tx, providerID, contextName, staleCutoff, staleProjectIDs); err != nil {
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
		if err := deleteStaleDetectedProjects(ctx, tx, providerID, contextName, staleCutoff, staleProjectIDs); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// beginStaleProjectDeletions acquires lifecycle fences before the write
// transaction starts. Waiting while a transaction is open could deadlock with
// an update worker that is finishing its own history writes.
func (r *ProjectRepository) beginStaleProjectDeletions(
	ctx context.Context,
	providerID string,
	contextName string,
	incoming []ProjectRecord,
	seenAt time.Time,
	staleCutoff time.Time,
) ([]string, []func(), error) {
	if staleCutoff.IsZero() {
		return nil, nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id
		FROM projects
		WHERE provider_id = ?
			AND context_name = ?
			AND source <> ?
			AND last_seen_at < ?
	`, providerID, contextName, ProjectSourceImported, formatTime(staleCutoff))
	if err != nil {
		return nil, nil, err
	}
	incomingIDs := make(map[string]struct{}, len(incoming))
	for _, project := range incoming {
		lastSeenAt := project.LastSeenAt
		if lastSeenAt.IsZero() {
			lastSeenAt = seenAt
		}
		if id := strings.TrimSpace(project.ID); id != "" && !lastSeenAt.Before(staleCutoff) {
			incomingIDs[id] = struct{}{}
		}
	}
	projectIDs := []string{}
	for rows.Next() {
		var projectID string
		if err := rows.Scan(&projectID); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if _, refreshed := incomingIDs[projectID]; !refreshed {
			projectIDs = append(projectIDs, projectID)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	sort.Strings(projectIDs)

	releases := make([]func(), 0, len(projectIDs))
	for _, projectID := range projectIDs {
		release, err := r.beginProjectDeletion(ctx, providerID, contextName, projectID)
		if err != nil {
			releaseProjectDeletions(releases)
			return nil, nil, err
		}
		releases = append(releases, release)
	}
	return projectIDs, releases, nil
}

func (r *ProjectRepository) beginProjectDeletion(ctx context.Context, providerID string, contextName string, projectID string) (func(), error) {
	runtimeScope, ok := runtimescope.New(providerID, contextName)
	if !ok {
		return nil, errors.New("project runtime scope is required")
	}
	key, ok := projectOperationKeyFromScope(runtimeScope, projectID)
	if !ok {
		return nil, errors.New("project ID is required")
	}
	return r.operations.beginDeletion(ctx, key)
}

func releaseProjectDeletions(releases []func()) {
	for _, release := range slices.Backward(releases) {
		release()
	}
}

func deleteStaleDetectedProjects(
	ctx context.Context,
	tx *sql.Tx,
	providerID string,
	contextName string,
	staleCutoff time.Time,
	projectIDs []string,
) error {
	for _, projectID := range projectIDs {
		var stale int
		err := tx.QueryRowContext(ctx, `
			SELECT 1
			FROM projects
			WHERE id = ?
				AND provider_id = ?
				AND context_name = ?
				AND source <> ?
				AND last_seen_at < ?
		`, projectID, providerID, contextName, ProjectSourceImported, formatTime(staleCutoff)).Scan(&stale)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err := deleteProjectOwnedMutableState(ctx, tx, providerID, contextName, projectID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			DELETE FROM projects
			WHERE id = ?
				AND provider_id = ?
				AND context_name = ?
				AND source <> ?
				AND last_seen_at < ?
		`, projectID, providerID, contextName, ProjectSourceImported, formatTime(staleCutoff))
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return errors.New("stale project changed while its deletion fence was held")
		}
	}
	return nil
}

// deleteProjectOwnedMutableState removes discovery and update policy state that
// belongs to one project incarnation. Durable telemetry, backups, audit rows,
// and completed update history remain as intentional history. Completed
// history loses rollback capability; unfinished history is detached so a late
// finisher cannot make it actionable for a future project with the same ID.
func deleteProjectOwnedMutableState(ctx context.Context, tx *sql.Tx, providerID string, contextName string, projectID string) error {
	if err := detachUpdateCheckLineageReferences(
		ctx,
		tx,
		`provider_id = ? AND context_name = ? AND project_id = ?`,
		providerID,
		contextName,
		projectID,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM image_update_checks
		WHERE COALESCE(project_id, '') = ?
	`, projectID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM image_lineage
		WHERE provider_id = ? AND context_name = ? AND project_id = ?
	`, providerID, contextName, projectID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM ignored_updates
		WHERE provider_id = ? AND context_name = ? AND COALESCE(project_id, '') = ?
	`, providerID, contextName, projectID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE update_history
		SET project_id = NULL,
			service_id = NULL,
			rollback_status = 'unavailable'
		WHERE provider_id = ? AND context_name = ? AND COALESCE(project_id, '') = ?
			AND (finished_at IS NULL OR TRIM(CAST(finished_at AS TEXT)) = '')
	`, providerID, contextName, projectID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE update_history
		SET rollback_status = 'unavailable'
		WHERE provider_id = ? AND context_name = ? AND COALESCE(project_id, '') = ?
			AND finished_at IS NOT NULL
			AND TRIM(CAST(finished_at AS TEXT)) <> ''
			AND COALESCE(rollback_status, '') = 'available'
	`, providerID, contextName, projectID); err != nil {
		return err
	}
	return nil
}

func deleteProjectSnapshot(ctx context.Context, tx *sql.Tx, providerID string, contextName string, projectID string) error {
	if err := deleteProjectOwnedMutableState(ctx, tx, providerID, contextName, projectID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM services WHERE project_id = ?", projectID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM projects
		WHERE id = ? AND provider_id = ? AND context_name = ?
	`, projectID, providerID, contextName)
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
		return snapshotProjectIDs(projects)
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

func snapshotProjectIDs(projects []ProjectRecord) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		id := strings.TrimSpace(project.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func (r *ProjectRepository) UpsertImported(ctx context.Context, record ProjectRecord) error {
	if record.Source == "" {
		record.Source = ProjectSourceImported
	}
	return r.SaveImportedSnapshot(ctx, record, nil, record.LastSeenAt)
}

func (r *ProjectRepository) Forget(ctx context.Context, project ProjectRecord, forgottenAt time.Time) error {
	return upsertForgottenProject(ctx, r.db, project, forgottenAt)
}

func upsertForgottenProject(ctx context.Context, exec projectExecContext, project ProjectRecord, forgottenAt time.Time) error {
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
	_, err := exec.ExecContext(ctx, `
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
	return unforgetProjectInScope(ctx, r.db, runtimeScope.ProviderID(), runtimeScope.ContextName(), name, projectID)
}

func unforgetProjectInScope(ctx context.Context, exec projectExecContext, providerID string, contextName string, name string, projectID string) error {
	name = strings.TrimSpace(name)
	projectID = strings.TrimSpace(projectID)
	_, err := exec.ExecContext(ctx, `
		DELETE FROM forgotten_projects
		WHERE provider_id = ? AND context_name = ?
			AND (name = ? OR (? <> '' AND project_id = ?))
	`, providerID, contextName, name, projectID, projectID)
	return err
}

// ForgetAndDelete atomically records the project's detector tombstone and
// removes the project snapshot. It derives ownership from the project row read
// inside the transaction.
func (r *ProjectRepository) ForgetAndDelete(ctx context.Context, projectID string, forgottenAt time.Time) error {
	return r.forgetAndDelete(ctx, runtimescope.Scope{}, projectID, forgottenAt)
}

// ForgetAndDeleteInScope is the authorization-safe remove-from-list mutation:
// ownership validation, tombstone insertion, service removal, and project
// deletion either all commit or all roll back.
func (r *ProjectRepository) ForgetAndDeleteInScope(ctx context.Context, runtimeScope runtimescope.Scope, projectID string, forgottenAt time.Time) error {
	if !runtimeScope.Valid() {
		return errors.New("runtime scope is required")
	}
	return r.forgetAndDelete(ctx, runtimeScope, projectID, forgottenAt)
}

func (r *ProjectRepository) forgetAndDelete(ctx context.Context, runtimeScope runtimescope.Scope, projectID string, forgottenAt time.Time) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return sql.ErrNoRows
	}
	project := ProjectRecord{ID: projectID}
	if err := r.db.QueryRowContext(ctx, `
		SELECT provider_id, context_name, name
		FROM projects
		WHERE id = ?
	`, projectID).Scan(&project.ProviderID, &project.ContextName, &project.Name); err != nil {
		return err
	}
	if runtimeScope.Valid() && !runtimeScope.Matches(project.ProviderID, project.ContextName) {
		return sql.ErrNoRows
	}
	releaseDeletion, err := r.beginProjectDeletion(ctx, project.ProviderID, project.ContextName, projectID)
	if err != nil {
		return err
	}
	defer releaseDeletion()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	project = ProjectRecord{ID: projectID}
	if err := tx.QueryRowContext(ctx, `
		SELECT provider_id, context_name, name
		FROM projects
		WHERE id = ?
	`, projectID).Scan(&project.ProviderID, &project.ContextName, &project.Name); err != nil {
		return err
	}
	if runtimeScope.Valid() && !runtimeScope.Matches(project.ProviderID, project.ContextName) {
		return sql.ErrNoRows
	}
	if err := upsertForgottenProject(ctx, tx, project, forgottenAt); err != nil {
		return err
	}
	if err := deleteProjectSnapshot(ctx, tx, project.ProviderID, project.ContextName, projectID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ProjectRepository) Delete(ctx context.Context, projectID string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return sql.ErrNoRows
	}
	var providerID string
	var contextName string
	if err := r.db.QueryRowContext(ctx, `
		SELECT provider_id, context_name
		FROM projects
		WHERE id = ?
	`, projectID).Scan(&providerID, &contextName); err != nil {
		return err
	}
	releaseDeletion, err := r.beginProjectDeletion(ctx, providerID, contextName, projectID)
	if err != nil {
		return err
	}
	defer releaseDeletion()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	providerID = ""
	contextName = ""
	if err := tx.QueryRowContext(ctx, `
		SELECT provider_id, context_name
		FROM projects
		WHERE id = ?
	`, projectID).Scan(&providerID, &contextName); err != nil {
		return err
	}
	if err := deleteProjectSnapshot(ctx, tx, providerID, contextName, projectID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ProjectRepository) DeleteInScope(ctx context.Context, runtimeScope runtimescope.Scope, projectID string) error {
	if !runtimeScope.Valid() {
		return errors.New("runtime scope is required")
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return sql.ErrNoRows
	}
	var providerID string
	var contextName string
	if err := r.db.QueryRowContext(ctx, "SELECT provider_id, context_name FROM projects WHERE id = ?", projectID).Scan(&providerID, &contextName); err != nil {
		return err
	}
	if !runtimeScope.Matches(providerID, contextName) {
		return sql.ErrNoRows
	}
	releaseDeletion, err := r.beginProjectDeletion(ctx, providerID, contextName, projectID)
	if err != nil {
		return err
	}
	defer releaseDeletion()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	providerID = ""
	contextName = ""
	if err := tx.QueryRowContext(ctx, "SELECT provider_id, context_name FROM projects WHERE id = ?", projectID).Scan(&providerID, &contextName); err != nil {
		return err
	}
	if !runtimeScope.Matches(providerID, contextName) {
		return sql.ErrNoRows
	}
	if err := deleteProjectSnapshot(ctx, tx, runtimeScope.ProviderID(), runtimeScope.ContextName(), projectID); err != nil {
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
