package discordfmt

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/core-team-builder/backend/internal/esoref"
	"github.com/core-team-builder/backend/internal/models"
)

// Emoji the formatters embed, spelled as escapes here for the same reason the
// production code does: so a terminal or editor that mangles the glyph can't
// silently change what the test asserts.
const (
	emojiTank    = "\U0001F6E1\uFE0F" // 🛡️
	emojiHealer  = "\u2747\uFE0F"     // ❇️
	emojiSupport = "\u2692\uFE0F"     // ⚒️
	emojiDPS     = "\u2694\uFE0F"     // ⚔️

	iconYes  = "\u2705"     // ✅ coming
	iconNo   = "\u274C"     // ❌ not coming
	iconNone = "\U0001F7E1" // 🟡 no response yet

	emojiCalendar = "\U0001F5D3\uFE0F" // 🗓️
)

// testTeam is a four-slot roster covering the cases the role grouping cares
// about: three assigned players across three different roles and one slot with
// no Discord handle (an "open" slot). The team's support_dps role has nobody on
// it, so it also exercises skipping an empty role group.
func testTeam() *models.Team {
	return &models.Team{
		Name: "Prog Team",
		Players: []models.Player{
			{Slot: 1, Name: "Alice", DiscordHandle: "alice", Role: "tank", Class: "dragonknight"},
			{Slot: 2, Name: "Bob", DiscordHandle: "bob", Role: "healer", Class: "arcanist"},
			{Slot: 3, Name: "Cara", DiscordHandle: "cara", Role: "dps", Class: "nightblade"},
			{Slot: 4, Role: "dps"},
		},
	}
}

// lines joins formatter output for comparison, so a mismatch reports one
// readable block instead of a slice dump.
func lines(got []string) string { return strings.Join(got, "\n") }

func TestRosterLines(t *testing.T) {
	team := testTeam()

	got := lines(rosterLines(team, nil, nil, nil, nil))
	want := strings.Join([]string{
		emojiTank + " __Tank__ (1/1)",
		iconNone + " @alice · Dragonknight · —",
		"",
		emojiHealer + " __Healer__ (1/1)",
		iconNone + " @bob · Arcanist · —",
		"",
		emojiDPS + " __DPS__ (1/2)",
		iconNone + " @cara · Nightblade · —",
		iconNone + " Slot 4 · — · —",
	}, "\n")

	if got != want {
		t.Errorf("roster lines:\n%s\n\nwant:\n%s", got, want)
	}
}

// TestRosterLinesRoleCounts pins the header tally, which drives whether a lead
// thinks a role still needs bodies. A slot counts as covered when its assigned
// player has not declined, or when anyone signed up to fill it.
func TestRosterLinesRoleCounts(t *testing.T) {
	team := testTeam()

	cases := []struct {
		name       string
		marks      map[int]string
		fillBySlot map[int]string
		wantDPS    string
	}{
		{
			name:    "no responses still counts the assigned player",
			wantDPS: "(1/2)",
		},
		{
			name:    "an explicit yes does not double count",
			marks:   map[int]string{3: models.RSVPYes},
			wantDPS: "(1/2)",
		},
		{
			name:    "a decline drops the slot back to uncovered",
			marks:   map[int]string{3: models.RSVPNo},
			wantDPS: "(0/2)",
		},
		{
			name:       "a filler covers the open slot",
			fillBySlot: map[int]string{4: "Erin"},
			wantDPS:    "(2/2)",
		},
		{
			name:       "a filler covers for a declining player",
			marks:      map[int]string{3: models.RSVPNo},
			fillBySlot: map[int]string{3: "Erin"},
			wantDPS:    "(1/2)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rosterLines(team, nil, tc.marks, tc.fillBySlot, nil)
			header := ""
			for _, l := range got {
				if strings.Contains(l, "__DPS__") {
					header = l
				}
			}
			if !strings.HasSuffix(header, tc.wantDPS) {
				t.Errorf("DPS header = %q, want it to end in %s", header, tc.wantDPS)
			}
		})
	}
}

// TestRosterLinesFillTags covers the two fill shapes, which read differently on
// purpose: taking an empty slot is just `fill`, while covering for someone who
// declined names who is being covered for.
func TestRosterLinesFillTags(t *testing.T) {
	team := testTeam()
	marks := map[int]string{3: models.RSVPNo}
	fills := map[int]string{3: "Erin", 4: "Frank"}

	got := lines(rosterLines(team, nil, marks, fills, nil))

	if want := iconYes + " Erin `fill for @cara` · Nightblade · —"; !strings.Contains(got, want) {
		t.Errorf("missing absent-slot fill line %q in:\n%s", want, got)
	}
	if want := iconYes + " Frank `fill` · — · —"; !strings.Contains(got, want) {
		t.Errorf("missing open-slot fill line %q in:\n%s", want, got)
	}
}

// TestRosterLinesPrefersResolvedNames checks the resolved Discord display name
// wins over the stored handle, and that a filler still outranks both.
func TestRosterLinesPrefersResolvedNames(t *testing.T) {
	team := testTeam()
	names := map[int]string{1: "Alice (raid lead)", 3: "Cara#1234"}

	got := lines(rosterLines(team, nil, nil, map[int]string{3: "Erin"}, names))

	if !strings.Contains(got, "Alice (raid lead) · Dragonknight") {
		t.Errorf("resolved name did not replace the handle:\n%s", got)
	}
	if strings.Contains(got, "@alice") {
		t.Errorf("raw handle still shown despite a resolved name:\n%s", got)
	}
	// Slot 3 is assigned, so the filler is covering for someone — and the person
	// covered for is named by their resolved name, not the raw handle.
	if !strings.Contains(got, "Erin `fill for Cara#1234` · Nightblade") {
		t.Errorf("filler should replace the resolved name:\n%s", got)
	}
}

// TestRosterLinesShowsGearAndWerewolf pulls the abbreviated gear from the
// primary encounter's loadout for the matching slot only.
func TestRosterLinesShowsGearAndWerewolf(t *testing.T) {
	team := testTeam()
	team.Players[0].Werewolf = true
	primary := &models.Encounter{Loadouts: []models.Loadout{
		{Slot: 1, Gear: []string{"roar_of_alkosh", "pillar_of_nirn"}},
	}}

	got := lines(rosterLines(team, primary, nil, nil, nil))

	if want := iconNone + " @alice · Dragonknight · WW · Alkosh/Pillar"; !strings.Contains(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
	// Slots without a loadout keep the em dash rather than borrowing slot 1's.
	if !strings.Contains(got, "@bob · Arcanist · —") {
		t.Errorf("gear leaked onto a slot with no loadout:\n%s", got)
	}
}

func TestRosterLinesEmptyRoster(t *testing.T) {
	if got := rosterLines(&models.Team{Name: "Empty"}, nil, nil, nil, nil); got != nil {
		t.Errorf("got %v, want nil for a team with no players", got)
	}
}

// TestRosterLinesOtherGroup puts players on a role the team does not define and
// on no role at all; the first keeps its raw key, the second lands in "Other".
func TestRosterLinesOtherGroup(t *testing.T) {
	team := &models.Team{
		Name: "Odd",
		Players: []models.Player{
			{Slot: 1, Name: "Ghost", Role: "necromancer_support"},
			{Slot: 2, Name: "Nobody"},
		},
	}

	got := lines(rosterLines(team, nil, nil, nil, nil))

	if !strings.Contains(got, "__necromancer_support__") {
		t.Errorf("unknown role key should render verbatim:\n%s", got)
	}
	if !strings.Contains(got, "__Other__") {
		t.Errorf("roleless player should land in Other:\n%s", got)
	}
}

func TestBuildPostSections(t *testing.T) {
	team := testTeam()
	team.PostFooter = "  Voice: #raid-1  "
	groupings := []models.Grouping{{
		Name:   "Ice cages",
		Groups: []models.GroupingGroup{{GroupNumber: 1, Name: "Left", Slots: []int{1, 3}}},
	}}

	title, desc := BuildPost(team, nil, groupings, nil, nil, []string{"Gale"}, nil, 0)

	if title != "Prog Team" {
		t.Errorf("title = %q, want the team name", title)
	}
	// Section order is what makes the post scannable, so assert the order rather
	// than mere presence.
	wantOrder := []string{"__Tank__", "__Fill list__", "**Groupings**", "__Ice cages__", "Voice: #raid-1"}
	prev := -1
	for _, marker := range wantOrder {
		at := strings.Index(desc, marker)
		if at < 0 {
			t.Fatalf("missing %q in:\n%s", marker, desc)
		}
		if at < prev {
			t.Errorf("%q is out of order in:\n%s", marker, desc)
		}
		prev = at
	}
	if strings.HasPrefix(desc, "\n") || strings.HasSuffix(desc, "\n") {
		t.Errorf("description should be trimmed, got %q", desc)
	}
	if !strings.Contains(desc, iconYes+" Gale") {
		t.Errorf("fill list entry missing:\n%s", desc)
	}
}

// TestBuildPostSchedule covers the schedule line's precedence: a locked run date
// wins, is dropped once it is in the past, and otherwise falls back to the live
// recurring computation and finally to plain text.
func TestBuildPostSchedule(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).Unix()
	past := time.Now().Add(-2 * time.Hour).Unix()

	t.Run("locked upcoming run is shown verbatim", func(t *testing.T) {
		_, desc := BuildPost(testTeam(), nil, nil, nil, nil, nil, nil, future)
		if !strings.Contains(desc, "Next run: <t:"+strconv.FormatInt(future, 10)+":F>") {
			t.Errorf("locked run not rendered:\n%s", desc)
		}
	})

	t.Run("locked run in the past is dropped entirely", func(t *testing.T) {
		_, desc := BuildPost(testTeam(), nil, nil, nil, nil, nil, nil, past)
		if strings.Contains(desc, "Next run") {
			t.Errorf("a past run should not advertise a next run:\n%s", desc)
		}
		// It must not roll forward to the recurring schedule either.
		if strings.Contains(desc, emojiCalendar) {
			t.Errorf("no schedule line expected at all:\n%s", desc)
		}
	})

	t.Run("no locked run falls back to the recurring schedule", func(t *testing.T) {
		team := testTeam()
		team.ScheduleDays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
		team.ScheduleTime = time.Now().UTC().Add(30 * time.Minute).Format("15:04")

		_, desc := BuildPost(team, nil, nil, nil, nil, nil, nil, 0)
		if !strings.Contains(desc, "Next run: <t:") {
			t.Errorf("daily schedule should produce a next run:\n%s", desc)
		}
	})

	t.Run("an unusable schedule falls back to plain text", func(t *testing.T) {
		team := testTeam()
		team.ScheduleDays = []string{"wed"}
		team.ScheduleTime = "" // no time, so no instant to anchor a timestamp on

		_, desc := BuildPost(team, nil, nil, nil, nil, nil, nil, 0)
		if !strings.Contains(desc, emojiCalendar+"  Schedule: Wed") {
			t.Errorf("plain schedule fallback missing:\n%s", desc)
		}
	})
}

func TestScheduleFallback(t *testing.T) {
	cases := []struct {
		name string
		days []string
		hhmm string
		want string
	}{
		{"no days at all", nil, "20:00", "Schedule: TBD"},
		{"days but no time", []string{"wed", "fri"}, "", "Schedule: Wed, Fri"},
		{"days and time", []string{"wed"}, "20:00", "Schedule: Wed · 20:00 UTC"},
		{"unknown day key passes through", []string{"someday"}, "", "Schedule: someday"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			team := &models.Team{ScheduleDays: tc.days, ScheduleTime: tc.hhmm}
			if got := scheduleFallback(team); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// --- pre-made runs ---

func premadeTeam() *models.Team {
	team := testTeam()
	team.PreMade = true
	team.PremadePost = "Bring pots."
	return team
}

func TestPremadeRosterLinesAdvanced(t *testing.T) {
	team := premadeTeam() // SimpleSignup false == advanced/specific signup
	primary := &models.Encounter{Loadouts: []models.Loadout{
		{Slot: 1, Gear: []string{"lucent_echoes"}},
	}}
	claimants := map[int]models.PremadeSignup{
		1: {Slot: 1, DiscordUserID: "111", DiscordUsername: "Alice"},
	}

	got := lines(premadeRosterLines(team, primary, claimants))
	// Unlike the trial overview, the header tally here counts *claimed* slots,
	// so an unclaimed slot reads 0 even though a roster name sits in it. The
	// slot number is joined with the same " · " separator as the other
	// segments, hence "1. · Alice".
	want := strings.Join([]string{
		emojiTank + " __Tank__ (1/1)",
		"1. · Alice · Dragonknight · LE · Alice",
		"",
		emojiHealer + " __Healer__ (0/1)",
		"2. · Bob · Arcanist · — · _open_",
		"",
		emojiDPS + " __DPS__ (0/2)",
		"3. · Cara · Nightblade · — · _open_",
		"4. · — · — · _open_",
	}, "\n")

	if got != want {
		t.Errorf("premade roster:\n%s\n\nwant:\n%s", got, want)
	}
}

// TestPremadeRosterLinesSimple checks simple (role-only) signup hides class and
// gear, which is the whole point of the mode.
func TestPremadeRosterLinesSimple(t *testing.T) {
	team := premadeTeam()
	team.SimpleSignup = true
	primary := &models.Encounter{Loadouts: []models.Loadout{{Slot: 1, Gear: []string{"lucent_echoes"}}}}

	got := lines(premadeRosterLines(team, primary, nil))

	if strings.Contains(got, "Dragonknight") || strings.Contains(got, "LE") {
		t.Errorf("simple signup must hide class and gear:\n%s", got)
	}
	if want := "1. · Alice · _open_"; !strings.Contains(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
}

// TestPremadeClaimantFallsBackToID mirrors the embed-rendering constraint: a
// stored display name is preferred, but an unnamed signup still has to show
// something, so it falls back to the raw Discord ID.
func TestPremadeClaimantFallsBackToID(t *testing.T) {
	team := premadeTeam()
	claimants := map[int]models.PremadeSignup{
		2: {Slot: 2, DiscordUserID: "999"},
		3: {Slot: 3, DiscordUserID: "888", DiscordUsername: "  "},
	}

	got := lines(premadeRosterLines(team, nil, claimants))

	if !strings.Contains(got, "· 999") {
		t.Errorf("missing ID fallback for an unnamed signup:\n%s", got)
	}
	if !strings.Contains(got, "· 888") {
		t.Errorf("a blank username should fall back to the ID too:\n%s", got)
	}
}

func TestBuildPremadePost(t *testing.T) {
	team := premadeTeam()
	scheduled := time.Now().Add(24 * time.Hour).Unix()
	claimants := map[int]models.PremadeSignup{1: {Slot: 1, DiscordUsername: "Alice"}}
	waitlist := []models.PremadeWaitlistEntry{{Role: "dps", DiscordUsername: "Wendy"}}
	tentative := []models.PremadeTentativeEntry{{Role: "healer", DiscordUsername: "Tess"}}

	title, desc := BuildPremadePost(team, "vHRC Farm", "", scheduled, nil, nil, claimants, waitlist, tentative)

	if title != "vHRC Farm" {
		t.Errorf("title = %q, want the run title", title)
	}
	for _, want := range []string{
		"<t:" + strconv.FormatInt(scheduled, 10) + ":F>",
		"Bring pots.",
		emojiTank + " __Tank__ (1/1)",
		"1. · Alice · Dragonknight · — · Alice",
		"__Waitlist__",
		emojiDPS + " DPS: Wendy",
		"__Tentative (maybe)__",
		"Tess",
		"Use the menu below to sign up",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("missing %q in:\n%s", want, desc)
		}
	}
}

func TestBuildPremadePostTitleAndBodyFallbacks(t *testing.T) {
	team := premadeTeam()

	t.Run("blank title falls back to the team name", func(t *testing.T) {
		title, _ := BuildPremadePost(team, "   ", "", 0, nil, nil, nil, nil, nil)
		if title != team.Name {
			t.Errorf("title = %q, want %q", title, team.Name)
		}
	})

	t.Run("override replaces the team body", func(t *testing.T) {
		_, desc := BuildPremadePost(team, "Run", "Override body", 0, nil, nil, nil, nil, nil)
		if !strings.Contains(desc, "Override body") {
			t.Errorf("override missing:\n%s", desc)
		}
		if strings.Contains(desc, "Bring pots.") {
			t.Errorf("team body should have been replaced:\n%s", desc)
		}
	})

	t.Run("no schedule omits the timestamp", func(t *testing.T) {
		_, desc := BuildPremadePost(team, "Run", "", 0, nil, nil, nil, nil, nil)
		if strings.Contains(desc, "<t:") {
			t.Errorf("unscheduled run should have no timestamp:\n%s", desc)
		}
	})
}

// TestBuildPremadePostGroupingsGate documents that groupings are advanced-signup
// only: a simple run deliberately hides per-slot detail.
func TestBuildPremadePostGroupingsGate(t *testing.T) {
	groupings := []models.Grouping{{
		Name:   "Ice cages",
		Groups: []models.GroupingGroup{{GroupNumber: 1, Slots: []int{1}}},
	}}

	advanced := premadeTeam()
	_, desc := BuildPremadePost(advanced, "Run", "", 0, nil, groupings, nil, nil, nil)
	if !strings.Contains(desc, "__Ice cages__") {
		t.Errorf("advanced signup should show groupings:\n%s", desc)
	}

	simple := premadeTeam()
	simple.SimpleSignup = true
	_, desc = BuildPremadePost(simple, "Run", "", 0, nil, groupings, nil, nil, nil)
	if strings.Contains(desc, "__Ice cages__") {
		t.Errorf("simple signup should hide groupings:\n%s", desc)
	}
}

// TestPremadeWaitlistLines checks the queue renders in the team's role order
// rather than arrival order, so the block lines up with the roster above.
func TestPremadeWaitlistLines(t *testing.T) {
	team := testTeam()
	waitlist := []models.PremadeWaitlistEntry{
		{Role: "dps", DiscordUsername: "Dee"},
		{Role: "tank", DiscordUsername: "Tam"},
		{Role: "dps", DiscordUsername: "Dot"},
	}

	got := lines(premadeWaitlistLines(team, waitlist))
	want := strings.Join([]string{
		"__Waitlist__",
		emojiTank + " Tank: Tam",
		emojiDPS + " DPS: Dee, Dot",
	}, "\n")

	if got != want {
		t.Errorf("waitlist:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestPremadeWaitlistAndTentativeEmpty(t *testing.T) {
	if got := premadeWaitlistLines(testTeam(), nil); got != nil {
		t.Errorf("got %v, want nil for an empty waitlist", got)
	}
	if got := premadeTentativeLines(nil); got != nil {
		t.Errorf("got %v, want nil for nobody tentative", got)
	}
}

func TestPremadeTentativeLines(t *testing.T) {
	got := lines(premadeTentativeLines([]models.PremadeTentativeEntry{
		{DiscordUsername: "Tess"},
		{DiscordUserID: "777"},
	}))
	want := "__Tentative (maybe)__\nTess, 777"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- groupings ---

func TestFormatGroupings(t *testing.T) {
	team := testTeam()
	groupings := []models.Grouping{
		{
			Name: "Ice cages",
			Groups: []models.GroupingGroup{
				{GroupNumber: 1, Name: "Left", Slots: []int{3, 1}},
				{GroupNumber: 2, Slots: nil},
			},
		},
		// An entirely empty grouping adds nothing and is skipped.
		{Name: "Unused", Groups: []models.GroupingGroup{{GroupNumber: 1}}},
	}

	got := lines(formatGroupings(team, groupings, nil, nil, false))
	want := strings.Join([]string{
		"**Groupings**",
		"",
		"__Ice cages__",
		"• Left: Alice, Cara", // slots sorted ascending regardless of input order
		"• Group 2: —",
	}, "\n")

	if got != want {
		t.Errorf("groupings:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestFormatGroupingsEmpty(t *testing.T) {
	team := testTeam()
	if got := formatGroupings(team, nil, nil, nil, false); got != nil {
		t.Errorf("got %v, want nil when there are no groupings", got)
	}
	onlyEmpty := []models.Grouping{{Name: "Unused", Groups: []models.GroupingGroup{{GroupNumber: 1}}}}
	if got := formatGroupings(team, onlyEmpty, nil, nil, false); got != nil {
		t.Errorf("got %v, want nil when no grouping has members", got)
	}
}

// TestFormatGroupingsNameResolution pins the precedence that keeps the
// groupings block in sync with the roster above it: filler, then resolved
// Discord name, then the static roster slot name.
func TestFormatGroupingsNameResolution(t *testing.T) {
	team := testTeam()
	groupings := []models.Grouping{{
		Name:   "Stacks",
		Groups: []models.GroupingGroup{{GroupNumber: 1, Slots: []int{1, 2, 4}}},
	}}
	fills := map[int]string{1: "Erin"}
	names := map[int]string{2: "Bobby"}

	got := lines(formatGroupings(team, groupings, fills, names, true))

	if want := "• Group 1: 1. Erin, 2. Bobby, 4. Slot 4"; !strings.Contains(got, want) {
		t.Errorf("got:\n%s\n\nwant a line %q", got, want)
	}
}

func TestPlayerGroupLines(t *testing.T) {
	groupings := []models.Grouping{
		{Name: "Ice cages", Groups: []models.GroupingGroup{
			{GroupNumber: 1, Name: "Left", Slots: []int{1}},
			{GroupNumber: 2, Slots: []int{3}},
		}},
		{Groups: []models.GroupingGroup{{GroupNumber: 1, Slots: []int{3}}}},
	}

	if got := playerGroupLines(groupings, 1); lines(got) != "• Ice cages: Left" {
		t.Errorf("slot 1 groups = %v", got)
	}
	// Unnamed grouping and group both fall back to generated labels.
	if got := playerGroupLines(groupings, 3); lines(got) != "• Ice cages: Group 2\n• Grouping: Group 1" {
		t.Errorf("slot 3 groups = %v", got)
	}
	if got := playerGroupLines(groupings, 2); got != nil {
		t.Errorf("got %v, want nil for a slot in no group", got)
	}
}

// --- player detail DM ---

func TestPlayerDetail(t *testing.T) {
	team := testTeam()
	team.DMFooter = "Ping @lead with questions."
	player := models.Player{
		Slot: 1, Name: "Alice", DiscordHandle: "alice", Role: "tank",
		Class: "dragonknight", Race: "nord",
		Mastery1: "lead_from_the_front",
	}
	encounters := []models.Encounter{{
		Name:     "Boss",
		Loadouts: []models.Loadout{{Slot: 1, Gear: []string{"roar_of_alkosh"}, ArmorHeavy: 5, ArmorLight: 2}},
	}}
	groupings := []models.Grouping{{
		Name: "Ice cages", Groups: []models.GroupingGroup{{GroupNumber: 1, Name: "Left", Slots: []int{1}}},
	}}

	title, desc := PlayerDetail(team, player, encounters, groupings)

	if want := "Prog Team — Your Build Details"; title != want {
		t.Errorf("title = %q, want %q", title, want)
	}
	for _, want := range []string{
		"__Player__",
		"1. Alice — Tank · @alice",
		"__Class & Race__",
		"Dragonknight · Nord",
		"__Build__",
		"__Groups__",
		"• Ice cages: Left",
		"__Gear__",
		"Roar of Alkosh",
		"__Armor__",
		"5H/2L",
		"**Requirements**",
		"__Personal Stats (before group buffs)__",
		"Ping @lead with questions.",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("missing %q in:\n%s", want, desc)
		}
	}
	// A single encounter needs no name header; it would just be noise.
	if strings.Contains(desc, "**Boss**") {
		t.Errorf("single encounter should not print its name:\n%s", desc)
	}
}

func TestPlayerDetailMultipleEncounters(t *testing.T) {
	team := testTeam()
	player := models.Player{Slot: 1, Name: "Alice", Role: "tank"}
	encounters := []models.Encounter{
		{Name: "Trash", Loadouts: []models.Loadout{{Slot: 1, Gear: []string{"pillar_of_nirn"}}}},
		{Name: "Boss"}, // no loadout for this slot
	}

	_, desc := PlayerDetail(team, player, encounters, nil)

	if !strings.Contains(desc, "**Trash**") || !strings.Contains(desc, "**Boss**") {
		t.Errorf("both encounter headers expected:\n%s", desc)
	}
	if !strings.Contains(desc, "_(no loadout set)_") {
		t.Errorf("an encounter with no loadout should say so:\n%s", desc)
	}
}

func TestPlayerDetailUnnamedSlot(t *testing.T) {
	team := testTeam()
	_, desc := PlayerDetail(team, models.Player{Slot: 7, Role: "dps"}, nil, nil)

	if !strings.Contains(desc, "7. Slot 7 — DPS") {
		t.Errorf("unnamed player should fall back to its slot:\n%s", desc)
	}
	if strings.Contains(desc, " · @") {
		t.Errorf("no handle means no discord tag:\n%s", desc)
	}
}

func TestLoadoutDetailFields(t *testing.T) {
	lo := models.Loadout{
		Slot:        1,
		Gear:        []string{"roar_of_alkosh"},
		Skills:      []string{"pierce_armor"},
		Mundus:      "thief",
		ArmorMedium: 1,
		PenExtra:    []string{"set_piece_bonuses", "set_piece_bonuses", "sharpened"},
	}

	got := loadoutDetailFields(lo)
	byHeader := map[string]string{}
	for _, f := range got {
		byHeader[f.header] = f.value
	}

	if byHeader["Gear"] != "Roar of Alkosh" {
		t.Errorf("Gear = %q", byHeader["Gear"])
	}
	if byHeader["Armor"] != "1M" {
		t.Errorf("Armor = %q, want only the non-zero piece counts", byHeader["Armor"])
	}
	// Repeated pen_extra keys collapse into a single "×N" entry, in first-seen order.
	if want := "Set-piece bonuses ×2, Sharpened (single)"; byHeader["Penetration"] != want {
		t.Errorf("Penetration = %q, want %q", byHeader["Penetration"], want)
	}
	if _, ok := byHeader["Potions"]; ok {
		t.Error("an unset data type should be omitted entirely")
	}
}

// TestLoadoutDetailFieldsBannerGate mirrors the web UI: a Focus Script left
// behind after the Banner Bearer grimoire is unslotted must not be shown.
func TestLoadoutDetailFieldsBannerGate(t *testing.T) {
	stale := models.Loadout{BannerBearerFocus: "mitigation", Skills: []string{"pierce_armor"}}
	for _, f := range loadoutDetailFields(stale) {
		if f.header == "Banner Focus" {
			t.Fatalf("stale focus script shown without the grimoire: %v", f)
		}
	}

	slotted := models.Loadout{BannerBearerFocus: "mitigation", Skills: []string{"banner_bearer"}}
	found := false
	for _, f := range loadoutDetailFields(slotted) {
		if f.header == "Banner Focus" {
			found = true
		}
	}
	if !found {
		t.Error("focus script should show when Banner Bearer is slotted")
	}
}

func TestLoadoutDetailFieldsEmpty(t *testing.T) {
	if got := loadoutDetailFields(models.Loadout{}); len(got) != 0 {
		t.Errorf("got %v, want no fields for an empty loadout", got)
	}
}

// --- pen / crit / buff coverage ---

// TestSelfRequiredNoGroupSources is the baseline: with nobody providing
// anything, each player must bring the full target themselves.
func TestSelfRequiredNoGroupSources(t *testing.T) {
	team := &models.Team{Players: []models.Player{{Slot: 1}}}

	pen, crit := selfRequired(team, nil)

	if pen != 18200 {
		t.Errorf("pen = %d, want the full 18200 target", pen)
	}
	// Crit cap 125 minus the 50 everyone has by default.
	if crit != 75 {
		t.Errorf("crit = %d, want 75", crit)
	}
}

func TestSelfRequiredSubtractsGroupSources(t *testing.T) {
	team := &models.Team{Players: []models.Player{{Slot: 1}, {Slot: 2}}}
	primary := &models.Encounter{Loadouts: []models.Loadout{
		// Major Breach (5948) + Minor Breach (2974) both come off Pierce Armor.
		{Slot: 1, Skills: []string{"pierce_armor"}},
		// Roar of Alkosh (6000) plus a group-bucket Crusher enchant (2108).
		{Slot: 2, Gear: []string{"roar_of_alkosh"}, PenExtra: []string{"crusher"}},
	}}

	pen, _ := selfRequired(team, primary)

	want := 18200 - (5948 + 2974 + 6000 + 2108)
	if pen != want {
		t.Errorf("pen = %d, want %d", pen, want)
	}
}

// TestSelfRequiredClampsAtZero over-provisions both stats across the group; a
// team that beats the cap needs nothing from each player, never a negative.
func TestSelfRequiredClampsAtZero(t *testing.T) {
	team := &models.Team{Players: []models.Player{{Slot: 1}, {Slot: 2}, {Slot: 3}}}
	primary := &models.Encounter{Loadouts: []models.Loadout{
		{
			Slot:         1,
			Skills:       []string{"pierce_armor"}, // major + minor breach
			Gear:         []string{"roar_of_alkosh", "anthelmirs_construct"},
			PenExtra:     []string{"crusher"},
			WeaponDamage: 20000, // Anthelmir's adds 0.5 pen per point
		},
		// Crit sources: 20 + 10 + 11 + 10 + 20 + (3 × 5) = 86 over the 50 base,
		// comfortably past the 125 cap.
		{Slot: 2, Gear: []string{"saxhleel_champion", "twilight_remedy", "lucent_echoes"}},
		{Slot: 3, Gear: []string{"glittering_goad", "nunatak", "elemental_catalyst"}},
	}}

	pen, crit := selfRequired(team, primary)

	if pen != 0 {
		t.Errorf("pen = %d, want it clamped to 0 once the team over-caps", pen)
	}
	if crit != 0 {
		t.Errorf("crit = %d, want it clamped to 0", crit)
	}
}

// TestSelfRequiredElementalCatalystScales covers the one crit source whose value
// is not a constant: it scales with the highest element count among wearers.
func TestSelfRequiredElementalCatalystScales(t *testing.T) {
	team := &models.Team{Players: []models.Player{{Slot: 1}, {Slot: 2}}}
	primary := &models.Encounter{Loadouts: []models.Loadout{
		{Slot: 1, Gear: []string{"elemental_catalyst"}, CatalystElements: 1},
		{Slot: 2, Gear: []string{"elemental_catalyst"}, CatalystElements: 3},
	}}

	_, crit := selfRequired(team, primary)

	// 125 cap - 50 base - (3 elements × 5%) = 60.
	if crit != 60 {
		t.Errorf("crit = %d, want 60 from the highest element count", crit)
	}
}

// TestSelfRequiredAnthelmirUsesHighestWeaponDamage is the pen analogue of the
// catalyst case above.
func TestSelfRequiredAnthelmirUsesHighestWeaponDamage(t *testing.T) {
	team := &models.Team{Players: []models.Player{{Slot: 1}, {Slot: 2}}}
	primary := &models.Encounter{Loadouts: []models.Loadout{
		{Slot: 1, Gear: []string{"anthelmirs_construct"}, WeaponDamage: 1000},
		{Slot: 2, Gear: []string{"anthelmirs_construct"}, WeaponDamage: 5000},
	}}

	pen, _ := selfRequired(team, primary)

	if want := 18200 - 2500; pen != want { // 5000 × 0.5
		t.Errorf("pen = %d, want %d", pen, want)
	}
}

func TestMissingSelfBuffs(t *testing.T) {
	team := &models.Team{Players: []models.Player{{Slot: 1}}}

	t.Run("nothing equipped means every self buff is missing", func(t *testing.T) {
		got := missingSelfBuffs(team, nil)
		for _, want := range []string{"Minor Berserk", "Major Brutality and Sorcery", "Major Resolve"} {
			if !containsString(got, want) {
				t.Errorf("%q should be missing, got %v", want, got)
			}
		}
	})

	t.Run("a group source covers the buff for everyone", func(t *testing.T) {
		primary := &models.Encounter{Loadouts: []models.Loadout{
			{Slot: 1, Gear: []string{"kinras_wrath"}, Skills: []string{"igneous_weapons"}},
		}}
		got := missingSelfBuffs(team, primary)
		if containsString(got, "Minor Berserk") {
			t.Errorf("Kinras's Wrath should cover Minor Berserk, got %v", got)
		}
		if containsString(got, "Major Brutality and Sorcery") {
			t.Errorf("Igneous Weapons should cover Major Brutality, got %v", got)
		}
		if !containsString(got, "Major Resolve") {
			t.Errorf("Major Resolve is still uncovered, got %v", got)
		}
	})
}

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// TestDetectHit walks each detection category, since a source is provided when
// any single one matches.
func TestDetectHit(t *testing.T) {
	player := models.Player{Class: "arcanist", Race: "breton", Subclassed: false, Mastery1: "tundras_maw"}
	lo := models.Loadout{
		Gear:    []string{"roar_of_alkosh"},
		Skills:  []string{"banner_bearer"},
		Potions: []string{"weapon_crit"},
		CPBlue:  []string{"force_of_nature"},
		Mundus:  "thief",
	}
	ctx := newPCContext(player, lo)

	cases := []struct {
		name   string
		detect esoref.DetectMap
		want   bool
	}{
		{"gear", esoref.DetectMap{Gear: []string{"roar_of_alkosh"}}, true},
		{"skills", esoref.DetectMap{Skills: []string{"banner_bearer"}}, true},
		{"potions", esoref.DetectMap{Potions: []string{"weapon_crit"}}, true},
		{"masteries", esoref.DetectMap{Masteries: []string{"tundras_maw"}}, true},
		{"cp", esoref.DetectMap{CP: []string{"force_of_nature"}}, true},
		{"class", esoref.DetectMap{Classes: []string{"arcanist"}}, true},
		{"race", esoref.DetectMap{Race: []string{"breton"}}, true},
		{"mundus", esoref.DetectMap{Mundus: []string{"thief"}}, true},
		{"no match", esoref.DetectMap{Gear: []string{"nunatak"}}, false},
		{"empty detect map matches nothing", esoref.DetectMap{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectHit(ctx, tc.detect); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNewPCContextSubclassing checks the mastery/skill-line split: a subclassed
// player contributes skill lines, a base-class player contributes masteries.
func TestNewPCContextSubclassing(t *testing.T) {
	base := newPCContext(models.Player{Mastery1: "tundras_maw", SkillLine1: "green_balance"}, models.Loadout{})
	if !base.masteries["tundras_maw"] {
		t.Error("base-class player should contribute masteries")
	}
	if len(base.skillLines) != 0 {
		t.Errorf("base-class player should contribute no skill lines, got %v", base.skillLines)
	}

	sub := newPCContext(models.Player{Subclassed: true, Mastery1: "tundras_maw", SkillLine1: "green_balance"}, models.Loadout{})
	if !sub.skillLines["green_balance"] {
		t.Error("subclassed player should contribute skill lines")
	}
	if len(sub.masteries) != 0 {
		t.Errorf("subclassed player should contribute no masteries, got %v", sub.masteries)
	}
}

func TestNewPCContextClampsInputs(t *testing.T) {
	cases := []struct {
		name         string
		in           models.Loadout
		wantElements int
		wantWD       int
	}{
		{"unset elements default to the full bonus", models.Loadout{}, 3, 0},
		{"in-range elements are kept", models.Loadout{CatalystElements: 2}, 2, 0},
		{"over-range elements clamp to 3", models.Loadout{CatalystElements: 9}, 3, 0},
		{"negative weapon damage floors at 0", models.Loadout{WeaponDamage: -5}, 3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newPCContext(models.Player{}, tc.in)
			if got.catalystElements != tc.wantElements {
				t.Errorf("catalystElements = %d, want %d", got.catalystElements, tc.wantElements)
			}
			if got.weaponDamage != tc.wantWD {
				t.Errorf("weaponDamage = %d, want %d", got.weaponDamage, tc.wantWD)
			}
		})
	}
}

// TestScribedSetRequiresAGrimoire guards the same staleness gate as the banner
// focus: scribed buffs only count while a grimoire skill is slotted.
func TestScribedSetRequiresAGrimoire(t *testing.T) {
	stale := scribedSet(models.Loadout{ScribedBuffs: []string{"major_breach"}, Skills: []string{"pierce_armor"}})
	if len(stale) != 0 {
		t.Errorf("got %v, want no scribed buffs without a grimoire", stale)
	}

	live := scribedSet(models.Loadout{ScribedBuffs: []string{"major_breach"}, Skills: []string{"banner_bearer"}})
	if !live["major_breach"] {
		t.Errorf("got %v, want the scribed buff to count", live)
	}
}

// --- small helpers ---

func TestCommaInt(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1,000"},
		{18200, "18,200"},
		{1234567, "1,234,567"},
		{-18200, "-18,200"},
	}
	for _, tc := range cases {
		if got := commaInt(tc.in); got != tc.want {
			t.Errorf("commaInt(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGearAbbrevList(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"none", nil, "—"},
		{"curated abbreviation", []string{"roar_of_alkosh"}, "Alkosh"},
		{"several joined by slash", []string{"roar_of_alkosh", "lucent_echoes"}, "Alkosh/LE"},
		// No curated entry, so the acronym is built from the label, skipping
		// "of"/"the"/"and".
		{"derived acronym", []string{"mighty_glacier"}, "MG"},
		{"unknown key falls back to itself", []string{"not_a_set"}, "not_a_set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gearAbbrevList(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildText(t *testing.T) {
	cases := []struct {
		name string
		in   models.Player
		want string
	}{
		{"no masteries", models.Player{}, "Masteries: —"},
		{"masteries listed", models.Player{Mastery1: "tundras_maw", Mastery2: "lead_from_the_front"}, "Masteries: Tundra's Maw, Lead From the Front"},
		{"subclassed with no lines", models.Player{Subclassed: true}, "Subclass: —"},
		{"subclassed lines joined by slash", models.Player{Subclassed: true, SkillLine1: "green_balance", SkillLine3: "assassination"}, "Subclass: Green Balance / Assassination"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildText(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDiscordTagAndWho(t *testing.T) {
	tagCases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"alice", "@alice"},
		{"@alice", "@alice"},
		{"  alice  ", "@alice"},
	}
	for _, tc := range tagCases {
		if got := discordTag(tc.in); got != tc.want {
			t.Errorf("discordTag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// who prefers the handle, then the roster name, then the slot.
	whoCases := []struct {
		name string
		in   models.Player
		want string
	}{
		{"handle wins", models.Player{Slot: 1, Name: "Alice", DiscordHandle: "alice"}, "@alice"},
		{"name when no handle", models.Player{Slot: 1, Name: "Alice"}, "Alice"},
		{"slot when neither", models.Player{Slot: 4}, "Slot 4"},
	}
	for _, tc := range whoCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := who(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRSVPIcon(t *testing.T) {
	cases := []struct{ in, want string }{
		{models.RSVPYes, iconYes},
		{models.RSVPNo, iconNo},
		{"", iconNone},
		{"maybe", iconNone},
	}
	for _, tc := range cases {
		if got := rsvpIcon(tc.in); got != tc.want {
			t.Errorf("rsvpIcon(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFillListLines(t *testing.T) {
	if got := fillListLines(nil); got != nil {
		t.Errorf("got %v, want nil for an empty list", got)
	}
	// A list of nothing but blanks has no content, so the header is dropped too.
	if got := fillListLines([]string{"", "  "}); got != nil {
		t.Errorf("got %v, want nil when every entry is blank", got)
	}
	got := lines(fillListLines([]string{"Gale", "  Hana  ", ""}))
	want := "__Fill list__\n" + iconYes + " Gale\n" + iconYes + " Hana"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRoleGroups(t *testing.T) {
	team := testTeam()

	got := roleGroups(team, []string{"dps", "tank", "", "mystery"})

	var labels []string
	for _, g := range got {
		labels = append(labels, g.label)
	}
	// The team's own roles come first in base order, then extras, then Other.
	want := []string{"Tank", "Healer", "Support DPS", "DPS", "mystery", "Other"}
	if lines(labels) != lines(want) {
		t.Errorf("got %v, want %v", labels, want)
	}
}

func TestClaimantDisplay(t *testing.T) {
	if got := claimantDisplay("Alice", "111"); got != "Alice" {
		t.Errorf("got %q, want the username", got)
	}
	if got := claimantDisplay("   ", "111"); got != "111" {
		t.Errorf("got %q, want the ID fallback", got)
	}
}

func TestExportSlotName(t *testing.T) {
	team := testTeam()
	if got := exportSlotName(team, 1); got != "Alice" {
		t.Errorf("got %q, want the roster name", got)
	}
	if got := exportSlotName(team, 4); got != "Slot 4" {
		t.Errorf("got %q, want the slot fallback for an unnamed slot", got)
	}
	if got := exportSlotName(team, 99); got != "Slot 99" {
		t.Errorf("got %q, want the slot fallback for an unknown slot", got)
	}
}

func TestSortedPlayersDoesNotMutateTheTeam(t *testing.T) {
	team := &models.Team{Players: []models.Player{{Slot: 3}, {Slot: 1}, {Slot: 2}}}

	got := sortedPlayers(team)

	if got[0].Slot != 1 || got[1].Slot != 2 || got[2].Slot != 3 {
		t.Errorf("got slots %d,%d,%d, want 1,2,3", got[0].Slot, got[1].Slot, got[2].Slot)
	}
	if team.Players[0].Slot != 3 {
		t.Error("sortedPlayers must not reorder the caller's slice")
	}
}
