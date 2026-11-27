// Package web wires the HTTP routes and handlers.
package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/rgmaz/job-application-tracker/internal/service"
	webassets "github.com/rgmaz/job-application-tracker/web"
)

// staticCacheControl keeps the vendored assets out of every page load. They are
// versioned by release, so a one-hour cache is safe and easy to invalidate.
const staticCacheControl = "public, max-age=3600"

// Options carries the services the router exposes. Fields may be nil for
// health-only use (the healthcheck command and its tests); the application
// routes then answer 500 instead of panicking.
type Options struct {
	Apps         *service.ApplicationService
	Docs         *service.DocumentService
	Settings     *service.SettingsService
	Auth         *service.AuthService
	Renderer     *Renderer
	Logger       *slog.Logger
	BaseURL      *url.URL
	CookieSecure bool
	// AuthDisabled skips the session gate. Config.Load refuses this unless a
	// trusted proxy or a loopback bind makes the deployment boundary explicit.
	AuthDisabled bool
}

// server holds the dependencies shared by the application handlers.
type server struct {
	apps         *service.ApplicationService
	docs         *service.DocumentService
	settings     *service.SettingsService
	auth         *service.AuthService
	renderer     *Renderer
	logger       *slog.Logger
	baseURL      *url.URL
	cookieSecure bool
	authDisabled bool
}

// NewRouter returns the application's HTTP handler.
func NewRouter(opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = silentLogger
	}
	s := &server{
		apps:         opts.Apps,
		docs:         opts.Docs,
		settings:     opts.Settings,
		auth:         opts.Auth,
		renderer:     opts.Renderer,
		logger:       logger,
		baseURL:      opts.BaseURL,
		cookieSecure: opts.CookieSecure,
		authDisabled: opts.AuthDisabled,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.Handle("GET /static/", staticHandler())

	mux.HandleFunc("GET /setup", s.setupForm)
	mux.HandleFunc("POST /setup", s.setup)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)

	mux.HandleFunc("GET /dashboard", s.dashboard)
	mux.HandleFunc("GET /settings", s.settingsPage)
	mux.HandleFunc("POST /settings/thresholds", s.updateThresholds)
	mux.HandleFunc("POST /settings/password", s.changePassword)
	mux.HandleFunc("POST /settings/session-secret", s.rotateSessionSecret)
	mux.HandleFunc("GET /applications", s.listApplications)
	mux.HandleFunc("GET /applications/rows", s.applicationRows)
	mux.HandleFunc("GET /applications/new", s.newApplicationForm)
	mux.HandleFunc("POST /applications", s.createApplication)
	mux.HandleFunc("GET /applications/{id}", s.applicationDetail)
	mux.HandleFunc("GET /applications/{id}/edit", s.editApplicationForm)
	mux.HandleFunc("POST /applications/{id}", s.updateApplication)
	mux.HandleFunc("POST /applications/{id}/status", s.changeStatus)
	mux.HandleFunc("POST /applications/{id}/events", s.addEvent)
	mux.HandleFunc("POST /applications/{id}/documents", s.uploadDocument)
	mux.HandleFunc("POST /applications/{id}/delete", s.deleteApplication)
	mux.HandleFunc("GET /documents/{id}/download", s.downloadDocument)
	mux.HandleFunc("POST /documents/{id}/delete", s.deleteDocument)

	return s.middleware(mux)
}

func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// ready reports whether the application dependencies were wired. Health-only
// routers are valid, so handlers guard instead of assuming.
func (s *server) ready(w http.ResponseWriter) bool {
	if s.apps == nil || s.docs == nil || s.settings == nil || s.renderer == nil {
		http.Error(w, "application routes are not configured", http.StatusInternalServerError)
		return false
	}
	return true
}

func staticHandler() http.Handler {
	files := http.FileServerFS(webassets.FS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", staticCacheControl)
		files.ServeHTTP(w, r)
	})
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// The status line is already sent; a write error is not actionable.
	_ = json.NewEncoder(w).Encode(struct {
		Status string `json:"status"`
	}{Status: "ok"})
}
