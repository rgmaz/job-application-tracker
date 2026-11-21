package config

import (
	"strings"
	"testing"
	"time"
)

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		want    func(*testing.T, Config)
		wantErr string
	}{
		{
			name: "defaults",
			env:  map[string]string{},
			want: func(t *testing.T, c Config) {
				t.Helper()
				if c.BindAddr != "0.0.0.0" {
					t.Errorf("BindAddr = %q, want 0.0.0.0", c.BindAddr)
				}
				if c.Port != 8080 {
					t.Errorf("Port = %d, want 8080", c.Port)
				}
				if c.DataDir != "./data" {
					t.Errorf("DataDir = %q, want ./data", c.DataDir)
				}
				if got := c.BaseURL.String(); got != "http://localhost:8080" {
					t.Errorf("BaseURL = %q, want http://localhost:8080", got)
				}
				if got := c.Location.String(); got != "Africa/Harare" {
					t.Errorf("Location = %q, want Africa/Harare", got)
				}
				if c.AuthDisabled || c.TrustProxyAuth || c.CookieSecure {
					t.Errorf("auth defaults = disabled:%t trust:%t secure:%t, want all false",
						c.AuthDisabled, c.TrustProxyAuth, c.CookieSecure)
				}
				if c.MaxUploadBytes != 25<<20 {
					t.Errorf("MaxUploadBytes = %d, want %d", c.MaxUploadBytes, 25<<20)
				}
				if c.SessionTTL != 720*time.Hour {
					t.Errorf("SessionTTL = %v, want 720h", c.SessionTTL)
				}
				if got := c.Addr(); got != "0.0.0.0:8080" {
					t.Errorf("Addr() = %q, want 0.0.0.0:8080", got)
				}
			},
		},
		{
			name: "overrides",
			env: map[string]string{
				"BIND_ADDR":         "127.0.0.1",
				"PORT":              "9090",
				"DATA_DIR":          "/srv/jat",
				"BASE_URL":          "https://jobs.example.com/",
				"TZ":                "UTC",
				"AUTH_DISABLED":     "true",
				"TRUST_PROXY_AUTH":  "true",
				"COOKIE_SECURE":     "false",
				"MAX_UPLOAD_MB":     "10",
				"SESSION_TTL_HOURS": "24",
			},
			want: func(t *testing.T, c Config) {
				t.Helper()
				if c.BindAddr != "127.0.0.1" {
					t.Errorf("BindAddr = %q, want 127.0.0.1", c.BindAddr)
				}
				if c.Port != 9090 {
					t.Errorf("Port = %d, want 9090", c.Port)
				}
				if c.DataDir != "/srv/jat" {
					t.Errorf("DataDir = %q, want /srv/jat", c.DataDir)
				}
				if got := c.BaseURL.String(); got != "https://jobs.example.com" {
					t.Errorf("BaseURL = %q, want https://jobs.example.com", got)
				}
				if got := c.Location.String(); got != "UTC" {
					t.Errorf("Location = %q, want UTC", got)
				}
				if !c.AuthDisabled || !c.TrustProxyAuth {
					t.Errorf("auth = disabled:%t trust:%t, want both true", c.AuthDisabled, c.TrustProxyAuth)
				}
				if c.CookieSecure {
					t.Error("CookieSecure = true, want explicit false override")
				}
				if c.MaxUploadBytes != 10<<20 {
					t.Errorf("MaxUploadBytes = %d, want %d", c.MaxUploadBytes, 10<<20)
				}
				if c.SessionTTL != 24*time.Hour {
					t.Errorf("SessionTTL = %v, want 24h", c.SessionTTL)
				}
				if got := c.Addr(); got != "127.0.0.1:9090" {
					t.Errorf("Addr() = %q, want 127.0.0.1:9090", got)
				}
			},
		},
		{
			name: "https base url implies secure cookie",
			env:  map[string]string{"BASE_URL": "https://jobs.example.com"},
			want: func(t *testing.T, c Config) {
				t.Helper()
				if !c.CookieSecure {
					t.Error("CookieSecure = false, want true for https BASE_URL")
				}
			},
		},
		{
			name: "base url path is preserved without trailing slash",
			env:  map[string]string{"BASE_URL": "https://example.com/jat/"},
			want: func(t *testing.T, c Config) {
				t.Helper()
				if got := c.BaseURL.String(); got != "https://example.com/jat" {
					t.Errorf("BaseURL = %q, want https://example.com/jat", got)
				}
			},
		},
		{
			name: "auth disabled allowed on ipv6 loopback",
			env: map[string]string{
				"BIND_ADDR":     "::1",
				"AUTH_DISABLED": "true",
			},
			want: func(t *testing.T, c Config) {
				t.Helper()
				if !c.AuthDisabled {
					t.Error("AuthDisabled = false, want true")
				}
				if got := c.Addr(); got != "[::1]:8080" {
					t.Errorf("Addr() = %q, want [::1]:8080", got)
				}
			},
		},
		{
			name: "auth disabled allowed on localhost",
			env: map[string]string{
				"BIND_ADDR":     "localhost",
				"AUTH_DISABLED": "true",
			},
		},
		{
			name: "auth disabled allowed with trusted proxy",
			env: map[string]string{
				"AUTH_DISABLED":    "true",
				"TRUST_PROXY_AUTH": "true",
			},
			want: func(t *testing.T, c Config) {
				t.Helper()
				if !c.AuthDisabled || !c.TrustProxyAuth {
					t.Errorf("auth = disabled:%t trust:%t, want both true", c.AuthDisabled, c.TrustProxyAuth)
				}
			},
		},
		{
			name: "empty values fall back to defaults",
			env: map[string]string{
				"PORT":          "",
				"TZ":            "",
				"AUTH_DISABLED": "",
			},
			want: func(t *testing.T, c Config) {
				t.Helper()
				if c.Port != 8080 {
					t.Errorf("Port = %d, want 8080", c.Port)
				}
				if got := c.Location.String(); got != "Africa/Harare" {
					t.Errorf("Location = %q, want Africa/Harare", got)
				}
				if c.AuthDisabled {
					t.Error("AuthDisabled = true, want false")
				}
			},
		},
		{
			name:    "auth disabled on public interface is rejected",
			env:     map[string]string{"AUTH_DISABLED": "true"},
			wantErr: "TRUST_PROXY_AUTH",
		},
		{
			name:    "invalid port",
			env:     map[string]string{"PORT": "abc"},
			wantErr: "PORT",
		},
		{
			name:    "port out of range",
			env:     map[string]string{"PORT": "70000"},
			wantErr: "PORT",
		},
		{
			name:    "invalid boolean",
			env:     map[string]string{"AUTH_DISABLED": "maybe"},
			wantErr: "AUTH_DISABLED",
		},
		{
			name:    "invalid timezone",
			env:     map[string]string{"TZ": "Mars/Olympus"},
			wantErr: "TZ",
		},
		{
			name:    "base url without scheme",
			env:     map[string]string{"BASE_URL": "jobs.example.com"},
			wantErr: "BASE_URL",
		},
		{
			name:    "base url with unsupported scheme",
			env:     map[string]string{"BASE_URL": "ftp://jobs.example.com"},
			wantErr: "BASE_URL",
		},
		{
			name:    "base url with query",
			env:     map[string]string{"BASE_URL": "https://jobs.example.com/?a=1"},
			wantErr: "BASE_URL",
		},
		{
			name:    "upload size out of range",
			env:     map[string]string{"MAX_UPLOAD_MB": "0"},
			wantErr: "MAX_UPLOAD_MB",
		},
		{
			name:    "session ttl out of range",
			env:     map[string]string{"SESSION_TTL_HOURS": "0"},
			wantErr: "SESSION_TTL_HOURS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := load(lookupFrom(tt.env))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("load() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("load() error = %q, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("load() error = %v", err)
			}
			if tt.want != nil {
				tt.want(t, got)
			}
		})
	}
}
