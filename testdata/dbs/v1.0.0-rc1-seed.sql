-- Seed data for the v1.0.0-rc1 upgrade-path fixture.
-- Tests create the fixture database with the v1 schema, apply these rows,
-- then reopen it through the current migrator and verify data survives.

UPDATE settings
SET value = '"light"', updated_at = '2026-06-14T00:00:00Z'
WHERE key = 'general.theme';

UPDATE settings
SET value = '"cairn-dev"', updated_at = '2026-06-14T00:00:00Z'
WHERE key = 'windows.wsl_distro';

DELETE FROM settings
WHERE key = 'macos.colima_disk_gb';

INSERT INTO providers (id, type, platform, display_name, enabled, config_json, last_status_json, last_checked_at)
VALUES (
  'linux_native',
  'linux_native',
  'linux',
  'Linux native',
  1,
  '{"socket":"/var/run/docker.sock"}',
  '{"state":"healthy","dockerVersion":"29.5.3","composeVersion":"5.1.4"}',
  '2026-06-14T00:01:00Z'
);

INSERT INTO docker_contexts (id, provider_id, name, docker_host, current, metadata_json, last_seen_at)
VALUES (
  'linux_native/default',
  'linux_native',
  'default',
  'unix:///var/run/docker.sock',
  1,
  '{"description":"default local socket"}',
  '2026-06-14T00:02:00Z'
);

INSERT INTO projects (id, provider_id, context_name, name, working_dir, compose_files_json, status, health, source, pinned, last_seen_at, metadata_json)
VALUES (
  'linux_native/app-db',
  'linux_native',
  'default',
  'app-db',
  '/home/alice/projects/app-db',
  '["compose.yaml"]',
  'running',
  'healthy',
  'imported',
  1,
  '2026-06-14T00:03:00Z',
  '{"services":2}'
);

INSERT INTO services (id, project_id, name, image_ref, build_context, dockerfile_path, build_target, status, health, replicas_running, replicas_total, metadata_json, last_seen_at)
VALUES (
  'linux_native/app-db/web',
  'linux_native/app-db',
  'web',
  'nginx:1.25',
  NULL,
  NULL,
  NULL,
  'running',
  'healthy',
  1,
  1,
  '{"ports":["8080:80"]}',
  '2026-06-14T00:04:00Z'
);

INSERT INTO containers_cache (id, provider_id, project_id, service_id, name, image_ref, image_id, status, health, restart_count, ports_json, labels_json, created_at, started_at, last_seen_at)
VALUES (
  'container-web',
  'linux_native',
  'linux_native/app-db',
  'linux_native/app-db/web',
  'app-db-web-1',
  'nginx:1.25',
  'sha256:local-old',
  'running',
  'healthy',
  0,
  '[{"private":80,"public":8080}]',
  '{"com.docker.compose.project":"app-db"}',
  '2026-06-14T00:04:30Z',
  '2026-06-14T00:04:35Z',
  '2026-06-14T00:05:00Z'
);

INSERT INTO images_cache (id, provider_id, repo_tags_json, repo_digests_json, size_bytes, created_at, used_by_json, dangling, last_seen_at)
VALUES (
  'sha256:local-old',
  'linux_native',
  '["nginx:1.25"]',
  '["nginx@sha256:old"]',
  187000000,
  '2026-06-14T00:04:00Z',
  '["container-web"]',
  0,
  '2026-06-14T00:05:00Z'
);

INSERT INTO volumes_cache (name, provider_id, driver, mountpoint, labels_json, used_by_json, estimated_size_bytes, created_at, last_seen_at)
VALUES (
  'app-db-data',
  'linux_native',
  'local',
  '/var/lib/docker/volumes/app-db-data/_data',
  '{"com.docker.compose.project":"app-db"}',
  '["container-web"]',
  4096,
  '2026-06-14T00:04:00Z',
  '2026-06-14T00:05:00Z'
);

INSERT INTO networks_cache (id, provider_id, name, driver, scope, subnet, gateway, internal, containers_json, labels_json, last_seen_at)
VALUES (
  'network-app-db',
  'linux_native',
  'app-db_default',
  'bridge',
  'local',
  '172.30.0.0/16',
  '172.30.0.1',
  0,
  '{"container-web":{"Name":"app-db-web-1"}}',
  '{"com.docker.compose.project":"app-db"}',
  '2026-06-14T00:05:00Z'
);

INSERT INTO metrics_samples (provider_id, project_id, service_id, container_id, cpu_percent, cpu_percent_max, memory_bytes, memory_bytes_max, memory_limit_bytes, network_rx_bytes, network_tx_bytes, block_read_bytes, block_write_bytes, pids, resolution, sampled_at)
VALUES (
  'linux_native',
  'linux_native/app-db',
  'linux_native/app-db/web',
  'container-web',
  12.5,
  25.0,
  67108864,
  134217728,
  536870912,
  1024,
  2048,
  4096,
  8192,
  7,
  'raw',
  '2026-06-14T00:05:00Z'
);

INSERT INTO image_lineage (id, provider_id, project_id, service_id, service_name, container_id, service_image_ref, service_image_id, service_digest, build_context, dockerfile_path, build_target, dockerfile_hash, build_args_json, source, confidence, discovered_at, updated_at)
VALUES (
  1,
  'linux_native',
  'linux_native/app-db',
  'linux_native/app-db/web',
  'web',
  'container-web',
  'nginx:1.25',
  'sha256:local-old',
  'sha256:old',
  NULL,
  NULL,
  NULL,
  NULL,
  NULL,
  'compose',
  'high',
  '2026-06-14T00:06:00Z',
  '2026-06-14T00:06:00Z'
);

INSERT INTO base_image_refs (id, lineage_id, name, tag, image_ref, platform, stage_name, stage_index, is_final_stage_base, build_time_digest, local_digest, remote_digest, status, last_checked_at, error)
VALUES (
  1,
  1,
  'nginx',
  '1.25',
  'nginx:1.25',
  'linux/amd64',
  NULL,
  0,
  1,
  'sha256:base-old',
  'sha256:base-old',
  'sha256:base-new',
  'base_image_update_available',
  '2026-06-14T00:07:00Z',
  NULL
);

INSERT INTO image_update_checks (provider_id, project_id, service_id, container_id, kind, image_ref, base_image_ref, local_image_id, local_digest, remote_digest, lineage_id, base_image_ref_id, confidence, recommended_action, status, checked_at, error)
VALUES (
  'linux_native',
  'linux_native/app-db',
  'linux_native/app-db/web',
  'container-web',
  'service_image',
  'nginx:1.25',
  NULL,
  'sha256:local-old',
  'sha256:old',
  'sha256:new',
  1,
  NULL,
  'high',
  'pull',
  'service_image_update_available',
  '2026-06-14T00:08:00Z',
  NULL
);

INSERT INTO ignored_updates (provider_id, image_ref, update_kind, base_image_ref, project_id, service_id, reason, created_at)
VALUES (
  'linux_native',
  'postgres:16',
  'service_image',
  NULL,
  'linux_native/app-db',
  NULL,
  'release fixture',
  '2026-06-14T00:09:00Z'
);

INSERT INTO update_history (provider_id, project_id, service_id, update_kind, image_ref, base_image_ref, old_image_id, old_digest, old_base_digest, new_image_id, new_digest, new_base_digest, dockerfile_hash, build_args_json, commands_json, result, health_result, rollback_status, started_at, finished_at, error)
VALUES (
  'linux_native',
  'linux_native/app-db',
  'linux_native/app-db/web',
  'service_image',
  'nginx:1.25',
  NULL,
  'sha256:local-old',
  'sha256:old',
  NULL,
  'sha256:local-new',
  'sha256:new',
  NULL,
  NULL,
  NULL,
  '["docker compose pull web","docker compose up -d web"]',
  'success',
  'healthy',
  NULL,
  '2026-06-14T00:10:00Z',
  '2026-06-14T00:11:00Z',
  NULL
);

INSERT INTO backups (id, provider_id, project_id, volume_name, backup_path, metadata_path, compressed_size_bytes, result, created_at, error)
VALUES (
  'backup-app-db-data',
  'linux_native',
  'linux_native/app-db',
  'app-db-data',
  '/home/alice/backups/app-db-data.tar.gz',
  '/home/alice/backups/app-db-data.json',
  1024,
  'success',
  '2026-06-14T00:12:00Z',
  NULL
);

INSERT INTO command_history (provider_id, source, command, working_dir, exit_code, duration_ms, created_at)
VALUES (
  'linux_native',
  'terminal',
  'docker compose ps',
  '/home/alice/projects/app-db',
  0,
  120,
  '2026-06-14T00:13:00Z'
);

INSERT INTO audit_log (action, target_type, target_id, provider_id, project_id, command, risk, status, exit_code, duration_ms, error, created_at)
VALUES (
  'update.apply',
  'service',
  'linux_native/app-db/web',
  'linux_native',
  'linux_native/app-db',
  'docker compose up -d web',
  'needs_confirmation',
  'success',
  0,
  2200,
  NULL,
  '2026-06-14T00:14:00Z'
);

INSERT INTO notifications (level, title, body, topic, read, created_at)
VALUES (
  'info',
  'Update applied',
  'web moved to sha256:new',
  'updates',
  0,
  '2026-06-14T00:15:00Z'
);
