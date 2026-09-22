package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

// listEncounters reads a team's encounters through the API.
func (a *testAPI) listEncounters(s *session, teamID int64) []models.Encounter {
	a.t.Helper()

	var payload struct {
		Encounters []models.Encounter `json:"encounters"`
	}
	a.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/encounters", teamID), s.token, nil).
		expect(http.StatusOK).
		decode(&payload)
	return payload.Encounters
}

// TestCreateTeamSeedsADefaultEncounter pins the invariant the rest of the
// encounter endpoints rely on: a new team is never encounter-less.
func TestCreateTeamSeedsADefaultEncounter(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")

	encounters := api.listEncounters(owner, team.ID)

	if len(encounters) != 1 {
		t.Fatalf("got %d encounters, want the seeded Default", len(encounters))
	}
	if encounters[0].Name != "Default" {
		t.Errorf("name = %q, want Default", encounters[0].Name)
	}
	// The list endpoint is deliberately shallow — loadouts come from the
	// single-encounter read, which is where every roster slot must already
	// have an empty loadout for the UI to write into.
	if len(encounters[0].Loadouts) != 0 {
		t.Errorf("got %d loadouts from the list endpoint, want none", len(encounters[0].Loadouts))
	}

	var full models.Encounter
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/encounters/%d", team.ID, encounters[0].ID), owner.token, nil).
		expect(http.StatusOK).
		decode(&full)
	if len(full.Loadouts) != models.TeamSize {
		t.Errorf("got %d loadouts, want one per slot (%d)", len(full.Loadouts), models.TeamSize)
	}
}

func TestCreateEncounter(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")

	var created models.Encounter
	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/encounters", team.ID), owner.token,
		map[string]any{"name": "Lokkestiiz"}).
		expect(http.StatusCreated).
		decode(&created)

	if created.Name != "Lokkestiiz" {
		t.Errorf("name = %q", created.Name)
	}
	if len(created.Loadouts) != models.TeamSize {
		t.Errorf("got %d loadouts, want one per slot", len(created.Loadouts))
	}
	if got := api.listEncounters(owner, team.ID); len(got) != 2 {
		t.Errorf("got %d encounters, want the Default plus the new one", len(got))
	}
}

// TestCreateEncounterValidation covers the three refusals the name rules
// enforce. The trial-mismatch one is the interesting case: a roster is for a
// single trial, so mixing bosses from two of them is rejected.
func TestCreateEncounterValidation(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")

	create := func(name string) *response {
		return api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/encounters", team.ID), owner.token,
			map[string]any{"name": name})
	}

	t.Run("an unknown name is rejected", func(t *testing.T) {
		msg := create("Not A Real Boss").expect(http.StatusBadRequest).errorMessage()
		if msg == "" {
			t.Error("want an explanatory error")
		}
	})

	t.Run("a duplicate name is rejected", func(t *testing.T) {
		create("Lokkestiiz").expect(http.StatusCreated)
		create("Lokkestiiz").expect(http.StatusBadRequest)
	})

	t.Run("a boss from another trial is rejected", func(t *testing.T) {
		// Lokkestiiz is Sunspire; Rakkhat is Maw of Lorkhaj.
		create("Rakkhat").expect(http.StatusBadRequest)
	})

	t.Run("a General-group name is always allowed", func(t *testing.T) {
		// "Trash" belongs to the General group, so it crosses no trial.
		create("Trash").expect(http.StatusCreated)
	})
}

func TestRenameEncounter(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")
	enc := api.listEncounters(owner, team.ID)[0]

	var updated models.Encounter
	api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/encounters/%d", team.ID, enc.ID), owner.token,
		map[string]any{"name": "Nahviintaas"}).
		expect(http.StatusOK).
		decode(&updated)

	if updated.Name != "Nahviintaas" {
		t.Errorf("name = %q", updated.Name)
	}

	// Renaming to its own current name must not trip the duplicate check.
	api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/encounters/%d", team.ID, enc.ID), owner.token,
		map[string]any{"name": "Nahviintaas"}).
		expect(http.StatusOK)
}

// TestDeleteEncounterKeepsTheLastOne protects the same invariant the seeding
// test establishes: a roster must always have at least one encounter.
func TestDeleteEncounterKeepsTheLastOne(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")
	first := api.listEncounters(owner, team.ID)[0]

	api.do(http.MethodDelete, fmt.Sprintf("/api/teams/%d/encounters/%d", team.ID, first.ID), owner.token, nil).
		expect(http.StatusBadRequest)

	// With a second one present the first becomes deletable.
	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/encounters", team.ID), owner.token,
		map[string]any{"name": "Lokkestiiz"}).expect(http.StatusCreated)
	api.do(http.MethodDelete, fmt.Sprintf("/api/teams/%d/encounters/%d", team.ID, first.ID), owner.token, nil).
		expect(http.StatusNoContent)

	if got := api.listEncounters(owner, team.ID); len(got) != 1 {
		t.Errorf("got %d encounters, want 1 remaining", len(got))
	}
}

// TestSaveLoadoutsRoundTrip drives the widest SQL in the encounter store: a
// full-roster loadout write and read-back, including the numeric inputs the
// crit/pen calculators depend on.
func TestSaveLoadoutsRoundTrip(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")
	enc := api.listEncounters(owner, team.ID)[0]

	api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/encounters/%d/loadouts", team.ID, enc.ID), owner.token,
		map[string]any{"loadouts": []map[string]any{
			{
				"slot":              1,
				"gear":              []string{"roar_of_alkosh", "pillar_of_nirn"},
				"skills":            []string{"pierce_armor"},
				"potions":           []string{"weapon_crit"},
				"cp_blue":           []string{"force_of_nature"},
				"crit_dmg":          []string{"minor_force"},
				"mundus":            "thief",
				"armor_heavy":       5,
				"armor_medium":      1,
				"armor_light":       1,
				"pen_extra":         []string{"crusher", "crusher"},
				"catalyst_elements": 2,
				"weapon_damage":     4200,
				"scribed_buffs":     []string{"major_breach"},
			},
			{"slot": 2, "gear": []string{"lucent_echoes"}},
		}}).
		expect(http.StatusOK)

	var got models.Encounter
	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/encounters/%d", team.ID, enc.ID), owner.token, nil).
		expect(http.StatusOK).
		decode(&got)

	bySlot := map[int]models.Loadout{}
	for _, lo := range got.Loadouts {
		bySlot[lo.Slot] = lo
	}

	one := bySlot[1]
	if len(one.Gear) != 2 || one.Gear[0] != "roar_of_alkosh" {
		t.Errorf("gear = %v, want the saved order preserved", one.Gear)
	}
	if one.Mundus != "thief" {
		t.Errorf("mundus = %q", one.Mundus)
	}
	if one.ArmorHeavy != 5 || one.ArmorMedium != 1 || one.ArmorLight != 1 {
		t.Errorf("armor = %d/%d/%d, want 5/1/1", one.ArmorHeavy, one.ArmorMedium, one.ArmorLight)
	}
	// Repeated pen_extra keys are meaningful (stacked sources), so both survive.
	if len(one.PenExtra) != 2 {
		t.Errorf("pen_extra = %v, want both stacks kept", one.PenExtra)
	}
	if one.CatalystElements != 2 || one.WeaponDamage != 4200 {
		t.Errorf("catalyst/wd = %d/%d, want 2/4200", one.CatalystElements, one.WeaponDamage)
	}
	if len(bySlot[2].Gear) != 1 {
		t.Errorf("slot 2 gear = %v", bySlot[2].Gear)
	}
	// Untouched slots stay present and empty rather than disappearing.
	if len(got.Loadouts) != models.TeamSize {
		t.Errorf("got %d loadouts, want all %d slots", len(got.Loadouts), models.TeamSize)
	}
}

// TestSaveLoadoutSlotIsConcurrencyChecked covers the optimistic-concurrency
// token: two editors on the same slot must not silently overwrite each other.
func TestSaveLoadoutSlotIsConcurrencyChecked(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	team := api.createTeam(owner, "Prog Team")
	enc := api.listEncounters(owner, team.ID)[0]

	path := fmt.Sprintf("/api/teams/%d/encounters/%d/loadouts/1", team.ID, enc.ID)

	var first models.Loadout
	api.do(http.MethodPut, path, owner.token, map[string]any{"gear": []string{"roar_of_alkosh"}}).
		expect(http.StatusOK).
		decode(&first)

	// A second save carrying the now-stale timestamp is refused.
	api.do(http.MethodPut, path, owner.token, map[string]any{
		"gear":                []string{"lucent_echoes"},
		"expected_updated_at": "2020-01-01T00:00:00Z",
	}).expect(http.StatusConflict)

	// The same save with the current token succeeds.
	api.do(http.MethodPut, path, owner.token, map[string]any{
		"gear":                []string{"lucent_echoes"},
		"expected_updated_at": first.UpdatedAt,
	}).expect(http.StatusOK)

	// Omitting the token entirely is the "no concurrency check" path the
	// simpler clients use, and must still save.
	api.do(http.MethodPut, path, owner.token, map[string]any{"gear": []string{"pillar_of_nirn"}}).
		expect(http.StatusOK)
}

// TestEncounterPermissions checks the read/write split: a viewer may read the
// roster but must not be able to change it.
func TestEncounterPermissions(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	viewer := api.registerUser("viewer")
	stranger := api.registerUser("stranger")
	team := api.createTeam(owner, "Prog Team")
	enc := api.listEncounters(owner, team.ID)[0]

	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/share", team.ID), owner.token,
		map[string]any{"username": viewer.user.Username, "role": "viewer"}).
		expect(http.StatusOK)

	t.Run("a viewer can read", func(t *testing.T) {
		api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/encounters", team.ID), viewer.token, nil).
			expect(http.StatusOK)
	})

	t.Run("a viewer cannot create", func(t *testing.T) {
		api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/encounters", team.ID), viewer.token,
			map[string]any{"name": "Lokkestiiz"}).
			expect(http.StatusForbidden)
	})

	t.Run("a viewer cannot save loadouts", func(t *testing.T) {
		api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/encounters/%d/loadouts", team.ID, enc.ID), viewer.token,
			map[string]any{"loadouts": []map[string]any{{"slot": 1}}}).
			expect(http.StatusForbidden)
	})

	t.Run("a viewer cannot delete", func(t *testing.T) {
		api.do(http.MethodDelete, fmt.Sprintf("/api/teams/%d/encounters/%d", team.ID, enc.ID), viewer.token, nil).
			expect(http.StatusForbidden)
	})

	// A non-member gets 404 rather than 403, so the API doesn't confirm the
	// team exists to someone with no access to it.
	t.Run("a stranger sees a 404", func(t *testing.T) {
		api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/encounters", team.ID), stranger.token, nil).
			expect(http.StatusNotFound)
	})
}

// TestEncounterFromAnotherTeamIs404 guards against reaching a valid encounter
// ID through a team the caller does happen to own.
func TestEncounterFromAnotherTeamIs404(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("owner")
	mine := api.createTeam(owner, "Mine")
	theirs := api.createTeam(owner, "Theirs")
	theirEnc := api.listEncounters(owner, theirs.ID)[0]

	api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/encounters/%d", mine.ID, theirEnc.ID), owner.token, nil).
		expect(http.StatusNotFound)
}
