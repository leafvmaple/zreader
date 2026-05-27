// Package store wraps the SQLite database that holds library metadata,
// chapter indexes and per-user reading progress.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO needed
)

//go:embed schema.sql
var schemaSQL string

// Store is the application's typed handle on the underlying SQLite database.
// All access goes through it so we never sprinkle raw `*sql.DB` calls across
// the codebase.
type Store struct {
	db *sql.DB
}

// Open creates (or opens) the SQLite database file under the given data dir
// and applies the embedded schema. The path is fixed at <dataDir>/library.db.
func Open(dataDir string) (*Store, error) {
	dbPath := filepath.Join(dataDir, "library.db")
	// modernc.org/sqlite uses the "sqlite" driver name. We enable WAL inside
	// schema.sql via PRAGMA; here we just make sure foreign-keys are on for
	// every connection (PRAGMA in schema.sql only runs once at boot).
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite at %s: %w", dbPath, err)
	}
	// SQLite is single-writer; many concurrent writers contend on the WAL.
	// Cap connections low so we surface bugs rather than queue requests.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)

	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Idempotent column add for DBs created before chapters.level existed.
	// CREATE TABLE IF NOT EXISTS in schema.sql only covers fresh installs;
	// existing DBs need ALTER. SQLite has no IF NOT EXISTS for ADD COLUMN,
	// so we run it and swallow the "duplicate column" error.
	if _, err := db.Exec(`ALTER TABLE chapters ADD COLUMN level INTEGER NOT NULL DEFAULT 1`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		_ = db.Close()
		return nil, fmt.Errorf("add chapters.level: %w", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO schema_version(version) VALUES (1)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("seed schema_version: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying *sql.DB. Idempotent.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// nowUnix returns the current unix-seconds timestamp. Kept as a method so
// tests can swap in a fixed clock later without rewriting call sites.
func (s *Store) nowUnix() int64 { return time.Now().Unix() }
