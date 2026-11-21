// Package config loads and validates the server configuration from the
// process environment.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBindAddr        = "0.0.0.0"
	defaultPort            = 8080
	defaultDataDir         = "./data"
	defaultBaseURL         = "http://localhost:8080"
	defaultTimezone        = "Africa/Harare"
	defaultMaxUploadMB     = 25
	defaultSessionTTLHours = 720

	maxUploadMB        = 1024
	maxSessionTTLHours = 24 * 365
)

// Config is the validated runtime configuration.
type Config struct {
	BindAddr       string
	Port           int
	DataDir        string
	BaseURL        *url.URL
	Location       *time.Location
	AuthDisabled   bool
	TrustProxyAuth bool
	CookieSecure   bool
	MaxUploadBytes int64
	SessionTTL     time.Duration
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

// Addr returns the host:port the HTTP server should listen on.
func (c Config) Addr() string {
	return net.JoinHostPort(c.BindAddr, strconv.Itoa(c.Port))
}

func load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		BindAddr: value(lookup, "BIND_ADDR", defaultBindAddr),
		DataDir:  value(lookup, "DATA_DIR", defaultDataDir),
	}

	port, err := integer(lookup, "PORT", defaultPort)
	if err != nil {
		return Config{}, err
	}
	if port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("config: PORT must be between 1 and 65535, got %d", port)
	}
	cfg.Port = port

	cfg.BaseURL, err = parseBaseURL(value(lookup, "BASE_URL", defaultBaseURL))
	if err != nil {
		return Config{}, err
	}

	tz := value(lookup, "TZ", defaultTimezone)
	cfg.Location, err = time.LoadLocation(tz)
	if err != nil {
		return Config{}, fmt.Errorf("config: TZ %q: %w", tz, err)
	}

	if cfg.AuthDisabled, err = boolean(lookup, "AUTH_DISABLED", false); err != nil {
		return Config{}, err
	}
	if cfg.TrustProxyAuth, err = boolean(lookup, "TRUST_PROXY_AUTH", false); err != nil {
		return Config{}, err
	}

	cfg.CookieSecure = cfg.BaseURL.Scheme == "https"
	if raw, ok := valueOK(lookup, "COOKIE_SECURE"); ok {
		cfg.CookieSecure, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: COOKIE_SECURE must be a boolean, got %q", raw)
		}
	}

	uploadMB, err := integer(lookup, "MAX_UPLOAD_MB", defaultMaxUploadMB)
	if err != nil {
		return Config{}, err
	}
	if uploadMB < 1 || uploadMB > maxUploadMB {
		return Config{}, fmt.Errorf("config: MAX_UPLOAD_MB must be between 1 and %d, got %d", maxUploadMB, uploadMB)
	}
	cfg.MaxUploadBytes = int64(uploadMB) << 20

	ttlHours, err := integer(lookup, "SESSION_TTL_HOURS", defaultSessionTTLHours)
	if err != nil {
		return Config{}, err
	}
	if ttlHours < 1 || ttlHours > maxSessionTTLHours {
		return Config{}, fmt.Errorf("config: SESSION_TTL_HOURS must be between 1 and %d, got %d", maxSessionTTLHours, ttlHours)
	}
	cfg.SessionTTL = time.Duration(ttlHours) * time.Hour

	if cfg.AuthDisabled && !cfg.TrustProxyAuth && !isLoopback(cfg.BindAddr) {
		return Config{}, fmt.Errorf(
			"config: AUTH_DISABLED=true requires TRUST_PROXY_AUTH=true unless BIND_ADDR is loopback (got %q)",
			cfg.BindAddr,
		)
	}

	return cfg, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("config: BASE_URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("config: BASE_URL must use http or https, got %q", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("config: BASE_URL must include a host, got %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("config: BASE_URL must not include a query or fragment, got %q", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func value(lookup func(string) (string, bool), key, fallback string) string {
	if v, ok := valueOK(lookup, key); ok {
		return v
	}
	return fallback
}

func valueOK(lookup func(string) (string, bool), key string) (string, bool) {
	raw, ok := lookup(key)
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	return raw, raw != ""
}

func integer(lookup func(string) (string, bool), key string, fallback int) (int, error) {
	raw, ok := valueOK(lookup, key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer, got %q", key, raw)
	}
	return n, nil
}

func boolean(lookup func(string) (string, bool), key string, fallback bool) (bool, error) {
	raw, ok := valueOK(lookup, key)
	if !ok {
		return fallback, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: %s must be a boolean, got %q", key, raw)
	}
	return b, nil
}
