package discordfmt

import (
	"testing"
	"time"
)

// at builds a fixed UTC instant for the schedule tests.
func at(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
}

// TestNextRunUnixAt walks the weekly schedule arithmetic against a fixed clock.
// The schedule carries no date, so "next" always means the nearest strictly
// future match within the coming week — the wrap-around and the same-day
// before/after cases are where an off-by-one lands.
func TestNextRunUnixAt(t *testing.T) {
	// 2026-09-16 is a Wednesday.
	const wednesday = 16

	cases := []struct {
		name string
		days []string
		hhmm string
		now  time.Time
		want time.Time
	}{
		{
			name: "later today",
			days: []string{"wed"},
			hhmm: "20:00",
			now:  at(2026, time.September, wednesday, 10, 0),
			want: at(2026, time.September, wednesday, 20, 0),
		},
		{
			name: "already passed today, so next week",
			days: []string{"wed"},
			hhmm: "08:00",
			now:  at(2026, time.September, wednesday, 10, 0),
			want: at(2026, time.September, wednesday+7, 8, 0),
		},
		{
			name: "exactly now counts as passed",
			days: []string{"wed"},
			hhmm: "10:00",
			now:  at(2026, time.September, wednesday, 10, 0),
			want: at(2026, time.September, wednesday+7, 10, 0),
		},
		{
			name: "one minute from now still counts",
			days: []string{"wed"},
			hhmm: "10:01",
			now:  at(2026, time.September, wednesday, 10, 0),
			want: at(2026, time.September, wednesday, 10, 1),
		},
		{
			name: "later this week",
			days: []string{"fri"},
			hhmm: "20:00",
			now:  at(2026, time.September, wednesday, 10, 0),
			want: at(2026, time.September, wednesday+2, 20, 0),
		},
		{
			name: "wraps to next week",
			days: []string{"mon"},
			hhmm: "20:00",
			now:  at(2026, time.September, wednesday, 10, 0),
			want: at(2026, time.September, wednesday+5, 20, 0),
		},
		{
			name: "picks the soonest of several days",
			days: []string{"sun", "thu", "mon"},
			hhmm: "20:00",
			now:  at(2026, time.September, wednesday, 10, 0),
			want: at(2026, time.September, wednesday+1, 20, 0),
		},
		{
			name: "day order does not matter",
			days: []string{"mon", "thu", "sun"},
			hhmm: "20:00",
			now:  at(2026, time.September, wednesday, 10, 0),
			want: at(2026, time.September, wednesday+1, 20, 0),
		},
		{
			name: "crosses a month boundary",
			days: []string{"fri"},
			hhmm: "20:00",
			now:  at(2026, time.September, 30, 10, 0), // Wednesday
			want: at(2026, time.October, 2, 20, 0),
		},
		{
			name: "crosses a year boundary",
			days: []string{"fri"},
			hhmm: "20:00",
			now:  at(2026, time.December, 31, 10, 0), // Thursday
			want: at(2027, time.January, 1, 20, 0),
		},
		{
			name: "day keys are case and space insensitive",
			days: []string{" WED ", "Fri"},
			hhmm: "20:00",
			now:  at(2026, time.September, wednesday, 10, 0),
			want: at(2026, time.September, wednesday, 20, 0),
		},
		{
			name: "midnight is a valid time",
			days: []string{"thu"},
			hhmm: "00:00",
			now:  at(2026, time.September, wednesday, 23, 0),
			want: at(2026, time.September, wednesday+1, 0, 0),
		},
		{
			name: "a non-UTC now is converted before matching",
			// 2026-09-16 21:00 -05:00 is 2026-09-17 02:00 UTC, a Thursday, so a
			// Thursday 20:00 UTC run is still ahead.
			days: []string{"thu"},
			hhmm: "20:00",
			now:  time.Date(2026, time.September, wednesday, 21, 0, 0, 0, time.FixedZone("CDT", -5*60*60)),
			want: at(2026, time.September, wednesday+1, 20, 0),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nextRunUnixAt(tc.days, tc.hhmm, tc.now)
			if !ok {
				t.Fatalf("got ok = false, want a run at %s", tc.want)
			}
			if got != tc.want.Unix() {
				t.Errorf("next run = %s, want %s", time.Unix(got, 0).UTC(), tc.want)
			}
		})
	}
}

// TestNextRunUnixAtUnset covers the inputs that have no next run, which the
// callers render as the plain day/time text (or no "Next run" line at all)
// rather than a dynamic timestamp.
func TestNextRunUnixAtUnset(t *testing.T) {
	now := at(2026, time.September, 16, 10, 0)

	cases := []struct {
		name string
		days []string
		hhmm string
	}{
		{"no days", nil, "20:00"},
		{"empty days", []string{}, "20:00"},
		{"no time", []string{"wed"}, ""},
		{"unparseable time", []string{"wed"}, "not a time"},
		{"out-of-range time", []string{"wed"}, "25:00"},
		{"12-hour time", []string{"wed"}, "8:00 PM"},
		{"unknown day keys only", []string{"someday", ""}, "20:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if unix, ok := nextRunUnixAt(tc.days, tc.hhmm, now); ok {
				t.Errorf("got %d, want no next run", unix)
			}
		})
	}
}

// TestNextRunUnixAtIgnoresUnknownDays keeps one bad day key from discarding the
// whole schedule — the recognized days still produce a run.
func TestNextRunUnixAtIgnoresUnknownDays(t *testing.T) {
	now := at(2026, time.September, 16, 10, 0) // Wednesday

	got, ok := nextRunUnixAt([]string{"someday", "fri"}, "20:00", now)
	if !ok {
		t.Fatal("got ok = false, want the recognized day to still schedule a run")
	}
	if want := at(2026, time.September, 18, 20, 0); got != want.Unix() {
		t.Errorf("next run = %s, want %s", time.Unix(got, 0).UTC(), want)
	}
}

// TestNextRunUnixUsesTheRealClock checks the exported wrapper is actually wired
// to time.Now, since the fixed-clock tests above would pass either way.
func TestNextRunUnixUsesTheRealClock(t *testing.T) {
	// Every day at a minute past the hour: the next occurrence is always within
	// the next hour, whenever the test happens to run.
	days := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	hhmm := time.Now().UTC().Add(30 * time.Minute).Format("15:04")

	got, ok := NextRunUnix(days, hhmm)
	if !ok {
		t.Fatal("got ok = false, want a next run for a daily schedule")
	}
	delta := time.Until(time.Unix(got, 0))
	if delta <= 0 || delta > time.Hour {
		t.Errorf("next run is %v away, want it within the next hour", delta)
	}
}
