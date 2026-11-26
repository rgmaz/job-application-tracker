package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rgmaz/job-application-tracker/internal/service"
)

func TestSettingsPage(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	rec := f.get(t, "/settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	for _, want := range []string{
		"Settings",
		"Staleness thresholds",
		"Africa/Harare",
		`name="applied"`,
		`value="14"`,
		`name="screening"`,
		`name="interviewing"`,
		`name="offer"`,
		`action="/settings/thresholds"`,
		"Save thresholds",
		"Password",
		"No password set yet.",
		"Sessions",
		"Sign out everywhere",
		`action="/settings/session-secret"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page does not contain %q", want)
		}
	}
}

func TestSettingsThresholdUpdate(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")

	rows, err := f.svc.List(context.Background(), service.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].IsStale != 0 {
		t.Fatalf("rows before update = %+v, want one non-stale application", rows)
	}

	form := url.Values{
		"applied":      {"1"},
		"screening":    {"7"},
		"interviewing": {"7"},
		"offer":        {"3"},
	}
	rec := f.post(t, "/settings/thresholds", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/settings?flash=thresholds" {
		t.Errorf("Location = %q, want /settings?flash=thresholds", got)
	}

	rows, err = f.svc.List(context.Background(), service.ListFilter{})
	if err != nil {
		t.Fatalf("List after update: %v", err)
	}
	if len(rows) != 1 || rows[0].IsStale != 1 {
		t.Errorf("rows after update = %+v, want one stale application", rows)
	}
	if rows[0].DueOn == nil || *rows[0].DueOn != "2026-11-21" {
		t.Errorf("due_on = %v, want 2026-11-21", rows[0].DueOn)
	}

	rec = f.get(t, rec.Header().Get("Location"))
	if !strings.Contains(rec.Body.String(), "Thresholds updated.") {
		t.Error("settings page does not show the thresholds flash")
	}
	if !strings.Contains(rec.Body.String(), `value="1"`) {
		t.Error("settings page does not show the saved threshold")
	}
}

func TestSettingsThresholdValidation(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	form := url.Values{
		"applied":      {"0"},
		"screening":    {"7"},
		"interviewing": {"7"},
		"offer":        {"366"},
	}
	rec := f.post(t, "/settings/thresholds", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Enter a whole number of days between 1 and 365.",
		`name="applied"`,
		`value="0"`,
		`value="366"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("validation response does not contain %q", want)
		}
	}

	thresholds, err := f.settings.Thresholds(context.Background())
	if err != nil {
		t.Fatalf("Thresholds: %v", err)
	}
	for _, threshold := range thresholds {
		if threshold.Status == "applied" && threshold.Days != 14 {
			t.Errorf("applied threshold = %d after a rejected update, want 14", threshold.Days)
		}
	}
}

func TestSettingsPasswordChange(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	ctx := context.Background()

	hash, err := service.HashPassword("old-password-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := f.db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?)",
		service.SettingPasswordHash, hash,
	); err != nil {
		t.Fatalf("store password hash: %v", err)
	}

	rec := f.get(t, "/settings")
	if !strings.Contains(rec.Body.String(), `action="/settings/password"`) {
		t.Fatal("settings page does not show the password form once a password is set")
	}

	rec = f.post(t, "/settings/password", url.Values{
		"current_password": {"wrong-password"},
		"new_password":     {"new-password-456"},
		"confirm_password": {"new-password-456"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong current password status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Current password is incorrect.") {
		t.Error("wrong current password response does not carry the field error")
	}

	rec = f.post(t, "/settings/password", url.Values{
		"current_password": {"old-password-123"},
		"new_password":     {"new-password-456"},
		"confirm_password": {"new-password-456"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/settings?flash=password" {
		t.Errorf("Location = %q, want /settings?flash=password", got)
	}

	var stored string
	if err := f.db.QueryRowContext(ctx,
		"SELECT value FROM settings WHERE key = ?", service.SettingPasswordHash,
	).Scan(&stored); err != nil {
		t.Fatalf("read password hash: %v", err)
	}
	if service.VerifyPassword(stored, "old-password-123") {
		t.Error("the old password still verifies after the change")
	}
	if !service.VerifyPassword(stored, "new-password-456") {
		t.Error("the new password does not verify after the change")
	}

	rec = f.get(t, rec.Header().Get("Location"))
	if !strings.Contains(rec.Body.String(), "Password changed.") {
		t.Error("settings page does not show the password flash")
	}
}

func TestSettingsPasswordValidation(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	ctx := context.Background()

	hash, err := service.HashPassword("old-password-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := f.db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?)",
		service.SettingPasswordHash, hash,
	); err != nil {
		t.Fatalf("store password hash: %v", err)
	}

	rec := f.post(t, "/settings/password", url.Values{
		"current_password": {"old-password-123"},
		"new_password":     {"short"},
		"confirm_password": {"different"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Password must be at least 12 characters.",
		"Passwords do not match.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("validation response does not contain %q", want)
		}
	}
}

func TestSettingsSessionSecretRotation(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	ctx := context.Background()

	before, err := f.settings.SessionSecret(ctx)
	if err != nil {
		t.Fatalf("SessionSecret: %v", err)
	}

	rec := f.post(t, "/settings/session-secret", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/settings?flash=sessions" {
		t.Errorf("Location = %q, want /settings?flash=sessions", got)
	}

	after, err := f.settings.SessionSecret(ctx)
	if err != nil {
		t.Fatalf("SessionSecret after rotation: %v", err)
	}
	if string(before) == string(after) {
		t.Error("session secret was not rotated")
	}

	rec = f.get(t, rec.Header().Get("Location"))
	if !strings.Contains(rec.Body.String(), "Signed out everywhere.") {
		t.Error("settings page does not show the sessions flash")
	}
}
