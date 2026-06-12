# 03 — Data Model (SQLite)

## 1. Storage strategy

SQLite is Cairn's local store for settings, provider state, cached Docker summaries, metrics history, lineage, update checks/history, backups metadata, command history, and audit log. **Docker is the source of truth for live state**; caches exist for fast cold render and offline display, and are reconciled from the Docker events stream + periodic full refresh.

- Driver: `modernc.org/sqlite` (no CGO). WAL mode, `busy_timeout=5000`, foreign keys ON.
- One DB file: `<app-data-dir>/cairn.db`. Migrations: embedded, sequential, forward-only (`store/migrations/NNNN_name.sql`), tracked in `schema_migrations`.
- All timestamps UTC ISO-8601 (`DATETIME` columns store text).
- JSON columns are suffixed `_json` and validated at write time.

## 2. Core tables

```sql
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at DATETIME NOT NULL
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,            -- JSON value
    updated_at DATETIME NOT NULL
);

CREATE TABLE providers (
    id TEXT PRIMARY KEY,            -- e.g. "linux_native", "wsl:Ubuntu", "ctx:my-remote"
    type TEXT NOT NULL,             -- linux_native|windows_wsl_ubuntu|macos_colima|existing_context|remote_ssh
    platform TEXT NOT NULL,         -- linux|windows|macos|any
    display_name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    config_json TEXT,               -- distro name, context name, colima profile, ssh host…
    last_status_json TEXT,          -- serialized ProviderStatus
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
    id TEXT PRIMARY KEY,            -- providerID + "/" + compose project name
    provider_id TEXT NOT NULL REFERENCES providers(id),
    context_name TEXT NOT NULL,
    name TEXT NOT NULL,
    working_dir TEXT,
    compose_files_json TEXT,        -- ["docker-compose.yml", ...]
    status TEXT,                    -- running|stopped|partial|error|unknown
    health TEXT,                    -- summary json
    source TEXT NOT NULL DEFAULT 'labels',  -- labels|compose_ls|imported
    pinned INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME,
    metadata_json TEXT
);

CREATE TABLE services (
    id TEXT PRIMARY KEY,            -- projectID + "/" + service name
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    image_ref TEXT,
    build_context TEXT,             -- NULL when service uses image: only
    dockerfile_path TEXT,
    build_target TEXT,
    status TEXT, health TEXT,
    replicas_running INTEGER, replicas_total INTEGER,
    metadata_json TEXT,
    last_seen_at DATETIME
);
```

## 3. Object caches

```sql
CREATE TABLE containers_cache (
    id TEXT PRIMARY KEY,            -- full container ID
    provider_id TEXT NOT NULL,
    project_id TEXT, service_id TEXT,
    name TEXT NOT NULL,
    image_ref TEXT, image_id TEXT,
    status TEXT, health TEXT,
    restart_count INTEGER,
    ports_json TEXT, labels_json TEXT,
    created_at DATETIME, started_at DATETIME,
    last_seen_at DATETIME
);
CREATE INDEX idx_containers_project ON containers_cache(project_id);

CREATE TABLE images_cache (
    id TEXT PRIMARY KEY,            -- image ID
    provider_id TEXT NOT NULL,
    repo_tags_json TEXT, repo_digests_json TEXT,
    size_bytes INTEGER, created_at DATETIME,
    used_by_json TEXT,              -- container IDs
    dangling INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME
);

CREATE TABLE volumes_cache (
    name TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    driver TEXT, mountpoint TEXT,
    labels_json TEXT, used_by_json TEXT,
    estimated_size_bytes INTEGER,   -- from docker system df -v; NULL if unknown
    created_at DATETIME, last_seen_at DATETIME,
    PRIMARY KEY (provider_id, name)
);

CREATE TABLE networks_cache (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    name TEXT NOT NULL,
    driver TEXT, scope TEXT, subnet TEXT, gateway TEXT,
    internal INTEGER, containers_json TEXT, labels_json TEXT,
    last_seen_at DATETIME
);
```

Cache reconciliation: rows not seen in a full refresh get `last_seen_at` checked; rows unseen for > 24 h are deleted (objects deleted while Cairn was closed).

## 4. Metrics

```sql
CREATE TABLE metrics_samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT, service_id TEXT, container_id TEXT,
    cpu_percent REAL,
    cpu_percent_max REAL,           -- NULL for raw rows; bucket max for downsampled rows
    memory_bytes INTEGER,
    memory_bytes_max INTEGER,       -- NULL for raw rows; bucket max for downsampled rows
    memory_limit_bytes INTEGER,
    network_rx_bytes INTEGER, network_tx_bytes INTEGER,   -- cumulative counters
    block_read_bytes INTEGER, block_write_bytes INTEGER,
    pids INTEGER,
    resolution TEXT NOT NULL DEFAULT 'raw',  -- raw|1m|15m
    sampled_at DATETIME NOT NULL
);
CREATE INDEX idx_metrics_container_time ON metrics_samples(container_id, sampled_at);
CREATE INDEX idx_metrics_project_time   ON metrics_samples(project_id, sampled_at);
CREATE INDEX idx_metrics_res_time       ON metrics_samples(resolution, sampled_at);
```

Retention (enforced hourly, see modules/05): raw kept 1 h → downsampled to 1 m rows kept 24 h → downsampled to 15 m rows kept 7 d → deleted. Aggregates store avg+max per window.

## 5. Lineage & updates

```sql
CREATE TABLE image_lineage (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT NOT NULL, service_id TEXT, service_name TEXT NOT NULL,
    container_id TEXT,
    service_image_ref TEXT, service_image_id TEXT, service_digest TEXT,
    build_context TEXT, dockerfile_path TEXT, build_target TEXT,
    dockerfile_hash TEXT, build_args_json TEXT,
    source TEXT NOT NULL,           -- compose_dockerfile|oci_annotation|cairn_label|unknown
    confidence TEXT NOT NULL,       -- high|medium|low|unknown
    discovered_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
);
CREATE INDEX idx_lineage_project   ON image_lineage(project_id);
CREATE INDEX idx_lineage_service   ON image_lineage(project_id, service_name);
CREATE INDEX idx_lineage_container ON image_lineage(container_id);

CREATE TABLE base_image_refs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    lineage_id INTEGER NOT NULL REFERENCES image_lineage(id) ON DELETE CASCADE,
    name TEXT NOT NULL, tag TEXT, image_ref TEXT NOT NULL,
    platform TEXT, stage_name TEXT, stage_index INTEGER,
    is_final_stage_base INTEGER NOT NULL DEFAULT 0,
    build_time_digest TEXT, local_digest TEXT, remote_digest TEXT,
    status TEXT NOT NULL,           -- UpdateStatus enum (04-api-contracts §6)
    last_checked_at DATETIME, error TEXT
);
CREATE INDEX idx_base_refs_lineage ON base_image_refs(lineage_id);
CREATE INDEX idx_base_refs_image   ON base_image_refs(image_ref);

CREATE TABLE image_update_checks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT, service_id TEXT, container_id TEXT,
    kind TEXT NOT NULL,             -- service_image|base_image
    image_ref TEXT NOT NULL, base_image_ref TEXT,
    local_image_id TEXT, local_digest TEXT, remote_digest TEXT,
    lineage_id INTEGER REFERENCES image_lineage(id),
    base_image_ref_id INTEGER REFERENCES base_image_refs(id),
    confidence TEXT, recommended_action TEXT,  -- pull_recreate|rebuild_redeploy|none|manual
    status TEXT NOT NULL,
    checked_at DATETIME NOT NULL, error TEXT
);
CREATE INDEX idx_checks_project ON image_update_checks(project_id, checked_at);
CREATE INDEX idx_checks_kind    ON image_update_checks(kind, status);

CREATE TABLE ignored_updates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    image_ref TEXT NOT NULL,
    update_kind TEXT NOT NULL DEFAULT 'service_image',
    base_image_ref TEXT, project_id TEXT, service_id TEXT,
    reason TEXT, created_at DATETIME NOT NULL
);

CREATE TABLE update_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT, service_id TEXT,
    update_kind TEXT NOT NULL,
    image_ref TEXT NOT NULL, base_image_ref TEXT,
    old_image_id TEXT, old_digest TEXT, old_base_digest TEXT,
    new_image_id TEXT, new_digest TEXT, new_base_digest TEXT,
    dockerfile_hash TEXT, build_args_json TEXT,
    commands_json TEXT,             -- exact commands run, ordered
    result TEXT NOT NULL,           -- success|success_warn|failed|rolled_back|manual_needed
    health_result TEXT, rollback_status TEXT,
    started_at DATETIME NOT NULL, finished_at DATETIME, error TEXT
);
```

## 6. Backups, history, audit, notifications

```sql
CREATE TABLE backups (
    id TEXT PRIMARY KEY,            -- UUIDv7
    provider_id TEXT NOT NULL,
    project_id TEXT, volume_name TEXT NOT NULL,
    backup_path TEXT NOT NULL, metadata_path TEXT,
    compressed_size_bytes INTEGER,
    result TEXT NOT NULL,           -- success|failed
    created_at DATETIME NOT NULL, error TEXT
);

CREATE TABLE command_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT, source TEXT NOT NULL DEFAULT 'app',  -- app|terminal|palette
    command TEXT NOT NULL, working_dir TEXT,
    exit_code INTEGER, duration_ms INTEGER,
    created_at DATETIME NOT NULL
);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,           -- e.g. container.stop, project.update, volume.delete
    target_type TEXT, target_id TEXT,
    provider_id TEXT, project_id TEXT,
    command TEXT,                   -- secrets redacted
    risk TEXT,                      -- safe|needs_confirmation|destructive|dangerous
    status TEXT NOT NULL,           -- started|success|failed|cancelled
    exit_code INTEGER, duration_ms INTEGER,
    error TEXT, created_at DATETIME NOT NULL
);
CREATE INDEX idx_audit_time ON audit_log(created_at);

CREATE TABLE notifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    level TEXT NOT NULL,            -- info|warn|error|success
    title TEXT NOT NULL, body TEXT,
    topic TEXT,                     -- update|provider|backup|system
    read INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL
);
```

## 7. Settings keys (normative list)

```text
general.theme                      "dark"|"light"|"system"   (default dark)
general.autostart_app             bool (default false)
general.language                  "en" (v1 en only)
provider.active_id                string
provider.autostart_backend        bool (default true)
updates.check_interval_hours      int (default 24; 0=manual only)
updates.notify                    bool (default true)
metrics.retention_raw_minutes     int (default 60)
metrics.sample_interval_seconds   int (default 2)
terminal.default_shell            string per provider
security.confirm_destructive      bool (always true; UI shows as locked in v1)
backups.directory                 string
registry.credentials_mode         "docker_helper"|"none" (default docker_helper)
windows.wsl_distro                string (default "Ubuntu")
linux.socket_path                 string (default /var/run/docker.sock)
linux.sudo_mode                   "ask"|"group"|"rootless"
macos.colima_profile              string (default "default")
macos.colima_cpu / _memory_gb / _disk_gb   ints
```
