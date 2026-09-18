package handlers

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

// TestHealth is the smoke test for the whole harness: routing, the middleware
// chain, and the JSON writer.
func TestHealth(t *testing.T) {
	api := newTestAPI(t)

	var payload map[string]string
	api.do(http.MethodGet, "/api/health", "", nil).expect(http.StatusOK).decode(&payload)

	if payload["status"] != "ok" {
		t.Errorf("status = %q, want %q", payload["status"], "ok")
	}
}

// TestRegisterFirstUserBecomesAdmin covers the bootstrap rule: the very first
// account is always allowed and is made an admin, which is how a fresh
// deployment gets someone who can manage the rest.
func TestRegisterFirstUserBecomesAdmin(t *testing.T) {
	api := newTestAPI(t)

	first := api.registerUser("founder")
	if !first.user.IsAdmin {
		t.Error("the first registered user is not an admin")
	}
	if first.token == "" || first.refreshToken == "" {
		t.Error("registration did not return both tokens")
	}

	second := api.registerUser("newcomer")
	if second.user.IsAdmin {
		t.Error("the second registered user is an admin, want a plain account")
	}
}

// TestRegisterRejectsDuplicatesAndWeakPasswords covers the two client-fixable
// registration failures, which the frontend distinguishes by status code.
func TestRegisterRejectsDuplicatesAndWeakPasswords(t *testing.T) {
	api := newTestAPI(t)
	existing := api.registerUser("founder")

	cases := []struct {
		name     string
		body     map[string]string
		want     int
		contains string
	}{
		{
			name: "duplicate username",
			body: map[string]string{"username": existing.user.Username, "email": "other@example.test", "password": "a-long-enough-password"},
			want: http.StatusConflict,
		},
		{
			name: "duplicate email",
			body: map[string]string{"username": "someone-else", "email": existing.user.Email, "password": "a-long-enough-password"},
			want: http.StatusConflict,
		},
		{
			name:     "password below the policy minimum",
			body:     map[string]string{"username": "shorty", "email": "shorty@example.test", "password": "short"},
			want:     http.StatusBadRequest,
			contains: "at least",
		},
		{
			name: "missing fields",
			body: map[string]string{"username": "nobody"},
			want: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := api.do(http.MethodPost, "/api/register", "", tc.body).expect(tc.want)
			if tc.contains != "" {
				if msg := resp.errorMessage(); !strings.Contains(msg, tc.contains) {
					t.Errorf("error = %q, want it to mention %q", msg, tc.contains)
				}
			}
		})
	}
}

// TestRegistrationToggle covers the admin switch end to end: once an admin
// turns self-registration off the public endpoint refuses new accounts, and
// the unauthenticated status endpoint reports it so the login page can hide
// the Register tab.
func TestRegistrationToggle(t *testing.T) {
	api := newTestAPI(t)
	admin := api.registerUser("founder")

	api.do(http.MethodPut, "/api/admin/settings", admin.token,
		map[string]bool{"registration_enabled": false}).expect(http.StatusOK)

	var status map[string]bool
	api.do(http.MethodGet, "/api/registration-status", "", nil).expect(http.StatusOK).decode(&status)
	if status["enabled"] {
		t.Error("registration-status reports open, want closed")
	}

	api.do(http.MethodPost, "/api/register", "", map[string]string{
		"username": "latecomer", "email": "latecomer@example.test", "password": "a-long-enough-password",
	}).expect(http.StatusForbidden)

	// Re-opening lets registration through again.
	api.do(http.MethodPut, "/api/admin/settings", admin.token,
		map[string]bool{"registration_enabled": true}).expect(http.StatusOK)
	api.registerUser("latecomer")
}

// TestRegistrationToggleIsAdminOnly guards the endpoint itself: a plain account
// must not be able to reopen registration.
func TestRegistrationToggleIsAdminOnly(t *testing.T) {
	api := newTestAPI(t)
	api.registerUser("founder")
	plain := api.registerUser("newcomer")

	api.do(http.MethodGet, "/api/admin/settings", plain.token, nil).expect(http.StatusForbidden)
	api.do(http.MethodPut, "/api/admin/settings", plain.token,
		map[string]bool{"registration_enabled": false}).expect(http.StatusForbidden)
	api.do(http.MethodGet, "/api/admin/users", plain.token, nil).expect(http.StatusForbidden)
}

// TestLogin covers credential verification against a real stored bcrypt hash,
// including the generic failure the handler returns for both a wrong password
// and an unknown username so neither reveals which it was.
func TestLogin(t *testing.T) {
	api := newTestAPI(t)
	user := api.registerUser("founder")

	var ok struct {
		Token string      `json:"token"`
		User  models.User `json:"user"`
	}
	api.do(http.MethodPost, "/api/login", "", map[string]string{
		"username": user.user.Username, "password": user.password,
	}).expect(http.StatusOK).decode(&ok)
	if ok.User.ID != user.user.ID {
		t.Errorf("logged in as user %d, want %d", ok.User.ID, user.user.ID)
	}

	wrongPassword := api.do(http.MethodPost, "/api/login", "", map[string]string{
		"username": user.user.Username, "password": "not-the-password",
	}).expect(http.StatusUnauthorized).errorMessage()
	unknownUser := api.do(http.MethodPost, "/api/login", "", map[string]string{
		"username": "nobody-at-all", "password": user.password,
	}).expect(http.StatusUnauthorized).errorMessage()

	if wrongPassword != unknownUser {
		t.Errorf("errors differ (%q vs %q), want one generic message so neither reveals whether the account exists", wrongPassword, unknownUser)
	}
}

// TestLoginNeverReturnsThePasswordHash guards the `json:"-"` tag on the model:
// the hash must not appear in any response that carries a user.
func TestLoginNeverReturnsThePasswordHash(t *testing.T) {
	api := newTestAPI(t)
	user := api.registerUser("founder")

	for _, resp := range []*response{
		api.do(http.MethodPost, "/api/login", "", map[string]string{"username": user.user.Username, "password": user.password}).expect(http.StatusOK),
		api.do(http.MethodGet, "/api/me", user.token, nil).expect(http.StatusOK),
		api.do(http.MethodGet, "/api/admin/users", user.token, nil).expect(http.StatusOK),
	} {
		if strings.Contains(string(resp.body), "password_hash") || strings.Contains(string(resp.body), "$2a$") {
			t.Errorf("a response leaked the password hash: %s", resp.body)
		}
	}
}

// TestProtectedRouteRequiresAToken covers the middleware on a live route.
func TestProtectedRouteRequiresAToken(t *testing.T) {
	api := newTestAPI(t)
	user := api.registerUser("founder")

	var me models.User
	api.do(http.MethodGet, "/api/me", user.token, nil).expect(http.StatusOK).decode(&me)
	if me.ID != user.user.ID {
		t.Errorf("/api/me returned user %d, want %d", me.ID, user.user.ID)
	}

	api.do(http.MethodGet, "/api/me", "", nil).expect(http.StatusUnauthorized)
	api.do(http.MethodGet, "/api/me", "not-a-token", nil).expect(http.StatusUnauthorized)
}

// TestRefreshRotatesAndRejectsReplay covers the single-use rotation the refresh
// endpoint promises: each exchange issues a new pair and burns the old refresh
// token, so a stolen one stops working the moment the real client uses theirs.
func TestRefreshRotatesAndRejectsReplay(t *testing.T) {
	api := newTestAPI(t)
	user := api.registerUser("founder")

	var rotated struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	api.do(http.MethodPost, "/api/refresh", "", map[string]string{
		"refresh_token": user.refreshToken,
	}).expect(http.StatusOK).decode(&rotated)

	if rotated.RefreshToken == user.refreshToken {
		t.Error("the refresh token was reused, want it rotated")
	}
	if rotated.ExpiresIn <= 0 {
		t.Errorf("expires_in = %d, want the access TTL in seconds", rotated.ExpiresIn)
	}
	api.do(http.MethodGet, "/api/me", rotated.Token, nil).expect(http.StatusOK)

	// Replaying the consumed token fails; the new one still works.
	api.do(http.MethodPost, "/api/refresh", "", map[string]string{
		"refresh_token": user.refreshToken,
	}).expect(http.StatusUnauthorized)
	api.do(http.MethodPost, "/api/refresh", "", map[string]string{
		"refresh_token": rotated.RefreshToken,
	}).expect(http.StatusOK)
}

// TestLogoutRevokesTheRefreshToken covers sign-out: the presented refresh token
// can no longer mint access tokens, and repeating the call is harmless.
func TestLogoutRevokesTheRefreshToken(t *testing.T) {
	api := newTestAPI(t)
	user := api.registerUser("founder")

	api.do(http.MethodPost, "/api/logout", "", map[string]string{
		"refresh_token": user.refreshToken,
	}).expect(http.StatusNoContent)

	api.do(http.MethodPost, "/api/refresh", "", map[string]string{
		"refresh_token": user.refreshToken,
	}).expect(http.StatusUnauthorized)

	// Idempotent: logging out again (or with garbage) still succeeds.
	api.do(http.MethodPost, "/api/logout", "", map[string]string{
		"refresh_token": user.refreshToken,
	}).expect(http.StatusNoContent)
	api.do(http.MethodPost, "/api/logout", "", map[string]string{
		"refresh_token": "never-existed",
	}).expect(http.StatusNoContent)
}

// resetLinkToken pulls the reset token out of the emailed link.
var resetLinkToken = regexp.MustCompile(`reset\.html\?token=([^\s]+)`)

// TestPasswordResetFlow covers forgot → email → reset end to end, including the
// three properties the flow is built on: the response never reveals whether the
// email exists, the token is single-use, and completing a reset signs the user
// out everywhere by revoking their refresh tokens.
func TestPasswordResetFlow(t *testing.T) {
	api := newTestAPI(t)
	user := api.registerUser("founder")

	known := api.do(http.MethodPost, "/api/forgot-password", "", map[string]string{
		"email": user.user.Email,
	}).expect(http.StatusOK)
	unknown := api.do(http.MethodPost, "/api/forgot-password", "", map[string]string{
		"email": "nobody@example.test",
	}).expect(http.StatusOK)
	if string(known.body) != string(unknown.body) {
		t.Errorf("responses differ (%s vs %s), want one generic message so neither reveals whether the account exists", known.body, unknown.body)
	}

	mail := api.mailer.waitForMail(t)
	if mail.to != user.user.Email {
		t.Errorf("email sent to %q, want %q", mail.to, user.user.Email)
	}
	match := resetLinkToken.FindStringSubmatch(mail.body)
	if match == nil {
		t.Fatalf("no reset link in the email body:\n%s", mail.body)
	}
	token, err := url.QueryUnescape(match[1])
	if err != nil {
		t.Fatalf("unescape reset token: %v", err)
	}

	const newPassword = "a-brand-new-password"
	api.do(http.MethodPost, "/api/reset-password", "", map[string]string{
		"token": token, "password": newPassword,
	}).expect(http.StatusOK)

	// The new password works and the old one does not.
	api.do(http.MethodPost, "/api/login", "", map[string]string{
		"username": user.user.Username, "password": newPassword,
	}).expect(http.StatusOK)
	api.do(http.MethodPost, "/api/login", "", map[string]string{
		"username": user.user.Username, "password": user.password,
	}).expect(http.StatusUnauthorized)

	// The token is single-use.
	api.do(http.MethodPost, "/api/reset-password", "", map[string]string{
		"token": token, "password": "yet-another-password",
	}).expect(http.StatusBadRequest)

	// Sign-out-everywhere: the session held before the reset is dead.
	api.do(http.MethodPost, "/api/refresh", "", map[string]string{
		"refresh_token": user.refreshToken,
	}).expect(http.StatusUnauthorized)
}

// TestResetPasswordEnforcesThePolicy checks the new password is validated
// before it is stored, not only at registration.
func TestResetPasswordEnforcesThePolicy(t *testing.T) {
	api := newTestAPI(t)
	user := api.registerUser("founder")

	api.do(http.MethodPost, "/api/forgot-password", "", map[string]string{"email": user.user.Email}).
		expect(http.StatusOK)
	mail := api.mailer.waitForMail(t)
	token, err := url.QueryUnescape(resetLinkToken.FindStringSubmatch(mail.body)[1])
	if err != nil {
		t.Fatalf("unescape reset token: %v", err)
	}

	api.do(http.MethodPost, "/api/reset-password", "", map[string]string{
		"token": token, "password": "short",
	}).expect(http.StatusBadRequest)

	// The token survives a rejected attempt, so the user can retry.
	api.do(http.MethodPost, "/api/reset-password", "", map[string]string{
		"token": token, "password": "a-long-enough-password",
	}).expect(http.StatusOK)
}
