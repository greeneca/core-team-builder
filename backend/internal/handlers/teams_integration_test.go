package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

// updatePayload is a minimal valid body for PUT /api/teams/{id}. The handler
// requires a name and validates the rest, so tests that only care about
// permissions still have to send a well-formed save.
func updatePayload(name string) map[string]any {
	return map[string]any{
		"name":          name,
		"schedule_days": []string{},
		"schedule_time": "",
		"players":       []any{},
	}
}

// TestCreateTeamSeedsARoster covers what a new team is born with: an owner
// membership, one active roster, and a full 12-slot lineup. The whole app
// assumes those exist, so a migration that broke the seeding would surface as
// confusing failures everywhere else.
func TestCreateTeamSeedsARoster(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")

	team := api.createTeam(owner, "Sunday Core")
	if team.Name != "Sunday Core" {
		t.Errorf("name = %q, want %q", team.Name, "Sunday Core")
	}
	if team.ActiveRosterID == 0 {
		t.Error("the new team has no active roster")
	}
	if len(team.Players) != models.TeamSize {
		t.Errorf("got %d players, want %d", len(team.Players), models.TeamSize)
	}
	if len(team.Rosters) != 1 {
		t.Errorf("got %d rosters, want 1", len(team.Rosters))
	}

	// Every slot 1..TeamSize is present exactly once.
	seen := map[int]bool{}
	for _, p := range team.Players {
		if seen[p.Slot] {
			t.Errorf("slot %d appears twice", p.Slot)
		}
		seen[p.Slot] = true
	}
	for slot := 1; slot <= models.TeamSize; slot++ {
		if !seen[slot] {
			t.Errorf("slot %d is missing", slot)
		}
	}

	// And a Default encounter to hang loadouts off.
	var encounters struct {
		Encounters []models.Encounter `json:"encounters"`
	}
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/encounters", team.ID), owner.token, nil).
		expect(http.StatusOK).decode(&encounters)
	if len(encounters.Encounters) != 1 || encounters.Encounters[0].Name != "Default" {
		t.Errorf("encounters = %+v, want exactly one named Default", encounters.Encounters)
	}
}

// TestListTeamsOnlyReturnsYourOwn covers the list scoping: a user sees the
// teams they own or were shared, never anyone else's.
func TestListTeamsOnlyReturnsYourOwn(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	stranger := api.registerUser("stranger")

	mine := api.createTeam(owner, "Sunday Core")
	api.createTeam(stranger, "Someone Else's Core")

	var listed struct {
		Teams []models.Team `json:"teams"`
	}
	api.do(http.MethodGet, "/api/teams", owner.token, nil).expect(http.StatusOK).decode(&listed)
	if len(listed.Teams) != 1 {
		t.Fatalf("got %d teams, want only the caller's own", len(listed.Teams))
	}
	if listed.Teams[0].ID != mine.ID {
		t.Errorf("listed team %d, want %d", listed.Teams[0].ID, mine.ID)
	}
}

// TestInaccessibleTeamIs404 covers the deliberate choice to answer 404 rather
// than 403 for a team the caller has no membership in, so the API does not
// confirm that someone else's team id exists.
func TestInaccessibleTeamIs404(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	stranger := api.registerUser("stranger")

	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d", team.ID)

	api.do(http.MethodGet, path, stranger.token, nil).expect(http.StatusNotFound)
	api.do(http.MethodPut, path, stranger.token, updatePayload("Hijacked")).expect(http.StatusNotFound)
	api.do(http.MethodDelete, path, stranger.token, nil).expect(http.StatusNotFound)

	// A team id that does not exist answers the same way, so the two are
	// indistinguishable from outside.
	api.do(http.MethodGet, "/api/teams/999999", stranger.token, nil).expect(http.StatusNotFound)
}

// TestSharingRoles covers the permission matrix sharing produces: a viewer can
// read but not write, an editor can write but not manage sharing, and only the
// owner can delete or share.
func TestSharingRoles(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	editor := api.registerUser("editor")
	viewer := api.registerUser("viewer")

	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d", team.ID)
	sharePath := path + "/share"

	api.do(http.MethodPost, sharePath, owner.token,
		map[string]string{"username": editor.user.Username, "role": models.RoleEditor}).expect(http.StatusOK)
	api.do(http.MethodPost, sharePath, owner.token,
		map[string]string{"username": viewer.user.Username, "role": models.RoleViewer}).expect(http.StatusOK)

	// Both shared users can read.
	api.do(http.MethodGet, path, editor.token, nil).expect(http.StatusOK)
	api.do(http.MethodGet, path, viewer.token, nil).expect(http.StatusOK)

	// Only the editor can write.
	api.do(http.MethodPut, path, editor.token, updatePayload("Renamed By Editor")).expect(http.StatusOK)
	api.do(http.MethodPut, path, viewer.token, updatePayload("Renamed By Viewer")).expect(http.StatusForbidden)

	// Neither can share or delete — that is the owner's alone.
	for _, s := range []*session{editor, viewer} {
		api.do(http.MethodPost, sharePath, s.token,
			map[string]string{"username": owner.user.Username, "role": models.RoleViewer}).expect(http.StatusForbidden)
		api.do(http.MethodDelete, path, s.token, nil).expect(http.StatusForbidden)
	}

	var after models.Team
	api.do(http.MethodGet, path, owner.token, nil).expect(http.StatusOK).decode(&after)
	if after.Name != "Renamed By Editor" {
		t.Errorf("name = %q, want the editor's save to have stuck", after.Name)
	}
}

// TestResharePromotesAnExistingMember covers the upsert: re-sharing with a
// different role changes the existing membership rather than failing or
// duplicating it.
func TestResharePromotesAnExistingMember(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	member := api.registerUser("member")

	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d", team.ID)
	sharePath := path + "/share"

	api.do(http.MethodPost, sharePath, owner.token,
		map[string]string{"username": member.user.Username, "role": models.RoleViewer}).expect(http.StatusOK)
	api.do(http.MethodPut, path, member.token, updatePayload("Nope")).expect(http.StatusForbidden)

	api.do(http.MethodPost, sharePath, owner.token,
		map[string]string{"username": member.user.Username, "role": models.RoleEditor}).expect(http.StatusOK)
	api.do(http.MethodPut, path, member.token, updatePayload("Now Allowed")).expect(http.StatusOK)
}

// TestShareRejectsBadInput covers the share endpoint's validation, including
// the roles it refuses to grant — "owner" is not a shareable role.
func TestShareRejectsBadInput(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	member := api.registerUser("member")

	sharePath := fmt.Sprintf("/api/teams/%d/share", api.createTeam(owner, "Sunday Core").ID)

	cases := []struct {
		name string
		body map[string]string
		want int
	}{
		{"unknown username", map[string]string{"username": "nobody-at-all", "role": models.RoleViewer}, http.StatusNotFound},
		{"missing username", map[string]string{"role": models.RoleViewer}, http.StatusBadRequest},
		{"owner is not shareable", map[string]string{"username": member.user.Username, "role": models.RoleOwner}, http.StatusBadRequest},
		{"unknown role", map[string]string{"username": member.user.Username, "role": "superuser"}, http.StatusBadRequest},
		{"sharing with yourself", map[string]string{"username": owner.user.Username, "role": models.RoleViewer}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api.do(http.MethodPost, sharePath, owner.token, tc.body).expect(tc.want)
		})
	}
}

// TestLeaveTeam covers a shared member removing themselves, and the owner being
// refused (they delete the team instead).
func TestLeaveTeam(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	member := api.registerUser("member")

	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d", team.ID)

	api.do(http.MethodPost, path+"/share", owner.token,
		map[string]string{"username": member.user.Username, "role": models.RoleEditor}).expect(http.StatusOK)
	api.do(http.MethodGet, path, member.token, nil).expect(http.StatusOK)

	api.do(http.MethodDelete, path+"/membership", member.token, nil).expect(http.StatusNoContent)
	api.do(http.MethodGet, path, member.token, nil).expect(http.StatusNotFound)

	// The owner cannot leave their own team.
	api.do(http.MethodDelete, path+"/membership", owner.token, nil).expect(http.StatusForbidden)
	api.do(http.MethodGet, path, owner.token, nil).expect(http.StatusOK)
}

// TestDeleteTeam covers the owner's delete and the cascade: the team is gone
// for everyone it was shared with, not just the owner.
func TestDeleteTeam(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	member := api.registerUser("member")

	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d", team.ID)
	api.do(http.MethodPost, path+"/share", owner.token,
		map[string]string{"username": member.user.Username, "role": models.RoleEditor}).expect(http.StatusOK)

	api.do(http.MethodDelete, path, owner.token, nil).expect(http.StatusNoContent)
	api.do(http.MethodGet, path, owner.token, nil).expect(http.StatusNotFound)
	api.do(http.MethodGet, path, member.token, nil).expect(http.StatusNotFound)
}

// TestSavePlayerPersistsToTheActiveRoster covers the per-slot save the roster
// UI autosaves through, and its validation of the ESO reference data.
func TestSavePlayerPersistsToTheActiveRoster(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	slotPath := fmt.Sprintf("/api/teams/%d/players/1", team.ID)
	api.do(http.MethodPut, slotPath, owner.token, map[string]any{
		"slot":  1,
		"name":  "Ayla",
		"role":  "tank",
		"class": "dragonknight",
	}).expect(http.StatusOK)

	var reloaded models.Team
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d", team.ID), owner.token, nil).
		expect(http.StatusOK).decode(&reloaded)

	var slot1 models.Player
	for _, p := range reloaded.Players {
		if p.Slot == 1 {
			slot1 = p
		}
	}
	if slot1.Name != "Ayla" || slot1.Class != "dragonknight" || slot1.Role != "tank" {
		t.Errorf("slot 1 = %+v, want the saved name/class/role", slot1)
	}

	// An unknown class is rejected against the allow-list in models/eso.go.
	api.do(http.MethodPut, slotPath, owner.token, map[string]any{
		"slot": 1, "name": "Ayla", "role": "tank", "class": "spellsword",
	}).expect(http.StatusBadRequest)
}

// TestTeamEndpointsRejectAnonymousCallers walks the team routes without a
// token. Every one is behind the bearer middleware, so a missing route
// registration would show up here as a 200 instead of a 401.
func TestTeamEndpointsRejectAnonymousCallers(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d", team.ID)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/teams"},
		{http.MethodPost, "/api/teams"},
		{http.MethodGet, path},
		{http.MethodPut, path},
		{http.MethodDelete, path},
		{http.MethodPost, path + "/share"},
		{http.MethodGet, path + "/rosters"},
		{http.MethodGet, path + "/encounters"},
		{http.MethodGet, path + "/groupings"},
		{http.MethodGet, path + "/images"},
		{http.MethodGet, path + "/roster-members"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			api.do(tc.method, tc.path, "", nil).expect(http.StatusUnauthorized)
		})
	}
}
