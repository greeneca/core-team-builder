package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testTTL = 15 * time.Minute

func testManager() *TokenManager {
	return NewTokenManager([]byte("test-signing-secret"), testTTL, 30*24*time.Hour)
}

// TestIssueParseRoundTrip covers the happy path: a freshly issued token parses
// back into the subject and username the session was minted for.
func TestIssueParseRoundTrip(t *testing.T) {
	m := testManager()

	token, err := m.Issue(42, "ayla")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	claims, err := m.Parse(token)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.Subject != "42" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "42")
	}
	if claims.Username != "ayla" {
		t.Errorf("Username = %q, want %q", claims.Username, "ayla")
	}
	if claims.ExpiresAt == nil {
		t.Fatal("ExpiresAt is nil, want the access TTL applied")
	}
	if got := claims.ExpiresAt.Sub(claims.IssuedAt.Time); got != testTTL {
		t.Errorf("token lifetime = %v, want %v", got, testTTL)
	}
}

// TestParseRejectsForeignSecret covers the core signature check: a token signed
// with a different secret must not be accepted, even though its shape and
// claims are otherwise valid.
func TestParseRejectsForeignSecret(t *testing.T) {
	other := NewTokenManager([]byte("a-different-secret"), testTTL, time.Hour)
	token, err := other.Issue(1, "mallory")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := testManager().Parse(token); err == nil {
		t.Fatal("got no error, want a token signed with another secret rejected")
	}
}

// TestParseRejectsExpiredToken covers expiry enforcement, the property the
// short access-token lifetime depends on for its value.
func TestParseRejectsExpiredToken(t *testing.T) {
	m := NewTokenManager([]byte("test-signing-secret"), -time.Minute, time.Hour)
	token, err := m.Issue(7, "ayla")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	_, err = m.Parse(token)
	if err == nil {
		t.Fatal("got no error, want an expired token rejected")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("err = %v, want it to report expiry", err)
	}
}

// TestParseRequiresExpiry rejects a token carrying no `exp` at all. Without
// WithExpirationRequired such a token would validate forever, so a leaked one
// could never be aged out.
func TestParseRequiresExpiry(t *testing.T) {
	m := testManager()
	claims := Claims{
		Username: "ayla",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   tokenIssuer,
			Audience: jwt.ClaimStrings{tokenAudience},
			Subject:  "42",
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := m.Parse(token); err == nil {
		t.Fatal("got no error, want a token without an expiry rejected")
	}
}

// TestParseRejectsAlgorithmTampering covers the algorithm-confusion defense.
// "none" strips the signature entirely; both must be refused regardless of the
// claims they carry.
func TestParseRejectsAlgorithmTampering(t *testing.T) {
	m := testManager()
	claims := Claims{
		Username: "mallory",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			Subject:   "1",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := m.Parse(unsigned); err == nil {
		t.Error("got no error, want an alg=none token rejected")
	}

	// HS384/HS512 are still HMAC, so the keyfunc's type check passes; only the
	// WithValidMethods allow-list stops them.
	downgraded, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString(m.secret)
	if err != nil {
		t.Fatalf("sign hs512: %v", err)
	}
	if _, err := m.Parse(downgraded); err == nil {
		t.Error("got no error, want an algorithm outside the HS256 allow-list rejected")
	}
}

// TestParseRejectsForeignIssuerAndAudience covers the replay guard: a token
// signed with this service's secret but minted for a different issuer or
// audience is not accepted against this API.
func TestParseRejectsForeignIssuerAndAudience(t *testing.T) {
	m := testManager()
	base := jwt.RegisteredClaims{
		Subject:   "42",
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}

	cases := []struct {
		name     string
		issuer   string
		audience string
	}{
		{"foreign issuer", "someone-else", tokenAudience},
		{"foreign audience", tokenIssuer, "someone-elses-api"},
		{"both unset", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := base
			rc.Issuer = tc.issuer
			rc.Audience = jwt.ClaimStrings{tc.audience}
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{RegisteredClaims: rc}).SignedString(m.secret)
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			if _, err := m.Parse(token); err == nil {
				t.Error("got no error, want the token rejected")
			}
		})
	}
}

// TestParseRejectsMalformedInput covers the strings a bearer header can realistically
// carry when something upstream is wrong; none should parse.
func TestParseRejectsMalformedInput(t *testing.T) {
	m := testManager()
	for _, tokenString := range []string{"", "not-a-token", "a.b.c", "Bearer sometoken"} {
		if _, err := m.Parse(tokenString); err == nil {
			t.Errorf("got no error for %q, want it rejected", tokenString)
		}
	}
}

// TestGenerateRefreshToken checks the two properties the refresh flow relies
// on: the returned hash is the one HashRefreshToken will later recompute from
// the presented token, and every token is distinct.
func TestGenerateRefreshToken(t *testing.T) {
	token, hash, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}
	if token == "" || hash == "" {
		t.Fatal("got an empty token or hash")
	}
	if token == hash {
		t.Error("the token and its hash are identical — the plaintext would be stored")
	}
	if got := HashRefreshToken(token); got != hash {
		t.Errorf("HashRefreshToken = %q, want the returned hash %q", got, hash)
	}

	seen := map[string]bool{token: true}
	for range 100 {
		next, _, err := GenerateRefreshToken()
		if err != nil {
			t.Fatalf("GenerateRefreshToken: %v", err)
		}
		if seen[next] {
			t.Fatalf("got a repeated token %q", next)
		}
		seen[next] = true
	}
}

// TestHashRefreshTokenIsStableHex pins the stored form: a hex SHA-256, so the
// persisted column stays a fixed 64 characters and lookups are exact matches.
func TestHashRefreshTokenIsStableHex(t *testing.T) {
	const token = "a-refresh-token"
	hash := HashRefreshToken(token)
	if len(hash) != 64 {
		t.Errorf("len(hash) = %d, want 64 hex characters", len(hash))
	}
	if hash != HashRefreshToken(token) {
		t.Error("hashing is not deterministic")
	}
	if hash == HashRefreshToken(token+"x") {
		t.Error("different tokens hashed to the same value")
	}
}

// TestGenerateOpaqueToken covers the password-reset credential, which shares
// the refresh token's hash-only storage rule.
func TestGenerateOpaqueToken(t *testing.T) {
	token, hash, err := GenerateOpaqueToken()
	if err != nil {
		t.Fatalf("GenerateOpaqueToken: %v", err)
	}
	if token == "" || hash == "" {
		t.Fatal("got an empty token or hash")
	}
	if got := HashRefreshToken(token); got != hash {
		t.Errorf("HashRefreshToken = %q, want the returned hash %q", got, hash)
	}

	other, _, err := GenerateOpaqueToken()
	if err != nil {
		t.Fatalf("GenerateOpaqueToken: %v", err)
	}
	if other == token {
		t.Error("got the same token twice")
	}
}

// TestTTLAccessors guards the values the auth response reports as expires_in
// and uses to date a refresh row.
func TestTTLAccessors(t *testing.T) {
	m := NewTokenManager(nil, 15*time.Minute, 30*24*time.Hour)
	if got := m.AccessTTL(); got != 15*time.Minute {
		t.Errorf("AccessTTL = %v, want 15m", got)
	}
	if got := m.RefreshTTL(); got != 30*24*time.Hour {
		t.Errorf("RefreshTTL = %v, want 720h", got)
	}
}
