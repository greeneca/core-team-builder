package main

import (
	"testing"
	"time"
)

// TestAutoPostDue defends when the scheduler posts: only inside the lead window
// before a known run, and only once per run. Posting after the run starts, or a
// second time for the same run, would spam the channel.
func TestAutoPostDue(t *testing.T) {
	run := time.Date(2026, 10, 18, 19, 0, 0, 0, time.UTC)
	lead := 24 * time.Hour
	at := func(d time.Duration) time.Time { return run.Add(d) }
	prev := run.Add(-7 * 24 * time.Hour)

	cases := []struct {
		name string
		now  time.Time
		run  int64
		last *time.Time
		want bool
	}{
		{"before the window", at(-lead - time.Minute), run.Unix(), nil, false},
		{"exactly at window start", at(-lead), run.Unix(), nil, true},
		{"inside the window", at(-time.Hour), run.Unix(), nil, true},
		{"at run start", at(0), run.Unix(), nil, false},
		{"after the run", at(time.Hour), run.Unix(), nil, false},
		{"already posted for this run", at(-time.Hour), run.Unix(), &run, false},
		{"last post was the previous run", at(-time.Hour), run.Unix(), &prev, true},
		{"no concrete schedule", at(-time.Hour), 0, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoPostDue(tc.now, tc.run, lead, tc.last); got != tc.want {
				t.Errorf("autoPostDue = %v, want %v", got, tc.want)
			}
		})
	}
}
