package main

import (
	"strings"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

// TestRemoveSignupOptions covers the "Remove a signup" picker: it lists slot
// claimants first, then tentative ("maybe") entries, and prefixes each value so
// handlePremadeEditRemoveSignup knows which list a pick came from.
func TestRemoveSignupOptions(t *testing.T) {
	team := &models.Team{Players: []models.Player{
		{Slot: 1, Role: "tank"},
		{Slot: 2, Role: "healer"},
	}}
	signups := []models.PremadeSignup{
		{Slot: 1, DiscordUserID: "111", DiscordUsername: "Ayla"},
		{Slot: 2, DiscordUserID: "n:Guest", DiscordUsername: ""},
	}
	tentative := []models.PremadeTentativeEntry{
		{DiscordUserID: "333", DiscordUsername: "Cyra", Role: "dps"},
		{DiscordUserID: "444", DiscordUsername: "Dax"},
	}

	opts := removeSignupOptions(team, signups, tentative)
	if len(opts) != 4 {
		t.Fatalf("got %d options, want 4", len(opts))
	}

	wantValues := []string{"slot:111", "slot:n:Guest", "tent:333", "tent:444"}
	for idx, want := range wantValues {
		if opts[idx].Value != want {
			t.Errorf("option %d value = %q, want %q", idx, opts[idx].Value, want)
		}
	}

	// A blank username falls back to the raw id so free-typed "n:<name>" signups
	// are still identifiable in the picker.
	if !strings.Contains(opts[1].Label, "n:Guest") {
		t.Errorf("option 1 label = %q, want it to name the raw id", opts[1].Label)
	}
	if !strings.Contains(opts[2].Label, "Maybe · DPS") {
		t.Errorf("option 2 label = %q, want the role-tagged maybe label", opts[2].Label)
	}
	// The buttons-style Maybe records no role, so none should be shown.
	if opts[3].Label != "Dax · Maybe" {
		t.Errorf("option 3 label = %q, want %q", opts[3].Label, "Dax · Maybe")
	}
}

// TestRemoveSignupOptionsCapsAtDiscordLimit guards the 25-option ceiling
// Discord enforces on a select menu, which slots plus maybes can now exceed.
func TestRemoveSignupOptionsCapsAtDiscordLimit(t *testing.T) {
	team := &models.Team{}
	signups := make([]models.PremadeSignup, 0, 20)
	for n := 1; n <= 20; n++ {
		signups = append(signups, models.PremadeSignup{Slot: n, DiscordUserID: string(rune('a' + n))})
	}
	tentative := make([]models.PremadeTentativeEntry, 0, 10)
	for n := 1; n <= 10; n++ {
		tentative = append(tentative, models.PremadeTentativeEntry{DiscordUserID: string(rune('A' + n))})
	}

	if got := len(removeSignupOptions(team, signups, tentative)); got != 25 {
		t.Fatalf("got %d options, want them capped at 25", got)
	}
}
