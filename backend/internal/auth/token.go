package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// tokenIssuer and tokenAudience pin the JWT `iss`/`aud` claims to this service.
// They are validated on parse so a token signed with the same secret but minted
// for a different purpose/service cannot be replayed against this API. Promote
// to configuration if these ever need to vary per environment.
const (
	tokenIssuer   = "core-team-builder"
	tokenAudience = "core-team-builder-api"
)

// TokenManager issues and validates signed JWT access tokens and generates the
// opaque refresh tokens that mint them.
type TokenManager struct {
	secret     []byte
	ttl        time.Duration
	refreshTTL time.Duration
}

// NewTokenManager constructs a TokenManager with the given signing secret, the
// access-token lifetime (ttl), and the refresh-token lifetime (refreshTTL).
func NewTokenManager(secret []byte, ttl, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{secret: secret, ttl: ttl, refreshTTL: refreshTTL}
}

// AccessTTL returns the configured access-token lifetime.
func (m *TokenManager) AccessTTL() time.Duration { return m.ttl }

// RefreshTTL returns the configured refresh-token lifetime.
func (m *TokenManager) RefreshTTL() time.Duration { return m.refreshTTL }

// Claims is the set of values embedded in an access token.
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// Issue creates a signed short-lived access token for the given user.
func (m *TokenManager) Issue(userID int64, username string) (string, error) {
	now := time.Now()
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// Parse validates an access-token string and returns its claims. It pins the
// signing algorithm to HS256 so a token cannot be downgraded to "none" or to an
// asymmetric algorithm that abuses the secret as a public key, requires a valid
// expiry, and verifies the issuer/audience so foreign tokens are rejected.
func (m *TokenManager) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
	)
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// GenerateRefreshToken returns a new opaque, high-entropy refresh token (the
// value handed to the client) together with the hash to persist server-side.
// Only the hash is stored, so a database leak does not expose usable tokens.
func GenerateRefreshToken() (token string, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashRefreshToken(token), nil
}

// HashRefreshToken returns the hex-encoded SHA-256 of a refresh token. Refresh
// tokens are high-entropy random values, so a fast hash (not bcrypt) is the
// correct choice for constant-cost lookups while keeping plaintext out of the DB.
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// GenerateOpaqueToken returns a new opaque, high-entropy token (the value handed
// to the user) together with the SHA-256 hash to persist server-side. Used for
// any single-use credential we store only as a hash, e.g. password resets.
func GenerateOpaqueToken() (token string, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashRefreshToken(token), nil
}
