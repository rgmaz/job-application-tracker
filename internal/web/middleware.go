package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

const (
	contentSecurityPolicy = "default-src 'self'"
	referrerPolicy        = "no-referrer"
)

// silentLogger keeps health-only routers and tests quiet when no logger is
// supplied.
var silentLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// middleware wraps the mux with the cross-cutting request policy. The order is
// outermost to innermost: log, security headers, recover, CSRF, auth. Logging
// stays outside recovery so a panic is recorded with the final 500 status.
func (s *server) middleware(next http.Handler) http.Handler {
	handler := s.requireAuth(next)
	handler = s.csrfProtect(handler)
	handler = recoverPanics(s.logger, handler)
	handler = securityHeaders(handler)
	return requestLogger(s.logger, handler)
}

// requireAuth redirects browser requests without a valid session to the login
// page. Public routes stay reachable so setup, login, health checks, and static
// assets work before a session exists.
func (s *server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil || s.authDisabled || publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		valid, err := s.currentSession(r)
		if err != nil {
			http.Error(w, "could not verify session", http.StatusInternalServerError)
			return
		}
		if !valid {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// publicPath reports whether path may be served without a session.
func publicPath(path string) bool {
	switch path {
	case "/healthz", "/login", "/setup", "/logout":
		return true
	}
	return strings.HasPrefix(path, "/static/")
}

// csrfProtect rejects state-changing requests whose Origin names another host.
// Browsers send Origin on cross-origin form posts and fetch calls; clients that
// omit it (curl, older browsers) are allowed so the service stays scriptable.
func (s *server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		origin := r.Header.Get("Origin")
		if origin == "" || s.sameOrigin(r.Host, origin) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
	})
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// sameOrigin compares hosts only. A TLS-terminating reverse proxy forwards an
// http request with the public Host, so the Origin scheme legitimately differs.
// The configured BASE_URL is also accepted because a proxy may rewrite Host to
// the upstream address while the browser still sends the public origin.
func (s *server) sameOrigin(host, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, host) {
		return true
	}
	return s.baseURL != nil && strings.EqualFold(u.Host, s.baseURL.Host)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", referrerPolicy)
		next.ServeHTTP(w, r)
	})
}

// requestLogger records one line per request. It never logs cookies, form
// bodies, or query strings, which keeps passwords out of the log.
func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = silentLogger
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.statusCode(),
			"bytes", rec.bytes,
			"duration", time.Since(start),
			"remote", clientKey(r),
		)
	})
}

// recoverPanics turns a handler panic into a 500 instead of a dropped
// connection. http.ErrAbortHandler keeps its special meaning for net/http.
func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = silentLogger
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			value := recover()
			if value == nil {
				return
			}
			if value == http.ErrAbortHandler {
				panic(value)
			}

			logger.Error("panic serving request",
				"method", r.Method,
				"path", r.URL.Path,
				"panic", value,
				"stack", string(debug.Stack()),
			)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status and size for the request log while
// passing the underlying writer through unchanged.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
