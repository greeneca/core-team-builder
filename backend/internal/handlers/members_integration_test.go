package handlers

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

// listRosterMembers reads a team's recruitment pool through the API.
func (a *testAPI) listRosterMembers(s *session, teamID int64) []models.RosterMember {
	a.t.Helper()

	var payload struct {
		Members []models.RosterMember `json:"members"`
	}
	a.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/roster-members", teamID), s.token, nil).
		expect(http.StatusOK).
		decode(&payload)
	return payload.Members
}

// createRosterMember adds a pool entry and returns it.
func (a *testAPI) createRosterMember(s *session, teamID int64, body map[string]any) models.RosterMember {
	a.t.Helper()

	var created models.RosterMember
	a.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/roster-members", teamID), s.token, body).
		expect(http.StatusCreated).
		decode(&created)
	return created
}

// TestCreateRosterMember covers the manual "add to pool" form end to end,
// including the normalization the handler applies on the way in: unknown roles
// and classes are dropped, days come back in week order, and availability is
// pruned to the days actually chosen.
func TestCreateRosterMember(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	created := api.createRosterMember(owner, team.ID, map[string]any{
		"display_name":     "  Ayla  ",
		"discord_username": " ayla#0001 ",
		"timezone":         " Europe/Berlin ",
		"days":             []string{"fri", "MON", "mon"},
		"roles":            []string{"tank", "raidlead"},
		"classes_by_role":  map[string][]string{"tank": {"Dragonknight", "spellsword"}},
		"availability": map[string]any{
			"mon": map[string]int{"start": 18, "end": 22},
			// Tuesday was not chosen, so this window is discarded.
			"tue": map[string]int{"start": 18, "end": 22},
		},
	})

	if created.DisplayName != "Ayla" {
		t.Errorf("display name = %q, want it trimmed to Ayla", created.DisplayName)
	}
	if created.DiscordUsername != "ayla#0001" {
		t.Errorf("discord username = %q, want it trimmed", created.DiscordUsername)
	}
	if created.Timezone != "Europe/Berlin" {
		t.Errorf("timezone = %q, want it trimmed", created.Timezone)
	}
	if want := []string{"mon", "fri"}; !reflect.DeepEqual(created.Days, want) {
		t.Errorf("days = %v, want %v", created.Days, want)
	}
	if want := []string{"tank"}; !reflect.DeepEqual(created.Roles, want) {
		t.Errorf("roles = %v, want the unknown role dropped (%v)", created.Roles, want)
	}
	if want := map[string][]string{"tank": {"dragonknight"}}; !reflect.DeepEqual(created.ClassesByRole, want) {
		t.Errorf("classes = %v, want the unknown class dropped (%v)", created.ClassesByRole, want)
	}
	if want := (map[string]models.DayWindow{"mon": {Start: 18, End: 22}}); !reflect.DeepEqual(created.Availability, want) {
		t.Errorf("availability = %v, want only the chosen day (%v)", created.Availability, want)
	}

	// A web-added member is complete from the start; only the Discord intake DM
	// leaves a member part-way through the flow.
	if created.Status != models.MemberStatusComplete {
		t.Errorf("status = %q, want %q", created.Status, models.MemberStatusComplete)
	}
	if created.Source != "manual" {
		t.Errorf("source = %q, want manual", created.Source)
	}

	if got := api.listRosterMembers(owner, team.ID); len(got) != 1 || got[0].ID != created.ID {
		t.Errorf("list = %+v, want just the member created", got)
	}
}

// TestCreateRosterMemberValidation walks the rejections. A display name is the
// one field with no sensible default — everything else is normalized rather
// than refused — so it is the only required one.
func TestCreateRosterMemberValidation(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d/roster-members", team.ID)

	tests := []struct {
		name string
		body any
		want string
	}{
		{"unreadable body", "not an object", "invalid request body"},
		{"missing display name", map[string]any{}, "display name is required"},
		{"blank display name", map[string]any{"display_name": "   "}, "display name is required"},
		{"invalid day", map[string]any{"display_name": "Ayla", "days": []string{"funday"}}, "invalid day: funday"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := api.do(http.MethodPost, path, owner.token, tc.body).expect(http.StatusBadRequest)
			if got := resp.errorMessage(); got != tc.want {
				t.Errorf("error = %q, want %q", got, tc.want)
			}
		})
	}

	if got := api.listRosterMembers(owner, team.ID); len(got) != 0 {
		t.Errorf("got %d members, want none of the rejected requests to have persisted", len(got))
	}
}

// TestUpdateRosterMember covers the edit form, and the fields it must leave
// alone: editing a Discord-sourced member cannot reset their intake state, or
// the bot would DM them again.
func TestUpdateRosterMember(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	created := api.createRosterMember(owner, team.ID, map[string]any{
		"display_name": "Ayla",
		"days":         []string{"mon"},
		"roles":        []string{"tank"},
	})

	var updated models.RosterMember
	api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/roster-members/%d", team.ID, created.ID), owner.token,
		map[string]any{
			"display_name": "Brynja",
			"days":         []string{"sat", "wed"},
			"roles":        []string{"healer"},
			"availability": map[string]any{"wed": map[string]int{"start": 19, "end": 23}},
		}).
		expect(http.StatusOK).
		decode(&updated)

	if updated.DisplayName != "Brynja" {
		t.Errorf("display name = %q, want Brynja", updated.DisplayName)
	}
	if want := []string{"wed", "sat"}; !reflect.DeepEqual(updated.Days, want) {
		t.Errorf("days = %v, want %v", updated.Days, want)
	}
	if want := []string{"healer"}; !reflect.DeepEqual(updated.Roles, want) {
		t.Errorf("roles = %v, want %v", updated.Roles, want)
	}
	if updated.ID != created.ID {
		t.Errorf("id = %d, want the update to edit %d in place", updated.ID, created.ID)
	}
	if updated.Status != created.Status || updated.Step != created.Step || updated.Source != created.Source {
		t.Errorf("intake state = %q/%q/%q, want it untouched (%q/%q/%q)",
			updated.Status, updated.Step, updated.Source, created.Status, created.Step, created.Source)
	}

	if got := api.listRosterMembers(owner, team.ID); len(got) != 1 {
		t.Errorf("got %d members, want the update to have edited rather than added", len(got))
	}
}

// TestUpdateRosterMemberRejections covers the four ways an edit is refused.
func TestUpdateRosterMemberRejections(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	created := api.createRosterMember(owner, team.ID, map[string]any{"display_name": "Ayla"})

	base := fmt.Sprintf("/api/teams/%d/roster-members", team.ID)
	path := fmt.Sprintf("%s/%d", base, created.ID)

	api.do(http.MethodPut, base+"/abc", owner.token, map[string]any{"display_name": "Ayla"}).
		expect(http.StatusBadRequest)
	api.do(http.MethodPut, path, owner.token, "not an object").
		expect(http.StatusBadRequest)
	api.do(http.MethodPut, path, owner.token, map[string]any{"display_name": "  "}).
		expect(http.StatusBadRequest)
	api.do(http.MethodPut, base+"/999999", owner.token, map[string]any{"display_name": "Ghost"}).
		expect(http.StatusNotFound)

	if got := api.listRosterMembers(owner, team.ID); got[0].DisplayName != "Ayla" {
		t.Errorf("display name = %q, want the rejected edits to have left Ayla", got[0].DisplayName)
	}
}

// TestDeleteRosterMember covers removal from the pool. Deleting an id that is
// not there succeeds rather than 404ing — the store issues an unconditional
// team-scoped DELETE — so the double-click case is harmless.
func TestDeleteRosterMember(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	created := api.createRosterMember(owner, team.ID, map[string]any{"display_name": "Ayla"})

	base := fmt.Sprintf("/api/teams/%d/roster-members", team.ID)
	api.do(http.MethodDelete, fmt.Sprintf("%s/%d", base, created.ID), owner.token, nil).
		expect(http.StatusNoContent)
	if got := api.listRosterMembers(owner, team.ID); len(got) != 0 {
		t.Errorf("got %d members, want the pool emptied", len(got))
	}

	api.do(http.MethodDelete, fmt.Sprintf("%s/%d", base, created.ID), owner.token, nil).
		expect(http.StatusNoContent)
	api.do(http.MethodDelete, base+"/abc", owner.token, nil).
		expect(http.StatusBadRequest)
}

// TestRosterMembersAreScopedToTheTeam covers the isolation the pool depends on:
// every query is team-scoped, so a member id from another team is invisible
// rather than editable.
func TestRosterMembersAreScopedToTheTeam(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	stranger := api.registerUser("stranger")
	team := api.createTeam(owner, "Sunday Core")
	otherTeam := api.createTeam(stranger, "Someone Else")

	theirs := api.createRosterMember(stranger, otherTeam.ID, map[string]any{"display_name": "Theirs"})
	api.createRosterMember(owner, team.ID, map[string]any{"display_name": "Ours"})

	if got := api.listRosterMembers(owner, team.ID); len(got) != 1 || got[0].DisplayName != "Ours" {
		t.Errorf("list = %+v, want only our own member", got)
	}

	// Addressing their member through our team is a 404 on update, and a no-op
	// on delete (the DELETE is team-scoped, so it simply matches no row).
	foreign := fmt.Sprintf("/api/teams/%d/roster-members/%d", team.ID, theirs.ID)
	api.do(http.MethodPut, foreign, owner.token, map[string]any{"display_name": "Hijacked"}).
		expect(http.StatusNotFound)
	api.do(http.MethodDelete, foreign, owner.token, nil).expect(http.StatusNoContent)

	if got := api.listRosterMembers(stranger, otherTeam.ID); len(got) != 1 || got[0].DisplayName != "Theirs" {
		t.Errorf("their pool = %+v, want it untouched", got)
	}
}

// TestRosterMemberPermissions covers the read/write split: the pool is readable
// by any team member but only editors and owners may change it.
func TestRosterMemberPermissions(t *testing.T) {
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

	base := fmt.Sprintf("/api/teams/%d/roster-members", team.ID)

	// The editor may add, edit, and remove.
	created := api.createRosterMember(editor, team.ID, map[string]any{"display_name": "Ayla"})
	item := fmt.Sprintf("%s/%d", base, created.ID)
	api.do(http.MethodPut, item, editor.token, map[string]any{"display_name": "Edited"}).
		expect(http.StatusOK)

	// The viewer may read but not write.
	api.do(http.MethodGet, base, viewer.token, nil).expect(http.StatusOK)
	api.do(http.MethodPost, base, viewer.token, map[string]any{"display_name": "Nope"}).
		expect(http.StatusForbidden)
	api.do(http.MethodPut, item, viewer.token, map[string]any{"display_name": "Nope"}).
		expect(http.StatusForbidden)
	api.do(http.MethodDelete, item, viewer.token, nil).expect(http.StatusForbidden)

	// Someone with no access to the team gets a 404, not a 403 — the team's
	// existence is not disclosed.
	api.do(http.MethodGet, base, outsider.token, nil).expect(http.StatusNotFound)
	api.do(http.MethodPost, base, outsider.token, map[string]any{"display_name": "Nope"}).
		expect(http.StatusNotFound)

	if got := api.listRosterMembers(owner, team.ID); len(got) != 1 || got[0].DisplayName != "Edited" {
		t.Errorf("pool = %+v, want only the editor's changes to have landed", got)
	}

	api.do(http.MethodDelete, item, editor.token, nil).expect(http.StatusNoContent)
}

// TestRosterMemberEndpointsRejectAnonymousCallers walks the pool routes without
// a token; all four sit behind the bearer middleware.
func TestRosterMemberEndpointsRejectAnonymousCallers(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	created := api.createRosterMember(owner, team.ID, map[string]any{"display_name": "Ayla"})

	base := fmt.Sprintf("/api/teams/%d/roster-members", team.ID)
	item := fmt.Sprintf("%s/%d", base, created.ID)

	routes := []struct{ method, path string }{
		{http.MethodGet, base},
		{http.MethodPost, base},
		{http.MethodPut, item},
		{http.MethodDelete, item},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			api.do(route.method, route.path, "", nil).expect(http.StatusUnauthorized)
		})
	}
}
