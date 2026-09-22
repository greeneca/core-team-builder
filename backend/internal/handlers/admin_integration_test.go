package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

// listUsers reads the admin user list.
func (a *testAPI) listUsers(s *session) []models.User {
	a.t.Helper()

	var payload struct {
		Users []models.User `json:"users"`
	}
	a.do(http.MethodGet, "/api/admin/users", s.token, nil).
		expect(http.StatusOK).
		decode(&payload)
	return payload.Users
}

// TestFirstUserBecomesAdmin is the bootstrap rule the whole admin surface rests
// on: the very first account is created as an admin, and later ones are not.
func TestFirstUserBecomesAdmin(t *testing.T) {
	api := newTestAPI(t)

	first := api.registerUser("first")
	second := api.registerUser("second")

	if !first.user.IsAdmin {
		t.Error("the first registered user should be an admin")
	}
	if second.user.IsAdmin {
		t.Error("a later user should not be an admin")
	}
}

func TestAdminEndpointsRequireAdmin(t *testing.T) {
	api := newTestAPI(t)
	api.registerUser("admin") // claims the bootstrap admin slot
	plain := api.registerUser("plain")

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/admin/users", nil},
		{http.MethodPost, "/api/admin/users", map[string]any{"username": "x", "email": "x@example.test", "password": "a-long-enough-password"}},
		{http.MethodDelete, "/api/admin/users/1", nil},
		{http.MethodPut, "/api/admin/users/1/admin", map[string]any{"is_admin": true}},
		{http.MethodGet, "/api/admin/settings", nil},
		{http.MethodPut, "/api/admin/settings", map[string]any{"registration_enabled": false}},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			api.do(tc.method, tc.path, plain.token, tc.body).expect(http.StatusForbidden)
			// And an anonymous caller never gets as far as the admin check.
			api.do(tc.method, tc.path, "", tc.body).expect(http.StatusUnauthorized)
		})
	}
}

func TestAdminCreateUser(t *testing.T) {
	api := newTestAPI(t)
	admin := api.registerUser("admin")

	var created models.User
	api.do(http.MethodPost, "/api/admin/users", admin.token, map[string]any{
		"username": "recruit",
		"email":    "recruit@example.test",
		"password": "another-long-password",
		"is_admin": true,
	}).
		expect(http.StatusCreated).
		decode(&created)

	if created.Username != "recruit" || !created.IsAdmin {
		t.Errorf("created = %+v, want an admin named recruit", created)
	}
	if created.PasswordHash != "" {
		t.Error("the password hash must never be serialized to a client")
	}
	if len(api.listUsers(admin)) != 2 {
		t.Error("want both users listed")
	}
}

// TestAdminCreateUserRejectsAWeakPassword checks the admin path enforces the
// same password policy as public registration, rather than bypassing it.
func TestAdminCreateUserRejectsAWeakPassword(t *testing.T) {
	api := newTestAPI(t)
	admin := api.registerUser("admin")

	msg := api.do(http.MethodPost, "/api/admin/users", admin.token, map[string]any{
		"username": "recruit",
		"email":    "recruit@example.test",
		"password": "short",
	}).
		expect(http.StatusBadRequest).
		errorMessage()

	if msg == "" {
		t.Error("want an explanatory error")
	}
}

// TestAdminCannotRemoveTheLastAdmin covers the lockout guard from both angles:
// deleting and demoting. Losing it would leave the instance unadministrable.
func TestAdminCannotRemoveTheLastAdmin(t *testing.T) {
	api := newTestAPI(t)
	admin := api.registerUser("admin")
	other := api.registerUser("other")

	t.Run("cannot demote the only admin", func(t *testing.T) {
		api.do(http.MethodPut, fmt.Sprintf("/api/admin/users/%d/admin", admin.user.ID), admin.token,
			map[string]any{"is_admin": false}).
			expect(http.StatusBadRequest)
	})

	t.Run("cannot delete yourself", func(t *testing.T) {
		api.do(http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", admin.user.ID), admin.token, nil).
			expect(http.StatusBadRequest)
	})

	t.Run("demotion works once a second admin exists", func(t *testing.T) {
		api.do(http.MethodPut, fmt.Sprintf("/api/admin/users/%d/admin", other.user.ID), admin.token,
			map[string]any{"is_admin": true}).
			expect(http.StatusOK)
		api.do(http.MethodPut, fmt.Sprintf("/api/admin/users/%d/admin", admin.user.ID), admin.token,
			map[string]any{"is_admin": false}).
			expect(http.StatusOK)
	})
}

func TestAdminDeleteUser(t *testing.T) {
	api := newTestAPI(t)
	admin := api.registerUser("admin")
	victim := api.registerUser("victim")

	api.do(http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", victim.user.ID), admin.token, nil).
		expect(http.StatusNoContent)

	if got := api.listUsers(admin); len(got) != 1 {
		t.Errorf("got %d users, want only the admin left", len(got))
	}
	// Their refresh token is gone (refresh_tokens cascades on user delete), so
	// the session cannot be renewed.
	api.do(http.MethodPost, "/api/refresh", "", map[string]string{
		"refresh_token": victim.refreshToken,
	}).expect(http.StatusUnauthorized)
	// Their already-issued access token is a stateless JWT, so it stays
	// syntactically valid until it expires: the middleware lets it through and
	// the failure surfaces at the user lookup as a 404, not a 401. The short
	// access TTL is what bounds the window.
	api.do(http.MethodGet, "/api/me", victim.token, nil).expect(http.StatusNotFound)

	api.do(http.MethodDelete, "/api/admin/users/99999", admin.token, nil).expect(http.StatusNotFound)
	api.do(http.MethodDelete, "/api/admin/users/not-a-number", admin.token, nil).expect(http.StatusBadRequest)
}

// TestSettingsReadAndEcho covers the admin settings reads themselves; whether
// the flag is *enforced* is TestRegistrationToggle's job in
// auth_integration_test.go.
func TestSettingsReadAndEcho(t *testing.T) {
	api := newTestAPI(t)
	admin := api.registerUser("admin")

	read := func() bool {
		var payload struct {
			RegistrationEnabled bool `json:"registration_enabled"`
		}
		api.do(http.MethodGet, "/api/admin/settings", admin.token, nil).
			expect(http.StatusOK).
			decode(&payload)
		return payload.RegistrationEnabled
	}

	if !read() {
		t.Fatal("registration should be enabled by default")
	}

	// The update response echoes the stored value, so the UI can render from
	// it without a follow-up read.
	var updated struct {
		RegistrationEnabled bool `json:"registration_enabled"`
	}
	api.do(http.MethodPut, "/api/admin/settings", admin.token,
		map[string]any{"registration_enabled": false}).
		expect(http.StatusOK).
		decode(&updated)
	if updated.RegistrationEnabled {
		t.Error("the update response should report the new value")
	}
	if read() {
		t.Error("the setting should have persisted")
	}
}

// TestUpdateSettingsIgnoresAnAbsentField lets a client PUT a partial body
// without clobbering settings it did not mean to change.
func TestUpdateSettingsIgnoresAnAbsentField(t *testing.T) {
	api := newTestAPI(t)
	admin := api.registerUser("admin")

	api.do(http.MethodPut, "/api/admin/settings", admin.token,
		map[string]any{"registration_enabled": false}).expect(http.StatusOK)

	var settings struct {
		RegistrationEnabled bool `json:"registration_enabled"`
	}
	api.do(http.MethodPut, "/api/admin/settings", admin.token, map[string]any{}).
		expect(http.StatusOK).
		decode(&settings)

	if settings.RegistrationEnabled {
		t.Error("an omitted field should leave the setting as it was")
	}
}

// --- Discord account linking ---

// TestDiscordLinkCodeLifecycle covers the web half of linking: issuing a code,
// reading the (absent) link, and unlinking. The bot half redeems the code.
func TestDiscordLinkCodeLifecycle(t *testing.T) {
	api := newTestAPI(t)
	user := api.registerUser("owner")

	var first struct {
		Code      string `json:"code"`
		ExpiresAt string `json:"expires_at"`
	}
	api.do(http.MethodPost, "/api/discord/link-code", user.token, nil).
		expect(http.StatusOK).
		decode(&first)

	if first.Code == "" {
		t.Fatal("want a link code")
	}
	if first.ExpiresAt == "" {
		t.Error("want an expiry so the UI can count down")
	}

	// Asking again issues a different code; only the newest may be valid.
	var second struct {
		Code string `json:"code"`
	}
	api.do(http.MethodPost, "/api/discord/link-code", user.token, nil).
		expect(http.StatusOK).
		decode(&second)
	if second.Code == first.Code {
		t.Error("a re-issued code should differ from the previous one")
	}

	// Nothing is linked until the bot redeems a code.
	var link struct {
		Linked bool `json:"linked"`
	}
	api.do(http.MethodGet, "/api/discord/link", user.token, nil).
		expect(http.StatusOK).
		decode(&link)
	if link.Linked {
		t.Error("no Discord account should be linked yet")
	}

	// Unlinking when nothing is linked is a no-op, not an error.
	api.do(http.MethodDelete, "/api/discord/link", user.token, nil).expect(http.StatusNoContent)
}

func TestDiscordLinkEndpointsRequireAuth(t *testing.T) {
	api := newTestAPI(t)

	api.do(http.MethodPost, "/api/discord/link-code", "", nil).expect(http.StatusUnauthorized)
	api.do(http.MethodGet, "/api/discord/link", "", nil).expect(http.StatusUnauthorized)
	api.do(http.MethodDelete, "/api/discord/link", "", nil).expect(http.StatusUnauthorized)
}
