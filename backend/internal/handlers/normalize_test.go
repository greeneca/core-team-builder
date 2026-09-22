package handlers

import (
	"reflect"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

// The normalizers below are the trust boundary between client-supplied JSON and
// the roster-member rows. They run before anything is persisted, so their
// behaviour is pinned here directly rather than only through the endpoints.

// TestNormalizeDays covers the day list the availability grid posts: it is
// lower-cased, de-duplicated, and returned in week order regardless of the
// order the client sent, because the Discord post renders it verbatim.
func TestNormalizeDays(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"sorted into week order", []string{"fri", "mon"}, []string{"mon", "fri"}},
		{"case and padding ignored", []string{" MON ", "Tue"}, []string{"mon", "tue"}},
		{"duplicates collapse", []string{"wed", "wed", "wed"}, []string{"wed"}},
		{"blanks skipped", []string{"", "  ", "sat"}, []string{"sat"}},
		{"full week", []string{"sun", "sat", "fri", "thu", "wed", "tue", "mon"},
			[]string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeDays(tc.in)
			if err != nil {
				t.Fatalf("normalizeDays(%v) returned %v, want no error", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNormalizeDaysRejectsAnUnknownDay covers the one input that is an error
// rather than something to drop: an unrecognised day is a client bug, and the
// message names it so the caller can see which one.
func TestNormalizeDaysRejectsAnUnknownDay(t *testing.T) {
	got, err := normalizeDays([]string{"mon", "funday"})
	if err == nil {
		t.Fatalf("normalizeDays returned %v, want an error", got)
	}
	if want := "invalid day: funday"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestNormalizeRoles covers the role filter: unknown roles are dropped rather
// than rejected, so an older or buggy client cannot write junk into the pool.
func TestNormalizeRoles(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"input order preserved", []string{"healer", "tank"}, []string{"healer", "tank"}},
		{"case and padding ignored", []string{" TANK "}, []string{"tank"}},
		{"duplicates collapse", []string{"dps", "dps"}, []string{"dps"}},
		{"unknown dropped", []string{"tank", "necromancer", "support_dps"}, []string{"tank", "support_dps"}},
		// "" is in models.ValidRoles (it is the unset player role) but is not a
		// role a pool member can claim, so it is skipped before the lookup.
		{"blank dropped", []string{"", "healer"}, []string{"healer"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeRoles(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNormalizeClassesByRole covers the per-role class map: both the key and
// the values are validated, and a role whose classes are all invalid is dropped
// entirely rather than left as an empty list.
func TestNormalizeClassesByRole(t *testing.T) {
	tests := []struct {
		name string
		in   map[string][]string
		want map[string][]string
	}{
		{"empty", nil, map[string][]string{}},
		{
			"kept and lower-cased",
			map[string][]string{" Tank ": {"Dragonknight", "warden"}},
			map[string][]string{"tank": {"dragonknight", "warden"}},
		},
		{
			"unknown role dropped",
			map[string][]string{"raidlead": {"sorcerer"}},
			map[string][]string{},
		},
		{
			"unknown class dropped",
			map[string][]string{"dps": {"sorcerer", "spellsword"}},
			map[string][]string{"dps": {"sorcerer"}},
		},
		{
			"role with no surviving class is dropped",
			map[string][]string{"healer": {"spellsword", ""}},
			map[string][]string{},
		},
		{
			"duplicates collapse",
			map[string][]string{"dps": {"templar", "templar"}},
			map[string][]string{"dps": {"templar"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeClassesByRole(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNormalizeAvailability covers the window map: entries for days the member
// did not pick are discarded, so availability can never describe a day the
// scheduler will not look at.
func TestNormalizeAvailability(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]models.DayWindow
		days []string
		want map[string]models.DayWindow
	}{
		{"empty", nil, []string{"mon"}, map[string]models.DayWindow{}},
		{
			"kept for a chosen day",
			map[string]models.DayWindow{"mon": {Start: 18, End: 22}},
			[]string{"mon"},
			map[string]models.DayWindow{"mon": {Start: 18, End: 22}},
		},
		{
			"dropped for an unchosen day",
			map[string]models.DayWindow{"tue": {Start: 18, End: 22}},
			[]string{"mon"},
			map[string]models.DayWindow{},
		},
		{
			"key case ignored",
			map[string]models.DayWindow{" MON ": {Start: 18, End: 22}},
			[]string{"mon"},
			map[string]models.DayWindow{"mon": {Start: 18, End: 22}},
		},
		{
			// End clamps to 24 rather than 23: 24 means "midnight / end of day",
			// which is how a window running to the end of the day is expressed.
			"hours clamped to range",
			map[string]models.DayWindow{"mon": {Start: -5, End: 99}},
			[]string{"mon"},
			map[string]models.DayWindow{"mon": {Start: 0, End: 24}},
		},
		{
			"zero end clamps up to one",
			map[string]models.DayWindow{"mon": {Start: 0, End: 0}},
			[]string{"mon"},
			map[string]models.DayWindow{"mon": {Start: 0, End: 1}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeAvailability(tc.in, tc.days); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClampHour(t *testing.T) {
	tests := []struct {
		name            string
		h, lo, hi, want int
	}{
		{"inside the range", 12, 0, 23, 12},
		{"below the floor", -1, 0, 23, 0},
		{"above the ceiling", 30, 0, 23, 23},
		{"at the floor", 0, 0, 23, 0},
		{"at the ceiling", 23, 0, 23, 23},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampHour(tc.h, tc.lo, tc.hi); got != tc.want {
				t.Errorf("clampHour(%d, %d, %d) = %d, want %d", tc.h, tc.lo, tc.hi, got, tc.want)
			}
		})
	}
}
