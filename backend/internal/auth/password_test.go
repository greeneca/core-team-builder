package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestHashPasswordEnforcesPolicy covers the length policy HashPassword applies
// before hashing. The upper bound matters as much as the lower one: bcrypt
// silently ignores bytes past 72, so accepting a longer passphrase would make
// it weaker than it looks.
func TestHashPasswordEnforcesPolicy(t *testing.T) {
	cases := []struct {
		name      string
		plaintext string
		want      error
	}{
		{"too short", strings.Repeat("a", MinPasswordLength-1), ErrPasswordTooShort},
		{"at minimum", strings.Repeat("a", MinPasswordLength), nil},
		{"at maximum", strings.Repeat("a", MaxPasswordLength), nil},
		{"too long", strings.Repeat("a", MaxPasswordLength+1), ErrPasswordTooLong},
		{"empty", "", ErrPasswordTooShort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := HashPassword(tc.plaintext)
			if err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				if hash != "" {
					t.Errorf("hash = %q, want it empty on rejection", hash)
				}
				if !IsPasswordPolicyError(err) {
					t.Error("IsPasswordPolicyError = false, want true so handlers answer 400")
				}
				return
			}
			if hash == "" {
				t.Error("hash is empty, want a bcrypt hash")
			}
		})
	}
}

// TestHashPasswordLengthIsMeasuredInBytes pins the policy to bytes rather than
// runes, matching bcrypt's own 72-byte limit. A multi-byte passphrase that
// looks short enough by rune count must still be rejected.
func TestHashPasswordLengthIsMeasuredInBytes(t *testing.T) {
	// 25 three-byte runes = 75 bytes, over the limit despite only 25 characters.
	long := strings.Repeat("字", 25)
	if _, err := HashPassword(long); err != ErrPasswordTooLong {
		t.Fatalf("err = %v, want ErrPasswordTooLong for a %d-byte password", err, len(long))
	}

	// 5 three-byte runes = 15 bytes, over the minimum despite only 5 characters.
	short := strings.Repeat("字", 5)
	if _, err := HashPassword(short); err != nil {
		t.Fatalf("err = %v, want a %d-byte password accepted", err, len(short))
	}
}

// TestCheckPasswordRoundTrip covers the verification path, including the cases
// a login handler relies on to reject: a wrong password and a stored hash that
// isn't a valid bcrypt hash at all (e.g. the unusable placeholder written for
// passwordless Discord accounts).
func TestCheckPasswordRoundTrip(t *testing.T) {
	const plaintext = "correct horse battery staple"
	hash, err := HashPassword(plaintext)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if !CheckPassword(hash, plaintext) {
		t.Error("got no match for the original password, want a match")
	}
	for _, wrong := range []string{"", plaintext + "x", strings.ToUpper(plaintext)} {
		if CheckPassword(hash, wrong) {
			t.Errorf("got a match for %q, want none", wrong)
		}
	}
	if CheckPassword("not-a-bcrypt-hash", plaintext) {
		t.Error("got a match against a malformed hash, want none")
	}
}

// TestHashPasswordIsSalted guards the per-hash random salt: the same password
// hashed twice must not produce the same digest, so a leaked table cannot be
// scanned for users who share a password.
func TestHashPasswordIsSalted(t *testing.T) {
	const plaintext = "correct horse battery staple"
	first, err := HashPassword(plaintext)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword(plaintext)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Error("got identical hashes for the same password, want distinct salts")
	}
	if !CheckPassword(second, plaintext) {
		t.Error("the second hash does not verify")
	}
}

// TestDefaultBcryptCost is the one test that runs at the production work
// factor. TestMain lowers the cost for everything else, so without this nothing
// would notice DefaultBcryptCost being dropped to a weak value — the rest of
// the suite would keep passing at whatever cost it was handed.
func TestDefaultBcryptCost(t *testing.T) {
	if DefaultBcryptCost < 12 {
		t.Errorf("DefaultBcryptCost = %d, want at least 12", DefaultBcryptCost)
	}

	restore := SetBcryptCostForTests(DefaultBcryptCost)
	defer restore()

	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("read cost from hash: %v", err)
	}
	if cost != DefaultBcryptCost {
		t.Errorf("hash cost = %d, want %d — HashPassword is not applying the configured work factor", cost, DefaultBcryptCost)
	}
}

// TestSetBcryptCostForTests covers the test-only knob itself: it has to change
// the cost actually baked into a hash, and its restore func has to put the
// previous value back so one test cannot leak a weak cost into the next.
func TestSetBcryptCostForTests(t *testing.T) {
	before := bcryptCost

	restore := SetBcryptCostForTests(bcrypt.MinCost)
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("read cost from hash: %v", err)
	}
	if cost != bcrypt.MinCost {
		t.Errorf("hash cost = %d, want %d", cost, bcrypt.MinCost)
	}

	restore()
	if bcryptCost != before {
		t.Errorf("cost after restore = %d, want %d", bcryptCost, before)
	}
}

// TestSetBcryptCostForTestsRejectsAnInvalidCost checks the bounds are enforced
// loudly. bcrypt silently substitutes its default for an out-of-range cost, so
// without this a typo would leave tests running at cost 10 while appearing to
// ask for something else.
func TestSetBcryptCostForTestsRejectsAnInvalidCost(t *testing.T) {
	for _, cost := range []int{bcrypt.MinCost - 1, bcrypt.MaxCost + 1, 0, -1} {
		t.Run(fmt.Sprint(cost), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("cost %d was accepted, want a panic", cost)
				}
			}()
			SetBcryptCostForTests(cost)
		})
	}
}

// TestIsPasswordPolicyError keeps the 400/500 split honest: only the two policy
// errors are client-correctable, so an internal hashing failure is not reported
// back to the caller as bad input.
func TestIsPasswordPolicyError(t *testing.T) {
	if !IsPasswordPolicyError(ErrPasswordTooShort) || !IsPasswordPolicyError(ErrPasswordTooLong) {
		t.Error("the policy errors should be reported as policy errors")
	}
	if IsPasswordPolicyError(nil) {
		t.Error("nil should not be reported as a policy error")
	}
	if IsPasswordPolicyError(errors.New("bcrypt: internal failure")) {
		t.Error("an unrelated error should not be reported as a policy error")
	}
}
