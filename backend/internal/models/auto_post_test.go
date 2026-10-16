package models

import "testing"

// TestAutoPostSettingsValidate defends the bounds the API enforces before a
// save. It covers what is rejected as well as what is accepted: durations are
// bounded even while a feature is off, and the channel is only required once
// auto post is on.
func TestAutoPostSettingsValidate(t *testing.T) {
	ok := AutoPostSettings{AutoPostLeadMinutes: 60, AutoDeleteDelayMinutes: 43200}
	cases := []struct {
		name   string
		mutate func(a *AutoPostSettings)
		valid  bool
	}{
		{"both off with defaults", func(a *AutoPostSettings) {}, true},
		{"auto post on with a channel", func(a *AutoPostSettings) { a.AutoPostEnabled = true; a.AutoPostChannelID = "1" }, true},
		{"auto post on without a channel", func(a *AutoPostSettings) { a.AutoPostEnabled = true }, false},
		{"lead 59 minutes", func(a *AutoPostSettings) { a.AutoPostLeadMinutes = 59 }, false},
		{"lead 43201 minutes", func(a *AutoPostSettings) { a.AutoPostLeadMinutes = 43201 }, false},
		{"delay 0", func(a *AutoPostSettings) { a.AutoDeleteDelayMinutes = 0 }, false},
		{"delay negative", func(a *AutoPostSettings) { a.AutoDeleteDelayMinutes = -1 }, false},
		{"delay too long while off", func(a *AutoPostSettings) { a.AutoDeleteDelayMinutes = 99999 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := ok
			tc.mutate(&a)
			if err := a.Validate(); (err == nil) != tc.valid {
				t.Errorf("Validate() = %v, want valid=%v", err, tc.valid)
			}
		})
	}
	if err := DefaultAutoPostSettings().Validate(); err != nil {
		t.Errorf("defaults must be storable, got %v", err)
	}
}
