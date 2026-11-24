package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rgmaz/job-application-tracker/internal/config"
	"github.com/rgmaz/job-application-tracker/internal/store"
	"github.com/rgmaz/job-application-tracker/internal/web"
)

func TestRun(t *testing.T) {
	t.Parallel()

	cfg := configForURL(t, "http://127.0.0.1:8080")

	tests := []struct {
		name    string
		args    []string
		loadErr error
		wantErr string
		wantOut string
	}{
		{
			name:    "missing command",
			wantErr: "missing command",
		},
		{
			name:    "unknown command",
			args:    []string{"nope"},
			wantErr: "unknown command",
		},
		{
			name:    "help",
			args:    []string{"help"},
			wantOut: "usage: jat",
		},
		{
			name:    "help flag",
			args:    []string{"--help"},
			wantOut: "usage: jat",
		},
		{
			name:    "backup is stubbed",
			args:    []string{"backup"},
			wantErr: "not implemented",
		},
		{
			name:    "config error is returned",
			args:    []string{"serve"},
			loadErr: errors.New("bad env"),
			wantErr: "bad env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			load := func() (config.Config, error) {
				if tt.loadErr != nil {
					return config.Config{}, tt.loadErr
				}
				return cfg, nil
			}

			var out bytes.Buffer
			err := run(context.Background(), tt.args, load, &out, io.Discard)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("run() error = nil, want containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("run() error = %q, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output = %q, want containing %q", out.String(), tt.wantOut)
			}
		})
	}
}

func TestRunMigrate(t *testing.T) {
	t.Parallel()

	cfg := configForURL(t, "http://127.0.0.1:8080")
	load := func() (config.Config, error) { return cfg, nil }

	var out bytes.Buffer
	if err := run(context.Background(), []string{"migrate"}, load, &out, io.Discard); err != nil {
		t.Fatalf("run(migrate): %v", err)
	}
	if !strings.Contains(out.String(), "migrations applied") {
		t.Errorf("output = %q, want containing %q", out.String(), "migrations applied")
	}

	db, err := store.Open(context.Background(), store.Path(cfg.DataDir))
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var version int64
	if err := db.QueryRow("SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatalf("read goose_db_version: %v", err)
	}
	if version != 2 {
		t.Errorf("goose version = %d, want 2", version)
	}
}

func TestRunHealthcheck(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(web.NewRouter(nil, nil))
	t.Cleanup(srv.Close)

	cfg := configForURL(t, srv.URL)
	load := func() (config.Config, error) { return cfg, nil }

	var out bytes.Buffer
	if err := run(context.Background(), []string{"healthcheck"}, load, &out, io.Discard); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got := out.String(); got != "ok\n" {
		t.Errorf("output = %q, want ok\\n", got)
	}
}
