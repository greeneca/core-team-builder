package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/models"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under the limit is untouched", "hello", 10, "hello"},
		{"exactly the limit is untouched", "hello", 5, "hello"},
		{"over the limit gains an ellipsis", "hello world", 8, "hello w…"},
		{"max of one has no room for an ellipsis", "hello", 1, "h"},
		{"max of zero yields nothing", "hello", 0, ""},
		// Counting runes, not bytes, is what keeps a multi-byte label from
		// being cut mid-character and rejected by Discord.
		{"multi-byte runes count as one", "héllo wörld", 8, "héllo w…"},
		{"emoji are not split", "🛡️🛡️🛡️", 2, "🛡…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if n := len([]rune(got)); n > tc.max {
				t.Errorf("result is %d runes, want at most %d", n, tc.max)
			}
		})
	}
}

func TestDiscordIDFromHandle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain id", "123456789012345678", "123456789012345678"},
		{"mention", "<@123456789012345678>", "123456789012345678"},
		{"nickname mention", "<@!123456789012345678>", "123456789012345678"},
		// A leading "@" on digits is an ID, not a username, so it must resolve
		// to a display name rather than render verbatim.
		{"at-prefixed id", "@123456789012345678", "123456789012345678"},
		{"surrounding space", "  123456  ", "123456"},
		{"a username is not an id", "alice", ""},
		{"an at-prefixed username is not an id", "@alice", ""},
		{"empty", "", ""},
		{"only an at", "@", ""},
		{"mixed digits and letters", "123abc", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := discordIDFromHandle(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsAllDigits(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"123", true},
		{"0", true},
		{"", true}, // vacuously true; callers screen for empty first
		{"12a", false},
		{"-1", false},
		{"1 2", false},
		{"１２３", false}, // full-width digits are not ASCII digits
	}
	for _, tc := range cases {
		if got := isAllDigits(tc.in); got != tc.want {
			t.Errorf("isAllDigits(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName(&discordgo.User{Username: "alice", GlobalName: "Alice A"}); got != "Alice A" {
		t.Errorf("got %q, want the global name to win", got)
	}
	if got := displayName(&discordgo.User{Username: "alice"}); got != "alice" {
		t.Errorf("got %q, want the username fallback", got)
	}
}

func TestMemberDisplayName(t *testing.T) {
	cases := []struct {
		name string
		in   *discordgo.Member
		want string
	}{
		{"nil member", nil, ""},
		{"nick wins", &discordgo.Member{Nick: "Tank Alice", User: &discordgo.User{Username: "alice", GlobalName: "Alice A"}}, "Tank Alice"},
		{"blank nick falls through", &discordgo.Member{Nick: "  ", User: &discordgo.User{Username: "alice", GlobalName: "Alice A"}}, "Alice A"},
		{"no user", &discordgo.Member{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := memberDisplayName(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMessageURL(t *testing.T) {
	if got := messageURL("g1", "c1", "m1"); got != "https://discord.com/channels/g1/c1/m1" {
		t.Errorf("got %q", got)
	}
	// A DM has no guild, and Discord's jump links use "@me" in that position.
	if got := messageURL("", "c1", "m1"); got != "https://discord.com/channels/@me/c1/m1" {
		t.Errorf("got %q, want the @me form for a DM", got)
	}
	if got := messageURL("g1", "", "m1"); got != "" {
		t.Errorf("got %q, want no link without a channel", got)
	}
	if got := messageURL("g1", "c1", ""); got != "" {
		t.Errorf("got %q, want no link without a message", got)
	}
}

func TestPostLinkSuffix(t *testing.T) {
	if got := postLinkSuffix(""); got != "" {
		t.Errorf("got %q, want nothing to append", got)
	}
	if got := postLinkSuffix("https://example.com"); got != "\n\nhttps://example.com" {
		t.Errorf("got %q", got)
	}
}

// --- DM intent parsing ---

// TestDMIntentWords covers the free-text replies the premade DM flows key off.
// They are case- and space-insensitive because people type into Discord, not a
// form.
func TestDMIntentWords(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) bool
		yes  []string
		no   []string
	}{
		{
			name: "affirmative",
			fn:   isAffirmative,
			yes:  []string{"yes", "Y", " YEP ", "confirm", "ok", "okay", "sure", "correct", "yeah", "yup"},
			no:   []string{"no", "maybe", "", "yesterday"},
		},
		{
			name: "skip",
			fn:   isSkip,
			yes:  []string{"skip", "Default", " none ", "no"},
			no:   []string{"yes", "", "skipped"},
		},
		{
			name: "cancel",
			fn:   isCancel,
			yes:  []string{"cancel", "STOP", "quit", "abort", "exit", "nevermind", "never mind"},
			no:   []string{"yes", "", "cancellation"},
		},
		{
			name: "clear",
			fn:   isClear,
			yes:  []string{"clear", "default", "reset", "none", "skip"},
			no:   []string{"yes", "", "clearly"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, in := range tc.yes {
				if !tc.fn(in) {
					t.Errorf("%q should be recognized", in)
				}
			}
			for _, in := range tc.no {
				if tc.fn(in) {
					t.Errorf("%q should not be recognized", in)
				}
			}
		})
	}
}

// TestNormalizeMilitaryTime rewrites "at 2100" into "at 21:00" for the NLP date
// parser. The "at"/"@" anchor is what keeps a bare year from being mangled.
func TestNormalizeMilitaryTime(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"four digits after at", "Friday at 2100", "Friday at 21:00"},
		{"three digits are zero padded", "Friday at 930", "Friday at 09:30"},
		{"case insensitive anchor", "Friday AT 2100", "Friday AT 21:00"},
		{"midnight", "at 0000", "at 00:00"},
		{"last valid minute", "at 2359", "at 23:59"},
		// Out-of-range values are left alone rather than silently coerced.
		{"hour out of range", "at 2500", "at 2500"},
		{"minute out of range", "at 2170", "at 2170"},
		// A bare year has no anchor, so it must survive untouched.
		{"bare year is untouched", "on 2026 we run", "on 2026 we run"},
		{"already formatted is untouched", "Friday at 21:00", "Friday at 21:00"},
		{"no time at all", "sometime next week", "sometime next week"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeMilitaryTime(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNormalizeMilitaryTimeAtSignAnchorIsDead pins a known limitation rather
// than the intended behaviour: the "@" half of militaryTimeRe never fires, so
// "Friday @ 2100" reaches the date parser unconverted while "Friday at 2100"
// works.
//
// Two things combine. The pattern is `\b(at|@)\s+(\d{3,4})\b`, and the \b sits
// before the whole alternation — "@" is not a word character, so the boundary
// only exists when "@" directly follows one ("Friday@ 2100"). Then
// normalizeMilitaryTime re-runs FindStringSubmatch on the *matched substring*
// alone, and in "@ 2100" that leading boundary is gone, so the submatch fails
// and the callback returns the text unchanged. Every "@" spelling is therefore
// a no-op.
//
// Moving the boundary inside the alternation — `(\bat|@)\s+(\d{3,4})\b` —
// fixes both halves. Pinned as-is so the behaviour is visible and a deliberate
// fix shows up here as a failing expectation.
func TestNormalizeMilitaryTimeAtSignAnchorIsDead(t *testing.T) {
	unconverted := []string{
		"Friday @ 2100", // the natural form
		"Friday @2100",  // no space before the digits
		"@ 2100",        // at the start of the message
		"Friday@ 2100",  // the only spelling the pattern itself matches
	}
	for _, in := range unconverted {
		if got := normalizeMilitaryTime(in); got != in {
			t.Errorf("normalizeMilitaryTime(%q) = %q; the @ anchor now works — update this test", in, got)
		}
	}
}

func TestPremadeTeamListMessage(t *testing.T) {
	teams := []models.Team{{Name: "Alpha"}, {Name: "Beta"}}

	got := premadeTeamListMessage(teams)

	if !strings.Contains(got, "**1.** Alpha") || !strings.Contains(got, "**2.** Beta") {
		t.Errorf("got:\n%s", got)
	}
	if !strings.Contains(got, "cancel") {
		t.Error("the prompt should mention how to bail out")
	}
}

// TestPremadeTeamListMessageCaps keeps the numbered list within the 25 choices
// the follow-up picker can offer.
func TestPremadeTeamListMessageCaps(t *testing.T) {
	teams := make([]models.Team, 0, 40)
	for n := 1; n <= 40; n++ {
		teams = append(teams, models.Team{Name: "Team"})
	}

	got := premadeTeamListMessage(teams)

	if strings.Contains(got, "**26.**") {
		t.Errorf("list should stop at 25:\n%s", got)
	}
	if !strings.Contains(got, "**25.**") {
		t.Errorf("list should reach 25:\n%s", got)
	}
}

// --- recruit intake ---

func TestParseSpan(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantDay   string
		wantStart int
		wantEnd   int
		wantOK    bool
	}{
		{"well formed", "mon~18~22", "mon", 18, 22, true},
		{"all day", "sat~0~24", "sat", 0, 24, true},
		{"too few fields", "mon~18", "", 0, 0, false},
		{"too many fields", "mon~18~22~x", "", 0, 0, false},
		{"non-numeric start", "mon~six~22", "", 0, 0, false},
		{"non-numeric end", "mon~18~ten", "", 0, 0, false},
		{"empty", "", "", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			day, start, end, ok := parseSpan(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if day != tc.wantDay || start != tc.wantStart || end != tc.wantEnd {
				t.Errorf("got (%q, %d, %d), want (%q, %d, %d)", day, start, end, tc.wantDay, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

func TestSpanLabel(t *testing.T) {
	if got := spanLabel(span{0, 24}); got != "All day (00:00–24:00)" {
		t.Errorf("got %q", got)
	}
	if got := spanLabel(span{9, 17}); got != "09:00–17:00" {
		t.Errorf("got %q", got)
	}
}

// TestQuickSpanRows offers "All day" plus each distinct window the recruit
// already entered on other days, so repeat schedules are one tap.
func TestQuickSpanRows(t *testing.T) {
	m := &models.RosterMember{
		Days: []string{"mon", "tue", "wed", "thu"},
		Availability: map[string]models.DayWindow{
			"mon": {Start: 18, End: 22},
			"tue": {Start: 18, End: 22}, // duplicate of Monday
			"wed": {Start: 20, End: 23},
			"thu": {Start: 0, End: 0}, // not yet filled in
		},
	}

	rows := quickSpanRows("42", "fri", m)
	btns := buttonsFrom(t, rows)

	var labels []string
	for _, b := range btns {
		labels = append(labels, b.Label)
	}
	want := []string{"All day (00:00–24:00)", "18:00–22:00", "20:00–23:00"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Errorf("labels = %v, want %v (duplicates and empty windows dropped)", labels, want)
	}
	if btns[0].Style != discordgo.PrimaryButton {
		t.Error("the All day preset should be the prominent button")
	}
	if !strings.HasPrefix(btns[1].CustomID, "signup_span:42:fri~18~22") {
		t.Errorf("custom id = %q, want it to carry the day and window", btns[1].CustomID)
	}
}

// TestQuickSpanRowsExcludesTheCurrentDay avoids offering the day's own
// half-entered window back to the user.
func TestQuickSpanRowsExcludesTheCurrentDay(t *testing.T) {
	m := &models.RosterMember{
		Days:         []string{"mon"},
		Availability: map[string]models.DayWindow{"mon": {Start: 18, End: 22}},
	}

	btns := buttonsFrom(t, quickSpanRows("1", "mon", m))

	if len(btns) != 1 {
		t.Fatalf("got %d buttons, want only the All day preset", len(btns))
	}
}

// TestQuickSpanRowsRespectsTheButtonBudget keeps within the three action rows
// left after the two selects the step already uses.
func TestQuickSpanRowsRespectsTheButtonBudget(t *testing.T) {
	m := &models.RosterMember{Availability: map[string]models.DayWindow{}}
	for h := 1; h <= 23; h++ {
		day := time.Month(h).String()
		m.Days = append(m.Days, day)
		m.Availability[day] = models.DayWindow{Start: 0, End: h}
	}

	rows := quickSpanRows("1", "fri", m)
	btns := buttonsFrom(t, rows)

	if len(btns) > 15 {
		t.Errorf("got %d buttons, want at most 15", len(btns))
	}
	for idx, r := range rows {
		if n := len(r.(discordgo.ActionsRow).Components); n > 5 {
			t.Errorf("row %d has %d buttons, want at most 5", idx, n)
		}
	}
}

func TestSignupSummary(t *testing.T) {
	m := &models.RosterMember{
		Timezone: "America/Toronto",
		Days:     []string{"mon", "wed"},
		Availability: map[string]models.DayWindow{
			"mon": {Start: 18, End: 22},
		},
		Roles:         []string{"tank", "healer"},
		ClassesByRole: map[string][]string{"tank": {"dragonknight"}},
	}

	got := signupSummary(m)

	for _, want := range []string{
		"__Availability__ (America/Toronto)",
		"Mon 18:00–22:00",
		"Wed", // no window recorded, so no times
		"__Roles & Classes__",
		"Tank: Dragonknight",
		"Healer", // no classes recorded, so no colon list
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Wed 00:00") {
		t.Errorf("a day with no window should show no times:\n%s", got)
	}
}

func TestSignupSummaryOmitsEmptySections(t *testing.T) {
	got := signupSummary(&models.RosterMember{})

	if strings.Contains(got, "__Availability__") || strings.Contains(got, "__Roles & Classes__") {
		t.Errorf("empty sections should be omitted:\n%s", got)
	}
	if !strings.Contains(got, "All done") {
		t.Errorf("the confirmation line should still be present:\n%s", got)
	}
}

func TestSignupSummaryWithoutATimezone(t *testing.T) {
	m := &models.RosterMember{
		Days:         []string{"mon"},
		Availability: map[string]models.DayWindow{"mon": {Start: 18, End: 22}},
	}

	got := signupSummary(m)

	if !strings.Contains(got, "__Availability__\n") {
		t.Errorf("want a bare Availability header with no timezone:\n%s", got)
	}
}

// --- scheduler backoff ---

// TestCleanupBackoff doubles per failure and saturates, so a persistently
// failing cleanup backs off instead of hammering Discord every tick.
func TestCleanupBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{-1, 5 * time.Minute}, // treated as the first attempt
		{0, 5 * time.Minute},
		{1, 5 * time.Minute},
		{2, 10 * time.Minute},
		{3, 20 * time.Minute},
		{4, 40 * time.Minute},
		{7, 5 * time.Minute << 6}, // 5h20m, still under the cap
		{8, 6 * time.Hour},        // would be 10h40m, so capped
		{100, 6 * time.Hour},      // shifting past the word size must not wrap
	}
	for _, tc := range cases {
		if got := cleanupBackoff(tc.attempt); got != tc.want {
			t.Errorf("cleanupBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

// TestCleanupBackoffIsMonotonic is the property the individual cases above only
// sample: waiting longer must never get shorter as failures accumulate.
func TestCleanupBackoffIsMonotonic(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 1; attempt <= 64; attempt++ {
		got := cleanupBackoff(attempt)
		if got < prev {
			t.Fatalf("attempt %d backed off %v, less than the previous %v", attempt, got, prev)
		}
		if got > 6*time.Hour {
			t.Fatalf("attempt %d exceeded the cap with %v", attempt, got)
		}
		prev = got
	}
}

// --- /coreteam roll ---

// TestTrialGroups drops the catch-all "General" bucket so a roll always lands
// on a real trial.
func TestTrialGroups(t *testing.T) {
	groups := trialGroups()

	if len(groups) == 0 {
		t.Fatal("got no trial groups")
	}
	for _, g := range groups {
		if g.Group == models.GeneralEncounterGroup {
			t.Errorf("the %q group should be excluded from the roll pool", g.Group)
		}
	}
	if len(groups) >= len(models.EncounterNameGroups) {
		t.Error("expected at least the General group to be filtered out")
	}
}
