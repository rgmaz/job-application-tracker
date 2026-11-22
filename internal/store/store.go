// Package store owns the SQLite connection and the embedded schema migrations.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// DatabaseName is the SQLite file created inside DATA_DIR.
const DatabaseName = "app.db"

// Path returns the database file path for a DATA_DIR.
func Path(dataDir string) string {
	return filepath.Join(dataDir, DatabaseName)
}

// Open opens the SQLite database at path, creating the parent directory if
// needed, and applies the connection pragmas the application relies on:
// WAL journaling, NORMAL synchronous writes, foreign keys, and a 5s busy
// timeout. The pool is limited to one connection: SQLite has a single writer,
// and serializing through one connection keeps pragmas and transactions
// consistent.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("store: database path is empty")
	}

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("store: create database directory %q: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// Force the first connection so malformed pragmas or an unusable path fail
	// at startup rather than on the first request.
	if err := db.PingContext(ctx); err != nil {
		// Best-effort cleanup of a handle that never became usable.
		_ = db.Close()
		return nil, fmt.Errorf("store: ping %q: %w", path, err)
	}

	return db, nil
}

func dsn(path string) string {
	u := &url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return u.String()
}
