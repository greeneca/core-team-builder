package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/models"
)

// signupTeam is a four-slot pre-made roster: two DPS so a role can be
// partially claimed, and one slot each of tank and healer.
func signupTeam() *models.Team {
	return &models.Team{
		Name:    "Prog Team",
		PreMade: true,
		Players: []models.Player{
			{Slot: 1, Role: "tank", Class: "dragonknight"},
			{Slot: 2, Role: "healer", Class: "arcanist"},
			{Slot: 3, Role: "dps", Class: "nightblade"},
			{Slot: 4, Role: "dps"},
		},
	}
}

// optionValues pulls the values out of a select's options, which is what the
// interaction handlers actually dispatch on.
func optionValues(opts []discordgo.SelectMenuOption) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Value)
	}
	return out
}

// selectFrom digs the single select menu out of an action row.
func selectFrom(t *testing.T, c discordgo.MessageComponent) discordgo.SelectMenu {
	t.Helper()
	row, ok := c.(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("component is %T, want an ActionsRow", c)
	}
	if len(row.Components) != 1 {
		t.Fatalf("row has %d components, want 1", len(row.Components))
	}
	sel, ok := row.Components[0].(discordgo.SelectMenu)
	if !ok {
		t.Fatalf("row component is %T, want a SelectMenu", row.Components[0])
	}
	return sel
}

// buttonsFrom flattens the buttons out of action rows, preserving row order.
func buttonsFrom(t *testing.T, rows []discordgo.MessageComponent) []discordgo.Button {
	t.Helper()
	var out []discordgo.Button
	for _, r := range rows {
		row, ok := r.(discordgo.ActionsRow)
		if !ok {
			t.Fatalf("component is %T, want an ActionsRow", r)
		}
		for _, c := range row.Components {
			btn, ok := c.(discordgo.Button)
			if !ok {
				t.Fatalf("row component is %T, want a Button", c)
			}
			out = append(out, btn)
		}
	}
	return out
}

// --- consolidated signup dropdown ---

// TestPremadeSignupOptionsAdvanced checks specific-signup mode offers one
// option per *open slot*, with the "slot:" prefix the handler dispatches on.
func TestPremadeSignupOptionsAdvanced(t *testing.T) {
	team := signupTeam()
	taken := map[int]bool{2: true}

	opts := premadeSignupOptions(team, taken)

	want := []string{"slot:1", "slot:3", "slot:4", "tent:"}
	if got := optionValues(opts); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("values = %v, want %v", got, want)
	}
	if !strings.Contains(opts[0].Label, "Slot 1 · Tank · Dragonknight") {
		t.Errorf("label = %q, want slot/role/class", opts[0].Label)
	}
}

// TestPremadeSignupOptionsSimple checks role mode collapses to one option per
// role regardless of how many slots that role has, carrying the open count.
func TestPremadeSignupOptionsSimple(t *testing.T) {
	team := signupTeam()
	team.SimpleSignup = true

	opts := premadeSignupOptions(team, map[int]bool{3: true})

	want := []string{"role:tank", "role:healer", "role:dps", "tent:"}
	if got := optionValues(opts); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("values = %v, want %v", got, want)
	}
	// Slot 3 is claimed, so DPS has one of its two slots left.
	if !strings.Contains(opts[2].Label, "DPS (1 open)") {
		t.Errorf("DPS label = %q, want it to report 1 open", opts[2].Label)
	}
}

// TestPremadeSignupOptionsWaitlist covers the two ways a full role behaves. The
// distinction matters: with the waitlist off, a full role must disappear from
// the dropdown rather than offer an action that would fail.
func TestPremadeSignupOptionsWaitlist(t *testing.T) {
	full := map[int]bool{1: true, 2: true, 3: true, 4: true}

	t.Run("advanced offers a wait option per full role", func(t *testing.T) {
		team := signupTeam()
		team.WaitlistEnabled = true

		got := optionValues(premadeSignupOptions(team, full))
		want := []string{"wait:tank", "wait:healer", "wait:dps", "tent:"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("values = %v, want %v", got, want)
		}
	})

	t.Run("simple reuses the role value for the waitlist", func(t *testing.T) {
		team := signupTeam()
		team.SimpleSignup = true
		team.WaitlistEnabled = true

		got := optionValues(premadeSignupOptions(team, full))
		want := []string{"role:tank", "role:healer", "role:dps", "tent:"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("values = %v, want %v", got, want)
		}
		if !strings.Contains(premadeSignupOptions(team, full)[0].Label, "full, join waitlist") {
			t.Error("a full role should say it joins the waitlist")
		}
	})

	t.Run("no waitlist leaves only the maybe option", func(t *testing.T) {
		team := signupTeam() // WaitlistEnabled false
		got := optionValues(premadeSignupOptions(team, full))
		if strings.Join(got, ",") != "tent:" {
			t.Errorf("values = %v, want only the tentative option", got)
		}
	})
}

// TestPremadeSignupOptionsEmptyRoster produces no options at all — not even
// "maybe", which would be meaningless with nothing to be tentative about.
func TestPremadeSignupOptionsEmptyRoster(t *testing.T) {
	if got := premadeSignupOptions(&models.Team{}, nil); len(got) != 0 {
		t.Errorf("got %v, want no options for an empty roster", got)
	}
	// Roleless slots don't count as roles either.
	roleless := &models.Team{Players: []models.Player{{Slot: 1}}}
	if got := premadeSignupOptions(roleless, nil); len(got) != 1 || got[0].Value != "slot:1" {
		t.Errorf("got %v, want just the open slot and no maybe", optionValues(got))
	}
}

func TestPremadeSignupRowDisabledWhenClosed(t *testing.T) {
	team := signupTeam()
	full := map[int]bool{1: true, 2: true, 3: true, 4: true}

	open := selectFrom(t, premadeSignupRow(team, map[int]bool{}))
	if open.Disabled {
		t.Error("a roster with open slots should have an enabled select")
	}

	// Every slot claimed and no waitlist: only "maybe" remains, so the select
	// stays enabled.
	stillOpen := selectFrom(t, premadeSignupRow(team, full))
	if stillOpen.Disabled {
		t.Error("the tentative option alone should keep the select usable")
	}

	closed := selectFrom(t, premadeSignupRow(&models.Team{}, nil))
	if !closed.Disabled {
		t.Error("an empty roster should render a disabled placeholder select")
	}
}

// TestPremadeSignupRowCapsAtDiscordLimit guards the 25-option ceiling Discord
// enforces; a 12-slot roster plus waitlist and maybe options can approach it.
func TestPremadeSignupRowCapsAtDiscordLimit(t *testing.T) {
	team := &models.Team{PreMade: true}
	for n := 1; n <= 30; n++ {
		team.Players = append(team.Players, models.Player{Slot: n, Role: "dps"})
	}

	sel := selectFrom(t, premadeSignupRow(team, nil))
	if len(sel.Options) != 25 {
		t.Errorf("got %d options, want them capped at 25", len(sel.Options))
	}
}

// --- simple "buttons" style ---

func TestPremadeRoleButtonRows(t *testing.T) {
	team := signupTeam()
	team.SimpleSignup = true

	btns := buttonsFrom(t, premadeRoleButtonRows(team, map[int]bool{3: true}))

	if len(btns) != 3 {
		t.Fatalf("got %d buttons, want one per role", len(btns))
	}
	wantIDs := []string{"premade_srole_tank", "premade_srole_healer", "premade_srole_dps"}
	for idx, want := range wantIDs {
		if btns[idx].CustomID != want {
			t.Errorf("button %d id = %q, want %q", idx, btns[idx].CustomID, want)
		}
	}
	if !strings.Contains(btns[2].Label, "DPS (1 open)") {
		t.Errorf("DPS label = %q", btns[2].Label)
	}
	// Colors are keyed to the role base so they read at a glance.
	if btns[0].Style != discordgo.PrimaryButton {
		t.Errorf("tank style = %v, want blurple", btns[0].Style)
	}
	if btns[1].Style != discordgo.SuccessButton {
		t.Errorf("healer style = %v, want green", btns[1].Style)
	}
	if btns[2].Style != discordgo.DangerButton {
		t.Errorf("dps style = %v, want red", btns[2].Style)
	}
}

// TestPremadeRoleButtonsWhenFull distinguishes a full role that can still be
// waitlisted from one that cannot: the latter must be visibly disabled rather
// than silently missing, so the post still shows the whole roster shape.
func TestPremadeRoleButtonsWhenFull(t *testing.T) {
	full := map[int]bool{1: true, 2: true, 3: true, 4: true}

	withWaitlist := signupTeam()
	withWaitlist.SimpleSignup = true
	withWaitlist.WaitlistEnabled = true
	for _, b := range buttonsFrom(t, premadeRoleButtonRows(withWaitlist, full)) {
		if b.Disabled {
			t.Errorf("button %q should stay pressable to join the waitlist", b.Label)
		}
		if !strings.Contains(b.Label, "waitlist") {
			t.Errorf("label = %q, want it to offer the waitlist", b.Label)
		}
	}

	noWaitlist := signupTeam()
	noWaitlist.SimpleSignup = true
	for _, b := range buttonsFrom(t, premadeRoleButtonRows(noWaitlist, full)) {
		if !b.Disabled {
			t.Errorf("button %q should be disabled when full with no waitlist", b.Label)
		}
		if !strings.Contains(b.Label, "full") {
			t.Errorf("label = %q, want it to read as full", b.Label)
		}
	}
}

func TestPremadeRoleButtonRowsEmptyRoster(t *testing.T) {
	rows := premadeRoleButtonRows(&models.Team{}, nil)

	btns := buttonsFrom(t, rows)
	if len(btns) != 1 {
		t.Fatalf("got %d buttons, want a single closed placeholder", len(btns))
	}
	if !btns[0].Disabled || btns[0].Label != "Signups are closed" {
		t.Errorf("got %+v, want a disabled closed button", btns[0])
	}
}

// TestPremadeRoleButtonRowsRespectsDiscordLayout keeps within 5 buttons per row
// and reserves the fifth row for the management actions.
func TestPremadeRoleButtonRowsRespectsDiscordLayout(t *testing.T) {
	team := &models.Team{SimpleSignup: true}
	for n := 1; n <= 30; n++ {
		team.Players = append(team.Players, models.Player{Slot: n, Role: fmt.Sprintf("role%02d", n)})
	}

	rows := premadeRoleButtonRows(team, nil)

	if len(rows) > 4 {
		t.Errorf("got %d rows, want at most 4 so the action row still fits", len(rows))
	}
	for idx, r := range rows {
		row := r.(discordgo.ActionsRow)
		if len(row.Components) > 5 {
			t.Errorf("row %d has %d buttons, want at most 5", idx, len(row.Components))
		}
	}
}

// TestPremadeComponentsPicksTheStyle is the dispatch the post's whole
// appearance hangs off, so each mode is pinned by a marker only it produces.
func TestPremadeComponentsPicksTheStyle(t *testing.T) {
	cases := []struct {
		name         string
		simple       bool
		style        string
		wantCustomID string
	}{
		{"advanced always uses the dropdown", false, "", premadeSignupID},
		{"simple defaults to the dropdown", true, "", premadeSignupID},
		{"simple dropdown explicitly", true, models.SimpleSignupStyleDropdown, premadeSignupID},
		{"simple buttons", true, models.SimpleSignupStyleButtons, premadeSignupRoleBtnPrefix + "tank"},
		{"simple ephemeral", true, models.SimpleSignupStyleEphemeral, premadeSignupOpenID},
		{"an unknown style falls back to the dropdown", true, "bogus", premadeSignupID},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			team := signupTeam()
			team.SimpleSignup = tc.simple
			team.SimpleSignupStyle = tc.style

			rows := premadeComponents(team, nil)

			if !containsCustomID(rows, tc.wantCustomID) {
				t.Errorf("components missing %q:\n%s", tc.wantCustomID, describeComponents(rows))
			}
		})
	}
}

// TestPremadeComponentsDetailsRow checks the build-details select is offered
// only in advanced mode, since simple signup deliberately hides per-slot detail.
func TestPremadeComponentsDetailsRow(t *testing.T) {
	advanced := signupTeam()
	if !containsCustomID(premadeComponents(advanced, nil), premadeDetailsID) {
		t.Error("advanced signup should offer the build-details select")
	}

	simple := signupTeam()
	simple.SimpleSignup = true
	if containsCustomID(premadeComponents(simple, nil), premadeDetailsID) {
		t.Error("simple signup should hide the build-details select")
	}
}

// containsCustomID reports whether any component (or nested component) carries
// the given custom ID.
func containsCustomID(rows []discordgo.MessageComponent, id string) bool {
	for _, r := range rows {
		row, ok := r.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, c := range row.Components {
			switch v := c.(type) {
			case discordgo.Button:
				if v.CustomID == id {
					return true
				}
			case discordgo.SelectMenu:
				if v.CustomID == id {
					return true
				}
			}
		}
	}
	return false
}

func describeComponents(rows []discordgo.MessageComponent) string {
	var b strings.Builder
	for _, r := range rows {
		row, ok := r.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, c := range row.Components {
			switch v := c.(type) {
			case discordgo.Button:
				fmt.Fprintf(&b, "button %q (%s)\n", v.Label, v.CustomID)
			case discordgo.SelectMenu:
				fmt.Fprintf(&b, "select %q\n", v.CustomID)
			}
		}
	}
	return b.String()
}

// --- trial-overview fill dropdown ---

// fillTeam mixes assigned and open slots, which is what decides who appears in
// the fill dropdown.
func fillTeam() *models.Team {
	return &models.Team{
		Players: []models.Player{
			{Slot: 1, Name: "Alice", DiscordHandle: "alice", Role: "tank"},
			{Slot: 2, Name: "Bob", DiscordHandle: "bob", Role: "healer"},
			{Slot: 3, Name: "Cara", Role: "dps"}, // no handle: an open slot
			{Slot: 4, Role: "dps"},
		},
	}
}

// TestPostFillSelectRow checks only fillable slots are listed: open ones, plus
// assigned players who declined. A slot with a present player must not be
// offered, or a filler could take a seat that is not free.
func TestPostFillSelectRow(t *testing.T) {
	team := fillTeam()
	marks := map[int]string{2: models.RSVPNo}

	row, ok := postFillSelectRow(team, nil, marks, false)
	if !ok {
		t.Fatal("got ok = false, want a fill row")
	}
	sel := selectFrom(t, row)

	want := []string{"2", "3", "4", postFillListValue, postFillLeaveValue}
	if got := optionValues(sel.Options); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("values = %v, want %v", got, want)
	}
	// The declined slot names who is being covered for.
	if !strings.Contains(sel.Options[0].Label, "Fill for Bob") {
		t.Errorf("label = %q, want it to name the absent player", sel.Options[0].Label)
	}
	if sel.Disabled {
		t.Error("an unlocked post should have an enabled select")
	}
}

func TestPostFillSelectRowSkipsClaimedSlots(t *testing.T) {
	team := fillTeam()
	fills := []models.PostFill{{Slot: 3, DiscordUserID: "111"}}

	row, _ := postFillSelectRow(team, fills, nil, false)
	sel := selectFrom(t, row)

	for _, v := range optionValues(sel.Options) {
		if v == "3" {
			t.Errorf("slot 3 is already filled and should not be offered: %v", optionValues(sel.Options))
		}
	}
	// A fill-list entry (slot 0) must not remove any roster slot.
	listOnly := []models.PostFill{{Slot: models.PostFillList, DiscordUserID: "222"}}
	row, _ = postFillSelectRow(team, listOnly, nil, false)
	if got := optionValues(selectFrom(t, row).Options); len(got) != 4 {
		t.Errorf("values = %v, want both open slots still offered", got)
	}
}

// TestPostFillSelectRowLocked disables signups once the run has started rather
// than removing the control, so the post still explains itself.
func TestPostFillSelectRowLocked(t *testing.T) {
	row, _ := postFillSelectRow(fillTeam(), nil, nil, true)
	sel := selectFrom(t, row)

	if !sel.Disabled {
		t.Error("a locked post should disable the select")
	}
	if !strings.Contains(sel.Placeholder, "closed") {
		t.Errorf("placeholder = %q, want it to explain signups are closed", sel.Placeholder)
	}
}

// TestPostFillSelectRowCapsOptions leaves room for the two trailing options
// within Discord's 25-option select limit.
func TestPostFillSelectRowCapsOptions(t *testing.T) {
	team := &models.Team{}
	for n := 1; n <= 40; n++ {
		team.Players = append(team.Players, models.Player{Slot: n, Role: "dps"})
	}

	row, _ := postFillSelectRow(team, nil, nil, false)
	sel := selectFrom(t, row)

	if len(sel.Options) != 25 {
		t.Errorf("got %d options, want 23 slots plus the 2 trailing actions", len(sel.Options))
	}
	last := optionValues(sel.Options)[23:]
	if last[0] != postFillListValue || last[1] != postFillLeaveValue {
		t.Errorf("trailing options = %v, want the list/leave actions preserved", last)
	}
}

func TestPostFillSelectRowNilTeam(t *testing.T) {
	if _, ok := postFillSelectRow(nil, nil, nil, false); ok {
		t.Error("got ok = true, want no row without a team")
	}
}

func TestIsFillableSlot(t *testing.T) {
	team := fillTeam()

	cases := []struct {
		name  string
		slot  int
		marks map[int]string
		want  bool
	}{
		{"assigned and present", 1, nil, false},
		{"assigned but declined", 1, map[int]string{1: models.RSVPNo}, true},
		{"assigned and confirmed", 1, map[int]string{1: models.RSVPYes}, false},
		{"open slot with a name", 3, nil, true},
		{"open slot with no name", 4, nil, true},
		{"slot not on the roster", 99, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFillableSlot(team, tc.marks, tc.slot); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFillForName(t *testing.T) {
	if got := fillForName(models.Player{Slot: 2, Name: "Bob"}); got != "Bob" {
		t.Errorf("got %q, want the roster name", got)
	}
	if got := fillForName(models.Player{Slot: 2, Name: "  "}); got != "Slot 2" {
		t.Errorf("got %q, want the slot fallback", got)
	}
}

// --- matching Discord users to roster slots ---

// TestMatchPlayer covers every handle form the roster accepts, because an RSVP
// that fails to match silently shows the wrong status on the post.
func TestMatchPlayer(t *testing.T) {
	team := &models.Team{Players: []models.Player{
		{Slot: 1, DiscordHandle: "<@111>"},
		{Slot: 2, DiscordHandle: "<@!222>"},
		{Slot: 3, DiscordHandle: "333"},
		{Slot: 4, DiscordHandle: "@444"},
		{Slot: 5, DiscordHandle: "@Alice"},
		{Slot: 6, DiscordHandle: "bob"},
		{Slot: 7, DiscordHandle: ""},
	}}

	cases := []struct {
		name     string
		user     *discordgo.User
		wantSlot int
	}{
		{"mention", &discordgo.User{ID: "111"}, 1},
		{"nickname mention", &discordgo.User{ID: "222"}, 2},
		{"raw id", &discordgo.User{ID: "333"}, 3},
		{"at-prefixed id", &discordgo.User{ID: "444"}, 4},
		{"username case insensitive", &discordgo.User{ID: "999", Username: "alice"}, 5},
		{"username without the at", &discordgo.User{ID: "999", Username: "BOB"}, 6},
		{"global name", &discordgo.User{ID: "999", GlobalName: "alice"}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := matchPlayer(team, tc.user)
			if !ok {
				t.Fatalf("no match for %+v", tc.user)
			}
			if p.Slot != tc.wantSlot {
				t.Errorf("matched slot %d, want %d", p.Slot, tc.wantSlot)
			}
		})
	}

	if _, ok := matchPlayer(team, &discordgo.User{ID: "000", Username: "nobody"}); ok {
		t.Error("an unknown user should not match")
	}
	// An empty global name must not match the blank handle on slot 7.
	if p, ok := matchPlayer(team, &discordgo.User{ID: "000", Username: "zzz", GlobalName: ""}); ok {
		t.Errorf("matched slot %d on an empty handle, want no match", p.Slot)
	}
}

func TestRSVPMarks(t *testing.T) {
	team := &models.Team{Players: []models.Player{
		{Slot: 1, DiscordHandle: "alice"},
		{Slot: 2, DiscordHandle: "bob"},
	}}
	rsvps := []models.RSVP{
		{DiscordUserID: "1", DiscordUsername: "alice", Status: models.RSVPYes},
		{DiscordUserID: "2", DiscordUsername: "bob", Status: models.RSVPNo},
		{DiscordUserID: "3", DiscordUsername: "carol", Status: models.RSVPYes}, // not on the roster
		{DiscordUserID: "4", DiscordUsername: "alice", Status: "maybe"},        // unknown status
	}

	marks := rsvpMarks(team, rsvps)

	if marks[1] != models.RSVPYes {
		t.Errorf("slot 1 = %q, want yes", marks[1])
	}
	if marks[2] != models.RSVPNo {
		t.Errorf("slot 2 = %q, want no", marks[2])
	}
	if len(marks) != 2 {
		t.Errorf("marks = %v, want only the two matched slots", marks)
	}
}

func TestRSVPMarksNilTeam(t *testing.T) {
	if got := rsvpMarks(nil, []models.RSVP{{Status: models.RSVPYes}}); len(got) != 0 {
		t.Errorf("got %v, want an empty map", got)
	}
}

// --- slot packing ---

// TestCompactedSimpleAssignment is the behaviour people expect when someone
// leaves a role-based run: the remaining claimants slide up so the empty slots
// collect at the bottom of each role.
func TestCompactedSimpleAssignment(t *testing.T) {
	team := signupTeam() // slots 3 and 4 are both DPS
	signups := []models.PremadeSignup{
		{Slot: 4, DiscordUserID: "d2", DiscordUsername: "Dee"},
	}

	got, changed := compactedSimpleAssignment(team, signups)

	if !changed {
		t.Fatal("got changed = false, want the lone DPS moved up to slot 3")
	}
	if len(got) != 1 || got[0].Slot != 3 || got[0].DiscordUserID != "d2" {
		t.Errorf("got %+v, want Dee on slot 3", got)
	}
}

func TestCompactedSimpleAssignmentNoopWhenAlreadyPacked(t *testing.T) {
	team := signupTeam()
	signups := []models.PremadeSignup{
		{Slot: 1, DiscordUserID: "t1"},
		{Slot: 3, DiscordUserID: "d1"},
	}

	got, changed := compactedSimpleAssignment(team, signups)

	if changed {
		t.Errorf("got changed = true for an already-packed set: %+v", got)
	}
}

// TestCompactedSimpleAssignmentPreservesOrder keeps the queue fair: packing
// must not reshuffle who is ahead of whom within a role.
func TestCompactedSimpleAssignmentPreservesOrder(t *testing.T) {
	team := &models.Team{Players: []models.Player{
		{Slot: 1, Role: "dps"}, {Slot: 2, Role: "dps"}, {Slot: 3, Role: "dps"},
	}}
	signups := []models.PremadeSignup{
		{Slot: 2, DiscordUserID: "first"},
		{Slot: 3, DiscordUserID: "second"},
	}

	got, changed := compactedSimpleAssignment(team, signups)

	if !changed {
		t.Fatal("got changed = false, want both moved up")
	}
	bySlot := map[int]string{}
	for _, sg := range got {
		bySlot[sg.Slot] = sg.DiscordUserID
	}
	if bySlot[1] != "first" || bySlot[2] != "second" {
		t.Errorf("got %v, want first on slot 1 and second on slot 2", bySlot)
	}
}

// TestCompactedSimpleAssignmentKeepsOrphans leaves a signup alone when its slot
// is gone from the roster, rather than silently reassigning that person.
func TestCompactedSimpleAssignmentKeepsOrphans(t *testing.T) {
	team := signupTeam()
	signups := []models.PremadeSignup{{Slot: 99, DiscordUserID: "ghost"}}

	got, changed := compactedSimpleAssignment(team, signups)

	if changed {
		t.Error("an off-roster slot should not count as a change")
	}
	if len(got) != 1 || got[0].Slot != 99 {
		t.Errorf("got %+v, want the orphan untouched", got)
	}
}

func TestFirstOpenSlotForRole(t *testing.T) {
	team := signupTeam()

	if slot, ok := firstOpenSlotForRole(team, nil, "dps"); !ok || slot != 3 {
		t.Errorf("got slot %d ok %v, want the lowest DPS slot 3", slot, ok)
	}
	if slot, ok := firstOpenSlotForRole(team, map[int]bool{3: true}, "dps"); !ok || slot != 4 {
		t.Errorf("got slot %d ok %v, want slot 4 once 3 is taken", slot, ok)
	}
	if _, ok := firstOpenSlotForRole(team, map[int]bool{3: true, 4: true}, "dps"); ok {
		t.Error("got ok = true, want none once every DPS slot is taken")
	}
	if _, ok := firstOpenSlotForRole(team, nil, "nonexistent"); ok {
		t.Error("got ok = true for a role nobody has")
	}
}

func TestSlotOptionLabel(t *testing.T) {
	team := signupTeam()

	if got := slotOptionLabel(team, team.Players[0]); got != "Slot 1 · Tank · Dragonknight" {
		t.Errorf("got %q", got)
	}
	// An unset class shows an em dash rather than an empty segment.
	if got := slotOptionLabel(team, team.Players[3]); got != "Slot 4 · DPS · —" {
		t.Errorf("got %q", got)
	}
}
