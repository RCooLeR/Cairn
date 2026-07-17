-- Update checks are published as explicitly ordered, complete generations.
-- Current readers must never infer lifecycle from mutable container/image
-- references or from timestamps shared by a possibly interrupted legacy run.

ALTER TABLE image_update_checks
    ADD COLUMN generation_id INTEGER NOT NULL DEFAULT 0;

ALTER TABLE image_update_checks
    ADD COLUMN is_current INTEGER NOT NULL DEFAULT 0 CHECK (is_current IN (0, 1));

-- No legacy row can prove that every sibling check from its run committed.
-- Keep scoped rows as non-current history and require one fresh complete run
-- before exposing current update state. Rows whose project no longer exists
-- have no lifecycle owner and are removed immediately.
UPDATE image_update_checks
SET is_current = 0;

DELETE FROM image_update_checks AS legacy
WHERE COALESCE(legacy.project_id, '') = ''
    OR NOT EXISTS (
        SELECT 1
        FROM projects
        WHERE projects.id = legacy.project_id
    );

-- Legacy rows have no trustworthy generation boundary, so cap each surviving
-- project/scope history directly. New publications use the stricter generation
-- and age limits in the repository.
DELETE FROM image_update_checks
WHERE id IN (
    SELECT id
    FROM (
        SELECT id,
            ROW_NUMBER() OVER (
                PARTITION BY provider_id, context_name, COALESCE(project_id, '')
                ORDER BY julianday(checked_at) DESC, id DESC
            ) AS legacy_rank
        FROM image_update_checks
    ) ranked
    WHERE ranked.legacy_rank > 1000
);

CREATE TABLE image_update_check_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    context_name TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    service_id TEXT NOT NULL DEFAULT '',
    reserved_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    status TEXT NOT NULL CHECK (status IN ('running', 'published', 'superseded', 'abandoned'))
);

CREATE INDEX idx_check_runs_project ON image_update_check_runs(
    provider_id,
    context_name,
    project_id,
    status,
    id
);

-- One project-wide head establishes a baseline. A service head exists only
-- when a later service-only run overrides that baseline (including an empty
-- generation that intentionally clears the service's current checks).
CREATE TABLE image_update_check_heads (
    provider_id TEXT NOT NULL,
    context_name TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    service_id TEXT NOT NULL DEFAULT '',
    last_run_id INTEGER NOT NULL REFERENCES image_update_check_runs(id),
    PRIMARY KEY (provider_id, context_name, project_id, service_id)
);

-- Check rows predate a project foreign key and intentionally remain historical
-- after publication. This trigger gives every present and future project-delete
-- path the same atomic lifecycle cleanup without rebuilding the large table.
CREATE TRIGGER trg_projects_delete_update_checks
AFTER DELETE ON projects
BEGIN
    DELETE FROM image_update_checks
    WHERE COALESCE(project_id, '') = OLD.id;
END;

DROP INDEX IF EXISTS idx_checks_latest;

CREATE INDEX idx_checks_latest ON image_update_checks(
    provider_id,
    context_name,
    COALESCE(project_id, ''),
    is_current,
    COALESCE(service_id, ''),
    kind,
    id
);

CREATE INDEX idx_checks_retention ON image_update_checks(
    provider_id,
    context_name,
    COALESCE(project_id, ''),
    is_current,
    generation_id,
    checked_at,
    id
);
