-- Historical update and lineage records belong to one exact Docker runtime.
-- Provider IDs alone do not distinguish multiple contexts/backends owned by
-- the same provider. Existing rows predate this identity, so they remain with
-- an empty context and are deliberately unclaimed by scoped readers/writers.

ALTER TABLE image_lineage
    ADD COLUMN context_name TEXT NOT NULL DEFAULT '';

ALTER TABLE image_update_checks
    ADD COLUMN context_name TEXT NOT NULL DEFAULT '';

ALTER TABLE ignored_updates
    ADD COLUMN context_name TEXT NOT NULL DEFAULT '';

ALTER TABLE update_history
    ADD COLUMN context_name TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS idx_lineage_project;
DROP INDEX IF EXISTS idx_lineage_service;
DROP INDEX IF EXISTS idx_lineage_container;

CREATE INDEX idx_lineage_project ON image_lineage(
    provider_id,
    context_name,
    project_id
);
CREATE INDEX idx_lineage_service ON image_lineage(
    provider_id,
    context_name,
    project_id,
    service_name
);
CREATE INDEX idx_lineage_container ON image_lineage(
    provider_id,
    context_name,
    container_id
);

DROP INDEX IF EXISTS idx_checks_project;
DROP INDEX IF EXISTS idx_checks_kind;
DROP INDEX IF EXISTS idx_checks_latest;

CREATE INDEX idx_checks_project ON image_update_checks(
    provider_id,
    context_name,
    project_id,
    checked_at
);
CREATE INDEX idx_checks_kind ON image_update_checks(
    provider_id,
    context_name,
    kind,
    status
);
CREATE INDEX idx_checks_latest ON image_update_checks(
    COALESCE(project_id, ''),
    provider_id,
    context_name,
    COALESCE(service_id, ''),
    COALESCE(container_id, ''),
    kind,
    image_ref,
    COALESCE(base_image_ref, ''),
    id
);

DROP INDEX IF EXISTS idx_ignored_updates_unique;

CREATE UNIQUE INDEX idx_ignored_updates_unique ON ignored_updates(
    provider_id,
    context_name,
    image_ref,
    update_kind,
    COALESCE(base_image_ref, ''),
    COALESCE(project_id, ''),
    COALESCE(service_id, '')
);

CREATE INDEX idx_update_history_scope ON update_history(
    provider_id,
    context_name,
    project_id,
    started_at,
    id
);
