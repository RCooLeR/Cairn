-- Docker object IDs are only unique within a daemon. The legacy cache keyed
-- containers, images, and networks globally, and volumes by provider alone.
-- Rebuild the derived cache with the complete active runtime scope.
--
-- Legacy rows cannot be assigned to a Docker context reliably, so they are
-- intentionally discarded and will be repopulated by the next reconciliation.

ALTER TABLE containers_cache RENAME TO containers_cache_unscoped;
DROP INDEX IF EXISTS idx_containers_project;
CREATE TABLE containers_cache (
    provider_id TEXT NOT NULL CHECK (length(trim(provider_id)) > 0),
    context_name TEXT NOT NULL CHECK (length(trim(context_name)) > 0),
    id TEXT NOT NULL,
    project_id TEXT,
    service_id TEXT,
    name TEXT NOT NULL,
    image_ref TEXT,
    image_id TEXT,
    status TEXT,
    state TEXT,
    health TEXT,
    restart_count INTEGER,
    ports_json TEXT,
    labels_json TEXT,
    created_at DATETIME,
    started_at DATETIME,
    last_seen_at DATETIME,
    PRIMARY KEY (provider_id, context_name, id)
);
CREATE INDEX idx_containers_project ON containers_cache(provider_id, context_name, project_id);
DROP TABLE containers_cache_unscoped;

ALTER TABLE images_cache RENAME TO images_cache_unscoped;
CREATE TABLE images_cache (
    provider_id TEXT NOT NULL CHECK (length(trim(provider_id)) > 0),
    context_name TEXT NOT NULL CHECK (length(trim(context_name)) > 0),
    id TEXT NOT NULL,
    repo_tags_json TEXT,
    repo_digests_json TEXT,
    size_bytes INTEGER,
    created_at DATETIME,
    used_by_json TEXT,
    dangling INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME,
    PRIMARY KEY (provider_id, context_name, id)
);
DROP TABLE images_cache_unscoped;

ALTER TABLE volumes_cache RENAME TO volumes_cache_unscoped;
CREATE TABLE volumes_cache (
    provider_id TEXT NOT NULL CHECK (length(trim(provider_id)) > 0),
    context_name TEXT NOT NULL CHECK (length(trim(context_name)) > 0),
    name TEXT NOT NULL,
    driver TEXT,
    mountpoint TEXT,
    labels_json TEXT,
    used_by_json TEXT,
    estimated_size_bytes INTEGER,
    created_at DATETIME,
    last_seen_at DATETIME,
    PRIMARY KEY (provider_id, context_name, name)
);
DROP TABLE volumes_cache_unscoped;

ALTER TABLE networks_cache RENAME TO networks_cache_unscoped;
CREATE TABLE networks_cache (
    provider_id TEXT NOT NULL CHECK (length(trim(provider_id)) > 0),
    context_name TEXT NOT NULL CHECK (length(trim(context_name)) > 0),
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    driver TEXT,
    scope TEXT,
    subnet TEXT,
    gateway TEXT,
    internal INTEGER,
    containers_json TEXT,
    labels_json TEXT,
    last_seen_at DATETIME,
    PRIMARY KEY (provider_id, context_name, id)
);
DROP TABLE networks_cache_unscoped;
