package main

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// interactionWithPerms builds a guild interaction whose member holds perms.
func interactionWithPerms(perms int64) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		GuildID: "guild-1",
		Member: &discordgo.Member{
			User:        &discordgo.User{ID: "user-1"},
			Permissions: perms,
		},
	}}
}

// TestCanPostTeamContentAllowsChannelManagers covers the paths of the
// /coreteam post and /coreteam recruit gate that resolve without a store
// lookup. A bound channel names a team the invoker may not belong to, so the
// permission has to come from Discord: Manage Channels (the permission that
// bound the channel) or Manage Server / Administrator.
func TestCanPostTeamContentAllowsChannelManagers(t *testing.T) {
	b := &bot{}
	ctx := context.Background()

	cases := []struct {
		name  string
		perms int64
	}{
		{"manage channels", discordgo.PermissionManageChannels},
		{"manage guild", discordgo.PermissionManageGuild},
		{"administrator", discordgo.PermissionAdministrator},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := b.canPostTeamContent(ctx, interactionWithPerms(tc.perms))
			if err != nil {
				t.Fatalf("canPostTeamContent: %v", err)
			}
			if !ok {
				t.Error("got denied, want allowed")
			}
		})
	}
}

// TestCanPostTeamContentDeniesWithoutMember covers the fail-closed path for an
// interaction carrying no guild member (a DM), which has no permissions to
// read and no roles to match against.
func TestCanPostTeamContentDeniesWithoutMember(t *testing.T) {
	b := &bot{}
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{}}

	ok, err := b.canPostTeamContent(context.Background(), i)
	if err != nil {
		t.Fatalf("canPostTeamContent: %v", err)
	}
	if ok {
		t.Error("got allowed, want denied")
	}
}

// TestIsOfferedTimezone guards the select-menu allow-list: the values come back
// from the client, so anything the bot did not put in the menu is rejected
// rather than persisted onto the account.
func TestIsOfferedTimezone(t *testing.T) {
	if !isOfferedTimezone("Europe/London") {
		t.Error("Europe/London should be offered")
	}
	for _, tz := range []string{"", "Europe/london", "Mars/Olympus", "'; DROP TABLE users; --"} {
		if isOfferedTimezone(tz) {
			t.Errorf("%q should not be offered", tz)
		}
	}
}
