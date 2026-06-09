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

// MinPasswordLength is the minimum acceptable password length.
const MinPasswordLength = 8

// ErrPasswordTooShort indicates the supplied password failed length validation.
var ErrPasswordTooShort = errors.New("password must be at least 8 characters")

// HashPassword validates and hashes a plaintext password for storage.
func HashPassword(plaintext string) (string, error) {
	if len(plaintext) < MinPasswordLength {
		return "", ErrPasswordTooShort
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
