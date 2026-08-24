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

// The "Build Details" button on a /coreteam post overview DMs the presser
// the build for their own roster slot, and its reply carries a picker for
// looking up any other slot's build. Everyone who can see the post can use the
// picker — the post already lists the roster with abbreviated gear, so this is
// the same information in full.
//
// The picker is the whole answer for someone with no slot of their own (nobody's
// handle matches theirs, or they're on the general fill list): rather than a
// dead end, they get the same lookup, which also lets a prospective filler read
// what an open slot expects before signing up.
//
// Every pick is delivered the same way a user's own build is — DM'd, falling
// back to an ephemeral embed when DMs are closed — and the picker is re-attached
// to the reply so several builds can be pulled without pressing the button
// again. The picker rides on its own ephemeral, whose message is not the post,
// so it carries the presser's own slot in its custom ID (0 when they have none)
// to keep marking it across re-renders.
const detailsPickID = "post_details_pick" // + ":<ownSlot>"

// handleGetMyDetails answers the post's build-details button: it resolves the
// presser's own slot (roster handle, else the open slot they signed up to fill),
// DMs that build, and offers the picker for looking up anyone else's.
func (b *bot) handleGetMyDetails(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	team, encs, gr, ok := b.detailsTeamData(ctx, s, i, false)
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
		p, found, fillList := b.fillSignupPlayer(ctx, i, team, user)
		player, matched, onFillList = p, found, fillList
	}

	ownSlot := 0
	if matched {
		ownSlot = player.Slot
	}
	picker := b.detailsPicker(i.GuildID, team, ownSlot)

	if !matched {
		respondDetails(s, i, false, detailsNoSlotNote(user, onFillList, picker != nil), nil, picker)
		return
	}

	embed := playerDetailEmbed(team, player, encs, gr)
	if dmPlayerDetail(s, user.ID, embed) {
		respondDetails(s, i, false, "Sent your trial details via DM."+detailsBrowseHint(picker), nil, picker)
		return
	}
	// DMs likely closed — fall back to an ephemeral reply (boxed embed) only the
	// user sees.
	respondDetails(s, i, false, "I couldn't DM you (your DMs may be closed). Here are your details:", embed, picker)
}

// handleDetailsPick DMs the build for the slot chosen in the picker, then leaves
// the picker in place so the user can look up another.
func (b *bot) handleDetailsPick(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		updateEphemeral(s, i, "Could not identify your Discord account.")
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	slot, err := strconv.Atoi(values[0])
	if err != nil {
		return
	}
	ownSlot := 0
	if parts := strings.Split(i.MessageComponentData().CustomID, ":"); len(parts) == 2 {
		ownSlot, _ = strconv.Atoi(parts[1])
	}

	ctx, cancel := handlerContext()
	defer cancel()

	team, encs, gr, ok := b.detailsTeamData(ctx, s, i, true)
	if !ok {
		return
	}
	player, found := playerForSlot(team, slot)
	if !found {
		updateEphemeral(s, i, "That slot is no longer on the roster. Press **Build Details** again for the current lineup.")
		return
	}

	whose := "your"
	if slot != ownSlot {
		whose = "**" + detailsSlotName(player, b.rosterNamesFromCache(i.GuildID, team)) + "**'s"
	}
	embed := playerDetailEmbed(team, player, encs, gr)
	picker := b.detailsPicker(i.GuildID, team, ownSlot)
	if dmPlayerDetail(s, user.ID, embed) {
		respondDetails(s, i, true, "Sent "+whose+" build details via DM."+detailsBrowseHint(picker), nil, picker)
		return
	}
	respondDetails(s, i, true, "I couldn't DM you (your DMs may be closed). Here are "+whose+" build details:", embed, picker)
}

// detailsTeamData loads the channel's team with the encounters and groupings
// PlayerDetail needs, replying with the reason and reporting ok=false when it
// can't. update selects the reply style: the button creates a fresh ephemeral,
// while the picker must update its existing one.
func (b *bot) detailsTeamData(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, update bool) (*models.Team, []models.Encounter, []models.Grouping, bool) {
	fail := func(msg string) {
		if update {
			updateEphemeral(s, i, msg)
			return
		}
		ephemeral(s, i, msg)
	}
	teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
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
func (b *bot) detailsPicker(guildID string, team *models.Team, ownSlot int) []discordgo.MessageComponent {
	// Cached names only — this is the interaction's acknowledgement, so it has to
	// stay inside Discord's 3-second window.
	opts := detailsSlotOptions(team, b.rosterNamesFromCache(guildID, team), ownSlot)
	if len(opts) == 0 {
		return nil
	}
	return selectRow(detailsPickID+":"+strconv.Itoa(ownSlot), "View a player's build", 1, 1, opts)
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
func detailsNoSlotNote(user *discordgo.User, onFillList, hasPicker bool) string {
	note := "You're not on this trial — no roster slot matches your Discord handle, and you haven't signed up to fill an open slot. Ask your raid lead to set your handle to `" + displayName(user) + "`, or use the signup dropdown to fill an open slot."
	if onFillList {
		note = "You're on the fill list, which isn't tied to a specific slot, so there's no build of your own to send yet. Sign up for an open slot to get its build details."
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

// playerDetailEmbed renders a slot's full build as the boxed embed both the DM
// and the DMs-closed fallback send.
func playerDetailEmbed(team *models.Team, player models.Player, encs []models.Encounter, gr []models.Grouping) *discordgo.MessageEmbed {
	title, desc := discordfmt.PlayerDetail(team, player, encs, gr)
	return &discordgo.MessageEmbed{
		Title:       truncate(title, embedTitleLimit),
		Description: truncate(desc, embedDescriptionLimit),
		Color:       embedColor,
	}
}

// dmPlayerDetail DMs a build embed, reporting whether it landed. A false result
// means the user's DMs are closed (or the channel couldn't be opened), so the
// caller shows the embed ephemerally instead.
func dmPlayerDetail(s *discordgo.Session, userID string, embed *discordgo.MessageEmbed) bool {
	dm, err := s.UserChannelCreate(userID)
	if err != nil {
		return false
	}
	_, err = s.ChannelMessageSendEmbed(dm.ID, embed)
	return err == nil
}

// respondDetails delivers a details reply. update distinguishes the button's
// fresh ephemeral from the picker's in-place update; the latter always sends
// explicit (possibly empty) embeds and components, since anything omitted from
// an update is left as it was.
func respondDetails(s *discordgo.Session, i *discordgo.InteractionCreate, update bool, content string, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	embeds := []*discordgo.MessageEmbed{}
	if embed != nil {
		embeds = append(embeds, embed)
	}
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	data := &discordgo.InteractionResponseData{
		Content:    content,
		Embeds:     embeds,
		Components: components,
	}
	typ := discordgo.InteractionResponseUpdateMessage
	if !update {
		typ = discordgo.InteractionResponseChannelMessageWithSource
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: typ, Data: data}); err != nil {
		log.Printf("details: respond: %v", err)
	}
}

// fillSignupPlayer resolves the user's signup on this post (via the signup
// dropdown). When they filled an open slot, it returns that slot's roster player
// (found=true). When they're on the general fill list (no specific slot), it
// returns onFillList=true so callers can explain there's no build to send.
func (b *bot) fillSignupPlayer(ctx context.Context, i *discordgo.InteractionCreate, team *models.Team, user *discordgo.User) (player models.Player, found, onFillList bool) {
	if i.Message == nil {
		return models.Player{}, false, false
	}
	fills, err := b.discord.ListFills(ctx, i.Message.ID)
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
