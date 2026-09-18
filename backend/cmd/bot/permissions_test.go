package main

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// fakeDiscordStore stands in for models.DiscordStore. The embedded interface
// leaves every method the test does not exercise nil, so calling an unexpected
// one panics with a clear stack instead of silently succeeding.
type fakeDiscordStore struct {
	discordStore
	editRoles    []string
	editRolesErr error
	listCalls    int
}

func (f *fakeDiscordStore) ListEditRoles(_ context.Context, _ string) ([]string, error) {
	f.listCalls++
	if f.editRolesErr != nil {
		return nil, f.editRolesErr
	}
	return f.editRoles, nil
}

// interactionWithRoles builds a guild interaction whose member holds perms and
// the given Discord roles.
func interactionWithRoles(perms int64, roles ...string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		GuildID: "guild-1",
		Member: &discordgo.Member{
			User:        &discordgo.User{ID: "user-1"},
			Permissions: perms,
			Roles:       roles,
		},
	}}
}

// TestCanPostTeamContentAllowsChannelManagers covers the paths of the
// /coreteam post and /coreteam recruit gate that resolve without a store
// lookup. A bound channel names a team the invoker may not belong to, so the
// permission has to come from Discord: Manage Channels (the permission that
// bound the channel) or Manage Server / Administrator.
//
// The store is a fake rather than nil so the test asserts "allowed" on its own
// terms; if a refactor reorders the checks it fails on the permission decision
// instead of panicking.
func TestCanPostTeamContentAllowsChannelManagers(t *testing.T) {
	store := &fakeDiscordStore{}
	b := &bot{discord: store}
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
			ok, err := b.canPostTeamContent(ctx, interactionWithRoles(tc.perms))
			if err != nil {
				t.Fatalf("canPostTeamContent: %v", err)
			}
			if !ok {
				t.Error("got denied, want allowed")
			}
		})
	}

	if store.listCalls != 0 {
		t.Errorf("ListEditRoles called %d times, want the Discord permission to decide on its own", store.listCalls)
	}
}

// TestCanPostTeamContentAllowsDesignatedRole covers the delegation path a
// server configures with /coreteam permissions add: a member with no Discord
// permission at all may post because they hold a role the guild designated.
func TestCanPostTeamContentAllowsDesignatedRole(t *testing.T) {
	b := &bot{discord: &fakeDiscordStore{editRoles: []string{"role-raid-lead"}}}
	i := interactionWithRoles(0, "role-member", "role-raid-lead")

	ok, err := b.canPostTeamContent(context.Background(), i)
	if err != nil {
		t.Fatalf("canPostTeamContent: %v", err)
	}
	if !ok {
		t.Error("got denied, want a designated role allowed")
	}
}

// TestCanPostTeamContentDeniesUndesignatedRole is the other half: holding
// *some* role is not enough, only one on the guild's list.
func TestCanPostTeamContentDeniesUndesignatedRole(t *testing.T) {
	b := &bot{discord: &fakeDiscordStore{editRoles: []string{"role-raid-lead"}}}

	cases := []struct {
		name      string
		editRoles []string
		roles     []string
	}{
		{"role not on the list", []string{"role-raid-lead"}, []string{"role-member"}},
		{"guild designated nothing", nil, []string{"role-member"}},
		{"member holds no roles", []string{"role-raid-lead"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b.discord = &fakeDiscordStore{editRoles: tc.editRoles}
			ok, err := b.canPostTeamContent(context.Background(), interactionWithRoles(0, tc.roles...))
			if err != nil {
				t.Fatalf("canPostTeamContent: %v", err)
			}
			if ok {
				t.Error("got allowed, want denied")
			}
		})
	}
}

// TestCanPostTeamContentPropagatesLookupError checks the gate fails closed when
// the designated-role lookup errors: no decision is returned alongside the
// error, so a database blip cannot read as permission granted.
func TestCanPostTeamContentPropagatesLookupError(t *testing.T) {
	wantErr := errors.New("database is down")
	b := &bot{discord: &fakeDiscordStore{editRolesErr: wantErr}}

	ok, err := b.canPostTeamContent(context.Background(), interactionWithRoles(0, "role-member"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if ok {
		t.Error("got allowed, want denied on a failed lookup")
	}
}

// TestCanPostTeamContentDeniesWithoutMember covers the fail-closed path for an
// interaction carrying no guild member (a DM), which has no permissions to
// read and no roles to match against.
func TestCanPostTeamContentDeniesWithoutMember(t *testing.T) {
	store := &fakeDiscordStore{editRoles: []string{"role-raid-lead"}}
	b := &bot{discord: store}
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{}}

	ok, err := b.canPostTeamContent(context.Background(), i)
	if err != nil {
		t.Fatalf("canPostTeamContent: %v", err)
	}
	if ok {
		t.Error("got allowed, want denied")
	}
	if store.listCalls != 0 {
		t.Errorf("ListEditRoles called %d times, want no lookup for an interaction with no member", store.listCalls)
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
