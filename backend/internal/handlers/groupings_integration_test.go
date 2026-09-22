package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

func (a *testAPI) listGroupings(s *session, teamID int64) []models.Grouping {
	a.t.Helper()

	var payload struct {
		Groupings []models.Grouping `json:"groupings"`
	}
	a.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/groupings", teamID), s.token, nil).
		expect(http.StatusOK).
		decode(&payload)
	return payload.Groupings
}

func (a *testAPI) createGrouping(s *session, teamID int64, name string, count int) models.Grouping {
	a.t.Helper()

	var g models.Grouping
	a.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/groupings", teamID), s.token,
		map[string]any{"name": name, "group_count": count}).
		expect(http.StatusCreated).
		decode(&g)
	return g
}

func TestCreateGrouping(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")

	g := api.createGrouping(owner, team.ID, "Ice cages", 3)

	if g.Name != "Ice cages" {
		t.Errorf("name = %q", g.Name)
	}
	if g.GroupCount != 3 {
		t.Errorf("group count = %d, want 3", g.GroupCount)
	}
	// A grouping always reports its full set of (initially empty) groups so the
	// client has something to render.
	if len(g.Groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(g.Groups))
	}
	for idx, grp := range g.Groups {
		if grp.GroupNumber != idx+1 {
			t.Errorf("group %d numbered %d", idx, grp.GroupNumber)
		}
		if len(grp.Slots) != 0 {
			t.Errorf("group %d starts with slots %v, want none", idx, grp.Slots)
		}
	}
	if got := api.listGroupings(owner, team.ID); len(got) != 1 {
		t.Errorf("got %d groupings, want 1", len(got))
	}
}

// TestCreateGroupingNormalizesInput covers the two forgiving defaults, which
// keep a sloppy client from producing an unusable grouping.
func TestCreateGroupingNormalizesInput(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")

	t.Run("a blank name becomes Grouping", func(t *testing.T) {
		g := api.createGrouping(owner, team.ID, "   ", 2)
		if g.Name != "Grouping" {
			t.Errorf("name = %q, want the default", g.Name)
		}
	})

	t.Run("the group count is clamped to the valid range", func(t *testing.T) {
		low := api.createGrouping(owner, team.ID, "Low", 0)
		if low.GroupCount != 1 {
			t.Errorf("group count = %d, want it raised to 1", low.GroupCount)
		}
		high := api.createGrouping(owner, team.ID, "High", 999)
		if high.GroupCount != models.MaxGroupsPerGrouping {
			t.Errorf("group count = %d, want it capped at %d", high.GroupCount, models.MaxGroupsPerGrouping)
		}
	})
}

// TestUpdateGroupingAssignsSlots is the core editing path: naming groups and
// putting roster slots in them.
func TestUpdateGroupingAssignsSlots(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")
	g := api.createGrouping(owner, team.ID, "Ice cages", 2)

	var updated models.Grouping
	api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/groupings/%d", team.ID, g.ID), owner.token,
		map[string]any{
			"name":        "Cages",
			"group_count": 2,
			"groups": []map[string]any{
				{"group_number": 1, "name": "Left", "slots": []int{1, 3}},
				{"group_number": 2, "name": "Right", "slots": []int{2}},
			},
		}).
		expect(http.StatusOK).
		decode(&updated)

	if updated.Name != "Cages" {
		t.Errorf("name = %q", updated.Name)
	}
	bySlot := map[int]string{}
	for _, grp := range updated.Groups {
		for _, slot := range grp.Slots {
			bySlot[slot] = grp.Name
		}
	}
	if bySlot[1] != "Left" || bySlot[3] != "Left" || bySlot[2] != "Right" {
		t.Errorf("slot assignment = %v", bySlot)
	}
}

// TestUpdateGroupingRejectsADuplicateSlot enforces the one rule that makes a
// grouping meaningful: a player is in at most one group per grouping.
func TestUpdateGroupingRejectsADuplicateSlot(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")
	g := api.createGrouping(owner, team.ID, "Ice cages", 2)

	msg := api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/groupings/%d", team.ID, g.ID), owner.token,
		map[string]any{
			"group_count": 2,
			"groups": []map[string]any{
				{"group_number": 1, "slots": []int{1}},
				{"group_number": 2, "slots": []int{1}},
			},
		}).
		expect(http.StatusBadRequest).
		errorMessage()

	if msg == "" {
		t.Error("want an explanatory error")
	}
}

func TestUpdateGroupingRejectsAnOutOfRangeSlot(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")
	g := api.createGrouping(owner, team.ID, "Ice cages", 1)

	for _, slot := range []int{0, -1, models.TeamSize + 1} {
		api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/groupings/%d", team.ID, g.ID), owner.token,
			map[string]any{
				"group_count": 1,
				"groups":      []map[string]any{{"group_number": 1, "slots": []int{slot}}},
			}).
			expect(http.StatusBadRequest)
	}
}

// TestUpdateGroupingDropsStaleGroups covers shrinking a grouping: entries above
// the new count are discarded rather than rejected, so the client can send the
// whole form back without pruning it first.
func TestUpdateGroupingDropsStaleGroups(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")
	g := api.createGrouping(owner, team.ID, "Ice cages", 3)

	var updated models.Grouping
	api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/groupings/%d", team.ID, g.ID), owner.token,
		map[string]any{
			"group_count": 1,
			"groups": []map[string]any{
				{"group_number": 1, "slots": []int{1}},
				{"group_number": 3, "slots": []int{2}}, // above the new count
			},
		}).
		expect(http.StatusOK).
		decode(&updated)

	if updated.GroupCount != 1 {
		t.Errorf("group count = %d, want 1", updated.GroupCount)
	}
	if len(updated.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(updated.Groups))
	}
	if len(updated.Groups[0].Slots) != 1 || updated.Groups[0].Slots[0] != 1 {
		t.Errorf("slots = %v, want only slot 1 to survive", updated.Groups[0].Slots)
	}
}

func TestDeleteGrouping(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")
	g := api.createGrouping(owner, team.ID, "Ice cages", 2)

	api.do(http.MethodDelete, fmt.Sprintf("/api/teams/%d/groupings/%d", team.ID, g.ID), owner.token, nil).
		expect(http.StatusNoContent)

	if got := api.listGroupings(owner, team.ID); len(got) != 0 {
		t.Errorf("got %d groupings, want none left", len(got))
	}
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/groupings/%d", team.ID, g.ID), owner.token, nil).
		expect(http.StatusNotFound)
}

func TestGroupingPermissions(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	viewer := api.registerUser("viewer")
	team := api.createTeam(owner, "Prog Team")
	g := api.createGrouping(owner, team.ID, "Ice cages", 2)

	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/share", team.ID), owner.token,
		map[string]any{"username": viewer.user.Username, "role": "viewer"}).
		expect(http.StatusOK)

	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/groupings", team.ID), viewer.token, nil).
		expect(http.StatusOK)
	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/groupings", team.ID), viewer.token,
		map[string]any{"name": "Nope", "group_count": 1}).
		expect(http.StatusForbidden)
	api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/groupings/%d", team.ID, g.ID), viewer.token,
		map[string]any{"group_count": 1}).
		expect(http.StatusForbidden)
	api.do(http.MethodDelete, fmt.Sprintf("/api/teams/%d/groupings/%d", team.ID, g.ID), viewer.token, nil).
		expect(http.StatusForbidden)
}
