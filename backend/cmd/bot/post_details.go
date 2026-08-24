package main

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/discordfmt"
	"github.com/core-team-builder/backend/internal/models"
)

// The "Build Details" button on a /coreteam post overview DMs the presser the
// build for their own roster slot, with a picker attached for looking up any
// other slot's build. Everyone who can see the post can use the picker — the
// post already lists the roster with abbreviated gear, so this is the same
// information in full.
//
// The picker is the whole answer for someone with no slot of their own (nobody's
// handle matches theirs, or they're on the general fill list): rather than a
// dead end, they get the same lookup, which also lets a prospective filler read
// what an open slot expects before signing up.
//
// The build and its picker live in one DM that each pick rewrites in place, so
// looking through several players doesn't fill the conversation with copies.
// See post_dm.go for how the DM is delivered, what happens when DMs are closed,
// and why the post's guild and channel travel in the picker's custom ID.
const detailsPickID = "post_details_pick" // + ":<guildID>:<channelID>:<messageID>:<ownSlot>"

// handleGetMyDetails answers the post's build-details button: it resolves the
// presser's own slot (roster handle, else the open slot they signed up to fill)
// and DMs that build with the picker for looking up anyone else's.
func (b *bot) handleGetMyDetails(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	if i.Message == nil {
		ephemeral(s, i, "Could not find the post to read.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	// The button still sits on the post, so the interaction's own guild, channel
	// and message are the ones the rest of the flow has to be told about.
	origin := postOrigin{guildID: i.GuildID, channelID: i.ChannelID, messageID: i.Message.ID}

	team, encs, gr, ok := b.detailsTeamData(ctx, s, i, origin.channelID, false)
	if !ok {
		return
	}

	player, matched := matchPlayer(team, user)
	onFillList := false
	if !matched {
		// A user who signed up to fill an open slot via the dropdown has no
		// roster handle, so match them to the slot they filled instead. Someone
		// on the general fill list isn't tied to a slot, so they have no build of
		// their own — only the picker.
		p, found, fillList := b.fillSignupPlayer(ctx, origin.messageID, team, user)
		player, matched, onFillList = p, found, fillList
	}

	ownSlot := 0
	if matched {
		ownSlot = player.Slot
	}
	picker := b.detailsPicker(origin, team, ownSlot)

	if !matched {
		openFlowInDM(s, i, user.ID, detailsNoSlotNote(user, team, onFillList, picker != nil), nil, picker)
		return
	}
	content := "Here's your build for **" + team.Name + "**." + detailsBrowseHint(picker)
	openFlowInDM(s, i, user.ID, content, playerDetailEmbed(team, player, encs, gr), picker)
}

// handleDetailsPick rewrites the flow message with the chosen slot's build,
// leaving the picker in place so the user can look up another.
func (b *bot) handleDetailsPick(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	origin, ok := parsePostOrigin(parts, 1)
	if !ok || len(parts) != 5 {
		return
	}
	ownSlot, _ := strconv.Atoi(parts[4])

	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	slot, err := strconv.Atoi(values[0])
	if err != nil {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	team, encs, gr, ok := b.detailsTeamData(ctx, s, i, origin.channelID, true)
	if !ok {
		return
	}
	player, found := playerForSlot(team, slot)
	if !found {
		endFlowStep(s, i, "That slot is no longer on the roster. Press **Build Details** on the post again for the current lineup.")
		return
	}

	picker := b.detailsPicker(origin, team, ownSlot)
	content := "Here's your build for **" + team.Name + "**."
	if slot != ownSlot {
		content = "Here's **" + detailsSlotName(player, b.rosterNamesFromCache(origin.guildID, team)) + "**'s build."
	}
	updateFlowStep(s, i, content+detailsBrowseHint(picker), playerDetailEmbed(team, player, encs, gr), picker)
}

// detailsTeamData loads the post channel's team with the encounters and
// groupings PlayerDetail needs, replying with the reason and reporting ok=false
// when it can't. inFlow selects the reply style: the button's own failures are
// answered with a fresh ephemeral (nothing has been sent yet), while a failure
// mid-flow rewrites the message the control sits on.
func (b *bot) detailsTeamData(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, channelID string, inFlow bool) (*models.Team, []models.Encounter, []models.Grouping, bool) {
	fail := func(msg string) {
		if inFlow {
			endFlowStep(s, i, msg)
			return
		}
		ephemeral(s, i, msg)
	}
	teamID, err := b.discord.GetChannelTeam(ctx, channelID)
	if errors.Is(err, models.ErrChannelNotBound) {
		fail("This channel isn't bound to a team anymore.")
		return nil, nil, nil, false
	}
	if err != nil {
		log.Printf("details: get binding: %v", err)
		fail("Something went wrong. Please try again.")
		return nil, nil, nil, false
	}
	team, encs, _, gr, err := b.loadTeamData(ctx, teamID)
	if err != nil {
		log.Printf("details: load team: %v", err)
		fail("Could not load the team. Please try again.")
		return nil, nil, nil, false
	}
	return team, encs, gr, true
}

// detailsPicker builds the "view a player's build" select, or nil when the team
// has no roster to pick from (so callers omit the control). ownSlot is the
// presser's own slot, marked in the list and carried in the custom ID so the
// next render can mark it again.
func (b *bot) detailsPicker(origin postOrigin, team *models.Team, ownSlot int) []discordgo.MessageComponent {
	// Cached names only — this runs while the interaction is still waiting on its
	// acknowledgement, so it has to stay inside Discord's 3-second window.
	opts := detailsSlotOptions(team, b.rosterNamesFromCache(origin.guildID, team), ownSlot)
	if len(opts) == 0 {
		return nil
	}
	id := detailsPickID + ":" + origin.encode() + ":" + strconv.Itoa(ownSlot)
	return selectRow(id, "View a player's build", 1, 1, opts)
}

// detailsSlotOptions lists every roster slot, open ones included: a slot with
// nobody assigned still has a role, class, and loadout, which is what a filler
// weighing it up wants to read. Capped at Discord's 25-option select limit.
func detailsSlotOptions(team *models.Team, names map[int]string, ownSlot int) []discordgo.SelectMenuOption {
	opts := make([]discordgo.SelectMenuOption, 0, len(team.Players))
	for _, p := range team.Players { // store returns players slot-ordered
		if len(opts) >= 25 {
			break
		}
		desc := slotOptionLabel(team, p)
		if p.Slot == ownSlot {
			desc += " · your slot"
		}
		opts = append(opts, discordgo.SelectMenuOption{
			Label:       truncate(detailsSlotName(p, names), 100),
			Value:       strconv.Itoa(p.Slot),
			Description: truncate(desc, 100),
			Emoji:       &discordgo.ComponentEmoji{Name: team.RoleEmoji(p.Role)},
		})
	}
	return opts
}

// detailsSlotName names a slot in the picker, falling back to "Open slot N" for
// a slot with neither a player nor a handle on it.
func detailsSlotName(p models.Player, names map[int]string) string {
	if n := rosterPlayerName(p, names); n != "" {
		return n
	}
	return "Open slot " + strconv.Itoa(p.Slot)
}

// detailsNoSlotNote explains why the presser has no build of their own, and
// points them at the picker when there is one.
func detailsNoSlotNote(user *discordgo.User, team *models.Team, onFillList, hasPicker bool) string {
	note := "You're not on **" + team.Name + "** — no roster slot matches your Discord handle, and you haven't signed up to fill an open slot. Ask your raid lead to set your handle to `" + displayName(user) + "`, or use the signup dropdown on the post to fill an open slot."
	if onFillList {
		note = "You're on the fill list for **" + team.Name + "**, which isn't tied to a specific slot, so there's no build of your own to send yet. Sign up for an open slot to get its build details."
	}
	if hasPicker {
		note += " In the meantime, pick any slot below to see its build."
	}
	return note
}

// detailsBrowseHint invites the user into the picker, when there is one.
func detailsBrowseHint(picker []discordgo.MessageComponent) string {
	if picker == nil {
		return ""
	}
	return " Pick a player below to see anyone else's build."
}

// playerDetailEmbed renders a slot's full build as the boxed embed the flow
// message carries.
func playerDetailEmbed(team *models.Team, player models.Player, encs []models.Encounter, gr []models.Grouping) *discordgo.MessageEmbed {
	title, desc := discordfmt.PlayerDetail(team, player, encs, gr)
	return &discordgo.MessageEmbed{
		Title:       truncate(title, embedTitleLimit),
		Description: truncate(desc, embedDescriptionLimit),
		Color:       embedColor,
	}
}

// fillSignupPlayer resolves the user's signup on this post (via the signup
// dropdown). When they filled an open slot, it returns that slot's roster player
// (found=true). When they're on the general fill list (no specific slot), it
// returns onFillList=true so callers can explain there's no build to send.
func (b *bot) fillSignupPlayer(ctx context.Context, messageID string, team *models.Team, user *discordgo.User) (player models.Player, found, onFillList bool) {
	fills, err := b.discord.ListFills(ctx, messageID)
	if err != nil {
		log.Printf("details: list fills: %v", err)
		return models.Player{}, false, false
	}
	for _, f := range fills {
		if f.DiscordUserID != user.ID {
			continue
		}
		if f.Slot == models.PostFillList {
			return models.Player{}, false, true
		}
		for _, p := range team.Players {
			if p.Slot == f.Slot {
				return p, true, false
			}
		}
	}
	return models.Player{}, false, false
}
