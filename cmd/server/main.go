// Command server runs the job application tracker.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/rgmaz/job-application-tracker/internal/config"
)

const usage = `usage: jat <command>

commands:
  serve        start the HTTP server
  migrate      apply database migrations
  backup       back up the database and files (wired in M6)
  healthcheck  probe the local /healthz endpoint
  help         show this message
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], config.Load, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "jat: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, load func() (config.Config, error), stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("missing command\n\n" + usage)
	}

	switch cmd := args[0]; cmd {
	case "help", "-h", "--help":
		if _, err := fmt.Fprint(stdout, usage); err != nil {
			return fmt.Errorf("write usage: %w", err)
		}
		return nil
	case "serve":
		cfg, err := load()
		if err != nil {
			return err
		}
		return serve(ctx, cfg, stderr)
	case "healthcheck":
		cfg, err := load()
		if err != nil {
			return err
		}
		return healthcheck(ctx, cfg, stdout)
	case "migrate":
		cfg, err := load()
		if err != nil {
			return err
		}
		return migrate(ctx, cfg, stdout)
	case "backup":
		return errors.New("backup: not implemented yet (arrives in M6)")
	default:
		return fmt.Errorf("unknown command %q (want serve, migrate, backup, healthcheck, help)", cmd)
	}
}
