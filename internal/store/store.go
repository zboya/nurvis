// Package store wraps SQLite connection and schema migration.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

//go:embed migrations/*.up.sql
var migrationsFS embed.FS

// Store holds the database connection and is the foundation for all repositories.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database file, runs pending migrations, and returns a Store.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// Recommended connection pool settings (WAL mode single-writer best practice)
	db.SetMaxOpenConns(1) // SQLite write concurrency is 1
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return nil, fmt.Errorf("store: pragma: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return s, nil
}

// DB returns the underlying *sql.DB for Repository use.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// migrate reads embedded migration files and executes any not-yet-applied scripts in version order.
func (s *Store) migrate() error {
	// Ensure the migration version table exists
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return err
	}

	// Query already-applied versions
	rows, err := s.db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return err
	}
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()

	// Collect all *.up.sql files under the migrations directory
	entries, err := fs.Glob(migrationsFS, "migrations/*.up.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)

	for _, entry := range entries {
		version := parseVersion(entry)
		if applied[version] {
			continue
		}
		data, err := migrationsFS.ReadFile(entry)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry, err)
		}
		if _, err := s.db.Exec(string(data)); err != nil {
			return fmt.Errorf("exec migration %s: %w", entry, err)
		}
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations(version, applied_at) VALUES(?,?)`,
			version, time.Now().UnixMilli(),
		); err != nil {
			return err
		}
		slog.Info("migration applied", "version", version, "file", entry)
	}
	return nil
}

// parseVersion extracts the version number from a filename like "migrations/0001_init.up.sql".
func parseVersion(name string) int {
	base := name
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	var v int
	fmt.Sscanf(base, "%d", &v)
	return v
}
