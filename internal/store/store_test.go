package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
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

	if got := migrationCount(t, ctx, s); got != 2 {
		t.Fatalf("migration count = %d, want 2", got)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := migrationCount(t, ctx, s); got != 2 {
		t.Fatalf("migration count after rerun = %d, want 2", got)
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

func TestCopyFilePublishesAtomicallyAndDoesNotOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "source.db")
	dst := filepath.Join(dir, "source.db.bak-test")
	if err := os.WriteFile(src, []byte("backup-data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "backup-data" {
		t.Fatalf("backup content = %q, %v", got, err)
	}
	matches, err := filepath.Glob(dst + ".tmp-*")
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
	if err := copyFile(src, dst); !errors.Is(err, os.ErrExist) {
		t.Fatalf("copyFile(existing) error = %v, want os.ErrExist", err)
	}
}

func TestReleaseDBFixtureUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cairn.db")
	fixturePath := filepath.Join("..", "..", "testdata", "dbs", "v1.0.0-rc1-seed.sql")
	seedSQL, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture seed: %v", err)
	}

	fixture, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	if err := fixture.Migrate(ctx); err != nil {
		_ = fixture.Close()
		t.Fatalf("prepare fixture schema: %v", err)
	}
	if _, err := fixture.writer.ExecContext(ctx, string(seedSQL)); err != nil {
		_ = fixture.Close()
		t.Fatalf("apply fixture seed: %v", err)
	}
	closeStore(t, fixture)

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen fixture store: %v", err)
	}
	defer closeStore(t, upgraded)
	if err := upgraded.Migrate(ctx); err != nil {
		t.Fatalf("migrate release fixture: %v", err)
	}
	if got := migrationCount(t, ctx, upgraded); got != 2 {
		t.Fatalf("migration count = %d, want 2", got)
	}

	settings := upgraded.Settings()
	if got, err := settings.GetString(ctx, "general.theme"); err != nil || got != "light" {
		t.Fatalf("general.theme after upgrade = %q, %v; want light, nil", got, err)
	}
	if got, err := settings.GetString(ctx, "windows.wsl_distro"); err != nil || got != "cairn-dev" {
		t.Fatalf("windows.wsl_distro after upgrade = %q, %v; want cairn-dev, nil", got, err)
	}
	if got, err := settings.GetInt(ctx, "macos.colima_disk_gb"); err != nil || got != 60 {
		t.Fatalf("macos.colima_disk_gb after upgrade = %d, %v; want default 60, nil", got, err)
	}

	if got := queryString(t, ctx, upgraded, "SELECT display_name FROM providers WHERE id = ?", "linux_native"); got != "Linux native" {
		t.Fatalf("provider display_name = %q, want Linux native", got)
	}
	if got := queryString(t, ctx, upgraded, "SELECT status FROM projects WHERE id = ?", "linux_native/app-db"); got != "running" {
		t.Fatalf("project status = %q, want running", got)
	}
	if got := queryString(t, ctx, upgraded, "SELECT image_ref FROM services WHERE id = ?", "linux_native/app-db/web"); got != "nginx:1.25" {
		t.Fatalf("service image_ref = %q, want nginx:1.25", got)
	}
	if got := queryString(t, ctx, upgraded, "SELECT status FROM image_update_checks WHERE image_ref = ?", "nginx:1.25"); got != "service_image_update_available" {
		t.Fatalf("update check status = %q, want service_image_update_available", got)
	}
	if got := queryString(t, ctx, upgraded, "SELECT status FROM base_image_refs WHERE id = 1"); got != "base_image_update_available" {
		t.Fatalf("base image status = %q, want base_image_update_available", got)
	}
	if got := queryString(t, ctx, upgraded, "SELECT result FROM update_history WHERE image_ref = ?", "nginx:1.25"); got != "success" {
		t.Fatalf("update history result = %q, want success", got)
	}
	if got := queryString(t, ctx, upgraded, "SELECT result FROM backups WHERE id = ?", "backup-app-db-data"); got != "success" {
		t.Fatalf("backup result = %q, want success", got)
	}
	if got := queryInt64(t, ctx, upgraded, "SELECT COUNT(*) FROM audit_log WHERE action = ?", "update.apply"); got != 1 {
		t.Fatalf("audit row count = %d, want 1", got)
	}
	if got := queryInt64(t, ctx, upgraded, "SELECT read FROM notifications WHERE title = ?", "Update applied"); got != 0 {
		t.Fatalf("notification read = %d, want 0", got)
	}
}

func TestOpenCreatesPrivateStoreDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs do not map cleanly to Unix permission bits")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "cairn.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	closeStore(t, s)
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("store directory mode = %o, want 700", got)
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

	all, err := settings.All(ctx)
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if all["linux.sudo_mode"] != "ask" {
		t.Fatalf("linux.sudo_mode default = %#v, want ask", all["linux.sudo_mode"])
	}
	if err := settings.SetValue(ctx, "linux.sudo_mode", "rootless"); err != nil {
		t.Fatalf("SetValue linux.sudo_mode: %v", err)
	}
	if got, err := settings.GetString(ctx, "linux.sudo_mode"); err != nil || got != "rootless" {
		t.Fatalf("linux.sudo_mode after set = %q, %v; want rootless, nil", got, err)
	}
	if err := settings.SetValue(ctx, "linux.sudo_mode", "silent-root"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("SetValue invalid enum error = %v, want ErrInvalidValue", err)
	}
	if err := settings.SetValue(ctx, "security.confirm_destructive", false); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("SetValue security.confirm_destructive=false error = %v, want ErrInvalidValue", err)
	}
	if err := settings.SetRaw(ctx, "security.confirm_destructive", "false"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("SetRaw security.confirm_destructive=false error = %v, want ErrInvalidValue", err)
	}
	if err := settings.SetValue(ctx, "metrics.sample_interval_seconds", float64(3.5)); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("SetValue fractional int error = %v, want ErrTypeMismatch", err)
	}
}

func TestNotificationsRoundTripAndMarkRead(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	repo := s.Notifications()
	firstID, err := repo.Insert(ctx, NotificationRecord{
		Level: "warn",
		Title: "Provider degraded",
		Body:  "Docker daemon stopped",
		Topic: "provider",
	})
	if err != nil {
		t.Fatalf("Insert first notification: %v", err)
	}
	if _, err := repo.Insert(ctx, NotificationRecord{
		Level: "info",
		Title: "Update available",
		Topic: "update",
		Read:  true,
	}); err != nil {
		t.Fatalf("Insert second notification: %v", err)
	}

	all, err := repo.List(ctx, false, 10)
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 || all[0].Title == "" {
		t.Fatalf("all notifications = %#v", all)
	}
	unread, err := repo.List(ctx, true, 10)
	if err != nil {
		t.Fatalf("List unread: %v", err)
	}
	if len(unread) != 1 || unread[0].ID != firstID || unread[0].Read {
		t.Fatalf("unread notifications = %#v", unread)
	}

	if err := repo.MarkRead(ctx, []int64{firstID}); err != nil {
		t.Fatalf("MarkRead one: %v", err)
	}
	unread, err = repo.List(ctx, true, 10)
	if err != nil {
		t.Fatalf("List unread after mark: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread after mark = %#v, want empty", unread)
	}

	if _, err := repo.Insert(ctx, NotificationRecord{Level: "error", Title: "Action failed"}); err != nil {
		t.Fatalf("Insert third notification: %v", err)
	}
	if err := repo.MarkRead(ctx, nil); err != nil {
		t.Fatalf("MarkRead all: %v", err)
	}
	unread, err = repo.List(ctx, true, 10)
	if err != nil {
		t.Fatalf("List unread after mark all: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread after mark all = %#v, want empty", unread)
	}
}

func TestAuditListIncludesViewerMetadata(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	if _, err := s.Audit().Insert(ctx, AuditRecord{
		Action:     "update.apply",
		TargetType: "project",
		TargetID:   "linux_native/app",
		ProviderID: "linux_native",
		ProjectID:  "linux_native/app",
		Command:    "docker compose up -d",
		Risk:       models.RiskNeedsConfirmation,
		Status:     "success",
		Duration:   2 * time.Second,
	}); err != nil {
		t.Fatalf("Insert audit record: %v", err)
	}

	entries, err := s.Audit().List(ctx, models.AuditFilter{Topic: "update.", Limit: 10})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one", entries)
	}
	metadata := entries[0].Metadata
	if metadata["command"] != "docker compose up -d" ||
		metadata["risk"] != string(models.RiskNeedsConfirmation) ||
		metadata["providerID"] != "linux_native" ||
		metadata["projectID"] != "linux_native/app" ||
		metadata["targetType"] != "project" ||
		metadata["durationMS"] != int64(2000) {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestAuditListEscapesTopicAndPreservesZeroDuration(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	if _, err := s.Audit().Insert(ctx, AuditRecord{Action: "update.apply", Status: "success"}); err != nil {
		t.Fatalf("Insert update.apply audit: %v", err)
	}
	if _, err := s.Audit().Insert(ctx, AuditRecord{Action: "update.%literal", Status: "success"}); err != nil {
		t.Fatalf("Insert literal audit: %v", err)
	}

	entries, err := s.Audit().List(ctx, models.AuditFilter{Topic: "update.%", Limit: 10})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != "update.%literal" {
		t.Fatalf("entries = %#v, want only literal wildcard action", entries)
	}
	if entries[0].Metadata["durationMS"] != int64(0) {
		t.Fatalf("duration metadata = %#v, want durationMS 0", entries[0].Metadata)
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

func queryString(t *testing.T, ctx context.Context, s *Store, query string, args ...any) string {
	t.Helper()

	var value string
	if err := s.writer.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		t.Fatalf("query string %q: %v", query, err)
	}
	return value
}

func queryInt64(t *testing.T, ctx context.Context, s *Store, query string, args ...any) int64 {
	t.Helper()

	var value int64
	if err := s.writer.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		t.Fatalf("query int64 %q: %v", query, err)
	}
	return value
}

func closeStore(t *testing.T, s *Store) {
	t.Helper()

	if err := s.Close(); err != nil {
		t.Errorf("close store: %v", err)
	}
}
