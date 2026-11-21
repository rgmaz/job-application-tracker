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
			name:    "migrate is stubbed",
			args:    []string{"migrate"},
			wantErr: "not implemented",
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

func TestRunHealthcheck(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(web.NewRouter())
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
