package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// PasswordMinLength is the shortest password accepted by setup and change.
	PasswordMinLength = 12

	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16

	// Limits applied when parsing a stored hash, so a corrupted or hostile
	// value cannot make verification allocate unbounded memory.
	maxArgonMemory  = 1 << 20
	maxArgonTime    = 10
	maxArgonThreads = 64
	maxSaltLen      = 64
	maxKeyLen       = 128
)

var errInvalidHash = errors.New("invalid password hash")

// HashPassword returns an argon2id PHC string with a fresh random salt.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches the stored PHC hash. It
// returns false for any malformed hash and compares the derived key in
// constant time.
func VerifyPassword(encoded, password string) bool {
	params, salt, want, err := parsePasswordHash(encoded)
	if err != nil {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parsePasswordHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, errInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return argonParams{}, nil, nil, errInvalidHash
	}

	var params argonParams
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return argonParams{}, nil, nil, errInvalidHash
	}
	if memory == 0 || memory > maxArgonMemory ||
		iterations == 0 || iterations > maxArgonTime ||
		threads == 0 || threads > maxArgonThreads {
		return argonParams{}, nil, nil, errInvalidHash
	}
	params = argonParams{memory: memory, time: iterations, threads: threads}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 || len(salt) > maxSaltLen {
		return argonParams{}, nil, nil, errInvalidHash
	}

	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 || len(key) > maxKeyLen {
		return argonParams{}, nil, nil, errInvalidHash
	}

	return params, salt, key, nil
}
