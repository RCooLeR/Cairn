package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	dbFileName      = "cairn.db"
	driverName      = "sqlite"
	backupKeepCount = 2
)

var (
	//go:embed migrations/*.sql
	migrationFiles embed.FS

	migrationFilePattern = regexp.MustCompile(`^(\d{4})_.+\.sql$`)

	ErrNewerSchema = errors.New("database schema is newer than this Cairn build")
)

type NewerSchemaError struct {
	Current int
	Latest  int
}

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("%v: current=%d latest=%d", ErrNewerSchema, e.Current, e.Latest)
}

func (e *NewerSchemaError) Unwrap() error {
	return ErrNewerSchema
}

type Store struct {
	path   string
	writer *sql.DB
	reader *sql.DB
}

func DefaultPath() (string, error) {
	var dir string

	switch runtime.GOOS {
	case "windows":
		dir = os.Getenv("APPDATA")
		if dir == "" {
			fallback, err := os.UserConfigDir()
			if err != nil {
				return "", err
			}
			dir = fallback
		}
		dir = filepath.Join(dir, "Cairn")
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, "Library", "Application Support", "Cairn")
	default:
		dir = os.Getenv("XDG_DATA_HOME")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, ".local", "share")
		}
		dir = filepath.Join(dir, "cairn")
	}

	return filepath.Join(dir, dbFileName), nil
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		defaultPath, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = defaultPath
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	writer, err := sql.Open(driverName, path)
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)

	reader, err := sql.Open(driverName, path)
	if err != nil {
		_ = writer.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(max(2, runtime.NumCPU()))

	s := &Store{path: path, writer: writer, reader: reader}
	if err := s.configure(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}

	return s, nil
}

func (s *Store) Close() error {
	var err error
	if s.reader != nil {
		err = errors.Join(err, s.reader.Close())
	}
	if s.writer != nil {
		err = errors.Join(err, s.writer.Close())
	}
	return err
}

func (s *Store) Settings() *SettingsRepository {
	return &SettingsRepository{db: s.writer}
}

func (s *Store) Migrate(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		return errors.New("store: no embedded migrations")
	}

	if err := ensureMigrationsTable(ctx, s.writer); err != nil {
		return err
	}

	applied, current, err := appliedMigrations(ctx, s.writer)
	if err != nil {
		return err
	}

	latest := migrations[len(migrations)-1].version
	if current > latest {
		return &NewerSchemaError{Current: current, Latest: latest}
	}

	pending := pendingMigrations(migrations, applied)
	if len(pending) > 0 {
		shouldBackup, err := s.shouldBackupBeforeMigration(ctx, current)
		if err != nil {
			return err
		}
		if shouldBackup {
			if err := s.backupBeforeMigration(ctx); err != nil {
				return err
			}
		}
	}

	for _, migration := range pending {
		if err := applyMigration(ctx, s.writer, migration); err != nil {
			return err
		}
	}

	return s.Settings().EnsureDefaults(ctx)
}

func (s *Store) configure(ctx context.Context) error {
	for _, db := range []*sql.DB{s.writer, s.reader} {
		for _, stmt := range []string{
			"PRAGMA busy_timeout = 5000",
			"PRAGMA foreign_keys = ON",
			"PRAGMA journal_mode = WAL",
			"PRAGMA synchronous = NORMAL",
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("store: apply %q: %w", stmt, err)
			}
		}
	}
	return nil
}

func (s *Store) shouldBackupBeforeMigration(ctx context.Context, current int) (bool, error) {
	if current > 0 {
		return true, nil
	}

	var tableCount int
	err := s.writer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
			AND name NOT LIKE 'sqlite_%'
			AND name <> 'schema_migrations'
	`).Scan(&tableCount)
	if err != nil {
		return false, err
	}

	return tableCount > 0, nil
}

func (s *Store) backupBeforeMigration(ctx context.Context) error {
	if s.path == "" || s.path == ":memory:" {
		return nil
	}

	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.Size() == 0) {
		return nil
	}
	if err != nil {
		return err
	}

	if _, err := s.writer.ExecContext(ctx, "PRAGMA wal_checkpoint(FULL)"); err != nil {
		return err
	}

	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupPath := fmt.Sprintf("%s.bak-%s", s.path, stamp)
	if err := copyFile(s.path, backupPath); err != nil {
		return err
	}

	return retainBackups(s.path, backupKeepCount)
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	migrations := make([]migration, 0, len(names))
	for i, name := range names {
		base := filepath.Base(name)
		matches := migrationFilePattern.FindStringSubmatch(base)
		if matches == nil {
			return nil, fmt.Errorf("store: invalid migration file name %q", base)
		}
		version, err := strconv.Atoi(matches[1])
		if err != nil {
			return nil, err
		}
		if version != i+1 {
			return nil, fmt.Errorf("store: migration versions must be sequential: got %04d want %04d", version, i+1)
		}
		content, err := migrationFiles.ReadFile(name)
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, migration{
			version: version,
			name:    base,
			sql:     string(content),
		})
	}

	return migrations, nil
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL
		)
	`)
	return err
}

func appliedMigrations(ctx context.Context, db *sql.DB) (map[int]struct{}, int, error) {
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_ = rows.Close()
	}()

	applied := make(map[int]struct{})
	current := 0
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, 0, err
		}
		applied[version] = struct{}{}
		if version > current {
			current = version
		}
	}

	return applied, current, rows.Err()
}

func pendingMigrations(migrations []migration, applied map[int]struct{}) []migration {
	pending := make([]migration, 0, len(migrations))
	for _, migration := range migrations {
		if _, ok := applied[migration.version]; !ok {
			pending = append(pending, migration)
		}
	}
	return pending
}

func applyMigration(ctx context.Context, db *sql.DB, migration migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
		return fmt.Errorf("store: apply migration %s: %w", migration.name, err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, applied_at)
		VALUES (?, ?)
	`, migration.version, now); err != nil {
		return fmt.Errorf("store: record migration %s: %w", migration.name, err)
	}

	return tx.Commit()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func retainBackups(dbPath string, keep int) error {
	if keep < 1 {
		return nil
	}

	matches, err := filepath.Glob(dbPath + ".bak-*")
	if err != nil {
		return err
	}
	if len(matches) <= keep {
		return nil
	}

	sort.Strings(matches)
	for _, old := range matches[:len(matches)-keep] {
		if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func normalizeSettingKey(key string) string {
	return strings.TrimSpace(key)
}
