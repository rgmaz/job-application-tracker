package main

import (
	"context"
	"fmt"
	"io"

	"github.com/rgmaz/job-application-tracker/internal/config"
	"github.com/rgmaz/job-application-tracker/internal/store"
)

func migrate(ctx context.Context, cfg config.Config, stdout io.Writer) error {
	path := store.Path(cfg.DataDir)

	db, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if err := store.Migrate(ctx, db); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(stdout, "migrations applied: %s\n", path); err != nil {
		return fmt.Errorf("write migrate output: %w", err)
	}
	return nil
}
