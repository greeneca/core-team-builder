package handlers

import (
	"os"
	"testing"

	"github.com/core-team-builder/backend/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

// TestMain runs the handler tests at the minimum bcrypt cost. The integration
// tests register and log in users over HTTP, so at the production work factor
// the suite spends most of its wall time key-stretching passwords that no
// assertion depends on.
func TestMain(m *testing.M) {
	restore := auth.SetBcryptCostForTests(bcrypt.MinCost)
	code := m.Run()
	restore()
	os.Exit(code)
}
