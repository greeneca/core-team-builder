package esoref

import "testing"

// TestLabelsFallBackToTheRawKey is the property every lookup shares: reference
// data is generated from the frontend, so a key the tables don't know about
// must still render as something rather than an empty string.
func TestLabelsFallBackToTheRawKey(t *testing.T) {
	const unknown = "not_a_real_key"

	lookups := map[string]func(string) string{
		"GearLabel":              GearLabel,
		"SkillLabel":             SkillLabel,
		"MasteryLabel":           MasteryLabel,
		"SkillLineLabel":         SkillLineLabel,
		"PotionLabel":            PotionLabel,
		"MundusLabel":            MundusLabel,
		"CPBlueLabel":            CPBlueLabel,
		"CritDmgLabel":           CritDmgLabel,
		"PenExtraLabel":          PenExtraLabel,
		"ScribedBuffLabel":       ScribedBuffLabel,
		"BannerBearerFocusLabel": BannerBearerFocusLabel,
		"RoleLabel":              RoleLabel,
		"ClassLabel":             ClassLabel,
		"RaceLabel":              RaceLabel,
		"DayLabel":               DayLabel,
	}
	for name, fn := range lookups {
		if got := fn(unknown); got != unknown {
			t.Errorf("%s(%q) = %q, want the raw key back", name, unknown, got)
		}
	}
}

func TestKnownLabels(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"gear", GearLabel("roar_of_alkosh"), "Roar of Alkosh"},
		{"skill", SkillLabel("pierce_armor"), "Pierce Armor"},
		{"class", ClassLabel("nightblade"), "Nightblade"},
		{"race", RaceLabel("breton"), "Breton"},
		{"day", DayLabel("wed"), "Wed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// TestClassAndRaceLabelEmpty covers the one place these two differ from the
// other lookups: an unset value renders as an em dash, not an empty cell.
func TestClassAndRaceLabelEmpty(t *testing.T) {
	if got := ClassLabel(""); got != "—" {
		t.Errorf("ClassLabel(\"\") = %q, want an em dash", got)
	}
	if got := RaceLabel(""); got != "—" {
		t.Errorf("RaceLabel(\"\") = %q, want an em dash", got)
	}
}

func TestGearAbbrev(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"curated abbreviation wins", "roar_of_alkosh", "Alkosh"},
		{"curated short form", "lucent_echoes", "LE"},
		// No curated entry: build an acronym from the label, dropping the
		// filler words "of", "the" and "and".
		{"derived acronym", "mighty_glacier", "MG"},
		// A single remaining word is used whole rather than reduced to one
		// letter, which would be useless in the roster line.
		{"single word label", "nunatak", "Nunatak"},
		{"unknown key", "not_a_set", "not_a_set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GearAbbrev(tc.in); got != tc.want {
				t.Errorf("GearAbbrev(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripParens(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no parens", "Pillar of Nirn", "Pillar of Nirn"},
		{"trailing group", "Sharpened (single)", "Sharpened"},
		{"leading group", "(perfected) Lucent Echoes", "Lucent Echoes"},
		{"several groups", "A (one) B (two)", "A  B"},
		// An unbalanced parenthesis must not spin or truncate the rest.
		{"unterminated", "Weird (thing", "Weird (thing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripParens(tc.in); got != tc.want {
				t.Errorf("stripParens(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsGrimoireSkill(t *testing.T) {
	if !IsGrimoireSkill("banner_bearer") {
		t.Error("banner_bearer is a grimoire skill")
	}
	if IsGrimoireSkill("pierce_armor") {
		t.Error("pierce_armor is not a grimoire skill")
	}
	if IsGrimoireSkill("") {
		t.Error("the empty key is not a grimoire skill")
	}
}

// TestCoverageTablesArePopulated guards the generated data itself: a botched
// `go generate` that emptied these would silently make every team look like it
// covers nothing, and the discordfmt calculations would quietly go wrong.
func TestCoverageTablesArePopulated(t *testing.T) {
	if len(CritGroupSources) == 0 {
		t.Error("CritGroupSources is empty")
	}
	if len(PenGroupSources) == 0 {
		t.Error("PenGroupSources is empty")
	}
	if len(PenExtraSources) == 0 {
		t.Error("PenExtraSources is empty")
	}
	if len(Buffs) == 0 {
		t.Error("Buffs is empty")
	}
	if CritCap <= CritBase {
		t.Errorf("CritCap %d must exceed CritBase %d", CritCap, CritBase)
	}
	if PenTarget <= 0 {
		t.Errorf("PenTarget = %d, want a positive target", PenTarget)
	}
}

// TestPenExtraBucketsAreKnown keeps the generator honest: discordfmt switches
// on these two bucket names, so a third would be silently ignored.
func TestPenExtraBucketsAreKnown(t *testing.T) {
	for _, s := range PenExtraSources {
		if s.Bucket != "group" && s.Bucket != "self" {
			t.Errorf("%s has unknown bucket %q", s.Value, s.Bucket)
		}
	}
}
