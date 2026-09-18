package auth

import (
	"fmt"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// SetBcryptCostForTests lowers the bcrypt work factor for the current test
// binary and returns a function restoring the previous value. Call it from
// TestMain so the whole package runs at the reduced cost:
//
//	func TestMain(m *testing.M) {
//		restore := auth.SetBcryptCostForTests(bcrypt.MinCost)
//		code := m.Run()
//		restore()
//		os.Exit(code)
//	}
//
// Hashing at the production cost is deliberately expensive (~250ms per call,
// several times that under -race), and the integration tests register users
// over HTTP, so the default would put minutes of pure key stretching into every
// run. Nothing about the code under test depends on the work factor, only on a
// real bcrypt hash existing.
//
// This lives in a non-test file because other packages' tests need it, which
// means it is compiled into the production binaries too. The testing.Testing()
// guard is what makes that safe: outside a test binary the call panics rather
// than quietly weakening every password the service goes on to hash. Anything
// that verifies the production cost itself should assert against
// DefaultBcryptCost, not whatever this leaves set.
func SetBcryptCostForTests(cost int) (restore func()) {
	if !testing.Testing() {
		panic("auth: SetBcryptCostForTests called outside a test binary")
	}
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		panic(fmt.Sprintf("auth: bcrypt cost %d is outside the valid range [%d, %d]",
			cost, bcrypt.MinCost, bcrypt.MaxCost))
	}

	previous := bcryptCost
	bcryptCost = cost
	return func() { bcryptCost = previous }
}
