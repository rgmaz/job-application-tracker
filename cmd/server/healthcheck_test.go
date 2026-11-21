package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/config"
	"github.com/rgmaz/job-application-tracker/internal/web"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u
}

func configForURL(t *testing.T, raw string) config.Config {
	t.Helper()
	u := mustURL(t, raw)
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host %q: %v", u.Host, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return config.Config{
		BindAddr:       host,
		Port:           port,
		BaseURL:        u,
		Location:       time.UTC,
		MaxUploadBytes: 1 << 20,
		SessionTTL:     time.Hour,
	}
}

func TestHealthcheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler http.Handler
		wantErr string
	}{
		{
			name:    "healthy",
			handler: web.NewRouter(),
		},
		{
			name: "unexpected status",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "down", http.StatusServiceUnavailable)
			}),
			wantErr: "503",
		},
		{
			name: "invalid body",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "not json")
			}),
			wantErr: "decode",
		},
		{
			name: "wrong status value",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"status":"degraded"}`)
			}),
			wantErr: "degraded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(tt.handler)
			t.Cleanup(srv.Close)

			var out bytes.Buffer
			err := healthcheck(context.Background(), configForURL(t, srv.URL), &out)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("healthcheck() error = nil, want containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("healthcheck() error = %q, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("healthcheck() error = %v", err)
			}
			if got := out.String(); got != "ok\n" {
				t.Errorf("output = %q, want ok\\n", got)
			}
		})
	}
}

func TestHealthcheckUnreachable(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cfg := configForURL(t, "http://"+ln.Addr().String())
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := healthcheck(ctx, cfg, io.Discard); err == nil {
		t.Fatal("healthcheck() error = nil, want connection failure")
	}
}

func TestHealthcheckURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{
			name: "unspecified ipv4 probes loopback",
			cfg:  config.Config{BindAddr: "0.0.0.0", Port: 8080},
			want: "http://127.0.0.1:8080/healthz",
		},
		{
			name: "unspecified ipv6 probes loopback",
			cfg:  config.Config{BindAddr: "::", Port: 8080},
			want: "http://[::1]:8080/healthz",
		},
		{
			name: "specific address is used",
			cfg:  config.Config{BindAddr: "192.168.1.10", Port: 9090},
			want: "http://192.168.1.10:9090/healthz",
		},
		{
			name: "hostname is used",
			cfg:  config.Config{BindAddr: "localhost", Port: 8080},
			want: "http://localhost:8080/healthz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := healthcheckURL(tt.cfg); got != tt.want {
				t.Errorf("healthcheckURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
