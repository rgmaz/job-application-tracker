package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/config"
	"github.com/rgmaz/job-application-tracker/internal/web"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 2 * time.Minute
	writeTimeout      = 2 * time.Minute
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func serve(ctx context.Context, cfg config.Config, stderr io.Writer) error {
	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr(), err)
	}
	return serveListener(ctx, cfg, ln, stderr)
}

func serveListener(ctx context.Context, cfg config.Config, ln net.Listener, stderr io.Writer) error {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	srv := &http.Server{
		Handler:           web.NewRouter(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening",
			"addr", ln.Addr().String(),
			"base_url", cfg.BaseURL.String(),
			"tz", cfg.Location.String(),
		)
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown requested")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		if err := <-errCh; err != nil {
			return fmt.Errorf("serve http: %w", err)
		}
		return nil
	}
}
