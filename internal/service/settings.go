package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rgmaz/job-application-tracker/internal/model"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
)

const (
	// SettingPasswordHash holds the argon2id PHC string for the single user.
	SettingPasswordHash = "password_hash"
	// SettingSessionSecret holds the hex HMAC key that signs session cookies.
	SettingSessionSecret = "session_secret"

	minThresholdDays  = 1
	maxThresholdDays  = 365
	sessionSecretSize = 32
)

// ThresholdValue is one active status and its staleness threshold in days.
type ThresholdValue struct {
	Status model.Status
	Days   int
}

// SettingsService reads and writes the editable settings: staleness
// thresholds, the password hash, and the session secret. The timezone is
// process configuration, so it is displayed but not stored.
type SettingsService struct {
	db       *sql.DB
	q        *storedb.Queries
	location *time.Location
}

// NewSettings returns a settings service backed by db. location is the
// effective timezone shown on the settings page.
func NewSettings(db *sql.DB, location *time.Location) *SettingsService {
	return &SettingsService{db: db, q: storedb.New(db), location: location}
}

// Timezone returns the configured IANA timezone name.
func (s *SettingsService) Timezone() string {
	if s.location == nil {
		return "UTC"
	}
	return s.location.String()
}

// ActiveStatuses returns the statuses that can become stale, in pipeline
// order. Terminal statuses never have a threshold row.
func ActiveStatuses() []model.Status {
	statuses := make([]model.Status, 0, len(model.Statuses))
	for _, status := range model.Statuses {
		if !status.IsTerminal() {
			statuses = append(statuses, status)
		}
	}
	return statuses
}

// Thresholds returns one value per active status, in pipeline order. A
// missing row reads as zero days so the page can still show the field.
func (s *SettingsService) Thresholds(ctx context.Context) ([]ThresholdValue, error) {
	rows, err := s.q.ListThresholds(ctx)
	if err != nil {
		return nil, fmt.Errorf("list thresholds: %w", err)
	}

	stored := make(map[string]int, len(rows))
	for _, row := range rows {
		stored[row.Status] = int(row.Days)
	}

	active := ActiveStatuses()
	values := make([]ThresholdValue, 0, len(active))
	for _, status := range active {
		values = append(values, ThresholdValue{
			Status: status,
			Days:   stored[string(status)],
		})
	}
	return values, nil
}

// UpdateThresholds validates and stores a threshold for every active status.
// The values are raw form strings so per-field messages can be returned.
func (s *SettingsService) UpdateThresholds(ctx context.Context, values map[string]string) error {
	active := ActiveStatuses()
	allowed := make(map[string]bool, len(active))
	for _, status := range active {
		allowed[string(status)] = true
	}

	errs := make(map[string]string)
	parsed := make(map[string]int, len(values))

	for rawStatus, rawDays := range values {
		status := strings.TrimSpace(rawStatus)
		if !allowed[status] {
			errs[status] = "This status does not use a threshold."
			continue
		}

		days, err := strconv.Atoi(strings.TrimSpace(rawDays))
		if err != nil || days < minThresholdDays || days > maxThresholdDays {
			errs[status] = fmt.Sprintf(
				"Enter a whole number of days between %d and %d.",
				minThresholdDays,
				maxThresholdDays,
			)
			continue
		}
		parsed[status] = days
	}

	for _, status := range active {
		if _, ok := values[string(status)]; !ok {
			if _, exists := errs[string(status)]; !exists {
				errs[string(status)] = "Enter a threshold for this status."
			}
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin threshold update: %w", err)
	}
	defer rollback(tx)

	q := s.q.WithTx(tx)
	for _, status := range active {
		if err := q.UpsertThreshold(ctx, storedb.UpsertThresholdParams{
			Status: string(status),
			Days:   int64(parsed[string(status)]),
		}); err != nil {
			return fmt.Errorf("update %s threshold: %w", status, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit threshold update: %w", err)
	}
	return nil
}

// PasswordSet reports whether a password hash is stored.
func (s *SettingsService) PasswordSet(ctx context.Context) (bool, error) {
	hash, err := s.passwordHash(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read password hash: %w", err)
	}
	return strings.TrimSpace(hash) != "", nil
}

// ChangePassword verifies the current password and stores a new hash.
func (s *SettingsService) ChangePassword(ctx context.Context, current, next, confirm string) error {
	hash, err := s.passwordHash(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return &ValidationError{Fields: map[string]string{
			"current_password": "Set a password before changing it.",
		}}
	}
	if err != nil {
		return fmt.Errorf("read password hash: %w", err)
	}

	errs := validateNewPassword(next, confirm, "new_password")
	if !VerifyPassword(hash, current) {
		errs["current_password"] = "Current password is incorrect."
	}
	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}

	hashed, err := HashPassword(next)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	if err := s.setPasswordHash(ctx, hashed); err != nil {
		return err
	}
	return nil
}

// SessionSecret returns the HMAC key used for session cookies, creating one
// on first use. A stored value that is not a 32-byte hex string is replaced,
// which also invalidates any cookies signed with the corrupt value.
func (s *SettingsService) SessionSecret(ctx context.Context) ([]byte, error) {
	value, err := s.q.GetSetting(ctx, SettingSessionSecret)
	if errors.Is(err, sql.ErrNoRows) {
		return s.rotateSessionSecret(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("read session secret: %w", err)
	}

	secret, decodeErr := hex.DecodeString(strings.TrimSpace(value))
	if decodeErr != nil || len(secret) != sessionSecretSize {
		return s.rotateSessionSecret(ctx)
	}
	return secret, nil
}

// RotateSessionSecret replaces the cookie-signing key, signing out every
// existing browser.
func (s *SettingsService) RotateSessionSecret(ctx context.Context) error {
	_, err := s.rotateSessionSecret(ctx)
	return err
}

func (s *SettingsService) rotateSessionSecret(ctx context.Context) ([]byte, error) {
	secret := make([]byte, sessionSecretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate session secret: %w", err)
	}
	if err := s.q.UpsertSetting(ctx, storedb.UpsertSettingParams{
		Key:   SettingSessionSecret,
		Value: hex.EncodeToString(secret),
	}); err != nil {
		return nil, fmt.Errorf("store session secret: %w", err)
	}
	return secret, nil
}

func (s *SettingsService) passwordHash(ctx context.Context) (string, error) {
	return s.q.GetSetting(ctx, SettingPasswordHash)
}

func (s *SettingsService) setPasswordHash(ctx context.Context, hash string) error {
	if err := s.q.UpsertSetting(ctx, storedb.UpsertSettingParams{
		Key:   SettingPasswordHash,
		Value: hash,
	}); err != nil {
		return fmt.Errorf("store password hash: %w", err)
	}
	return nil
}

// validateNewPassword returns per-field messages. field names the password
// input so setup ("password") and change ("new_password") can share the rules.
func validateNewPassword(password, confirm, field string) map[string]string {
	errs := make(map[string]string)
	if utf8.RuneCountInString(password) < PasswordMinLength {
		errs[field] = fmt.Sprintf("Password must be at least %d characters.", PasswordMinLength)
	}
	if password != confirm {
		errs["confirm_password"] = "Passwords do not match."
	}
	return errs
}
