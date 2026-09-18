package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// okHandler records whether the protected handler ran and what user ID the
// middleware put in the request context.
func okHandler(ran *bool, gotUserID *int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ran = true
		if id, ok := UserIDFromContext(r.Context()); ok {
			*gotUserID = id
		}
		w.WriteHeader(http.StatusOK)
	})
}

// TestMiddlewareAcceptsValidBearer covers the success path end to end: a valid
// token reaches the handler with the authenticated user ID in context, which is
// the only way handlers learn who is calling.
func TestMiddlewareAcceptsValidBearer(t *testing.T) {
	m := testManager()
	token, err := m.Issue(42, "ayla")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	var ran bool
	var gotUserID int64
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	m.Middleware(okHandler(&ran, &gotUserID)).ServeHTTP(rec, req)

	if !ran {
		t.Fatal("the protected handler did not run")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotUserID != 42 {
		t.Errorf("context user ID = %d, want 42", gotUserID)
	}
}

// TestMiddlewareAcceptsAnyBearerCasing covers the scheme comparison, which is
// case-insensitive per RFC 7235 — clients do send "bearer".
func TestMiddlewareAcceptsAnyBearerCasing(t *testing.T) {
	m := testManager()
	token, err := m.Issue(42, "ayla")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		t.Run(scheme, func(t *testing.T) {
			var ran bool
			var gotUserID int64
			req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
			req.Header.Set("Authorization", scheme+" "+token)
			rec := httptest.NewRecorder()

			m.Middleware(okHandler(&ran, &gotUserID)).ServeHTTP(rec, req)

			if !ran {
				t.Error("the protected handler did not run")
			}
		})
	}
}

// TestMiddlewareRejectsBadAuthorization covers every way a request can fail the
// gate. In all of them the protected handler must not run at all — answering
// 401 after the handler already did its work would be no protection.
func TestMiddlewareRejectsBadAuthorization(t *testing.T) {
	m := testManager()
	valid, err := m.Issue(42, "ayla")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	foreign, err := NewTokenManager([]byte("another-secret"), testTTL, time.Hour).Issue(42, "mallory")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// A structurally valid token whose subject is not a user ID. The claim is
	// attacker-influenced only via a forged token, but parsing it must still
	// fail closed rather than reaching the handler with a zero ID.
	nonNumeric, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			Subject:   "not-a-number",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(m.secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	cases := []struct {
		name   string
		header string
	}{
		{"missing header", ""},
		{"scheme only", "Bearer"},
		{"wrong scheme", "Basic " + valid},
		{"bare token", valid},
		{"empty token", "Bearer "},
		{"garbage token", "Bearer not-a-token"},
		{"foreign secret", "Bearer " + foreign},
		{"non-numeric subject", "Bearer " + nonNumeric},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ran bool
			var gotUserID int64
			req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()

			m.Middleware(okHandler(&ran, &gotUserID)).ServeHTTP(rec, req)

			if ran {
				t.Error("the protected handler ran, want it blocked")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// TestUserIDFromContextWithoutMiddleware covers the lookup on a request that
// never passed the gate, so a handler reading it can tell "not authenticated"
// from "user 0".
func TestUserIDFromContextWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	if id, ok := UserIDFromContext(req.Context()); ok {
		t.Errorf("got user ID %d, want no ID on an unauthenticated request", id)
	}
}
