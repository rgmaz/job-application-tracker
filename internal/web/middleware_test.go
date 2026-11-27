package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthGateRedirectsAnonymousRequests(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.signOut()

	for _, target := range []string{
		"/",
		"/dashboard",
		"/applications",
		"/applications/new",
		"/settings",
		"/documents/1/download",
	} {
		t.Run(target, func(t *testing.T) {
			rec := f.get(t, target)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("GET %s status = %d, want 303: %s", target, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != "/login" {
				t.Errorf("GET %s Location = %q, want /login", target, got)
			}
		})
	}
}

func TestAuthGateRejectsTamperedSession(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.session = sessionCookieName + "=not-a-signed-token"

	rec := f.get(t, "/dashboard")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("tampered session = %d %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAuthGateAllowsPublicPaths(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.signOut()

	tests := []struct {
		target     string
		wantStatus int
		wantBody   string
	}{
		{target: "/healthz", wantStatus: http.StatusOK, wantBody: `"status":"ok"`},
		{target: "/setup", wantStatus: http.StatusOK, wantBody: "Set up"},
		{target: "/static/app.css", wantStatus: http.StatusOK, wantBody: ""},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := f.get(t, tt.target)
			if rec.Code != tt.wantStatus {
				t.Fatalf("GET %s status = %d, want %d", tt.target, rec.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("GET %s body does not contain %q", tt.target, tt.wantBody)
			}
		})
	}
}

func TestCSRFOriginCheck(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	rec := f.request(t, http.MethodPost, "/applications", createForm(), map[string]string{
		"Origin": "https://evil.example",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign Origin status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := f.count(t); got != 0 {
		t.Errorf("applications after rejected cross-origin post = %d, want 0", got)
	}

	// A TLS-terminating proxy forwards http with the public Host, so the
	// scheme must not be part of the comparison.
	rec = f.request(t, http.MethodPost, "/applications", createForm(), map[string]string{
		"Origin": "https://example.com",
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("same-origin post status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := f.count(t); got != 1 {
		t.Errorf("applications after same-origin post = %d, want 1", got)
	}

	rec = f.request(t, http.MethodPost, "/applications", createForm(), map[string]string{
		"Origin": "null",
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("opaque Origin status = %d, want 403", rec.Code)
	}

	rec = f.request(t, http.MethodGet, "/applications", nil, map[string]string{
		"Origin": "https://evil.example",
	})
	if rec.Code != http.StatusOK {
		t.Errorf("cross-origin GET status = %d, want 200", rec.Code)
	}
}

func TestCSRFOriginCheckUsesConfiguredBaseURL(t *testing.T) {
	t.Parallel()

	base, err := url.Parse("https://jobs.example.com")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	f := newWebFixtureWithOptions(t, Options{BaseURL: base})

	// The test request's Host is example.com, so this only passes through the
	// configured public origin, as it would behind a Host-rewriting proxy.
	rec := f.request(t, http.MethodPost, "/applications", createForm(), map[string]string{
		"Origin": "https://jobs.example.com",
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("configured-origin post status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := f.count(t); got != 1 {
		t.Errorf("applications = %d, want 1", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	tests := []struct {
		name   string
		code   int
		mutate func()
	}{
		{name: "page", code: http.StatusOK},
		{
			name: "redirect",
			code: http.StatusSeeOther,
			mutate: func() {
				f.signOut()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mutate != nil {
				tt.mutate()
			}
			rec := f.get(t, "/dashboard")
			if rec.Code != tt.code {
				t.Fatalf("status = %d, want %d", rec.Code, tt.code)
			}

			want := map[string]string{
				"Content-Security-Policy": contentSecurityPolicy,
				"X-Frame-Options":         "DENY",
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         referrerPolicy,
			}
			for header, value := range want {
				if got := rec.Header().Get(header); got != value {
					t.Errorf("%s = %q, want %q", header, got, value)
				}
			}
		})
	}
}

func TestAuthDisabledBypassesGate(t *testing.T) {
	t.Parallel()

	f := newWebFixtureWithOptions(t, Options{AuthDisabled: true})
	f.signOut()

	rec := f.get(t, "/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("auth-disabled GET /dashboard = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestPanicRecovery(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := recoverPanics(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Errorf("body = %q, want a generic error", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "panic serving request") || !strings.Contains(logs.String(), "boom") {
		t.Errorf("panic was not logged: %s", logs.String())
	}
}

func TestRequestLogging(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := requestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/logged?secret=hidden", nil))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	line := logs.String()
	for _, want := range []string{
		"http request",
		"method=POST",
		"path=/logged",
		"status=201",
		"bytes=7",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log line does not contain %q: %s", want, line)
		}
	}
	if strings.Contains(line, "secret=hidden") {
		t.Errorf("log line leaks the query string: %s", line)
	}
}
