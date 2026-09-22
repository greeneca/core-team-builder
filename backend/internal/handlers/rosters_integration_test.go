package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

// rosterList is the envelope GET /api/teams/{id}/rosters returns: the team's
// rosters plus a pointer to whichever one is active.
type rosterList struct {
	Rosters        []models.Roster `json:"rosters"`
	ActiveRosterID int64           `json:"active_roster_id"`
}

// listRosters reads a team's rosters through the API.
func (a *testAPI) listRosters(s *session, teamID int64) rosterList {
	a.t.Helper()

	var payload rosterList
	a.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/rosters", teamID), s.token, nil).
		expect(http.StatusOK).
		decode(&payload)
	return payload
}

// createRoster adds a roster to a team. A non-zero copyFrom seeds the new
// roster from an existing one instead of starting it empty.
func (a *testAPI) createRoster(s *session, teamID int64, name string, copyFrom int64) models.Roster {
	a.t.Helper()

	body := map[string]any{"name": name}
	if copyFrom != 0 {
		body["copy_from"] = copyFrom
	}
	var roster models.Roster
	a.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/rosters", teamID), s.token, body).
		expect(http.StatusCreated).
		decode(&roster)
	return roster
}

// TestListRostersReportsTheActiveOne covers the envelope the roster switcher
// reads on every page load. The active id has to name a roster that is actually
// in the list, or the UI has nothing to select.
func TestListRostersReportsTheActiveOne(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	list := api.listRosters(owner, team.ID)

	if len(list.Rosters) != 1 {
		t.Fatalf("got %d rosters, want the seeded one", len(list.Rosters))
	}
	if list.ActiveRosterID != list.Rosters[0].ID {
		t.Errorf("active_roster_id = %d, want %d", list.ActiveRosterID, list.Rosters[0].ID)
	}
	// The list view is deliberately shallow; lineups come from the single-roster
	// read so switching tabs does not ship twelve players per roster.
	if len(list.Rosters[0].Players) != 0 {
		t.Errorf("got %d players from the list endpoint, want none", len(list.Rosters[0].Players))
	}
}

// TestCreateRoster covers the fresh-roster path: a full empty lineup, its own
// Default encounter, and no change to which roster is active.
func TestCreateRoster(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	created := api.createRoster(owner, team.ID, "Progression", 0)

	if created.Name != "Progression" {
		t.Errorf("name = %q, want Progression", created.Name)
	}
	if len(created.Players) != models.TeamSize {
		t.Errorf("got %d players, want one per slot (%d)", len(created.Players), models.TeamSize)
	}
	for _, p := range created.Players {
		if p.Name != "" {
			t.Errorf("slot %d = %q, want an empty slot", p.Slot, p.Name)
		}
	}

	// Creating a roster does not activate it — that is a separate explicit step.
	list := api.listRosters(owner, team.ID)
	if len(list.Rosters) != 2 {
		t.Fatalf("got %d rosters, want 2", len(list.Rosters))
	}
	if list.ActiveRosterID == created.ID {
		t.Error("creating a roster made it active, want the original to stay active")
	}

	// It gets its own Default encounter rather than sharing the team's.
	var payload struct {
		Encounters []models.Encounter `json:"encounters"`
	}
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/encounters?roster_id=%d", team.ID, created.ID), owner.token, nil).
		expect(http.StatusOK).
		decode(&payload)
	if len(payload.Encounters) != 1 || payload.Encounters[0].Name != "Default" {
		t.Errorf("encounters = %+v, want exactly one named Default", payload.Encounters)
	}
}

// TestCreateRosterDefaultsTheName pins the fallback the "+" button relies on:
// it posts no name at all and expects a usable roster back.
func TestCreateRosterDefaultsTheName(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	var created models.Roster
	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/rosters", team.ID), owner.token,
		map[string]any{"name": "   "}).
		expect(http.StatusCreated).
		decode(&created)

	if created.Name != "Roster" {
		t.Errorf("name = %q, want the Roster default", created.Name)
	}
}

// TestCreateRosterValidation walks the rejections: an unreadable body, an
// over-long name, and a copy source that is missing or belongs to someone else.
// The cross-team case matters most — copying is the one place a roster id from
// outside the team reaches the store.
func TestCreateRosterValidation(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	stranger := api.registerUser("stranger")
	team := api.createTeam(owner, "Sunday Core")
	otherTeam := api.createTeam(stranger, "Someone Else")
	otherRoster := api.listRosters(stranger, otherTeam.ID).Rosters[0]

	path := fmt.Sprintf("/api/teams/%d/rosters", team.ID)

	tests := []struct {
		name string
		body any
		want int
	}{
		{"unreadable body", "not an object", http.StatusBadRequest},
		{"name too long", map[string]any{"name": strings.Repeat("r", maxRosterNameLen+1)}, http.StatusBadRequest},
		{"copy source missing", map[string]any{"name": "Copy", "copy_from": 999999}, http.StatusBadRequest},
		{"copy source on another team", map[string]any{"name": "Copy", "copy_from": otherRoster.ID}, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api.do(http.MethodPost, path, owner.token, tc.body).expect(tc.want)
		})
	}

	// A name exactly at the limit is fine; the cap is inclusive.
	api.do(http.MethodPost, path, owner.token,
		map[string]any{"name": strings.Repeat("r", maxRosterNameLen)}).
		expect(http.StatusCreated)
}

// TestCreateRosterCopiesTheSource covers the "duplicate this roster" action:
// the copy has to carry the lineup over, and then be independent of the source
// so editing the copy cannot corrupt the roster it came from.
func TestCreateRosterCopiesTheSource(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	source := api.listRosters(owner, team.ID).Rosters[0]

	api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/players/1?roster_id=%d", team.ID, source.ID), owner.token,
		map[string]any{"slot": 1, "name": "Ayla", "role": "tank", "class": "dragonknight"}).
		expect(http.StatusOK)

	copied := api.createRoster(owner, team.ID, "Copy", source.ID)

	if got := playerInSlot(t, copied.Players, 1); got.Name != "Ayla" || got.Class != "dragonknight" {
		t.Errorf("copied slot 1 = %+v, want the source's Ayla", got)
	}

	// Editing the copy must not write through to the source.
	api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/players/1?roster_id=%d", team.ID, copied.ID), owner.token,
		map[string]any{"slot": 1, "name": "Brynja", "role": "healer", "class": "warden"}).
		expect(http.StatusOK)

	var reloaded models.Roster
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/rosters/%d", team.ID, source.ID), owner.token, nil).
		expect(http.StatusOK).
		decode(&reloaded)
	if got := playerInSlot(t, reloaded.Players, 1); got.Name != "Ayla" {
		t.Errorf("source slot 1 = %q, want it untouched by the copy's edit", got.Name)
	}
}

// playerInSlot returns the player occupying a slot, failing when it is absent.
func playerInSlot(t *testing.T, players []models.Player, slot int) models.Player {
	t.Helper()

	for _, p := range players {
		if p.Slot == slot {
			return p
		}
	}
	t.Fatalf("slot %d is missing from the lineup", slot)
	return models.Player{}
}

// TestGetRosterReturnsTheLineup covers the read the roster tab issues when you
// switch to a non-active roster.
func TestGetRosterReturnsTheLineup(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	created := api.createRoster(owner, team.ID, "Progression", 0)

	var got models.Roster
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/rosters/%d", team.ID, created.ID), owner.token, nil).
		expect(http.StatusOK).
		decode(&got)

	if got.ID != created.ID || got.Name != "Progression" {
		t.Errorf("got %+v, want the roster just created", got)
	}
	if len(got.Players) != models.TeamSize {
		t.Errorf("got %d players, want one per slot (%d)", len(got.Players), models.TeamSize)
	}
}

// TestRenameRoster covers the rename plus the two ways it is refused. A blank
// name is rejected here even though create silently defaults it — the tab has
// to keep a label once it has one.
func TestRenameRoster(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	roster := api.listRosters(owner, team.ID).Rosters[0]
	path := fmt.Sprintf("/api/teams/%d/rosters/%d", team.ID, roster.ID)

	var renamed models.Roster
	api.do(http.MethodPut, path, owner.token, map[string]any{"name": "  Farming  "}).
		expect(http.StatusOK).
		decode(&renamed)
	if renamed.Name != "Farming" {
		t.Errorf("name = %q, want it trimmed to Farming", renamed.Name)
	}

	api.do(http.MethodPut, path, owner.token, map[string]any{"name": "   "}).
		expect(http.StatusBadRequest)
	api.do(http.MethodPut, path, owner.token,
		map[string]any{"name": strings.Repeat("r", maxRosterNameLen+1)}).
		expect(http.StatusBadRequest)
	api.do(http.MethodPut, path, owner.token, "not an object").
		expect(http.StatusBadRequest)

	// None of the rejections should have overwritten the good name.
	if got := api.listRosters(owner, team.ID).Rosters[0]; got.Name != "Farming" {
		t.Errorf("name = %q, want the rejected renames to have left Farming", got.Name)
	}
}

// TestActivateRoster covers the switcher's "make this the live roster" action.
// The active roster is what the Discord bot posts, so the change has to land on
// the team record and not just the roster list.
func TestActivateRoster(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	created := api.createRoster(owner, team.ID, "Progression", 0)

	var updated models.Team
	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/rosters/%d/activate", team.ID, created.ID), owner.token, nil).
		expect(http.StatusOK).
		decode(&updated)

	if updated.ActiveRosterID != created.ID {
		t.Errorf("team active_roster_id = %d, want %d", updated.ActiveRosterID, created.ID)
	}
	if got := api.listRosters(owner, team.ID).ActiveRosterID; got != created.ID {
		t.Errorf("listed active_roster_id = %d, want %d", got, created.ID)
	}
}

// TestDeleteRosterKeepsTheLastOne covers the floor the whole app leans on: a
// team always has at least one roster, so the delete has to refuse the last.
func TestDeleteRosterKeepsTheLastOne(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	original := api.listRosters(owner, team.ID).Rosters[0]
	created := api.createRoster(owner, team.ID, "Progression", 0)

	api.do(http.MethodDelete, fmt.Sprintf("/api/teams/%d/rosters/%d", team.ID, created.ID), owner.token, nil).
		expect(http.StatusNoContent)

	list := api.listRosters(owner, team.ID)
	if len(list.Rosters) != 1 || list.Rosters[0].ID != original.ID {
		t.Fatalf("rosters = %+v, want only the original", list.Rosters)
	}

	resp := api.do(http.MethodDelete, fmt.Sprintf("/api/teams/%d/rosters/%d", team.ID, original.ID), owner.token, nil).
		expect(http.StatusBadRequest)
	if msg := resp.errorMessage(); !strings.Contains(msg, "at least one roster") {
		t.Errorf("error = %q, want it to explain the last-roster rule", msg)
	}
}

// TestDeleteActiveRosterPromotesAnother covers the dangling-pointer case: the
// team's active_roster_id must never survive pointing at a deleted roster.
func TestDeleteActiveRosterPromotesAnother(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	original := api.listRosters(owner, team.ID).Rosters[0]
	created := api.createRoster(owner, team.ID, "Progression", 0)

	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/rosters/%d/activate", team.ID, created.ID), owner.token, nil).
		expect(http.StatusOK)
	api.do(http.MethodDelete, fmt.Sprintf("/api/teams/%d/rosters/%d", team.ID, created.ID), owner.token, nil).
		expect(http.StatusNoContent)

	if got := api.listRosters(owner, team.ID).ActiveRosterID; got != original.ID {
		t.Errorf("active_roster_id = %d, want it promoted to the survivor %d", got, original.ID)
	}
}

// TestRosterFromAnotherTeamIs404 covers the ownership check on every
// roster-scoped route. A roster id is guessable, so the handlers must confirm it
// belongs to the team in the path rather than trusting the id alone.
func TestRosterFromAnotherTeamIs404(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	stranger := api.registerUser("stranger")
	team := api.createTeam(owner, "Sunday Core")
	otherTeam := api.createTeam(stranger, "Someone Else")
	otherRoster := api.listRosters(stranger, otherTeam.ID).Rosters[0]

	foreign := fmt.Sprintf("/api/teams/%d/rosters/%d", team.ID, otherRoster.ID)
	api.do(http.MethodGet, foreign, owner.token, nil).expect(http.StatusNotFound)
	api.do(http.MethodPut, foreign, owner.token, map[string]any{"name": "Stolen"}).expect(http.StatusNotFound)
	api.do(http.MethodDelete, foreign, owner.token, nil).expect(http.StatusNotFound)
	api.do(http.MethodPost, foreign+"/activate", owner.token, nil).expect(http.StatusNotFound)

	// The stranger's roster is untouched by any of that.
	if got := api.listRosters(stranger, otherTeam.ID); len(got.Rosters) != 1 {
		t.Errorf("got %d rosters on the other team, want 1", len(got.Rosters))
	}

	// A roster id that is not a number never reaches the store.
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/rosters/abc", team.ID), owner.token, nil).
		expect(http.StatusBadRequest)
}

// TestRosterQueryParamSelectsTheRoster covers ?roster_id=, the mechanism every
// roster-scoped read uses to target a roster other than the active one, and its
// two rejections.
func TestRosterQueryParamSelectsTheRoster(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	stranger := api.registerUser("stranger")
	team := api.createTeam(owner, "Sunday Core")
	otherTeam := api.createTeam(stranger, "Someone Else")
	otherRoster := api.listRosters(stranger, otherTeam.ID).Rosters[0]
	created := api.createRoster(owner, team.ID, "Progression", 0)

	// Rename the new roster's encounter so the two rosters are distinguishable.
	// Encounter names come from an allow-list, so this has to be a real one.
	var payload struct {
		Encounters []models.Encounter `json:"encounters"`
	}
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/encounters?roster_id=%d", team.ID, created.ID), owner.token, nil).
		expect(http.StatusOK).
		decode(&payload)
	if len(payload.Encounters) != 1 {
		t.Fatalf("got %d encounters on the new roster, want its seeded Default", len(payload.Encounters))
	}
	api.do(http.MethodPut,
		fmt.Sprintf("/api/teams/%d/encounters/%d", team.ID, payload.Encounters[0].ID), owner.token,
		map[string]any{"name": "Lokkestiiz"}).
		expect(http.StatusOK)

	// The active roster's encounters are unaffected, which is what proves the
	// query param routed the write to the other roster.
	if got := api.listEncounters(owner, team.ID); len(got) != 1 || got[0].Name != "Default" {
		t.Errorf("active roster encounters = %+v, want the untouched Default", got)
	}

	base := fmt.Sprintf("/api/teams/%d/encounters", team.ID)
	api.do(http.MethodGet, base+"?roster_id=abc", owner.token, nil).expect(http.StatusBadRequest)
	api.do(http.MethodGet, fmt.Sprintf("%s?roster_id=%d", base, otherRoster.ID), owner.token, nil).
		expect(http.StatusNotFound)
}

// TestRosterPermissions covers the read/write split on the roster routes:
// viewers may browse rosters but every mutation is editor-and-up.
func TestRosterPermissions(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	editor := api.registerUser("editor")
	viewer := api.registerUser("viewer")
	outsider := api.registerUser("outsider")
	team := api.createTeam(owner, "Sunday Core")

	sharePath := fmt.Sprintf("/api/teams/%d/share", team.ID)
	api.do(http.MethodPost, sharePath, owner.token,
		map[string]string{"username": editor.user.Username, "role": models.RoleEditor}).expect(http.StatusOK)
	api.do(http.MethodPost, sharePath, owner.token,
		map[string]string{"username": viewer.user.Username, "role": models.RoleViewer}).expect(http.StatusOK)

	roster := api.listRosters(owner, team.ID).Rosters[0]
	collection := fmt.Sprintf("/api/teams/%d/rosters", team.ID)
	item := fmt.Sprintf("%s/%d", collection, roster.ID)

	// Reads are open to anyone on the team; outsiders get the same 404 a missing
	// team would produce, so membership is not leaked.
	api.do(http.MethodGet, collection, editor.token, nil).expect(http.StatusOK)
	api.do(http.MethodGet, collection, viewer.token, nil).expect(http.StatusOK)
	api.do(http.MethodGet, item, viewer.token, nil).expect(http.StatusOK)
	api.do(http.MethodGet, collection, outsider.token, nil).expect(http.StatusNotFound)

	// The editor may mutate.
	created := api.createRoster(editor, team.ID, "Editor's Roster", 0)
	api.do(http.MethodPut, fmt.Sprintf("%s/%d", collection, created.ID), editor.token,
		map[string]any{"name": "Renamed"}).expect(http.StatusOK)
	api.do(http.MethodPost, fmt.Sprintf("%s/%d/activate", collection, created.ID), editor.token, nil).
		expect(http.StatusOK)
	api.do(http.MethodDelete, fmt.Sprintf("%s/%d", collection, created.ID), editor.token, nil).
		expect(http.StatusNoContent)

	// The viewer may not.
	api.do(http.MethodPost, collection, viewer.token, map[string]any{"name": "Nope"}).
		expect(http.StatusForbidden)
	api.do(http.MethodPut, item, viewer.token, map[string]any{"name": "Nope"}).
		expect(http.StatusForbidden)
	api.do(http.MethodPost, item+"/activate", viewer.token, nil).expect(http.StatusForbidden)
	api.do(http.MethodDelete, item, viewer.token, nil).expect(http.StatusForbidden)

	if got := api.listRosters(owner, team.ID); len(got.Rosters) != 1 {
		t.Errorf("got %d rosters, want the viewer's attempts to have changed nothing", len(got.Rosters))
	}
}

// TestRosterEndpointsRejectAnonymousCallers walks the roster routes without a
// token. Every one sits behind the bearer middleware, so a route registered
// outside it would show up here as something other than a 401.
func TestRosterEndpointsRejectAnonymousCallers(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	roster := api.listRosters(owner, team.ID).Rosters[0]

	collection := fmt.Sprintf("/api/teams/%d/rosters", team.ID)
	item := fmt.Sprintf("%s/%d", collection, roster.ID)

	routes := []struct{ method, path string }{
		{http.MethodGet, collection},
		{http.MethodPost, collection},
		{http.MethodGet, item},
		{http.MethodPut, item},
		{http.MethodDelete, item},
		{http.MethodPost, item + "/activate"},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			api.do(route.method, route.path, "", nil).expect(http.StatusUnauthorized)
		})
	}
}
