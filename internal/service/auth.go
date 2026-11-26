package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionTokenVersion = "v1"
	sessionClockSkew    = time.Minute

	defaultSessionTTL  = 30 * 24 * time.Hour
	loginAttemptLimit  = 5
	loginAttemptWindow = 15 * time.Minute
)

var (
	// ErrInvalidCredentials hides whether a password has been configured.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrRateLimited reports a client that exceeded the login attempt window.
	ErrRateLimited = errors.New("too many login attempts")
	// ErrAlreadyConfigured reports a setup attempt after a password exists.
	ErrAlreadyConfigured = errors.New("password is already configured")
)

// AuthService owns first-run setup, password login, the login rate limit, and
// HMAC-signed session tokens. Session state lives entirely in the token, so
// rotating the session secret signs every browser out at once.
type AuthService struct {
	settings *SettingsService
	clock    Clock
	ttl      time.Duration
	limiter  *LoginLimiter
}

// NewAuth returns an auth service. A non-positive ttl falls back to 30 days.
func NewAuth(settings *SettingsService, clock Clock, ttl time.Duration) *AuthService {
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	return &AuthService{
		settings: settings,
		clock:    clock,
		ttl:      ttl,
		limiter:  NewLoginLimiter(loginAttemptLimit, loginAttemptWindow, clock),
	}
}

// SessionTTL returns the lifetime of a new session token.
func (a *AuthService) SessionTTL() time.Duration { return a.ttl }

// PasswordSet reports whether first-run setup has completed.
func (a *AuthService) PasswordSet(ctx context.Context) (bool, error) {
	return a.settings.PasswordSet(ctx)
}

// Setup stores the first password. It refuses to overwrite an existing one so
// a reachable setup page cannot reset the account.
func (a *AuthService) Setup(ctx context.Context, password, confirm string) error {
	set, err := a.settings.PasswordSet(ctx)
	if err != nil {
		return fmt.Errorf("check password state: %w", err)
	}
	if set {
		return ErrAlreadyConfigured
	}

	errs := validateNewPassword(password, confirm, "password")
	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}

	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return a.settings.setPasswordHash(ctx, hash)
}

// Login verifies password for the client identified by key. Failed attempts
// are counted per key; a successful login clears that key's window.
func (a *AuthService) Login(ctx context.Context, key, password string) error {
	if !a.limiter.Allowed(key) {
		return ErrRateLimited
	}

	hash, err := a.settings.passwordHash(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		a.limiter.Failure(key)
		return ErrInvalidCredentials
	}
	if err != nil {
		return fmt.Errorf("read password hash: %w", err)
	}
	if !VerifyPassword(hash, password) {
		a.limiter.Failure(key)
		return ErrInvalidCredentials
	}

	a.limiter.Success(key)
	return nil
}

// NewSession returns a signed token and its expiry.
func (a *AuthService) NewSession(ctx context.Context) (string, time.Time, error) {
	secret, err := a.settings.SessionSecret(ctx)
	if err != nil {
		return "", time.Time{}, err
	}

	issued := a.clock.Now()
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(sessionTokenVersion + ":" + strconv.FormatInt(issued.Unix(), 10)),
	)
	return payload + "." + signSession(secret, payload), issued.Add(a.ttl), nil
}

// ValidateSession reports whether token carries a valid signature, has not
// expired, and was not issued in the future. Malformed tokens are simply
// invalid, not errors.
func (a *AuthService) ValidateSession(ctx context.Context, token string) (bool, error) {
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || payload == "" || signature == "" {
		return false, nil
	}

	secret, err := a.settings.SessionSecret(ctx)
	if err != nil {
		return false, err
	}
	if subtle.ConstantTimeCompare([]byte(signature), []byte(signSession(secret, payload))) != 1 {
		return false, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return false, nil
	}
	version, rawIssued, ok := strings.Cut(string(decoded), ":")
	if !ok || version != sessionTokenVersion {
		return false, nil
	}
	issuedUnix, err := strconv.ParseInt(rawIssued, 10, 64)
	if err != nil {
		return false, nil
	}

	issued := time.Unix(issuedUnix, 0)
	now := a.clock.Now()
	if issued.After(now.Add(sessionClockSkew)) {
		return false, nil
	}
	if now.Sub(issued) > a.ttl {
		return false, nil
	}
	return true, nil
}

func signSession(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload)) // hash.Write never returns an error
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type loginAttempt struct {
	count int
	reset time.Time
}

// LoginLimiter is a fixed-window counter keyed by client address. It is
// in-memory on purpose: a restart clears the window, which is acceptable for
// a single-user service behind a proxy.
type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	limit    int
	window   time.Duration
	clock    Clock
}

// NewLoginLimiter returns a limiter allowing limit failures per window.
func NewLoginLimiter(limit int, window time.Duration, clock Clock) *LoginLimiter {
	if limit <= 0 {
		limit = loginAttemptLimit
	}
	if window <= 0 {
		window = loginAttemptWindow
	}
	return &LoginLimiter{
		attempts: make(map[string]loginAttempt),
		limit:    limit,
		window:   window,
		clock:    clock,
	}
}

// Allowed reports whether key has attempts left in its current window.
func (l *LoginLimiter) Allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	attempt, ok := l.attempts[key]
	if !ok {
		return true
	}
	if l.clock.Now().After(attempt.reset) {
		delete(l.attempts, key)
		return true
	}
	return attempt.count < l.limit
}

// Failure records one failed attempt for key.
func (l *LoginLimiter) Failure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	attempt, ok := l.attempts[key]
	if !ok || now.After(attempt.reset) {
		attempt = loginAttempt{reset: now.Add(l.window)}
	}
	attempt.count++
	l.attempts[key] = attempt
}

// Success clears key's window after a valid login.
func (l *LoginLimiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
