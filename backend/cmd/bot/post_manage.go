package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/models"
)

// The "Manage" button on a /coreteam post overview is the entry point for run
// admins to act on the post itself rather than on their own attendance. It opens
// a DM with a menu of manage actions; today the only one is "RSVP for a player",
// which answers for someone who can't (or didn't) press the buttons themselves.
// Adding an action means adding an entry to postManageActions and a case to
// handlePostManageAction — the button, the permission gate, and the routing stay
// as they are.
//
// Access matches the premade run's "Edit run" button minus its owner check (a
// /coreteam post records no poster): server admins and roles designated with
// /coreteam permissions. The button press is checked with canActAsRunAdmin,
// which reads the interaction's member; every step after that arrives from the
// DM, where there is no member to read, so it re-checks the same bar over REST
// with canActAsRunAdminInGuild. Re-checking at each step means a role revoked
// mid-flow takes effect immediately, which matters more here than for a
// throwaway ephemeral: a Manage DM sticks around and its buttons keep working
// until something says otherwise.
//
// See post_dm.go for how the DM is delivered, what happens when the user has
// DMs closed, and why the post's guild, channel and message id travel in the
// follow-up controls' custom IDs.
//
// The RSVP an admin records is indistinguishable from a self-press — same
// discord_rsvps row, same ✅/❌ mark on the post, same filler displacement and
// fill-list DMs — so a player who later presses the buttons themselves simply
// overrides it.
const (
	postManageID         = "post_manage"           // the button on the post
	postManageActionID   = "post_manage_action"    // + ":<origin>"
	postManageRSVPPickID = "post_manage_rsvp_pick" // + ":<origin>"
	postManageRSVPSetID  = "post_manage_rsvp_set"  // + ":<origin>:<slot>:<status>"
)

// postManageActionRSVP is the manage menu's RSVP-on-behalf action.
const postManageActionRSVP = "rsvp"

// postManageDenyMsg is the ephemeral shown when someone without permission
// presses "Manage" (Discord can't hide a button per-user).
const postManageDenyMsg = "Only a designated role or a server admin can manage this post. A server admin can grant a role access with `/coreteam permissions add`."

// onPostManageComponent routes the manage flow's controls. Everything after the
// initial button carries context in its custom ID, so dispatch on the leading
// segment and read the origin back out of the rest.
func (b *bot) onPostManageComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if parts[0] == postManageID {
		b.handlePostManage(s, i)
		return
	}
	origin, ok := parsePostOrigin(parts, 1)
	if !ok {
		return
	}
	switch parts[0] {
	case postManageActionID:
		if len(parts) == 4 {
			b.handlePostManageAction(s, i, origin)
		}
	case postManageRSVPPickID:
		if len(parts) == 4 {
			b.handlePostManageRSVPPick(s, i, origin)
		}
	case postManageRSVPSetID:
		if len(parts) == 6 {
			b.handlePostManageRSVPSet(s, i, origin, parts[4], parts[5])
		}
	}
}

// handlePostManage opens the flow: it checks the presser may act as an admin,
// then DMs them the menu of manage actions.
func (b *bot) handlePostManage(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	if i.Message == nil {
		ephemeral(s, i, "Could not find the post to manage.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	if !b.requirePostManage(ctx, s, i) {
		return
	}

	origin := postOrigin{guildID: i.GuildID, channelID: i.ChannelID, messageID: i.Message.ID}
	actions := postManageActions(b.postSignupsClosed(ctx, origin.messageID))
	menu := selectRow(postManageActionID+":"+origin.encode(), "Choose an action", 1, 1, actions)
	openFlowInDM(s, i, user.ID, "What would you like to do with this post?", nil, menu)
}

// postManageActions lists the manage menu's actions. Discord can't disable an
// individual select option, so an action unavailable right now stays listed with
// a description saying why; its handler rejects it too. locked is the post's
// signups-closed state (see postLocked), which rules out RSVPing.
func postManageActions(locked bool) []discordgo.SelectMenuOption {
	rsvp := discordgo.SelectMenuOption{
		Label:       "RSVP for a player",
		Value:       postManageActionRSVP,
		Description: "Answer Coming / Not Coming on a roster player's behalf",
		Emoji:       &discordgo.ComponentEmoji{Name: "\U0001F4DD"}, // 📝
	}
	if locked {
		rsvp.Description = "Unavailable — this run has already started"
	}
	return []discordgo.SelectMenuOption{rsvp}
}

// handlePostManageAction runs the chosen manage action, replacing the menu with
// that action's first step.
func (b *bot) handlePostManageAction(s *discordgo.Session, i *discordgo.InteractionCreate, origin postOrigin) {
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	if !b.requirePostManageStep(ctx, s, i, origin) {
		return
	}

	switch values[0] {
	case postManageActionRSVP:
		b.openPostManageRSVPPicker(ctx, s, i, origin)
	}
}

// openPostManageRSVPPicker swaps the manage menu for a picker of the roster
// players whose RSVP can be set.
func (b *bot) openPostManageRSVPPicker(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, origin postOrigin) {
	// Signups close once the run has started. Checked here as well as in the menu
	// so a stale menu can't slip past it; the post is refreshed directly (the
	// interaction's message is this flow's DM, not the post) so its own controls
	// render locked for everyone.
	if b.postSignupsClosed(ctx, origin.messageID) {
		endFlowStep(s, i, "This run has already started — signups are closed.")
		if err := b.refreshPostMessage(ctx, s, origin.guildID, origin.channelID, origin.messageID); err != nil {
			log.Printf("post manage: lock post: %v", err)
		}
		return
	}

	team, err := b.postTeam(ctx, origin.channelID)
	if err != nil {
		log.Printf("post manage: load team: %v", err)
		endFlowStep(s, i, "Something went wrong. Please try again.")
		return
	}
	rsvps, err := b.discord.ListRSVPs(ctx, origin.messageID)
	if err != nil {
		log.Printf("post manage: list rsvps: %v", err)
		endFlowStep(s, i, "Something went wrong. Please try again.")
		return
	}

	// Cached names only — this is the interaction's acknowledgement, so it has to
	// stay inside Discord's 3-second window.
	names := b.rosterNamesFromCache(origin.guildID, team)
	opts := postManageRSVPOptions(team, names, rsvpMarks(team, rsvps))
	if len(opts) == 0 {
		endFlowStep(s, i, "No roster slot on this team has a Discord handle set, so there's nobody to RSVP for. Assign handles in the web app first.")
		return
	}

	picker := selectRow(postManageRSVPPickID+":"+origin.encode(), "Choose a player", 1, 1, opts)
	updateFlowStep(s, i, "Who are you RSVPing for?", nil, picker)
}

// postManageRSVPOptions lists the roster players an admin can answer for: those
// with a Discord handle set, since an open slot has nobody to RSVP for and a
// handle is what ties the response back to the slot. Each option notes the
// player's current response so the admin can see what they're changing. Capped
// at Discord's 25-option select limit.
func postManageRSVPOptions(team *models.Team, names map[int]string, marks map[int]string) []discordgo.SelectMenuOption {
	opts := make([]discordgo.SelectMenuOption, 0, len(team.Players))
	for _, p := range team.Players { // store returns players slot-ordered
		if strings.TrimSpace(p.DiscordHandle) == "" {
			continue
		}
		if len(opts) >= 25 {
			break
		}
		opts = append(opts, discordgo.SelectMenuOption{
			Label:       truncate(rosterPlayerName(p, names), 100),
			Value:       strconv.Itoa(p.Slot),
			Description: truncate(slotOptionLabel(team, p)+" · "+postManageRSVPStatusLabel(marks[p.Slot]), 100),
			Emoji:       &discordgo.ComponentEmoji{Name: team.RoleEmoji(p.Role)},
		})
	}
	return opts
}

// postManageRSVPStatusLabel describes a slot's current response for a picker
// option, so the admin sees what they'd be changing.
func postManageRSVPStatusLabel(status string) string {
	switch status {
	case models.RSVPYes:
		return "Currently: Coming"
	case models.RSVPNo:
		return "Currently: Not Coming"
	default:
		return "No response yet"
	}
}

// rosterPlayerName names a roster player for a picker option: their resolved
// Discord display name, else the roster name, else the raw handle. Returns ""
// for a slot with none of the three (an open slot); callers decide how to label
// that.
func rosterPlayerName(p models.Player, names map[int]string) string {
	if n := strings.TrimSpace(names[p.Slot]); n != "" {
		return n
	}
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	return strings.TrimPrefix(strings.TrimSpace(p.DiscordHandle), "@")
}

// handlePostManageRSVPPick confirms the chosen player and swaps the picker for
// the Coming / Not Coming buttons, which carry the post and slot forward.
func (b *bot) handlePostManageRSVPPick(s *discordgo.Session, i *discordgo.InteractionCreate, origin postOrigin) {
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

	if !b.requirePostManageStep(ctx, s, i, origin) {
		return
	}

	team, err := b.postTeam(ctx, origin.channelID)
	if err != nil {
		log.Printf("post manage: pick load team: %v", err)
		endFlowStep(s, i, "Something went wrong. Please try again.")
		return
	}
	player, ok := playerForSlot(team, slot)
	if !ok {
		endFlowStep(s, i, "That slot is no longer on the roster. Press **Manage** on the post again to start over.")
		return
	}
	name := rosterPlayerName(player, b.rosterNamesFromCache(origin.guildID, team))

	prefix := fmt.Sprintf("%s:%s:%d:", postManageRSVPSetID, origin.encode(), slot)
	answers := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Coming",
				Emoji:    &discordgo.ComponentEmoji{Name: "✅"},
				Style:    discordgo.SuccessButton,
				CustomID: prefix + models.RSVPYes,
			},
			discordgo.Button{
				Label:    "Not Coming",
				Emoji:    &discordgo.ComponentEmoji{Name: "❌"},
				Style:    discordgo.DangerButton,
				CustomID: prefix + models.RSVPNo,
			},
		}},
	}
	updateFlowStep(s, i, fmt.Sprintf("How is **%s** responding?", name), nil, answers)
}

// handlePostManageRSVPSet records the RSVP against the chosen roster player,
// then runs the same follow-on effects a self-press would: the post is
// re-rendered, a filler covering a returning player is displaced, and fill-list
// backups are told when a slot opens.
func (b *bot) handlePostManageRSVPSet(s *discordgo.Session, i *discordgo.InteractionCreate, origin postOrigin, slotStr, status string) {
	if status != models.RSVPYes && status != models.RSVPNo {
		return
	}
	slot, err := strconv.Atoi(slotStr)
	if err != nil {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	if !b.requirePostManageStep(ctx, s, i, origin) {
		return
	}
	if b.postSignupsClosed(ctx, origin.messageID) {
		endFlowStep(s, i, "This run has already started — signups are closed.")
		return
	}

	team, err := b.postTeam(ctx, origin.channelID)
	if err != nil {
		log.Printf("post manage: set load team: %v", err)
		endFlowStep(s, i, "Something went wrong. Please try again.")
		return
	}
	player, ok := playerForSlot(team, slot)
	if !ok {
		endFlowStep(s, i, "That slot is no longer on the roster. Press **Manage** on the post again to start over.")
		return
	}

	target := b.postRSVPTarget(s, origin.guildID, player)
	if err := b.discord.SetRSVP(ctx, origin.messageID, origin.channelID, target.ID, target.Username, target.GlobalName, status); err != nil {
		log.Printf("post manage: set rsvp: %v", err)
		endFlowStep(s, i, "Something went wrong saving that RSVP. Please try again.")
		return
	}
	b.clearRivalSlotRSVPs(ctx, origin.messageID, team, slot, target.ID)

	// Acknowledge before the post refresh and the DMs below, which are slow
	// enough to risk Discord's 3-second deadline.
	name := rosterPlayerName(player, b.rosterNamesFromCache(origin.guildID, team))
	endFlowStep(s, i, fmt.Sprintf("Marked **%s** as **%s**. Press **Manage** on the post to make another change.", name, rsvpLogLabel(status)))

	if rerr := b.refreshPostMessage(ctx, s, origin.guildID, origin.channelID, origin.messageID); rerr != nil {
		log.Printf("post manage: refresh post: %v", rerr)
	}

	// The same side effects a self-press triggers, keyed to the player the RSVP
	// is for rather than the admin who pressed.
	if status == models.RSVPYes {
		b.displaceFillerForReturningPlayer(ctx, s, origin.guildID, origin.channelID, origin.messageID, target)
	} else {
		b.notifyFillListOfOpening(ctx, s, origin.guildID, origin.channelID, origin.messageID, target)
	}

	// Re-render once more to pick up anything the side effects changed (e.g. a
	// filler moved to the fill list).
	if rerr := b.refreshPostMessage(ctx, s, origin.guildID, origin.channelID, origin.messageID); rerr != nil {
		log.Printf("post manage: re-refresh post: %v", rerr)
	}

	b.logAction(ctx, s, origin.guildID, actionLogEntry{
		Title:  postManageLogTitle(s, origin.channelID, origin.messageID),
		Action: fmt.Sprintf("RSVP'd **%s** on behalf of **%s**", rsvpLogLabel(status), name),
		Actor:  b.postManageActor(s, origin.guildID, invokingUser(i)),
		URL:    messageURL(origin.guildID, origin.channelID, origin.messageID),
	})
}

// postManageActor names the admin for the action log. The press arrives from a
// DM, so interactionDisplayName's member (and its server nickname) isn't there
// to read; resolve the nickname from the guild instead, falling back to the
// account's own display name.
func (b *bot) postManageActor(s *discordgo.Session, guildID string, user *discordgo.User) string {
	if user == nil {
		return ""
	}
	if guildID != "" {
		if name := b.resolveMemberName(s, guildID, user.ID); name != "" {
			return name
		}
	}
	return displayName(user)
}

// postManageLogTitle names the post for the action log the way the post itself
// does. Unlike logPostAction this can't read the interaction's message (that's
// the flow's DM), so it fetches the post; a failed fetch yields "" and the log
// falls back to its generic title.
func postManageLogTitle(s *discordgo.Session, channelID, messageID string) string {
	msg, err := s.ChannelMessage(channelID, messageID)
	if err != nil {
		log.Printf("post manage: fetch post for log title (%s): %v", messageID, err)
		return ""
	}
	return existingEmbedTitle(msg)
}

// postRSVPTarget resolves a roster player's stored handle to the Discord
// identity to record their RSVP under. It mirrors matchPlayer in reverse, so the
// resulting row always maps back to this slot: an ID/mention handle keeps that
// ID (its names are looked up for display, but the ID alone is enough to match),
// and an "@username" handle resolves to the matching guild member.
//
// When a text handle matches nobody in the server, the RSVP is recorded under a
// synthetic "n:<name>" ID — the same convention premade signups use for players
// without a Discord account. That keeps the response stable and matchable by
// username, at the cost of not being mentionable; pingPostAttendees skips
// non-numeric IDs for exactly this reason.
func (b *bot) postRSVPTarget(s *discordgo.Session, guildID string, p models.Player) *discordgo.User {
	handle := strings.TrimSpace(p.DiscordHandle)
	if id := discordIDFromHandle(handle); id != "" {
		if u, err := s.User(id); err == nil && u != nil {
			return u
		}
		return &discordgo.User{ID: id}
	}
	username := strings.TrimPrefix(handle, "@")
	if m := b.searchGuildMemberByUsername(s, guildID, username); m != nil && m.User != nil {
		return m.User
	}
	return &discordgo.User{ID: "n:" + strings.ToLower(username), Username: username}
}

// clearRivalSlotRSVPs deletes any other response on this post that the roster
// would match to the same slot. A player can be recorded under more than one
// identity — an admin answering for an unresolvable "@username" handle writes a
// synthetic ID, while the player's own press writes their real one — and leaving
// both behind would let the stale one drive the pre-run ping. Best-effort:
// failures are logged, since the RSVP itself is already saved.
func (b *bot) clearRivalSlotRSVPs(ctx context.Context, messageID string, team *models.Team, slot int, keepUserID string) {
	rsvps, err := b.discord.ListRSVPs(ctx, messageID)
	if err != nil {
		log.Printf("post manage: list rsvps for cleanup: %v", err)
		return
	}
	for _, r := range rsvps {
		if r.DiscordUserID == keepUserID {
			continue
		}
		u := &discordgo.User{ID: r.DiscordUserID, Username: r.DiscordUsername, GlobalName: r.DiscordGlobalName}
		if p, ok := matchPlayer(team, u); !ok || p.Slot != slot {
			continue
		}
		if err := b.discord.DeleteRSVP(ctx, messageID, r.DiscordUserID); err != nil {
			log.Printf("post manage: delete superseded rsvp: %v", err)
		}
	}
}

// postTeam loads the team bound to a post's channel.
func (b *bot) postTeam(ctx context.Context, channelID string) (*models.Team, error) {
	teamID, err := b.discord.GetChannelTeam(ctx, channelID)
	if err != nil {
		return nil, err
	}
	return b.teams.Get(ctx, teamID)
}

// playerForSlot returns the roster player occupying a slot.
func playerForSlot(team *models.Team, slot int) (models.Player, bool) {
	for _, p := range team.Players {
		if p.Slot == slot {
			return p, true
		}
	}
	return models.Player{}, false
}

// requirePostManage gates the button press on canActAsRunAdmin, replying with
// the rejection (or an error notice) and reporting false when it doesn't pass.
// Rejections stay ephemeral: they're one-shot answers, and DMing someone to say
// they may not do a thing they can't do is worse than saying so in place.
func (b *bot) requirePostManage(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	allowed, err := b.canActAsRunAdmin(ctx, i)
	if err != nil {
		log.Printf("post manage: permission: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return false
	}
	if !allowed {
		ephemeral(s, i, postManageDenyMsg)
		return false
	}
	return true
}

// requirePostManageStep is requirePostManage for the flow's later steps, which
// arrive from the DM (no member to read, so the check goes over REST) and must
// rewrite their existing message rather than create a new response.
func (b *bot) requirePostManageStep(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, origin postOrigin) bool {
	user := invokingUser(i)
	if user == nil {
		return false
	}
	allowed, err := b.canActAsRunAdminInGuild(ctx, s, origin.guildID, origin.channelID, user.ID)
	if err != nil {
		log.Printf("post manage: permission: %v", err)
		endFlowStep(s, i, "Something went wrong. Please try again.")
		return false
	}
	if !allowed {
		endFlowStep(s, i, postManageDenyMsg)
		return false
	}
	return true
}
