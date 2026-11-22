package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/store"
)

func TestServeListener(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cfg := configForURL(t, "http://"+ln.Addr().String())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveListener(ctx, cfg, ln, io.Discard)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		cancel()
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("serveListener() = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveListener did not shut down")
	}
}

func TestServeListenError(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	cfg := configForURL(t, "http://"+ln.Addr().String())
	err = serve(context.Background(), cfg, io.Discard)
	if err == nil {
		t.Fatal("serve() error = nil, want listen failure")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Errorf("serve() error = %q, want containing listen", err)
	}

	// Migrations must run before the listener is opened, even when listening
	// then fails.
	db, openErr := store.Open(context.Background(), store.Path(cfg.DataDir))
	if openErr != nil {
		t.Fatalf("open migrated database: %v", openErr)
	}
	defer func() { _ = db.Close() }()

	var version int64
	if err := db.QueryRow("SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatalf("serve did not migrate before listening: %v", err)
	}
	if version != 2 {
		t.Errorf("goose version = %d, want 2", version)
	}
}
