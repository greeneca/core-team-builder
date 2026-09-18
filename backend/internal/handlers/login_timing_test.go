package handlers

import (
	"testing"

	"github.com/core-team-builder/backend/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

// TestTimingDummyHashMatchesTheProductionCost pins the anti-enumeration control
// in handleLogin to the real work factor. The dummy is a hardcoded string, so
// raising auth.DefaultBcryptCost would otherwise leave the unknown-account path
// measurably faster than a wrong password and reintroduce the very oracle the
// generic error message exists to close.
//
// This needs no database, so it runs on every `go test ./...`.
func TestTimingDummyHashMatchesTheProductionCost(t *testing.T) {
	cost, err := bcrypt.Cost([]byte(timingDummyHash))
	if err != nil {
		t.Fatalf("timingDummyHash is not a parseable bcrypt hash: %v", err)
	}
	if cost != auth.DefaultBcryptCost {
		t.Errorf("timingDummyHash cost = %d, want %d — regenerate the constant in handlers.go so an unknown username costs the same as a wrong password",
			cost, auth.DefaultBcryptCost)
	}
}

// TestTimingDummyHashNeverMatches guards the other half: the dummy must not be
// a hash of anything a caller could supply, or an attacker would have found a
// password that logs in as a nonexistent user.
func TestTimingDummyHashNeverMatches(t *testing.T) {
	for _, guess := range []string{"", "password", "invalid", timingDummyHash} {
		if auth.CheckPassword(timingDummyHash, guess) {
			t.Errorf("the dummy hash matched %q", guess)
		}
	}
}
