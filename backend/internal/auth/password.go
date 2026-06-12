// Package auth handles password hashing and token-based authentication.
//
// Password handling follows current best practice:
//   - Passwords are never stored or logged in plaintext.
//   - Hashing uses bcrypt with a per-hash random salt (handled by bcrypt).
//   - Verification uses a constant-time comparison (handled by bcrypt).
package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost controls the work factor. 12 is a sensible 2020s default that
// balances security and login latency. Raise as hardware improves.
const bcryptCost = 12

// MinPasswordLength is the minimum acceptable password length. Length is the
// primary strength factor (NIST SP 800-63B), so we favor a longer minimum over
// composition rules.
const MinPasswordLength = 12

// MaxPasswordLength bounds the password at bcrypt's hard limit. bcrypt silently
// ignores any bytes past 72, which would make a long passphrase weaker than it
// appears, so we reject oversized input instead of truncating it. Measured in
// bytes (not runes) to match bcrypt.
const MaxPasswordLength = 72

// ErrPasswordTooShort indicates the supplied password is below MinPasswordLength.
var ErrPasswordTooShort = errors.New("password must be at least 12 characters")

// ErrPasswordTooLong indicates the supplied password exceeds MaxPasswordLength.
var ErrPasswordTooLong = errors.New("password must be at most 72 bytes")

// IsPasswordPolicyError reports whether err is a client-correctable password
// validation failure (vs. an internal hashing error). Handlers use it to decide
// between a 400 and a 500.
func IsPasswordPolicyError(err error) bool {
	return errors.Is(err, ErrPasswordTooShort) || errors.Is(err, ErrPasswordTooLong)
}

// HashPassword validates and hashes a plaintext password for storage.
func HashPassword(plaintext string) (string, error) {
	if len(plaintext) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}
	if len(plaintext) > MaxPasswordLength {
		return "", ErrPasswordTooLong
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword reports whether plaintext matches the stored bcrypt hash.
// The comparison is constant-time to resist timing attacks.
func CheckPassword(hash, plaintext string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}
