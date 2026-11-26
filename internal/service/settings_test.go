package service_test

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/model"
	"github.com/rgmaz/job-application-tracker/internal/service"
	"github.com/rgmaz/job-application-tracker/internal/store"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
)

type settingsFixture struct {
	apps     *service.ApplicationService
	settings *service.SettingsService
	q        *storedb.Queries
	clock    *fixedClock
}

func newSettingsFixture(t *testing.T, now time.Time) *settingsFixture {
	t.Helper()

	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}

	clock := &fixedClock{now: now}
	return &settingsFixture{
		apps:     service.NewApplications(db, clock),
		settings: service.NewSettings(db, harare(t)),
		q:        storedb.New(db),
		clock:    clock,
	}
}

func thresholdMap(values map[string]string) map[string]string {
	defaults := map[string]string{
		"applied":      "14",
		"screening":    "7",
		"interviewing": "7",
		"offer":        "3",
	}
	for status, days := range values {
		defaults[status] = days
	}
	return defaults
}

func thresholdDays(t *testing.T, values []service.ThresholdValue) map[string]int {
	t.Helper()

	days := make(map[string]int, len(values))
	for _, value := range values {
		days[string(value.Status)] = value.Days
	}
	return days
}

func TestSettingsThresholds(t *testing.T) {
	t.Parallel()

	f := newSettingsFixture(t, time.Date(2026, 11, 25, 10, 0, 0, 0, harare(t)))
	ctx := context.Background()

	got, err := f.settings.Thresholds(ctx)
	if err != nil {
		t.Fatalf("Thresholds: %v", err)
	}
	want := map[string]int{"applied": 14, "screening": 7, "interviewing": 7, "offer": 3}
	if days := thresholdDays(t, got); !reflect.DeepEqual(days, want) {
		t.Errorf("thresholds = %v, want %v", days, want)
	}
	for _, value := range got {
		if value.Status.IsTerminal() {
			t.Errorf("Thresholds included terminal status %q", value.Status)
		}
	}

	if err := f.settings.UpdateThresholds(ctx, thresholdMap(map[string]string{
		"applied":      "2",
		"screening":    "5",
		"interviewing": "6",
		"offer":        "4",
	})); err != nil {
		t.Fatalf("UpdateThresholds: %v", err)
	}

	got, err = f.settings.Thresholds(ctx)
	if err != nil {
		t.Fatalf("Thresholds after update: %v", err)
	}
	want = map[string]int{"applied": 2, "screening": 5, "interviewing": 6, "offer": 4}
	if days := thresholdDays(t, got); !reflect.DeepEqual(days, want) {
		t.Errorf("thresholds after update = %v, want %v", days, want)
	}
}

func TestSettingsThresholdValidation(t *testing.T) {
	t.Parallel()

	f := newSettingsFixture(t, time.Date(2026, 11, 25, 10, 0, 0, 0, harare(t)))
	ctx := context.Background()

	missingOffer := thresholdMap(nil)
	delete(missingOffer, "offer")
	terminal := thresholdMap(map[string]string{"accepted": "5"})

	tests := []struct {
		name       string
		values     map[string]string
		wantFields []string
	}{
		{
			name:       "zero days",
			values:     thresholdMap(map[string]string{"applied": "0"}),
			wantFields: []string{"applied"},
		},
		{
			name:       "above the maximum",
			values:     thresholdMap(map[string]string{"offer": "366"}),
			wantFields: []string{"offer"},
		},
		{
			name:       "not a number",
			values:     thresholdMap(map[string]string{"screening": "soon"}),
			wantFields: []string{"screening"},
		},
		{
			name:       "missing status",
			values:     missingOffer,
			wantFields: []string{"offer"},
		},
		{
			name:       "terminal status",
			values:     terminal,
			wantFields: []string{"accepted"},
		},
		{
			name: "several invalid",
			values: thresholdMap(map[string]string{
				"applied":      "-1",
				"interviewing": "1000",
			}),
			wantFields: []string{"applied", "interviewing"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := f.settings.UpdateThresholds(ctx, tt.values)
			fields := validationFields(t, err)
			if got := sortedKeys(fields); !reflect.DeepEqual(got, tt.wantFields) {
				t.Errorf("fields = %v, want %v", got, tt.wantFields)
			}
		})
	}

	got, err := f.settings.Thresholds(ctx)
	if err != nil {
		t.Fatalf("Thresholds after failed updates: %v", err)
	}
	want := map[string]int{"applied": 14, "screening": 7, "interviewing": 7, "offer": 3}
	if days := thresholdDays(t, got); !reflect.DeepEqual(days, want) {
		t.Errorf("thresholds changed after invalid input: %v, want %v", days, want)
	}
}

func TestSettingsThresholdsAffectStaleness(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	f := newSettingsFixture(t, time.Date(2026, 11, 25, 10, 0, 0, 0, loc))
	ctx := context.Background()

	app, err := f.apps.Create(ctx, service.CreateApplicationInput{
		ApplicationFields: service.ApplicationFields{
			Company:   "Acme",
			Role:      "Engineer",
			Source:    "linkedin",
			AppliedOn: "2026-11-20",
		},
		Status: "applied",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, err := f.apps.List(ctx, service.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != app.ID {
		t.Fatalf("rows = %+v, want the created application", rows)
	}
	if rows[0].IsStale != 0 {
		t.Errorf("is_stale = %d with a 14-day threshold, want 0", rows[0].IsStale)
	}
	if rows[0].DueOn == nil || *rows[0].DueOn != "2026-12-04" {
		t.Errorf("due_on = %v, want 2026-12-04", rows[0].DueOn)
	}

	if err := f.settings.UpdateThresholds(ctx, thresholdMap(map[string]string{"applied": "1"})); err != nil {
		t.Fatalf("UpdateThresholds: %v", err)
	}

	rows, err = f.apps.List(ctx, service.ListFilter{})
	if err != nil {
		t.Fatalf("List after threshold change: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].IsStale != 1 {
		t.Errorf("is_stale = %d after a 1-day threshold, want 1", rows[0].IsStale)
	}
	if rows[0].DueOn == nil || *rows[0].DueOn != "2026-11-21" {
		t.Errorf("due_on = %v, want 2026-11-21", rows[0].DueOn)
	}
}

func TestSettingsPasswordChange(t *testing.T) {
	t.Parallel()

	f := newSettingsFixture(t, time.Date(2026, 11, 25, 10, 0, 0, 0, harare(t)))
	ctx := context.Background()

	set, err := f.settings.PasswordSet(ctx)
	if err != nil {
		t.Fatalf("PasswordSet: %v", err)
	}
	if set {
		t.Error("PasswordSet = true before a password was stored")
	}

	err = f.settings.ChangePassword(ctx, "anything", "a-new-password", "a-new-password")
	fields := validationFields(t, err)
	if fields["current_password"] == "" {
		t.Errorf("ChangePassword before setup fields = %v, want a current_password error", fields)
	}

	hash, err := service.HashPassword("old-password-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := f.q.UpsertSetting(ctx, storedb.UpsertSettingParams{
		Key:   service.SettingPasswordHash,
		Value: hash,
	}); err != nil {
		t.Fatalf("store password hash: %v", err)
	}

	set, err = f.settings.PasswordSet(ctx)
	if err != nil {
		t.Fatalf("PasswordSet after storing a hash: %v", err)
	}
	if !set {
		t.Error("PasswordSet = false after a hash was stored")
	}

	tests := []struct {
		name       string
		current    string
		next       string
		confirm    string
		wantFields []string
	}{
		{
			name:       "wrong current password",
			current:    "not-the-password",
			next:       "new-password-456",
			confirm:    "new-password-456",
			wantFields: []string{"current_password"},
		},
		{
			name:       "new password too short",
			current:    "old-password-123",
			next:       "short",
			confirm:    "short",
			wantFields: []string{"new_password"},
		},
		{
			name:       "confirmation mismatch",
			current:    "old-password-123",
			next:       "new-password-456",
			confirm:    "new-password-457",
			wantFields: []string{"confirm_password"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := f.settings.ChangePassword(ctx, tt.current, tt.next, tt.confirm)
			fields := validationFields(t, err)
			if got := sortedKeys(fields); !reflect.DeepEqual(got, tt.wantFields) {
				t.Errorf("fields = %v, want %v", got, tt.wantFields)
			}
		})
	}

	if err := f.settings.ChangePassword(ctx, "old-password-123", "new-password-456", "new-password-456"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	stored, err := f.q.GetSetting(ctx, service.SettingPasswordHash)
	if err != nil {
		t.Fatalf("read password hash: %v", err)
	}
	if service.VerifyPassword(stored, "old-password-123") {
		t.Error("the old password still verifies after a change")
	}
	if !service.VerifyPassword(stored, "new-password-456") {
		t.Error("the new password does not verify after a change")
	}
}

func TestSettingsSessionSecret(t *testing.T) {
	t.Parallel()

	f := newSettingsFixture(t, time.Date(2026, 11, 25, 10, 0, 0, 0, harare(t)))
	ctx := context.Background()

	first, err := f.settings.SessionSecret(ctx)
	if err != nil {
		t.Fatalf("SessionSecret: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("session secret length = %d, want 32", len(first))
	}

	again, err := f.settings.SessionSecret(ctx)
	if err != nil {
		t.Fatalf("SessionSecret again: %v", err)
	}
	if !reflect.DeepEqual(first, again) {
		t.Error("SessionSecret generated a new secret instead of reusing the stored one")
	}

	if err := f.settings.RotateSessionSecret(ctx); err != nil {
		t.Fatalf("RotateSessionSecret: %v", err)
	}

	rotated, err := f.settings.SessionSecret(ctx)
	if err != nil {
		t.Fatalf("SessionSecret after rotation: %v", err)
	}
	if reflect.DeepEqual(first, rotated) {
		t.Error("RotateSessionSecret left the secret unchanged")
	}
	if len(rotated) != 32 {
		t.Errorf("rotated secret length = %d, want 32", len(rotated))
	}
}

func TestSettingsTimezone(t *testing.T) {
	t.Parallel()

	f := newSettingsFixture(t, time.Date(2026, 11, 25, 10, 0, 0, 0, harare(t)))
	if got := f.settings.Timezone(); got != "Africa/Harare" {
		t.Errorf("Timezone = %q, want Africa/Harare", got)
	}
}

func TestSettingsActiveStatuses(t *testing.T) {
	t.Parallel()

	got := service.ActiveStatuses()
	want := []model.Status{
		model.StatusApplied,
		model.StatusScreening,
		model.StatusInterviewing,
		model.StatusOffer,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ActiveStatuses = %v, want %v", got, want)
	}
}

func TestPasswordHashAndVerify(t *testing.T) {
	t.Parallel()

	hash, err := service.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Errorf("hash = %q, want an argon2id PHC string", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Error("hash contains the plaintext password")
	}
	if !service.VerifyPassword(hash, "correct horse battery staple") {
		t.Error("VerifyPassword rejected the correct password")
	}
	if service.VerifyPassword(hash, "wrong password") {
		t.Error("VerifyPassword accepted a wrong password")
	}

	other, err := service.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword again: %v", err)
	}
	if hash == other {
		t.Error("two hashes of the same password are identical; salt is not random")
	}

	for _, invalid := range []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=65536,t=3,p=4$onlyonefield",
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=18$m=65536,t=3,p=4$c2FsdA$aGFzaA",
	} {
		if service.VerifyPassword(invalid, "correct horse battery staple") {
			t.Errorf("VerifyPassword accepted malformed hash %q", invalid)
		}
	}
}

func TestPasswordHashRejectsEmptyInput(t *testing.T) {
	t.Parallel()

	hash, err := service.HashPassword("")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !service.VerifyPassword(hash, "") {
		t.Error("VerifyPassword rejected the empty password it hashed")
	}
	if service.VerifyPassword(hash, "x") {
		t.Error("VerifyPassword accepted a different password")
	}
}

func TestSettingsThresholdsUnknownStatus(t *testing.T) {
	t.Parallel()

	f := newSettingsFixture(t, time.Date(2026, 11, 25, 10, 0, 0, 0, harare(t)))
	err := f.settings.UpdateThresholds(context.Background(), thresholdMap(map[string]string{"passport": "5"}))
	fields := validationFields(t, err)
	if fields["passport"] == "" {
		t.Errorf("fields = %v, want a passport error", fields)
	}
}
