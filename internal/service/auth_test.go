package service_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/service"
)

const testSessionTTL = 720 * time.Hour

func newAuthFixture(t *testing.T, now time.Time) (*settingsFixture, *service.AuthService) {
	t.Helper()

	f := newSettingsFixture(t, now)
	return f, service.NewAuth(f.settings, f.clock, testSessionTTL)
}

func TestAuthSetup(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	f, auth := newAuthFixture(t, time.Date(2026, 11, 26, 9, 0, 0, 0, loc))
	ctx := context.Background()

	set, err := auth.PasswordSet(ctx)
	if err != nil {
		t.Fatalf("PasswordSet: %v", err)
	}
	if set {
		t.Error("PasswordSet = true before setup")
	}

	tests := []struct {
		name       string
		password   string
		confirm    string
		wantFields []string
	}{
		{
			name:       "too short",
			password:   "short",
			confirm:    "short",
			wantFields: []string{"password"},
		},
		{
			name:       "confirmation mismatch",
			password:   "a-long-enough-password",
			confirm:    "a-different-password",
			wantFields: []string{"confirm_password"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.Setup(ctx, tt.password, tt.confirm)
			fields := validationFields(t, err)
			if got := sortedKeys(fields); !reflect.DeepEqual(got, tt.wantFields) {
				t.Errorf("fields = %v, want %v", got, tt.wantFields)
			}
		})
	}

	if err := auth.Setup(ctx, "a-long-enough-password", "a-long-enough-password"); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	set, err = auth.PasswordSet(ctx)
	if err != nil {
		t.Fatalf("PasswordSet after setup: %v", err)
	}
	if !set {
		t.Error("PasswordSet = false after setup")
	}

	stored, err := f.q.GetSetting(ctx, service.SettingPasswordHash)
	if err != nil {
		t.Fatalf("read password hash: %v", err)
	}
	if !service.VerifyPassword(stored, "a-long-enough-password") {
		t.Error("the stored hash does not verify the setup password")
	}

	if err := auth.Setup(ctx, "another-long-password", "another-long-password"); !errors.Is(err, service.ErrAlreadyConfigured) {
		t.Errorf("second Setup error = %v, want ErrAlreadyConfigured", err)
	}
}

func TestAuthLogin(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	_, auth := newAuthFixture(t, time.Date(2026, 11, 26, 9, 0, 0, 0, loc))
	ctx := context.Background()

	if err := auth.Login(ctx, "127.0.0.1", "anything"); !errors.Is(err, service.ErrInvalidCredentials) {
		t.Errorf("Login before setup error = %v, want ErrInvalidCredentials", err)
	}

	if err := auth.Setup(ctx, "correct-password-123", "correct-password-123"); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if err := auth.Login(ctx, "127.0.0.1", "wrong-password"); !errors.Is(err, service.ErrInvalidCredentials) {
		t.Errorf("Login(wrong) error = %v, want ErrInvalidCredentials", err)
	}
	if err := auth.Login(ctx, "127.0.0.1", "correct-password-123"); err != nil {
		t.Errorf("Login(correct) error = %v, want nil", err)
	}
}

func TestAuthLoginRateLimit(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	f, auth := newAuthFixture(t, time.Date(2026, 11, 26, 9, 0, 0, 0, loc))
	ctx := context.Background()

	if err := auth.Setup(ctx, "correct-password-123", "correct-password-123"); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	for i := range 5 {
		if err := auth.Login(ctx, "203.0.113.5", "wrong-password"); !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidCredentials", i+1, err)
		}
	}

	if err := auth.Login(ctx, "203.0.113.5", "correct-password-123"); !errors.Is(err, service.ErrRateLimited) {
		t.Errorf("Login after the burst error = %v, want ErrRateLimited", err)
	}

	// A different client is not affected by this address's window.
	if err := auth.Login(ctx, "198.51.100.9", "correct-password-123"); err != nil {
		t.Errorf("Login from another address error = %v, want nil", err)
	}

	f.clock.now = f.clock.now.Add(16 * time.Minute)
	if err := auth.Login(ctx, "203.0.113.5", "correct-password-123"); err != nil {
		t.Errorf("Login after the window error = %v, want nil", err)
	}
}

func TestAuthLoginSuccessClearsFailures(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	_, auth := newAuthFixture(t, time.Date(2026, 11, 26, 9, 0, 0, 0, loc))
	ctx := context.Background()

	if err := auth.Setup(ctx, "correct-password-123", "correct-password-123"); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	for range 4 {
		if err := auth.Login(ctx, "127.0.0.1", "wrong-password"); !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("Login(wrong) error = %v, want ErrInvalidCredentials", err)
		}
	}
	if err := auth.Login(ctx, "127.0.0.1", "correct-password-123"); err != nil {
		t.Fatalf("Login(correct) error = %v, want nil", err)
	}

	for range 4 {
		if err := auth.Login(ctx, "127.0.0.1", "wrong-password"); !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("Login(wrong) after success error = %v, want ErrInvalidCredentials", err)
		}
	}
}

func TestAuthSession(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, loc)
	f, auth := newAuthFixture(t, now)
	ctx := context.Background()

	token, expires, err := auth.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if token == "" || strings.Contains(token, "=") {
		t.Errorf("token = %q, want a non-empty unpadded value", token)
	}
	if want := now.Add(testSessionTTL); !expires.Equal(want) {
		t.Errorf("expires = %v, want %v", expires, want)
	}

	valid, err := auth.ValidateSession(ctx, token)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if !valid {
		t.Error("ValidateSession rejected a fresh token")
	}

	tampered := token[:len(token)-1] + "A"
	if tampered == token {
		tampered = token[:len(token)-1] + "B"
	}
	if valid, err := auth.ValidateSession(ctx, tampered); err != nil || valid {
		t.Errorf("ValidateSession(tampered) = %v, %v; want false, nil", valid, err)
	}

	if valid, err := auth.ValidateSession(ctx, token+"x"); err != nil || valid {
		t.Errorf("ValidateSession(extra suffix) = %v, %v; want false, nil", valid, err)
	}

	for _, malformed := range []string{"", ".", "nodot", "a.b.c", "$"} {
		if valid, err := auth.ValidateSession(ctx, malformed); err != nil || valid {
			t.Errorf("ValidateSession(%q) = %v, %v; want false, nil", malformed, valid, err)
		}
	}

	f.clock.now = now.Add(testSessionTTL + time.Minute)
	if valid, err := auth.ValidateSession(ctx, token); err != nil || valid {
		t.Errorf("ValidateSession(expired) = %v, %v; want false, nil", valid, err)
	}

	f.clock.now = now.Add(-2 * time.Minute)
	if valid, err := auth.ValidateSession(ctx, token); err != nil || valid {
		t.Errorf("ValidateSession(issued in the future) = %v, %v; want false, nil", valid, err)
	}
}

func TestAuthSessionRotationInvalidatesTokens(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	f, auth := newAuthFixture(t, time.Date(2026, 11, 26, 9, 0, 0, 0, loc))
	ctx := context.Background()

	token, _, err := auth.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if valid, err := auth.ValidateSession(ctx, token); err != nil || !valid {
		t.Fatalf("ValidateSession before rotation = %v, %v; want true, nil", valid, err)
	}

	if err := f.settings.RotateSessionSecret(ctx); err != nil {
		t.Fatalf("RotateSessionSecret: %v", err)
	}

	if valid, err := auth.ValidateSession(ctx, token); err != nil || valid {
		t.Errorf("ValidateSession after rotation = %v, %v; want false, nil", valid, err)
	}

	fresh, _, err := auth.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession after rotation: %v", err)
	}
	if valid, err := auth.ValidateSession(ctx, fresh); err != nil || !valid {
		t.Errorf("ValidateSession(fresh) = %v, %v; want true, nil", valid, err)
	}
}

func TestAuthSessionTTLDefault(t *testing.T) {
	t.Parallel()

	f := newSettingsFixture(t, time.Date(2026, 11, 26, 9, 0, 0, 0, harare(t)))
	auth := service.NewAuth(f.settings, f.clock, 0)
	if got := auth.SessionTTL(); got != 30*24*time.Hour {
		t.Errorf("SessionTTL = %v, want 720h", got)
	}
}
