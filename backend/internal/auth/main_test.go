package auth

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain runs this package at the minimum bcrypt cost. Every test here that
// hashes a password cares only that a real bcrypt hash comes back, not how
// expensive it was to produce — the production work factor is asserted
// separately by TestDefaultBcryptCost, which restores it for the duration.
func TestMain(m *testing.M) {
	restore := SetBcryptCostForTests(bcrypt.MinCost)
	code := m.Run()
	restore()
	os.Exit(code)
}
