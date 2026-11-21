package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/config"
)

const healthcheckTimeout = 3 * time.Second

func healthcheck(ctx context.Context, cfg config.Config, stdout io.Writer) error {
	url := healthcheckURL(cfg)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build healthcheck request: %w", err)
	}

	client := &http.Client{Timeout: healthcheckTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck %s: unexpected status %s", url, resp.Status)
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<10)).Decode(&body); err != nil {
		return fmt.Errorf("healthcheck %s: decode response: %w", url, err)
	}
	if body.Status != "ok" {
		return fmt.Errorf("healthcheck %s: status %q, want ok", url, body.Status)
	}

	if _, err := fmt.Fprintln(stdout, "ok"); err != nil {
		return fmt.Errorf("write healthcheck output: %w", err)
	}
	return nil
}

func healthcheckURL(cfg config.Config) string {
	return "http://" + net.JoinHostPort(probeHost(cfg.BindAddr), strconv.Itoa(cfg.Port)) + "/healthz"
}

// probeHost turns a listen address into something the local client can dial.
func probeHost(bind string) string {
	if bind == "" {
		return "127.0.0.1"
	}
	ip := net.ParseIP(bind)
	if ip == nil {
		return bind
	}
	if !ip.IsUnspecified() {
		return bind
	}
	if ip.To4() == nil {
		return "::1"
	}
	return "127.0.0.1"
}
