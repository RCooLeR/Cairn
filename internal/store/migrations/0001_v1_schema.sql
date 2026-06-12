CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at DATETIME NOT NULL
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE providers (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    platform TEXT NOT NULL,
    display_name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    config_json TEXT,
    last_status_json TEXT,
    last_checked_at DATETIME
);

CREATE TABLE docker_contexts (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(id),
    name TEXT NOT NULL,
    docker_host TEXT,
    current INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT,
    last_seen_at DATETIME
);

CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(id),
    context_name TEXT NOT NULL,
    name TEXT NOT NULL,
    working_dir TEXT,
    compose_files_json TEXT,
    status TEXT,
    health TEXT,
    source TEXT NOT NULL DEFAULT 'labels',
    pinned INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME,
    metadata_json TEXT
);

CREATE TABLE services (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    image_ref TEXT,
    build_context TEXT,
    dockerfile_path TEXT,
    build_target TEXT,
    status TEXT,
    health TEXT,
    replicas_running INTEGER,
    replicas_total INTEGER,
    metadata_json TEXT,
    last_seen_at DATETIME
);

CREATE TABLE containers_cache (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    project_id TEXT,
    service_id TEXT,
    name TEXT NOT NULL,
    image_ref TEXT,
    image_id TEXT,
    status TEXT,
    health TEXT,
    restart_count INTEGER,
    ports_json TEXT,
    labels_json TEXT,
    created_at DATETIME,
    started_at DATETIME,
    last_seen_at DATETIME
);
CREATE INDEX idx_containers_project ON containers_cache(project_id);

CREATE TABLE images_cache (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    repo_tags_json TEXT,
    repo_digests_json TEXT,
    size_bytes INTEGER,
    created_at DATETIME,
    used_by_json TEXT,
    dangling INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME
);

CREATE TABLE volumes_cache (
    name TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    driver TEXT,
    mountpoint TEXT,
    labels_json TEXT,
    used_by_json TEXT,
    estimated_size_bytes INTEGER,
    created_at DATETIME,
    last_seen_at DATETIME,
    PRIMARY KEY (provider_id, name)
);

CREATE TABLE networks_cache (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    name TEXT NOT NULL,
    driver TEXT,
    scope TEXT,
    subnet TEXT,
    gateway TEXT,
    internal INTEGER,
    containers_json TEXT,
    labels_json TEXT,
    last_seen_at DATETIME
);

CREATE TABLE metrics_samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT,
    service_id TEXT,
    container_id TEXT,
    cpu_percent REAL,
    cpu_percent_max REAL,
    memory_bytes INTEGER,
    memory_bytes_max INTEGER,
    memory_limit_bytes INTEGER,
    network_rx_bytes INTEGER,
    network_tx_bytes INTEGER,
    block_read_bytes INTEGER,
    block_write_bytes INTEGER,
    pids INTEGER,
    resolution TEXT NOT NULL DEFAULT 'raw',
    sampled_at DATETIME NOT NULL
);
CREATE INDEX idx_metrics_container_time ON metrics_samples(container_id, sampled_at);
CREATE INDEX idx_metrics_project_time ON metrics_samples(project_id, sampled_at);
CREATE INDEX idx_metrics_res_time ON metrics_samples(resolution, sampled_at);

CREATE TABLE image_lineage (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    service_id TEXT,
    service_name TEXT NOT NULL,
    container_id TEXT,
    service_image_ref TEXT,
    service_image_id TEXT,
    service_digest TEXT,
    build_context TEXT,
    dockerfile_path TEXT,
    build_target TEXT,
    dockerfile_hash TEXT,
    build_args_json TEXT,
    source TEXT NOT NULL,
    confidence TEXT NOT NULL,
    discovered_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX idx_lineage_project ON image_lineage(project_id);
CREATE INDEX idx_lineage_service ON image_lineage(project_id, service_name);
CREATE INDEX idx_lineage_container ON image_lineage(container_id);

CREATE TABLE base_image_refs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    lineage_id INTEGER NOT NULL REFERENCES image_lineage(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    tag TEXT,
    image_ref TEXT NOT NULL,
    platform TEXT,
    stage_name TEXT,
    stage_index INTEGER,
    is_final_stage_base INTEGER NOT NULL DEFAULT 0,
    build_time_digest TEXT,
    local_digest TEXT,
    remote_digest TEXT,
    status TEXT NOT NULL,
    last_checked_at DATETIME,
    error TEXT
);
CREATE INDEX idx_base_refs_lineage ON base_image_refs(lineage_id);
CREATE INDEX idx_base_refs_image ON base_image_refs(image_ref);

CREATE TABLE image_update_checks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT,
    service_id TEXT,
    container_id TEXT,
    kind TEXT NOT NULL,
    image_ref TEXT NOT NULL,
    base_image_ref TEXT,
    local_image_id TEXT,
    local_digest TEXT,
    remote_digest TEXT,
    lineage_id INTEGER REFERENCES image_lineage(id),
    base_image_ref_id INTEGER REFERENCES base_image_refs(id),
    confidence TEXT,
    recommended_action TEXT,
    status TEXT NOT NULL,
    checked_at DATETIME NOT NULL,
    error TEXT
);
CREATE INDEX idx_checks_project ON image_update_checks(project_id, checked_at);
CREATE INDEX idx_checks_kind ON image_update_checks(kind, status);

CREATE TABLE ignored_updates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    image_ref TEXT NOT NULL,
    update_kind TEXT NOT NULL DEFAULT 'service_image',
    base_image_ref TEXT,
    project_id TEXT,
    service_id TEXT,
    reason TEXT,
    created_at DATETIME NOT NULL
);

CREATE TABLE update_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT,
    service_id TEXT,
    update_kind TEXT NOT NULL,
    image_ref TEXT NOT NULL,
    base_image_ref TEXT,
    old_image_id TEXT,
    old_digest TEXT,
    old_base_digest TEXT,
    new_image_id TEXT,
    new_digest TEXT,
    new_base_digest TEXT,
    dockerfile_hash TEXT,
    build_args_json TEXT,
    commands_json TEXT,
    result TEXT NOT NULL,
    health_result TEXT,
    rollback_status TEXT,
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    error TEXT
);

CREATE TABLE backups (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    project_id TEXT,
    volume_name TEXT NOT NULL,
    backup_path TEXT NOT NULL,
    metadata_path TEXT,
    compressed_size_bytes INTEGER,
    result TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    error TEXT
);

CREATE TABLE command_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT,
    source TEXT NOT NULL DEFAULT 'app',
    command TEXT NOT NULL,
    working_dir TEXT,
    exit_code INTEGER,
    duration_ms INTEGER,
    created_at DATETIME NOT NULL
);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    provider_id TEXT,
    project_id TEXT,
    command TEXT,
    risk TEXT,
    status TEXT NOT NULL,
    exit_code INTEGER,
    duration_ms INTEGER,
    error TEXT,
    created_at DATETIME NOT NULL
);
CREATE INDEX idx_audit_time ON audit_log(created_at);

CREATE TABLE notifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    level TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT,
    topic TEXT,
    read INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL
);
