package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testPassword = "correct-password-123"

func (f *webFixture) setupPassword(t *testing.T) {
	t.Helper()

	if err := f.auth.Setup(context.Background(), testPassword, testPassword); err != nil {
		t.Fatalf("Setup: %v", err)
	}
}

func sessionCookie(t *testing.T, rec interface{ Result() *http.Response }) *http.Cookie {
	t.Helper()

	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie
		}
	}
	return nil
}

func TestSetupFlow(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	rec := f.get(t, "/setup")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /setup status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Set up",
		`action="/setup"`,
		`name="password"`,
		`name="confirm_password"`,
		"At least 12 characters.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("setup page does not contain %q", want)
		}
	}

	rec = f.post(t, "/setup", url.Values{
		"password":         {"short"},
		"confirm_password": {"different"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid setup status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"Password must be at least 12 characters.",
		"Passwords do not match.",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("invalid setup response does not contain %q", want)
		}
	}

	rec = f.post(t, "/setup", url.Values{
		"password":         {testPassword},
		"confirm_password": {testPassword},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("setup status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/dashboard?flash=welcome" {
		t.Errorf("Location = %q, want /dashboard?flash=welcome", got)
	}

	cookie := sessionCookie(t, rec)
	if cookie == nil {
		t.Fatal("setup response did not set a session cookie")
	}
	if !cookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, want /", cookie.Path)
	}
	if cookie.MaxAge != int((30 * 24 * 60 * 60)) {
		t.Errorf("MaxAge = %d, want 30 days", cookie.MaxAge)
	}
	if valid, err := f.auth.ValidateSession(context.Background(), cookie.Value); err != nil || !valid {
		t.Errorf("cookie token validity = %v, %v; want true, nil", valid, err)
	}

	rec = f.get(t, "/setup")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("GET /setup after setup = %d %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}

	rec = f.post(t, "/setup", url.Values{
		"password":         {"another-password-456"},
		"confirm_password": {"another-password-456"},
	})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?flash=configured" {
		t.Errorf("second setup = %d %q, want 303 /login?flash=configured", rec.Code, rec.Header().Get("Location"))
	}
	if err := f.auth.Login(context.Background(), "127.0.0.1", testPassword); err != nil {
		t.Errorf("the original password no longer works after a rejected second setup: %v", err)
	}
}

func TestLoginFlow(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.signOut()

	rec := f.get(t, "/login")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/setup" {
		t.Fatalf("GET /login before setup = %d %q, want 303 /setup", rec.Code, rec.Header().Get("Location"))
	}

	f.setupPassword(t)

	rec = f.get(t, "/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Sign in", `action="/login"`, `name="password"`, "current-password"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("login page does not contain %q", want)
		}
	}

	rec = f.post(t, "/login", url.Values{"password": {"wrong-password"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Incorrect password.") {
		t.Error("wrong password response does not explain the failure")
	}
	if cookie := sessionCookie(t, rec); cookie != nil {
		t.Error("a failed login set a session cookie")
	}

	rec = f.post(t, "/login", url.Values{"password": {testPassword}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", got)
	}
	cookie := sessionCookie(t, rec)
	if cookie == nil {
		t.Fatal("login response did not set a session cookie")
	}
	if valid, err := f.auth.ValidateSession(context.Background(), cookie.Value); err != nil || !valid {
		t.Errorf("login cookie validity = %v, %v; want true, nil", valid, err)
	}

	req := f.request(t, http.MethodGet, "/login", nil, map[string]string{
		"Cookie": sessionCookieName + "=" + cookie.Value,
	})
	if req.Code != http.StatusSeeOther || req.Header().Get("Location") != "/dashboard" {
		t.Errorf("GET /login with a valid session = %d %q, want 303 /dashboard", req.Code, req.Header().Get("Location"))
	}
}

func TestLogout(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.signOut()
	f.setupPassword(t)

	rec := f.post(t, "/logout", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/login?flash=signed_out" {
		t.Errorf("Location = %q, want /login?flash=signed_out", got)
	}
	cookie := sessionCookie(t, rec)
	if cookie == nil {
		t.Fatal("logout did not clear the session cookie")
	}
	if cookie.MaxAge >= 0 {
		t.Errorf("cleared cookie MaxAge = %d, want a negative value", cookie.MaxAge)
	}

	rec = f.get(t, "/login?flash=signed_out")
	if !strings.Contains(rec.Body.String(), "Signed out.") {
		t.Error("login page does not show the signed out flash")
	}
}

func TestLoginRateLimit(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.setupPassword(t)

	for i := range 5 {
		rec := f.post(t, "/login", url.Values{"password": {"wrong-password"}})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i+1, rec.Code)
		}
	}

	rec := f.post(t, "/login", url.Values{"password": {testPassword}})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status after burst = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Too many attempts.") {
		t.Error("rate-limited response does not explain the wait")
	}
	if cookie := sessionCookie(t, rec); cookie != nil {
		t.Error("a rate-limited login set a session cookie")
	}
}

func TestLoginCookieTampering(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.setupPassword(t)

	rec := f.post(t, "/login", url.Values{"password": {testPassword}})
	cookie := sessionCookie(t, rec)
	if cookie == nil {
		t.Fatal("login did not set a session cookie")
	}

	tampered := cookie.Value[:len(cookie.Value)-1] + "A"
	if tampered == cookie.Value {
		tampered = cookie.Value[:len(cookie.Value)-1] + "B"
	}
	req := f.request(t, http.MethodGet, "/login", nil, map[string]string{
		"Cookie": sessionCookieName + "=" + tampered,
	})
	if req.Code != http.StatusOK {
		t.Errorf("GET /login with a tampered cookie = %d, want the login form", req.Code)
	}
}

func TestSessionCookieSecureFlag(t *testing.T) {
	t.Parallel()

	f := newWebFixtureWithOptions(t, Options{CookieSecure: true})
	f.setupPassword(t)

	rec := f.post(t, "/login", url.Values{"password": {testPassword}})
	cookie := sessionCookie(t, rec)
	if cookie == nil {
		t.Fatal("login did not set a session cookie")
	}
	if !cookie.Secure {
		t.Error("Secure option did not mark the session cookie Secure")
	}
}

// TestAuthLifecycle walks the whole browser journey through the router: an
// anonymous request is gated, setup signs the user in, the session reaches a
// protected page, logout clears it, and password login starts a new one.
func TestAuthLifecycle(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.signOut()

	rec := f.get(t, "/dashboard")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous dashboard = %d %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}

	rec = f.post(t, "/setup", url.Values{
		"password":         {testPassword},
		"confirm_password": {testPassword},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("setup status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	cookie := sessionCookie(t, rec)
	if cookie == nil {
		t.Fatal("setup did not set a session cookie")
	}
	session := sessionCookieName + "=" + cookie.Value

	rec = f.request(t, http.MethodGet, "/dashboard", nil, map[string]string{"Cookie": session})
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated dashboard = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rec = f.request(t, http.MethodPost, "/logout", nil, map[string]string{"Cookie": session})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/login?flash=signed_out" {
		t.Errorf("logout Location = %q, want /login?flash=signed_out", got)
	}
	if cleared := sessionCookie(t, rec); cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("logout cookie = %+v, want a clearing cookie", cleared)
	}

	rec = f.get(t, "/dashboard")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("dashboard after logout = %d %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}

	rec = f.post(t, "/login", url.Values{"password": {"wrong-password"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong login status = %d, want 401", rec.Code)
	}

	rec = f.post(t, "/login", url.Values{"password": {testPassword}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	cookie = sessionCookie(t, rec)
	if cookie == nil {
		t.Fatal("login did not set a session cookie")
	}

	rec = f.request(t, http.MethodGet, "/dashboard", nil, map[string]string{
		"Cookie": sessionCookieName + "=" + cookie.Value,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard after login = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginRateLimitExpires(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.signOut()
	f.setupPassword(t)

	for range 5 {
		rec := f.post(t, "/login", url.Values{"password": {"wrong-password"}})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt status = %d, want 401", rec.Code)
		}
	}
	if rec := f.post(t, "/login", url.Values{"password": {testPassword}}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status inside the window = %d, want 429", rec.Code)
	}

	f.clock.now = f.clock.now.Add(16 * time.Minute)

	rec := f.post(t, "/login", url.Values{"password": {testPassword}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status after the window = %d, want 303: %s", rec.Code, rec.Body.String())
	}
}
