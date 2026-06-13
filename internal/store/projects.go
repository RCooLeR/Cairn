package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
		if _, err := tx.ExecContext(ctx, "DELETE FROM services WHERE project_id = ?", project.ID); err != nil {
			return err
		}
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

func (r *ProjectRepository) UpsertImported(ctx context.Context, record ProjectRecord) error {
	if record.Source == "" {
		record.Source = ProjectSourceImported
	}
	return r.SaveSnapshot(ctx, record.ProviderID, []ProjectRecord{record}, nil, record.LastSeenAt, time.Time{})
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
