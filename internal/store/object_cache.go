package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

type ObjectCacheRepository struct {
	db *sql.DB
}

type ContainerCacheRecord struct {
	Summary   models.ContainerSummary
	Labels    map[string]string
	StartedAt time.Time
}

type ImageCacheRecord struct {
	Summary  models.ImageSummary
	UsedBy   []string
	Dangling bool
}

type VolumeCacheRecord struct {
	Summary   models.VolumeSummary
	UsedBy    []string
	CreatedAt time.Time
}

type NetworkCacheRecord struct {
	Summary    models.NetworkSummary
	Subnet     string
	Gateway    string
	Containers []string
}

func (s *Store) Objects() *ObjectCacheRepository {
	return &ObjectCacheRepository{db: s.writer}
}

func (r *ObjectCacheRepository) SaveContainers(ctx context.Context, providerID string, records []ContainerCacheRecord, seenAt time.Time) error {
	return r.saveContainers(ctx, providerID, records, seenAt, false)
}

func (r *ObjectCacheRepository) SaveContainersSnapshot(ctx context.Context, providerID string, records []ContainerCacheRecord, seenAt time.Time) error {
	return r.saveContainers(ctx, providerID, records, seenAt, true)
}

func (r *ObjectCacheRepository) saveContainers(ctx context.Context, providerID string, records []ContainerCacheRecord, seenAt time.Time, replace bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if replace {
		if _, err := tx.ExecContext(ctx, "DELETE FROM containers_cache WHERE provider_id = ?", providerID); err != nil {
			return err
		}
	}

	for _, record := range records {
		summary := record.Summary
		state := cacheContainerState(summary)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO containers_cache (
				id, provider_id, project_id, service_id, name, image_ref, image_id,
				status, state, health, restart_count, ports_json, labels_json, created_at,
				started_at, last_seen_at
			)
			VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''),
				NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?)
			ON CONFLICT(id) DO UPDATE SET
				provider_id = excluded.provider_id,
				project_id = excluded.project_id,
				service_id = excluded.service_id,
				name = excluded.name,
				image_ref = excluded.image_ref,
				image_id = excluded.image_id,
				status = excluded.status,
				state = excluded.state,
				health = excluded.health,
				restart_count = excluded.restart_count,
				ports_json = excluded.ports_json,
				labels_json = excluded.labels_json,
				created_at = excluded.created_at,
				started_at = excluded.started_at,
				last_seen_at = excluded.last_seen_at
		`, summary.ID, providerID, summary.ProjectID, summary.Service, summary.Name, summary.Image,
			summary.ImageID, summary.Status, state, string(summary.Health), summary.Restarts,
			jsonText(summary.Ports, "[]"), jsonText(record.Labels, "{}"), formatTime(summary.CreatedAt),
			formatTime(record.StartedAt), formatTime(seenAt)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *ObjectCacheRepository) ListContainers(ctx context.Context, providerID string) ([]ContainerCacheRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, image_ref, image_id, status, state, health, restart_count,
			project_id, service_id, ports_json, labels_json, created_at, started_at
		FROM containers_cache
		WHERE provider_id = ?
		ORDER BY name
	`, providerID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var records []ContainerCacheRecord
	for rows.Next() {
		var (
			projectID  sql.NullString
			serviceID  sql.NullString
			imageRef   sql.NullString
			imageID    sql.NullString
			status     sql.NullString
			state      sql.NullString
			health     sql.NullString
			createdAt  sql.NullString
			startedAt  sql.NullString
			portsJSON  string
			labelsJSON string
			record     ContainerCacheRecord
		)
		if err := rows.Scan(
			&record.Summary.ID,
			&record.Summary.Name,
			&imageRef,
			&imageID,
			&status,
			&state,
			&health,
			&record.Summary.Restarts,
			&projectID,
			&serviceID,
			&portsJSON,
			&labelsJSON,
			&createdAt,
			&startedAt,
		); err != nil {
			return nil, err
		}
		record.Summary.ProjectID = projectID.String
		record.Summary.Service = serviceID.String
		record.Summary.Image = imageRef.String
		record.Summary.ImageID = imageID.String
		record.Summary.Status = status.String
		record.Summary.State = cacheContainerState(models.ContainerSummary{State: state.String, Status: status.String})
		record.Summary.Health = models.HealthStatus(health.String)
		record.Summary.CreatedAt = parseStoreTime(createdAt.String)
		record.StartedAt = parseStoreTime(startedAt.String)
		if record.Summary.Health == "" {
			record.Summary.Health = models.HealthStatusUnknown
		}
		if err := json.Unmarshal([]byte(nullJSON(portsJSON, "[]")), &record.Summary.Ports); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(nullJSON(labelsJSON, "{}")), &record.Labels); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func cacheContainerState(summary models.ContainerSummary) string {
	if state := strings.TrimSpace(summary.State); state != "" {
		return state
	}
	return normalizeCachedContainerState(summary.Status)
}

func normalizeCachedContainerState(status string) string {
	value := strings.ToLower(strings.TrimSpace(status))
	switch {
	case value == "":
		return ""
	case value == "running" || strings.HasPrefix(value, "up "):
		return "running"
	case value == "exited" || strings.HasPrefix(value, "exited "):
		return "exited"
	case value == "paused" || strings.HasPrefix(value, "paused"):
		return "paused"
	case value == "restarting" || strings.HasPrefix(value, "restarting"):
		return "restarting"
	case value == "removing" || strings.HasPrefix(value, "removing"):
		return "removing"
	case value == "created" || strings.HasPrefix(value, "created"):
		return "created"
	case value == "dead" || strings.HasPrefix(value, "dead"):
		return "dead"
	default:
		return value
	}
}

func (r *ObjectCacheRepository) SaveImages(ctx context.Context, providerID string, records []ImageCacheRecord, seenAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, record := range records {
		summary := record.Summary
		dangling := 0
		if record.Dangling {
			dangling = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO images_cache (
				id, provider_id, repo_tags_json, repo_digests_json, size_bytes,
				created_at, used_by_json, dangling, last_seen_at
			)
			VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				provider_id = excluded.provider_id,
				repo_tags_json = excluded.repo_tags_json,
				repo_digests_json = excluded.repo_digests_json,
				size_bytes = excluded.size_bytes,
				created_at = excluded.created_at,
				used_by_json = excluded.used_by_json,
				dangling = excluded.dangling,
				last_seen_at = excluded.last_seen_at
		`, summary.ID, providerID, jsonText(summary.RepoTags, "[]"), jsonText(summary.RepoDigests, "[]"),
			summary.SizeBytes, formatTime(summary.CreatedAt), jsonText(record.UsedBy, "[]"), dangling,
			formatTime(seenAt)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *ObjectCacheRepository) SaveVolumes(ctx context.Context, providerID string, records []VolumeCacheRecord, seenAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, record := range records {
		summary := record.Summary
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO volumes_cache (
				name, provider_id, driver, mountpoint, labels_json, used_by_json,
				estimated_size_bytes, created_at, last_seen_at
			)
			VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), ?)
			ON CONFLICT(provider_id, name) DO UPDATE SET
				driver = excluded.driver,
				mountpoint = excluded.mountpoint,
				labels_json = excluded.labels_json,
				used_by_json = excluded.used_by_json,
				estimated_size_bytes = excluded.estimated_size_bytes,
				created_at = excluded.created_at,
				last_seen_at = excluded.last_seen_at
		`, summary.Name, providerID, summary.Driver, summary.Mountpoint, jsonText(summary.Labels, "{}"),
			jsonText(record.UsedBy, "[]"), summary.SizeBytes, formatTime(record.CreatedAt), formatTime(seenAt)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *ObjectCacheRepository) SaveNetworks(ctx context.Context, providerID string, records []NetworkCacheRecord, seenAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, record := range records {
		summary := record.Summary
		internal := 0
		if summary.Internal {
			internal = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO networks_cache (
				id, provider_id, name, driver, scope, subnet, gateway, internal,
				containers_json, labels_json, last_seen_at
			)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
				?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				provider_id = excluded.provider_id,
				name = excluded.name,
				driver = excluded.driver,
				scope = excluded.scope,
				subnet = excluded.subnet,
				gateway = excluded.gateway,
				internal = excluded.internal,
				containers_json = excluded.containers_json,
				labels_json = excluded.labels_json,
				last_seen_at = excluded.last_seen_at
		`, summary.ID, providerID, summary.Name, summary.Driver, summary.Scope, record.Subnet,
			record.Gateway, internal, jsonText(record.Containers, "[]"), jsonText(summary.Labels, "{}"),
			formatTime(seenAt)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *ObjectCacheRepository) DeleteStale(ctx context.Context, providerID string, cutoff time.Time) error {
	cutoffText := formatTime(cutoff)
	for _, stmt := range []string{
		"DELETE FROM containers_cache WHERE provider_id = ? AND last_seen_at < ?",
		"DELETE FROM images_cache WHERE provider_id = ? AND last_seen_at < ?",
		"DELETE FROM volumes_cache WHERE provider_id = ? AND last_seen_at < ?",
		"DELETE FROM networks_cache WHERE provider_id = ? AND last_seen_at < ?",
	} {
		if _, err := r.db.ExecContext(ctx, stmt, providerID, cutoffText); err != nil {
			return err
		}
	}
	return nil
}

func jsonText(value any, fallback string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(raw)
}

func nullJSON(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func parseStoreTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
