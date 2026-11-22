package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rgmaz/job-application-tracker/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func currentVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()

	var version int64
	if err := db.QueryRow("SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatalf("read goose_db_version: %v", err)
	}
	return version
}

func TestOpenCreatesParentDirectory(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nested", "data")
	db, err := store.Open(context.Background(), filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("parent directory was not created: %v", err)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	if _, err := store.Open(context.Background(), ""); err == nil {
		t.Fatal("store.Open(\"\") error = nil, want error")
	}
}

func TestOpenAppliesPragmas(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	tests := []struct {
		name  string
		query string
		want  int
	}{
		{name: "foreign_keys", query: "PRAGMA foreign_keys", want: 1},
		{name: "busy_timeout", query: "PRAGMA busy_timeout", want: 5000},
		{name: "synchronous", query: "PRAGMA synchronous", want: 1}, // NORMAL
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int
			if err := db.QueryRowContext(ctx, tt.query).Scan(&got); err != nil {
				t.Fatalf("%s: %v", tt.query, err)
			}
			if got != tt.want {
				t.Errorf("%s = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}

func TestMigrate(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	if got := currentVersion(t, db); got != 2 {
		t.Fatalf("goose version = %d, want 2", got)
	}

	for _, table := range []string{
		"applications", "documents", "events", "settings",
		"status_thresholds", "goose_db_version",
	} {
		var name string
		err := db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}

	var thresholds int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM status_thresholds").Scan(&thresholds); err != nil {
		t.Fatalf("count thresholds: %v", err)
	}
	if thresholds != 4 {
		t.Errorf("seeded thresholds = %d, want 4", thresholds)
	}

	// A second run must be a no-op rather than an error or a re-apply.
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("second store.Migrate: %v", err)
	}
	if got := currentVersion(t, db); got != 2 {
		t.Errorf("goose version after second run = %d, want 2", got)
	}
}

func TestMigrateConcurrentDatabases(t *testing.T) {
	t.Parallel()

	const databases = 4

	dir := t.TempDir()
	paths := make([]string, databases)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("app-%d.db", i))
	}

	var wg sync.WaitGroup
	errs := make([]error, databases)
	for i := range paths {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			db, err := store.Open(context.Background(), paths[i])
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = db.Close() }()

			errs[i] = store.Migrate(context.Background(), db)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("database %d: %v", i, err)
		}
	}
}
