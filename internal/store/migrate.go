package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sync"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var (
	gooseOnce sync.Once
	gooseErr  error
)

// Migrate applies all pending Up migrations from the embedded migrations
// directory. It is safe to call from parallel tests: goose's process-wide
// settings are initialized exactly once, before any migration runs.
func Migrate(ctx context.Context, db *sql.DB) error {
	if err := initGoose(); err != nil {
		return err
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("store: apply migrations: %w", err)
	}
	return nil
}

func initGoose() error {
	gooseOnce.Do(func() {
		goose.SetBaseFS(migrationsFS)
		goose.SetLogger(goose.NopLogger())
		gooseErr = goose.SetDialect("sqlite3")
	})
	return gooseErr
}
