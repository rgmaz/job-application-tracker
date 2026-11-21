package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
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
}
