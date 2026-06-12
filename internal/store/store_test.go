package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func TestMigrateFreshDatabaseCreatesV1Schema(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	tables := tableNames(t, ctx, s)
	expected := []string{
		"audit_log",
		"backups",
		"base_image_refs",
		"command_history",
		"containers_cache",
		"docker_contexts",
		"ignored_updates",
		"image_lineage",
		"image_update_checks",
		"images_cache",
		"metrics_samples",
		"networks_cache",
		"notifications",
		"projects",
		"providers",
		"schema_migrations",
		"services",
		"settings",
		"update_history",
		"volumes_cache",
	}
	for _, name := range expected {
		if !slices.Contains(tables, name) {
			t.Fatalf("missing table %q in %v", name, tables)
		}
	}

	indexes := indexNames(t, ctx, s)
	for _, name := range []string{
		"idx_audit_time",
		"idx_base_refs_image",
		"idx_base_refs_lineage",
		"idx_checks_kind",
		"idx_checks_project",
		"idx_containers_project",
		"idx_lineage_container",
		"idx_lineage_project",
		"idx_lineage_service",
		"idx_metrics_container_time",
		"idx_metrics_project_time",
		"idx_metrics_res_time",
	} {
		if !slices.Contains(indexes, name) {
			t.Fatalf("missing index %q in %v", name, indexes)
		}
	}

	if got := migrationCount(t, ctx, s); got != 1 {
		t.Fatalf("migration count = %d, want 1", got)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := migrationCount(t, ctx, s); got != 1 {
		t.Fatalf("migration count after rerun = %d, want 1", got)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, ctx)
	defer closeStore(t, s)

	if _, err := s.writer.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL
		)
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := s.writer.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, applied_at)
		VALUES (9999, '2026-06-12T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert newer schema: %v", err)
	}

	if err := s.Migrate(ctx); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("Migrate error = %v, want ErrNewerSchema", err)
	}
}

func TestPragmas(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	if got := queryPragmaString(t, ctx, s, "journal_mode"); got != "wal" {
		t.Fatalf("journal_mode = %q, want wal", got)
	}
	if got := queryPragmaInt(t, ctx, s, "foreign_keys"); got != 1 {
		t.Fatalf("foreign_keys = %d, want 1", got)
	}
	if got := queryPragmaInt(t, ctx, s, "busy_timeout"); got != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", got)
	}
	if got := queryPragmaInt(t, ctx, s, "synchronous"); got != 1 {
		t.Fatalf("synchronous = %d, want NORMAL(1)", got)
	}
}

func TestSettingsDefaultsAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	settings := s.Settings()

	theme, err := settings.GetString(ctx, "general.theme")
	if err != nil {
		t.Fatalf("GetString general.theme: %v", err)
	}
	if theme != "dark" {
		t.Fatalf("general.theme = %q, want dark", theme)
	}

	autostart, err := settings.GetBool(ctx, "provider.autostart_backend")
	if err != nil {
		t.Fatalf("GetBool provider.autostart_backend: %v", err)
	}
	if !autostart {
		t.Fatalf("provider.autostart_backend = false, want true")
	}

	sampleInterval, err := settings.GetInt(ctx, "metrics.sample_interval_seconds")
	if err != nil {
		t.Fatalf("GetInt metrics.sample_interval_seconds: %v", err)
	}
	if sampleInterval != 2 {
		t.Fatalf("metrics.sample_interval_seconds = %d, want 2", sampleInterval)
	}

	if err := settings.SetString(ctx, "general.theme", "light"); err != nil {
		t.Fatalf("SetString general.theme: %v", err)
	}
	if got, err := settings.GetString(ctx, "general.theme"); err != nil || got != "light" {
		t.Fatalf("general.theme after set = %q, %v; want light, nil", got, err)
	}

	if err := settings.SetInt(ctx, "updates.check_interval_hours", 6); err != nil {
		t.Fatalf("SetInt updates.check_interval_hours: %v", err)
	}
	if got, err := settings.GetInt(ctx, "updates.check_interval_hours"); err != nil || got != 6 {
		t.Fatalf("updates.check_interval_hours after set = %d, %v; want 6, nil", got, err)
	}

	if err := settings.SetRaw(ctx, "general.theme", "{not-json"); !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("SetRaw invalid JSON error = %v, want ErrInvalidJSON", err)
	}
	if err := settings.SetBool(ctx, "general.theme", true); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("SetBool wrong key error = %v, want ErrTypeMismatch", err)
	}
	if err := settings.SetString(ctx, "missing.setting", "x"); !errors.Is(err, ErrUnknownSetting) {
		t.Fatalf("SetString unknown key error = %v, want ErrUnknownSetting", err)
	}
}

func openMigratedStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()

	s := openStore(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		_ = s.Close()
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func openStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cairn.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func tableNames(t *testing.T, ctx context.Context, s *Store) []string {
	t.Helper()

	rows, err := s.writer.QueryContext(ctx, `
		SELECT name
		FROM sqlite_master
		WHERE type = 'table'
			AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close table rows: %v", err)
		}
	}()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table rows: %v", err)
	}
	return names
}

func indexNames(t *testing.T, ctx context.Context, s *Store) []string {
	t.Helper()

	rows, err := s.writer.QueryContext(ctx, `
		SELECT name
		FROM sqlite_master
		WHERE type = 'index'
			AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close index rows: %v", err)
		}
	}()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("index rows: %v", err)
	}
	return names
}

func migrationCount(t *testing.T, ctx context.Context, s *Store) int {
	t.Helper()

	var count int
	if err := s.writer.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("migration count: %v", err)
	}
	return count
}

func queryPragmaInt(t *testing.T, ctx context.Context, s *Store, name string) int {
	t.Helper()

	var value int
	if err := s.writer.QueryRowContext(ctx, "PRAGMA "+name).Scan(&value); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return value
}

func queryPragmaString(t *testing.T, ctx context.Context, s *Store, name string) string {
	t.Helper()

	var value string
	if err := s.writer.QueryRowContext(ctx, "PRAGMA "+name).Scan(&value); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return value
}

func closeStore(t *testing.T, s *Store) {
	t.Helper()

	if err := s.Close(); err != nil {
		t.Errorf("close store: %v", err)
	}
}
