package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestUpdateHistoryRepositoryNeverPersistsOrRevivesBuildArgumentValues(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const secret = "repository-boundary-build-argument-secret"
	historyID, err := db.Updates().InsertHistory(ctx, UpdateHistoryRecord{
		ProviderID: "provider",
		UpdateKind: models.UpdateKindBaseImage,
		ImageRef:   "example/app:latest",
		BuildArgs:  map[string]string{"TOKEN": secret},
	})
	if err != nil {
		t.Fatalf("insert update history: %v", err)
	}
	var persisted string
	if err := db.writer.QueryRowContext(ctx, `SELECT build_args_json FROM update_history WHERE id = ?`, historyID).Scan(&persisted); err != nil {
		t.Fatalf("read persisted update build args: %v", err)
	}
	if persisted != "{}" || strings.Contains(persisted, secret) {
		t.Fatalf("persisted update BuildArgs = %q, want empty object", persisted)
	}

	if _, err := db.writer.ExecContext(ctx, `UPDATE update_history SET build_args_json = ? WHERE id = ?`, `{"TOKEN":"`+secret+`"}`, historyID); err != nil {
		t.Fatalf("seed legacy update build args: %v", err)
	}
	history, err := db.Updates().GetHistory(ctx, historyID)
	if err != nil {
		t.Fatalf("get update history: %v", err)
	}
	if len(history.BuildArgs) != 0 {
		t.Fatalf("legacy update BuildArgs were revived: %#v", history.BuildArgs)
	}
}

func TestMigrateScrubsLegacyBuildArgumentValuesWithoutPendingSchemaWork(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []string{
		`INSERT INTO providers (id, type, platform, display_name) VALUES ('provider', 'linux_native', 'linux', 'Provider')`,
		`INSERT INTO projects (id, provider_id, context_name, name, source, pinned, last_seen_at, metadata_json)
		 VALUES ('project', 'provider', '', 'project', 'imported', 0, '` + now + `', '{}')`,
		`INSERT INTO services (id, project_id, name, metadata_json, last_seen_at)
		 VALUES ('service', 'project', 'app', '{"buildArgs":{"TOKEN":"literal-secret-value"},"dependsOn":["db"]}', '` + now + `')`,
		`INSERT INTO services (id, project_id, name, metadata_json, last_seen_at)
		 VALUES ('corrupt-service', 'project', 'corrupt', 'invalid "buildArgs" TOKEN literal-secret-value', '` + now + `')`,
		`INSERT INTO image_lineage (provider_id, context_name, project_id, service_id, service_name, build_args_json, source, confidence, discovered_at, updated_at)
		 VALUES ('provider', '', 'project', 'service', 'app', '{"TOKEN":"literal-secret-value"}', 'compose_dockerfile', 'low', '` + now + `', '` + now + `')`,
		`INSERT INTO update_history (provider_id, context_name, project_id, service_id, update_kind, image_ref, build_args_json, result, started_at)
		 VALUES ('provider', '', 'project', 'service', 'base_image', 'example/app:latest', '{"TOKEN":"literal-secret-value"}', 'success', '` + now + `')`,
	}
	for _, statement := range statements {
		if _, err := db.writer.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed legacy build args: %v", err)
		}
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("repeat migrate/scrub: %v", err)
	}

	var serviceMetadata, corruptServiceMetadata, lineageArgs, historyArgs string
	if err := db.writer.QueryRowContext(ctx, `SELECT metadata_json FROM services WHERE id = 'service'`).Scan(&serviceMetadata); err != nil {
		t.Fatalf("read service metadata: %v", err)
	}
	if err := db.writer.QueryRowContext(ctx, `SELECT build_args_json FROM image_lineage WHERE service_id = 'service'`).Scan(&lineageArgs); err != nil {
		t.Fatalf("read lineage args: %v", err)
	}
	if err := db.writer.QueryRowContext(ctx, `SELECT build_args_json FROM update_history WHERE service_id = 'service'`).Scan(&historyArgs); err != nil {
		t.Fatalf("read history args: %v", err)
	}
	if err := db.writer.QueryRowContext(ctx, `SELECT metadata_json FROM services WHERE id = 'corrupt-service'`).Scan(&corruptServiceMetadata); err != nil {
		t.Fatalf("read corrupt service metadata: %v", err)
	}
	for label, value := range map[string]string{
		"service metadata": serviceMetadata,
		"lineage args":     lineageArgs,
		"history args":     historyArgs,
	} {
		if strings.Contains(value, "literal-secret-value") || strings.Contains(value, "TOKEN") {
			t.Fatalf("%s retained legacy build argument data: %s", label, value)
		}
	}
	if !strings.Contains(serviceMetadata, "dependsOn") {
		t.Fatalf("unrelated service metadata was removed: %s", serviceMetadata)
	}
	if corruptServiceMetadata != "{}" {
		t.Fatalf("invalid legacy service metadata was retained: %q", corruptServiceMetadata)
	}
}

func TestMigrateRemovesLegacyBuildArgumentValuesFromLiveDatabaseFiles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cairn.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const secret = "unique-legacy-build-argument-secret-7b9136"
	statements := []string{
		`INSERT INTO providers (id, type, platform, display_name) VALUES ('provider', 'linux_native', 'linux', 'Provider')`,
		`INSERT INTO projects (id, provider_id, context_name, name, source, pinned, last_seen_at, metadata_json)
		 VALUES ('project', 'provider', '', 'project', 'imported', 0, '` + now + `', '{}')`,
		`INSERT INTO services (id, project_id, name, metadata_json, last_seen_at)
		 VALUES ('service', 'project', 'app', '{"buildArgs":{"TOKEN":"` + secret + `"}}', '` + now + `')`,
		`INSERT INTO image_lineage (provider_id, context_name, project_id, service_id, service_name, build_args_json, source, confidence, discovered_at, updated_at)
		 VALUES ('provider', '', 'project', 'service', 'app', '{"TOKEN":"` + secret + `"}', 'compose_dockerfile', 'low', '` + now + `', '` + now + `')`,
		`INSERT INTO update_history (provider_id, context_name, project_id, service_id, update_kind, image_ref, build_args_json, result, started_at)
		 VALUES ('provider', '', 'project', 'service', 'base_image', 'example/app:latest', '{"TOKEN":"` + secret + `"}', 'success', '` + now + `')`,
	}
	for _, statement := range statements {
		if _, err := db.writer.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed legacy build args: %v", err)
		}
	}
	if !storeFilesContain(t, path, secret) {
		t.Fatal("legacy fixture did not reach a live database file")
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("repeat migrate/scrub: %v", err)
	}
	if storeFilesContain(t, path, secret) {
		t.Fatal("live database or sidecar retained the legacy build-argument secret")
	}
}

func TestPrivacyMigrationVacuumsDeletedBuildArgumentResidueBeforeBackup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cairn.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := ensureMigrationsTable(ctx, db.writer); err != nil {
		t.Fatalf("ensure migrations table: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil || len(migrations) < buildArgPrivacyVersion {
		t.Fatalf("load migrations = %d/%v", len(migrations), err)
	}
	for _, migration := range migrations[:buildArgPrivacyVersion-1] {
		if err := applyMigration(ctx, db.writer, migration); err != nil {
			t.Fatalf("apply fixture migration %s: %v", migration.name, err)
		}
	}
	// Simulate an older build that deleted a secret-bearing row without secure
	// deletion. No live row remains for the ordinary UPDATE scrub to touch.
	if _, err := db.writer.ExecContext(ctx, "PRAGMA secure_delete = OFF"); err != nil {
		t.Fatalf("disable secure delete for legacy fixture: %v", err)
	}
	const needle = "deleted-build-argument-secret-6dc850f5"
	legacyValue := `{"TOKEN":"` + needle + strings.Repeat("x", 4000) + `"}`
	result, err := db.writer.ExecContext(ctx, `
		INSERT INTO update_history (provider_id, context_name, update_kind, image_ref, build_args_json, result, started_at)
		VALUES ('provider', '', 'base_image', 'example/app:latest', ?, 'success', ?)
	`, legacyValue, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("insert deleted legacy fixture: %v", err)
	}
	historyID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("legacy fixture id: %v", err)
	}
	if _, err := db.writer.ExecContext(ctx, `DELETE FROM update_history WHERE id = ?`, historyID); err != nil {
		t.Fatalf("delete legacy fixture: %v", err)
	}
	if err := db.checkpointWALTruncate(ctx); err != nil {
		t.Fatalf("checkpoint legacy fixture: %v", err)
	}
	if !storeFilesContain(t, path, needle) {
		t.Fatal("deleted legacy fixture did not leave free-page residue")
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("privacy migration: %v", err)
	}
	if storeFilesContain(t, path, needle) {
		t.Fatal("live database retained deleted build-argument residue")
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("privacy migration backups = %#v/%v, want one", backups, err)
	}
	raw, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatalf("read privacy migration backup: %v", err)
	}
	if bytes.Contains(raw, []byte(needle)) {
		t.Fatal("privacy migration backup retained deleted build-argument residue")
	}
}

func storeFilesContain(t *testing.T, path string, value string) bool {
	t.Helper()
	for _, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		raw, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read database file %s: %v", filepath.Base(candidate), err)
		}
		if bytes.Contains(raw, []byte(value)) {
			return true
		}
	}
	return false
}

func TestPendingMigrationBackupDoesNotRetainLegacyBuildArgumentValues(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cairn.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := ensureMigrationsTable(ctx, db.writer); err != nil {
		t.Fatalf("ensure migrations table: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil || len(migrations) < 10 {
		t.Fatalf("load migrations = %d/%v", len(migrations), err)
	}
	for _, migration := range migrations[:9] {
		if err := applyMigration(ctx, db.writer, migration); err != nil {
			t.Fatalf("apply fixture migration %s: %v", migration.name, err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []string{
		`INSERT INTO providers (id, type, platform, display_name) VALUES ('provider', 'linux_native', 'linux', 'Provider')`,
		`INSERT INTO projects (id, provider_id, context_name, name, source, pinned, last_seen_at, metadata_json)
		 VALUES ('project', 'provider', '', 'project', 'imported', 0, '` + now + `', '{}')`,
		`INSERT INTO services (id, project_id, name, metadata_json, last_seen_at)
		 VALUES ('service', 'project', 'app', '{"buildArgs":{"TOKEN":"literal-secret-value"}}', '` + now + `')`,
		`INSERT INTO image_lineage (provider_id, context_name, project_id, service_id, service_name, build_args_json, source, confidence, discovered_at, updated_at)
		 VALUES ('provider', '', 'project', 'service', 'app', '{"TOKEN":"literal-secret-value"}', 'compose_dockerfile', 'low', '` + now + `', '` + now + `')`,
		`INSERT INTO update_history (provider_id, context_name, project_id, service_id, update_kind, image_ref, build_args_json, result, started_at)
		 VALUES ('provider', '', 'project', 'service', 'base_image', 'example/app:latest', '{"TOKEN":"literal-secret-value"}', 'success', '` + now + `')`,
	}
	for _, statement := range statements {
		if _, err := db.writer.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed legacy build args: %v", err)
		}
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate with pending schema work: %v", err)
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("migration backups = %#v/%v, want one", backups, err)
	}
	raw, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatalf("read migration backup: %v", err)
	}
	if bytes.Contains(raw, []byte("literal-secret-value")) {
		t.Fatal("new migration backup retained the legacy build-argument secret")
	}

	backup, err := Open(ctx, backups[0])
	if err != nil {
		t.Fatalf("open migration backup: %v", err)
	}
	t.Cleanup(func() {
		if err := backup.Close(); err != nil {
			t.Errorf("close migration backup: %v", err)
		}
	})
	var serviceMetadata, lineageArgs, historyArgs string
	if err := backup.writer.QueryRowContext(ctx, `SELECT metadata_json FROM services WHERE id = 'service'`).Scan(&serviceMetadata); err != nil {
		t.Fatalf("read backup service metadata: %v", err)
	}
	if err := backup.writer.QueryRowContext(ctx, `SELECT build_args_json FROM image_lineage WHERE service_id = 'service'`).Scan(&lineageArgs); err != nil {
		t.Fatalf("read backup lineage args: %v", err)
	}
	if err := backup.writer.QueryRowContext(ctx, `SELECT build_args_json FROM update_history WHERE service_id = 'service'`).Scan(&historyArgs); err != nil {
		t.Fatalf("read backup history args: %v", err)
	}
	for label, value := range map[string]string{
		"service metadata": serviceMetadata,
		"lineage args":     lineageArgs,
		"history args":     historyArgs,
	} {
		if strings.Contains(value, "literal-secret-value") || strings.Contains(value, "TOKEN") {
			t.Fatalf("backup %s retained legacy build argument data: %s", label, value)
		}
	}
}
