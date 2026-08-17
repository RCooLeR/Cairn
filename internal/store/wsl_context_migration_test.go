package store

import (
	"context"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestWSLContextCanonicalizationMigrationPreservesScopedProjectGraph(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreBeforeLatestMigration(t, ctx)
	defer closeStore(t, db)

	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO providers (id, type, platform, display_name)
		VALUES ('windows_wsl_ubuntu', 'windows_wsl_ubuntu', 'windows', 'Windows WSL Ubuntu');

		INSERT INTO projects (
			id, provider_id, context_name, name, working_dir, compose_files_json,
			status, health, source, last_seen_at, metadata_json
		) VALUES (
			'windows_wsl_ubuntu/demo', 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'demo',
			'E:\\demo', '["E:\\\\demo\\\\compose.yaml"]', 'running', 'healthy',
			'labels', '2026-07-18T12:00:00Z', '{}'
		);

		INSERT INTO services (
			id, project_id, name, image_ref, status, health,
			replicas_running, replicas_total, metadata_json, last_seen_at
		) VALUES (
			'windows_wsl_ubuntu/demo/web', 'windows_wsl_ubuntu/demo', 'web',
			'nginx:alpine', 'running', 'healthy', 1, 1, '{}', '2026-07-18T12:00:00Z'
		);

		INSERT INTO forgotten_projects (
			provider_id, context_name, name, project_id, forgotten_at
		) VALUES (
			'windows_wsl_ubuntu', 'wsl:Ubuntu', 'retired',
			'windows_wsl_ubuntu/retired', '2026-07-18T12:00:00Z'
		);

		INSERT INTO containers_cache (
			provider_id, context_name, id, project_id, service_id, name,
			image_ref, status, state, health, labels_json, last_seen_at
		) VALUES (
			'windows_wsl_ubuntu', 'wsl:Ubuntu', 'container-web',
			'windows_wsl_ubuntu/demo', 'windows_wsl_ubuntu/demo/web', 'demo-web-1',
			'nginx:alpine', 'running', 'running', 'healthy',
			'{"com.docker.compose.project":"demo"}', '2026-07-18T12:00:00Z'
		);

		INSERT INTO images_cache (
			provider_id, context_name, id, repo_tags_json, last_seen_at
		) VALUES (
			'windows_wsl_ubuntu', 'wsl:Ubuntu', 'image-nginx',
			'["nginx:alpine"]', '2026-07-18T12:00:00Z'
		);

		INSERT INTO volumes_cache (
			provider_id, context_name, name, driver, last_seen_at
		) VALUES (
			'windows_wsl_ubuntu', 'wsl:Ubuntu', 'demo-data', 'local',
			'2026-07-18T12:00:00Z'
		);

		INSERT INTO networks_cache (
			provider_id, context_name, id, name, driver, last_seen_at
		) VALUES (
			'windows_wsl_ubuntu', 'wsl:Ubuntu', 'network-demo', 'demo_default',
			'bridge', '2026-07-18T12:00:00Z'
		);

		INSERT INTO metrics_samples (
			id, provider_id, context_name, project_id, service_id, container_id,
			cpu_percent, resolution, sampled_at
		) VALUES (
			701, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
			'windows_wsl_ubuntu/demo/web', 'container-web', 12.5, 'raw',
			'2026-07-18T12:00:00Z'
		);

		INSERT INTO metrics_samples (
			id, provider_id, context_name, project_id, resolution, sampled_at
		) VALUES (
			702, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/orphan',
			'raw', '2026-07-18T12:00:00Z'
		);

		INSERT INTO image_lineage (
			id, provider_id, context_name, project_id, service_id, service_name,
			service_image_ref, source, confidence, discovered_at, updated_at
		) VALUES (
			101, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
			'windows_wsl_ubuntu/demo/web', 'web', 'nginx:alpine', 'compose', 'high',
			'2026-07-18T12:00:00Z', '2026-07-18T12:00:00Z'
		);

		INSERT INTO base_image_refs (
			id, lineage_id, name, image_ref, stage_index, status
		) VALUES (201, 101, 'alpine', 'alpine:latest', 0, 'current');

		INSERT INTO image_update_checks (
			id, provider_id, context_name, project_id, service_id, kind, image_ref,
			lineage_id, base_image_ref_id, status, checked_at, generation_id, is_current
		) VALUES (
			301, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
			'windows_wsl_ubuntu/demo/web', 'service_image', 'nginx:alpine',
			101, 201, 'current', '2026-07-18T12:00:00Z', 401, 1
		);

		INSERT INTO ignored_updates (
			id, provider_id, context_name, image_ref, update_kind,
			project_id, service_id, reason, created_at
		) VALUES (
			501, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'nginx:alpine', 'service_image',
			'windows_wsl_ubuntu/demo', 'windows_wsl_ubuntu/demo/web', 'pinned',
			'2026-07-18T12:00:00Z'
		);

		INSERT INTO update_history (
			id, provider_id, context_name, project_id, service_id,
			update_kind, image_ref, result, started_at, finished_at
		) VALUES (
			601, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
			'windows_wsl_ubuntu/demo/web', 'service_image', 'nginx:alpine', 'success',
			'2026-07-18T12:00:00Z', '2026-07-18T12:01:00Z'
		);

		INSERT INTO image_update_check_runs (
			id, provider_id, context_name, project_id, service_id,
			started_at, finished_at, status
		) VALUES (
			401, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo', '',
			'2026-07-18T12:00:00Z', '2026-07-18T12:01:00Z', 'published'
		);

		INSERT INTO image_update_check_heads (
			provider_id, context_name, project_id, service_id, last_run_id
		) VALUES (
			'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo', '', 401
		);
	`); err != nil {
		t.Fatalf("seed legacy WSL project graph: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate legacy WSL project graph: %v", err)
	}

	canonicalScope := runtimescope.Must("windows_wsl_ubuntu", "wsl:ubuntu")
	projects, err := db.Projects().ListByScope(ctx, canonicalScope)
	if err != nil {
		t.Fatalf("ListByScope(canonical): %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "windows_wsl_ubuntu/demo" {
		t.Fatalf("canonical projects = %#v, want migrated demo", projects)
	}
	services, err := db.Projects().ListServices(ctx, "windows_wsl_ubuntu/demo")
	if err != nil || len(services) != 1 || services[0].ID != "windows_wsl_ubuntu/demo/web" {
		t.Fatalf("migrated project services = %#v, %v", services, err)
	}

	for _, table := range []string{
		"projects",
		"forgotten_projects",
		"metrics_samples",
		"image_lineage",
		"image_update_checks",
		"ignored_updates",
		"update_history",
		"image_update_check_runs",
		"image_update_check_heads",
	} {
		var count int
		if err := db.writer.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM `+table+`
			WHERE provider_id = 'windows_wsl_ubuntu'
				AND context_name = 'wsl:ubuntu'
		`).Scan(&count); err != nil {
			t.Fatalf("count canonical rows in %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("canonical rows in %s = %d, want 1", table, count)
		}
	}
	for _, table := range []string{"containers_cache", "images_cache", "volumes_cache", "networks_cache"} {
		if got := queryInt64(t, ctx, db, `
			SELECT COUNT(*) FROM `+table+`
			WHERE provider_id = 'windows_wsl_ubuntu'
		`); got != 0 {
			t.Fatalf("reconstructible legacy rows in %s = %d, want discarded", table, got)
		}
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM metrics_samples
		WHERE id = 702 AND context_name = 'wsl:Ubuntu'
	`); got != 1 {
		t.Fatalf("orphan scoped metric count = %d, want quarantined legacy row", got)
	}
	if got := queryInt64(t, ctx, db, "SELECT COUNT(*) FROM base_image_refs WHERE id = 201 AND lineage_id = 101"); got != 1 {
		t.Fatalf("related base image reference count = %d, want 1", got)
	}

	now := time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	skipped, err := db.Projects().SaveDetectedSnapshot(ctx, canonicalScope, []ProjectRecord{{
		ID:          "windows_wsl_ubuntu/demo",
		ProviderID:  canonicalScope.ProviderID(),
		ContextName: canonicalScope.ContextName(),
		Name:        "demo",
		Status:      models.ProjectStatusRunning,
		Health:      models.HealthStatusHealthy,
		Source:      ProjectSourceLabels,
		LastSeenAt:  now,
	}}, nil, now, time.Time{})
	if err != nil {
		t.Fatalf("SaveDetectedSnapshot(canonical same ID): %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("SaveDetectedSnapshot(canonical same ID) skipped = %v, want none", skipped)
	}
}

func TestWSLContextCanonicalizationMigrationLeavesAmbiguousCaseCollisionsIsolated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreBeforeLatestMigration(t, ctx)
	defer closeStore(t, db)

	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO providers (id, type, platform, display_name)
		VALUES ('windows_wsl_ubuntu', 'windows_wsl_ubuntu', 'windows', 'Windows WSL Ubuntu');

		INSERT INTO projects (id, provider_id, context_name, name, source)
		VALUES
			('windows_wsl_ubuntu/legacy-demo', 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'demo', 'labels'),
			('windows_wsl_ubuntu/canonical-demo', 'windows_wsl_ubuntu', 'wsl:ubuntu', 'demo', 'labels');

		INSERT INTO metrics_samples (
			id, provider_id, context_name, project_id, resolution, sampled_at
		) VALUES (
			801, 'windows_wsl_ubuntu', 'wsl:Ubuntu',
			'windows_wsl_ubuntu/legacy-demo', 'raw', '2026-07-18T12:00:00Z'
		);

		INSERT INTO containers_cache (provider_id, context_name, id, name)
		VALUES
			('windows_wsl_ubuntu', 'wsl:Ubuntu', 'shared-container', 'legacy'),
			('windows_wsl_ubuntu', 'wsl:ubuntu', 'shared-container', 'canonical');

		INSERT INTO images_cache (provider_id, context_name, id, repo_tags_json)
		VALUES
			('windows_wsl_ubuntu', 'wsl:Ubuntu', 'shared-image', '["legacy"]'),
			('windows_wsl_ubuntu', 'wsl:ubuntu', 'shared-image', '["canonical"]');

		INSERT INTO volumes_cache (provider_id, context_name, name, driver)
		VALUES
			('windows_wsl_ubuntu', 'wsl:Ubuntu', 'shared-volume', 'legacy'),
			('windows_wsl_ubuntu', 'wsl:ubuntu', 'shared-volume', 'canonical');

		INSERT INTO networks_cache (provider_id, context_name, id, name, driver)
		VALUES
			('windows_wsl_ubuntu', 'wsl:Ubuntu', 'shared-network', 'legacy', 'bridge'),
			('windows_wsl_ubuntu', 'wsl:ubuntu', 'shared-network', 'canonical', 'bridge');

		INSERT INTO forgotten_projects (
			provider_id, context_name, name, project_id, forgotten_at
		) VALUES
			('windows_wsl_ubuntu', 'wsl:Ubuntu', 'shared', 'legacy-id', '2026-07-18T12:00:00Z'),
			('windows_wsl_ubuntu', 'wsl:ubuntu', 'shared', 'canonical-id', '2026-07-18T12:00:00Z');

		INSERT INTO ignored_updates (
			provider_id, context_name, image_ref, update_kind, reason, created_at
		) VALUES
			('windows_wsl_ubuntu', 'wsl:Ubuntu', 'nginx:latest', 'service_image', 'legacy', '2026-07-18T12:00:00Z'),
			('windows_wsl_ubuntu', 'wsl:ubuntu', 'nginx:latest', 'service_image', 'canonical', '2026-07-18T12:00:00Z');

		INSERT INTO image_update_check_runs (
			id, provider_id, context_name, project_id, service_id,
			started_at, finished_at, status
		) VALUES
			(901, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/canonical-demo', '',
			 '2026-07-18T12:00:00Z', '2026-07-18T12:01:00Z', 'published'),
			(902, 'windows_wsl_ubuntu', 'wsl:ubuntu', 'windows_wsl_ubuntu/canonical-demo', '',
			 '2026-07-18T12:02:00Z', '2026-07-18T12:03:00Z', 'published');

		INSERT INTO image_update_check_heads (
			provider_id, context_name, project_id, service_id, last_run_id
		) VALUES
			('windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/canonical-demo', '', 901),
			('windows_wsl_ubuntu', 'wsl:ubuntu', 'windows_wsl_ubuntu/canonical-demo', '', 902);

		INSERT INTO image_update_checks (
			id, provider_id, context_name, project_id, kind, image_ref,
			status, checked_at, generation_id, is_current
		) VALUES
			(903, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/canonical-demo',
			 'service_image', 'nginx:1.26', 'update_available', '2026-07-18T12:01:00Z', 901, 1),
			(904, 'windows_wsl_ubuntu', 'wsl:ubuntu', 'windows_wsl_ubuntu/canonical-demo',
			 'service_image', 'nginx:1.27', 'update_available', '2026-07-18T12:03:00Z', 902, 1);
	`); err != nil {
		t.Fatalf("seed WSL case collisions: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate WSL case collisions: %v", err)
	}

	for _, table := range []string{"projects", "metrics_samples"} {
		var count int
		if err := db.writer.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM `+table+`
			WHERE provider_id = 'windows_wsl_ubuntu'
				AND context_name = 'wsl:Ubuntu'
		`).Scan(&count); err != nil {
			t.Fatalf("count isolated legacy rows in %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("isolated legacy rows in %s = %d, want 1", table, count)
		}
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM forgotten_projects
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND context_name = 'wsl:ubuntu'
			AND name = 'shared'
	`); got != 1 {
		t.Fatalf("merged canonical forgotten project count = %d, want 1", got)
	}
	for _, table := range []string{"containers_cache", "images_cache", "volumes_cache", "networks_cache"} {
		if got := queryInt64(t, ctx, db, `
			SELECT COUNT(*) FROM `+table+`
			WHERE provider_id = 'windows_wsl_ubuntu'
				AND context_name = 'wsl:Ubuntu'
		`); got != 0 {
			t.Fatalf("noncanonical derived-cache rows in %s = %d, want discarded", table, got)
		}
		if got := queryInt64(t, ctx, db, `
			SELECT COUNT(*) FROM `+table+`
			WHERE provider_id = 'windows_wsl_ubuntu'
				AND context_name = 'wsl:ubuntu'
		`); got != 1 {
			t.Fatalf("canonical derived-cache rows in %s = %d, want preserved", table, got)
		}
	}
	if got := queryString(t, ctx, db, `
		SELECT name FROM containers_cache
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND context_name = 'wsl:ubuntu'
			AND id = 'shared-container'
	`); got != "canonical" {
		t.Fatalf("canonical collision row = %q, want canonical", got)
	}
	if got := queryString(t, ctx, db, `
		SELECT reason FROM ignored_updates
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND context_name = 'wsl:ubuntu'
			AND image_ref = 'nginx:latest'
	`); got != "canonical" {
		t.Fatalf("canonical ignored-update collision row = %q, want canonical", got)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM image_update_check_heads
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND project_id = 'windows_wsl_ubuntu/canonical-demo'
	`); got != 0 {
		t.Fatalf("ambiguous update heads = %d, want cleared", got)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM image_update_checks
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND project_id = 'windows_wsl_ubuntu/canonical-demo'
			AND is_current = 1
	`); got != 0 {
		t.Fatalf("ambiguous current update checks = %d, want demoted", got)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM image_update_checks
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND context_name = 'wsl:ubuntu'
			AND project_id = 'windows_wsl_ubuntu/canonical-demo'
	`); got != 2 {
		t.Fatalf("canonicalized ambiguous update checks = %d, want 2 historical rows", got)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM image_update_check_runs
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND context_name = 'wsl:ubuntu'
			AND project_id = 'windows_wsl_ubuntu/canonical-demo'
	`); got != 2 {
		t.Fatalf("canonicalized ambiguous update runs = %d, want 2 preserved rows", got)
	}
}

func TestWSLContextCanonicalizationMigrationMergesDenyAliasesAndQuarantinesInvalidServices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreBeforeLatestMigration(t, ctx)
	defer closeStore(t, db)

	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO providers (id, type, platform, display_name)
		VALUES ('windows_wsl_ubuntu', 'windows_wsl_ubuntu', 'windows', 'Windows WSL Ubuntu');

		INSERT INTO projects (id, provider_id, context_name, name, source)
		VALUES ('windows_wsl_ubuntu/demo', 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'demo', 'labels');

		INSERT INTO forgotten_projects (
			provider_id, context_name, name, project_id, forgotten_at
		) VALUES
			('windows_wsl_ubuntu', 'wsl:Ubuntu', 'retired', 'older-id', '2026-07-18T12:00:00Z'),
			('windows_wsl_ubuntu', 'WSL:UBUNTU', 'retired', 'newer-id', '2026-07-18T13:00:00Z');

		INSERT INTO ignored_updates (
			provider_id, context_name, image_ref, update_kind, reason, created_at
		) VALUES
			('windows_wsl_ubuntu', 'wsl:Ubuntu', 'alpine:latest', 'service_image', 'older', '2026-07-18T12:00:00Z'),
			('windows_wsl_ubuntu', 'WSL:UBUNTU', 'alpine:latest', 'service_image', 'newer', '2026-07-18T13:00:00Z');

		INSERT INTO ignored_updates (
			provider_id, context_name, image_ref, update_kind, project_id, service_id,
			reason, created_at
		) VALUES
			(
				'windows_wsl_ubuntu', 'wsl:Ubuntu', 'nginx:latest', 'service_image',
				'windows_wsl_ubuntu/demo', 'windows_wsl_ubuntu/demo/missing',
				'invalid-service-older', '2026-07-18T12:00:00Z'
			),
			(
				'windows_wsl_ubuntu', 'WSL:UBUNTU', 'nginx:latest', 'service_image',
				'windows_wsl_ubuntu/demo', 'windows_wsl_ubuntu/demo/missing',
				'invalid-service-newer', '2026-07-18T13:00:00Z'
			);

		INSERT INTO metrics_samples (
			id, provider_id, context_name, project_id, service_id, resolution, sampled_at
		) VALUES (
			1101, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
			'windows_wsl_ubuntu/demo/missing', 'raw', '2026-07-18T12:00:00Z'
		);

		INSERT INTO update_history (
			id, provider_id, context_name, project_id, service_id,
			update_kind, image_ref, result, started_at
		) VALUES (
			1102, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
			'windows_wsl_ubuntu/demo/missing', 'service_image', 'nginx:latest',
			'success', '2026-07-18T12:00:00Z'
		);
	`); err != nil {
		t.Fatalf("seed WSL deny aliases and invalid services: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate WSL deny aliases and invalid services: %v", err)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM forgotten_projects
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND context_name = 'wsl:ubuntu'
			AND name = 'retired'
	`); got != 1 {
		t.Fatalf("canonical forgotten alias count = %d, want 1", got)
	}
	if got := queryString(t, ctx, db, `
		SELECT project_id FROM forgotten_projects
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND context_name = 'wsl:ubuntu'
			AND name = 'retired'
	`); got != "newer-id" {
		t.Fatalf("retained forgotten alias = %q, want newer-id", got)
	}
	if got := queryString(t, ctx, db, `
		SELECT reason FROM ignored_updates
		WHERE provider_id = 'windows_wsl_ubuntu'
			AND context_name = 'wsl:ubuntu'
			AND image_ref = 'alpine:latest'
	`); got != "newer" {
		t.Fatalf("retained ignored alias = %q, want newer", got)
	}
	for _, assertion := range []struct {
		table string
		id    int
	}{
		{table: "metrics_samples", id: 1101},
		{table: "update_history", id: 1102},
	} {
		if got := queryString(t, ctx, db, `
			SELECT context_name FROM `+assertion.table+` WHERE id = ?
		`, assertion.id); got != "wsl:Ubuntu" {
			t.Fatalf("invalid-service %s context = %q, want quarantined wsl:Ubuntu", assertion.table, got)
		}
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM ignored_updates
		WHERE image_ref = 'nginx:latest'
			AND context_name IN ('wsl:Ubuntu', 'WSL:UBUNTU')
	`); got != 2 {
		t.Fatalf("quarantined invalid-service ignored aliases = %d, want both 2", got)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*) FROM ignored_updates
		WHERE image_ref = 'nginx:latest' AND context_name = 'wsl:ubuntu'
	`); got != 0 {
		t.Fatalf("claimed invalid-service ignored aliases = %d, want 0", got)
	}
}

func TestWSLContextCanonicalizationMigrationQuarantinesCrossScopeUpdateReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreBeforeLatestMigration(t, ctx)
	defer closeStore(t, db)

	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO providers (id, type, platform, display_name)
		VALUES ('windows_wsl_ubuntu', 'windows_wsl_ubuntu', 'windows', 'Windows WSL Ubuntu');

		INSERT INTO projects (id, provider_id, context_name, name, source)
		VALUES ('windows_wsl_ubuntu/demo', 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'demo', 'labels');

		INSERT INTO services (id, project_id, name, image_ref, status, health)
		VALUES (
			'windows_wsl_ubuntu/demo/web', 'windows_wsl_ubuntu/demo', 'web',
			'nginx:alpine', 'running', 'healthy'
		);

		-- The lineage and its base ref are physically valid, but the missing
		-- service makes their logical runtime ownership invalid.
		INSERT INTO image_lineage (
			id, provider_id, context_name, project_id, service_id, service_name,
			service_image_ref, source, confidence, discovered_at, updated_at
		) VALUES (
			2001, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
			'windows_wsl_ubuntu/demo/missing', 'missing', 'nginx:alpine',
			'compose', 'high', '2026-07-18T12:00:00Z', '2026-07-18T12:00:00Z'
		);

		INSERT INTO base_image_refs (id, lineage_id, name, image_ref, stage_index, status)
		VALUES (2002, 2001, 'alpine', 'alpine:latest', 0, 'current');

		INSERT INTO image_update_check_runs (
			id, provider_id, context_name, project_id, service_id,
			started_at, finished_at, status
		) VALUES
			(
				2003, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
				'windows_wsl_ubuntu/demo/missing', '2026-07-18T12:00:00Z',
				'2026-07-18T12:01:00Z', 'published'
			),
			(
				2005, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
				'windows_wsl_ubuntu/demo/web', '2026-07-18T12:02:00Z',
				'2026-07-18T12:03:00Z', 'published'
			);

		-- This check has a valid direct owner but points at quarantined lineage
		-- and generation rows, so it must not be claimed by the canonical scope.
		INSERT INTO image_update_checks (
			id, provider_id, context_name, project_id, service_id, kind, image_ref,
			lineage_id, base_image_ref_id, status, checked_at, generation_id, is_current
		) VALUES (
			2004, 'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo',
			'windows_wsl_ubuntu/demo/web', 'base_image', 'alpine:latest',
			2001, 2002, 'current', '2026-07-18T12:01:00Z', 2003, 1
		);

		-- The referenced run is valid and will migrate, but it is a service run;
		-- a project-wide head must not claim it as its own generation.
		INSERT INTO image_update_check_heads (
			provider_id, context_name, project_id, service_id, last_run_id
		) VALUES (
			'windows_wsl_ubuntu', 'wsl:Ubuntu', 'windows_wsl_ubuntu/demo', '', 2005
		);
	`); err != nil {
		t.Fatalf("seed cross-scope update references: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate cross-scope update references: %v", err)
	}
	for _, assertion := range []struct {
		table string
		id    int
	}{
		{table: "image_lineage", id: 2001},
		{table: "image_update_check_runs", id: 2003},
		{table: "image_update_checks", id: 2004},
	} {
		if got := queryString(t, ctx, db, `
			SELECT context_name FROM `+assertion.table+` WHERE id = ?
		`, assertion.id); got != "wsl:Ubuntu" {
			t.Fatalf("cross-scope %s context = %q, want quarantined wsl:Ubuntu", assertion.table, got)
		}
	}
	if got := queryString(t, ctx, db, `
		SELECT context_name FROM image_update_check_runs WHERE id = 2005
	`); got != "wsl:ubuntu" {
		t.Fatalf("valid referenced run context = %q, want wsl:ubuntu", got)
	}
	if got := queryString(t, ctx, db, `
		SELECT context_name FROM image_update_check_heads WHERE last_run_id = 2005
	`); got != "wsl:Ubuntu" {
		t.Fatalf("mismatched project head context = %q, want quarantined wsl:Ubuntu", got)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*)
		FROM image_update_checks AS checks
		JOIN image_lineage AS lineage ON lineage.id = checks.lineage_id
		WHERE checks.context_name = 'wsl:ubuntu'
			AND lineage.context_name <> checks.context_name
	`); got != 0 {
		t.Fatalf("canonical checks with cross-scope lineage = %d, want 0", got)
	}
	if got := queryInt64(t, ctx, db, `
		SELECT COUNT(*)
		FROM image_update_check_heads AS heads
		JOIN image_update_check_runs AS runs ON runs.id = heads.last_run_id
		WHERE heads.context_name = 'wsl:ubuntu'
			AND (
				runs.context_name <> heads.context_name
				OR runs.provider_id <> heads.provider_id
				OR runs.project_id <> heads.project_id
				OR runs.service_id <> heads.service_id
			)
	`); got != 0 {
		t.Fatalf("canonical heads with cross-scope run = %d, want 0", got)
	}
	if got := queryInt64(t, ctx, db, "SELECT COUNT(*) FROM pragma_foreign_key_check"); got != 0 {
		t.Fatalf("foreign key violations after migration = %d, want 0", got)
	}
}

func TestWSLContextCanonicalizationMigrationUsesRuntimeUnicodeRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreBeforeLatestMigration(t, ctx)
	defer closeStore(t, db)

	if _, err := db.writer.ExecContext(ctx, `
		INSERT INTO providers (id, type, platform, display_name)
		VALUES ('windows_wsl_ubuntu', 'windows_wsl_ubuntu', 'windows', 'Windows WSL Ubuntu');

		INSERT INTO projects (id, provider_id, context_name, name, source)
		VALUES (
			'windows_wsl_ubuntu/unicode', 'windows_wsl_ubuntu', '  WSL:ÜBUNTU  ',
			'unicode', 'labels'
		);

		INSERT INTO metrics_samples (
			id, provider_id, context_name, project_id, resolution, sampled_at
		) VALUES (
			1001, 'windows_wsl_ubuntu', '  WSL:ÜBUNTU  ',
			'windows_wsl_ubuntu/unicode', 'raw', '2026-07-18T12:00:00Z'
		);
	`); err != nil {
		t.Fatalf("seed non-ASCII WSL identity: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(non-ASCII WSL identity): %v", err)
	}
	if got := migrationCount(t, ctx, db); got != latestSchemaVersion {
		t.Fatalf("migration count after Unicode identity = %d, want %d", got, latestSchemaVersion)
	}
	want, ok := runtimescope.WindowsWSLContextV1("ÜBUNTU")
	if !ok {
		t.Fatal("WindowsWSLContextV1 rejected Unicode distro")
	}
	if got := queryString(t, ctx, db, `
		SELECT context_name FROM projects
		WHERE id = 'windows_wsl_ubuntu/unicode'
	`); got != want {
		t.Fatalf("migrated Unicode project context = %q, want %q", got, want)
	}
	if got := queryString(t, ctx, db, `
		SELECT context_name FROM metrics_samples WHERE id = 1001
	`); got != want {
		t.Fatalf("migrated Unicode metric context = %q, want %q", got, want)
	}
}

func openStoreBeforeLatestMigration(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	db := openStore(t, ctx)
	if err := ensureMigrationsTable(ctx, db.writer); err != nil {
		closeStore(t, db)
		t.Fatalf("ensure migrations table: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		closeStore(t, db)
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) != latestSchemaVersion {
		closeStore(t, db)
		t.Fatalf("migration count = %d, want %d", len(migrations), latestSchemaVersion)
	}
	for _, migration := range migrations[:latestSchemaVersion-1] {
		if err := applyMigration(ctx, db.writer, migration); err != nil {
			closeStore(t, db)
			t.Fatalf("apply pre-canonicalization migration %s: %v", migration.name, err)
		}
	}
	return db
}
