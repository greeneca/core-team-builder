package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/discordfmt"
	"github.com/core-team-builder/backend/internal/esoref"
	"github.com/core-team-builder/backend/internal/models"
)

// The /coreteam signup flow posts a one-off, scheduled trial run from a pre-made
// team that players claim per slot. It is built entirely from message components
// (selects/buttons) and one modal, so no privileged intents are needed.
//
// Custom ID grammar:
//
//	premade_team                       (ephemeral team picker; value = team id)
//	premade_tz:<teamID>                (ephemeral timezone picker; value = IANA tz)
//	premade_modal:<teamID>:<tz>        (modal: title + date + time)
//	premade_claim                      (on the post; value = slot to claim, or role in simple mode)
//	premade_details                    (on the post; value = slot to DM details for)
//	premade_wait                       (on the post; value = role to waitlist for)
//	premade_leave                      (on the post; release the presser's slot/waitlist)
const (
	premadePrefix      = "premade_"
	premadeModalPrefix = "premade_modal:"
	premadeTeamID      = "premade_team"
	premadeClaimID     = "premade_claim"
	premadeDetailsID   = "premade_details"
	premadeWaitID      = "premade_wait"
	premadeLeaveID     = "premade_leave"
)

// onPremadeComponent dispatches every premade_* component interaction.
func (b *bot) onPremadeComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	switch {
	case id == premadeTeamID:
		b.handlePremadeTeamSelect(s, i)
	case strings.HasPrefix(id, "premade_tz:"):
		b.handlePremadeTzSelect(s, i)
	case id == premadeClaimID:
		b.handlePremadeClaim(s, i)
	case id == premadeDetailsID:
		b.handlePremadeDetails(s, i)
	case id == premadeWaitID:
		b.handlePremadeWaitlist(s, i)
	case id == premadeLeaveID:
		b.handlePremadeLeave(s, i)
	}
}

// handlePremade starts the flow: it resolves the runner's linked account, lists
// their pre-made teams they can edit, and shows an ephemeral team picker.
func (b *bot) handlePremade(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	ctx, cancel := handlerContext()
	defer cancel()

	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		ephemeral(s, i, "Link your account first with /coreteam link, then mark a team as pre-made in the web app.")
		return
	}
	if err != nil {
		log.Printf("premade: get user: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	teams, err := b.listEditablePremadeTeams(ctx, appUserID)
	if err != nil {
		log.Printf("premade: list teams: %v", err)
		ephemeral(s, i, "Something went wrong loading your teams. Please try again.")
		return
	}
	if len(teams) == 0 {
		ephemeral(s, i, "You don't have any pre-made teams you can run. Mark a team as pre-made in the web app (Team Features) first.")
		return
	}

	options := make([]discordgo.SelectMenuOption, 0, len(teams))
	for idx, t := range teams {
		if idx >= 25 {
			break
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(t.Name, 100),
			Value: strconv.FormatInt(t.ID, 10),
		})
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "Which pre-made team would you like to run?",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						CustomID:    premadeTeamID,
						Placeholder: "Select a pre-made team",
						Options:     options,
					},
				}},
			},
		},
	})
	if err != nil {
		log.Printf("premade: respond: %v", err)
	}
}

// listEditablePremadeTeams returns the user's pre-made teams they own or can
// edit, most-recently-updated first.
func (b *bot) listEditablePremadeTeams(ctx context.Context, appUserID int64) ([]models.Team, error) {
	all, err := b.teams.ListForUser(ctx, appUserID)
	if err != nil {
		return nil, err
	}
	var out []models.Team
	for _, t := range all {
		if !t.PreMade {
			continue
		}
		if t.OwnerID == appUserID {
			out = append(out, t)
			continue
		}
		_, role, err := b.teams.Access(ctx, t.ID, appUserID)
		if err != nil {
			return nil, err
		}
		if role == models.RoleOwner || role == models.RoleEditor {
			out = append(out, t)
		}
	}
	return out, nil
}

// handlePremadeTeamSelect advances from the team picker to the timezone picker.
func (b *bot) handlePremadeTeamSelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	teamID := values[0]

	opts := make([]discordgo.SelectMenuOption, 0, len(signupTimezones))
	for _, tz := range signupTimezones {
		opts = append(opts, discordgo.SelectMenuOption{Label: tzOffsetLabel(tz), Value: tz})
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: "Which timezone are you entering the date and time in?",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						CustomID:    "premade_tz:" + teamID,
						Placeholder: "Select your timezone",
						Options:     opts,
					},
				}},
			},
		},
	})
	if err != nil {
		log.Printf("premade: team select: %v", err)
	}
}

// handlePremadeTzSelect opens the title/date/time modal once a timezone is set.
func (b *bot) handlePremadeTzSelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	teamID := strings.TrimPrefix(id, "premade_tz:")
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	tz := values[0]

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: premadeModalPrefix + teamID + ":" + tz,
			Title:    "Schedule the run",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:    "premade_title",
						Label:       "Title",
						Style:       discordgo.TextInputShort,
						Required:    true,
						MaxLength:   100,
						Placeholder: "e.g. Saturday vAA Carry",
					},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:    "premade_date",
						Label:       "Date (YYYY-MM-DD)",
						Style:       discordgo.TextInputShort,
						Required:    true,
						MaxLength:   10,
						Placeholder: "2026-07-04",
					},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:    "premade_time",
						Label:       "Time (HH:MM, 24h)",
						Style:       discordgo.TextInputShort,
						Required:    true,
						MaxLength:   5,
						Placeholder: "20:00",
					},
				}},
			},
		},
	})
	if err != nil {
		log.Printf("premade: tz select modal: %v", err)
	}
}

// handlePremadeModalSubmit parses the schedule, creates the run, and posts the
// public announcement.
func (b *bot) handlePremadeModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	parts := strings.SplitN(i.ModalSubmitData().CustomID, ":", 3)
	if len(parts) != 3 {
		ephemeral(s, i, "Something went wrong. Please run /coreteam signup again.")
		return
	}
	teamID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		ephemeral(s, i, "That selection was invalid. Please run /coreteam signup again.")
		return
	}
	tz := parts[2]

	loc, err := time.LoadLocation(tz)
	if err != nil {
		ephemeral(s, i, "I couldn't understand that timezone. Please run /coreteam signup again.")
		return
	}
	title := strings.TrimSpace(modalValue(i, "premade_title"))
	date := strings.TrimSpace(modalValue(i, "premade_date"))
	clock := strings.TrimSpace(modalValue(i, "premade_time"))
	when, err := time.ParseInLocation("2006-01-02 15:04", date+" "+clock, loc)
	if err != nil {
		ephemeral(s, i, "I couldn't read that date/time. Use the format YYYY-MM-DD and HH:MM (24h), e.g. 2026-07-04 and 20:00.")
		return
	}
	scheduledUTC := when.UTC()

	ctx, cancel := handlerContext()
	defer cancel()

	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		ephemeral(s, i, "Link your account first with /coreteam link.")
		return
	}
	if err != nil {
		log.Printf("premade: modal get user: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	_, role, err := b.teams.Access(ctx, teamID, appUserID)
	if err != nil {
		log.Printf("premade: modal access: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if role != models.RoleOwner && role != models.RoleEditor {
		ephemeral(s, i, "You don't have permission to run that team.")
		return
	}
	team, _, primary, _, err := b.loadTeamData(ctx, teamID)
	if err != nil {
		log.Printf("premade: load team: %v", err)
		ephemeral(s, i, "Could not load that team. Please try again.")
		return
	}
	if !team.PreMade {
		ephemeral(s, i, "That team isn't marked as pre-made anymore.")
		return
	}

	run, err := b.premade.CreateRun(ctx, teamID, i.GuildID, i.ChannelID, title, scheduledUTC, appUserID)
	if err != nil {
		log.Printf("premade: create run: %v", err)
		ephemeral(s, i, "Something went wrong creating the run. Please try again.")
		return
	}

	embed := b.premadeEmbed(team, run, primary, nil, nil)
	msg, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: premadeComponents(team, nil),
	})
	if err != nil {
		log.Printf("premade: post: %v", err)
		ephemeral(s, i, "Couldn't post the run here. Check my permissions in this channel and try again.")
		return
	}
	if err := b.premade.SetRunMessage(ctx, run.ID, msg.ID); err != nil {
		log.Printf("premade: set message id: %v", err)
	}
	ephemeral(s, i, "Posted your pre-made run. Players can claim slots from the post above.")
}

// handlePremadeClaim locks a slot to the presser (releasing any prior claim). In
// specific mode the selected value is a slot number; in simple mode it's a role
// and we claim the first open slot matching it.
func (b *bot) handlePremadeClaim(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil || i.Message == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	run, ok := b.premadeRunForMessage(ctx, s, i)
	if !ok {
		return
	}
	team, _, _, _, err := b.loadTeamData(ctx, run.TeamID)
	if err != nil {
		log.Printf("premade: claim load team: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	if team.SimpleSignup {
		b.claimSimple(ctx, s, i, run, team, user, values[0])
		return
	}

	slot, err := strconv.Atoi(values[0])
	if err != nil {
		return
	}
	err = b.premade.ClaimSlot(ctx, run.ID, slot, user.ID, displayName(user))
	if errors.Is(err, models.ErrSlotTaken) {
		ephemeral(s, i, "That slot was just taken by someone else. Pick another open slot.")
		return
	}
	if err != nil {
		log.Printf("premade: claim: %v", err)
		ephemeral(s, i, "Something went wrong claiming that slot. Please try again.")
		return
	}
	// Holding a slot supersedes waiting for one.
	if err := b.premade.LeaveWaitlist(ctx, run.ID, user.ID); err != nil {
		log.Printf("premade: claim clear waitlist: %v", err)
	}
	b.renderPremadeUpdate(ctx, s, i, run)
}

// claimSimple claims the first open slot matching the chosen role, retrying when
// another user grabs the same slot first.
func (b *bot) claimSimple(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, run *models.PremadeRun, team *models.Team, user *discordgo.User, role string) {
	for attempt := 0; attempt < 16; attempt++ {
		signups, err := b.premade.ListSignups(ctx, run.ID)
		if err != nil {
			log.Printf("premade: claim simple list: %v", err)
			ephemeral(s, i, "Something went wrong claiming a slot. Please try again.")
			return
		}
		taken := map[int]bool{}
		for _, sg := range signups {
			// ClaimSlot releases the presser's prior claim, so don't let their own
			// existing slot block the search for a matching role.
			if sg.DiscordUserID == user.ID {
				continue
			}
			taken[sg.Slot] = true
		}
		slot, ok := firstOpenSlotForRole(team, taken, role)
		if !ok {
			ephemeral(s, i, "No open slots for that role right now. Pick another role.")
			return
		}
		err = b.premade.ClaimSlot(ctx, run.ID, slot, user.ID, displayName(user))
		if errors.Is(err, models.ErrSlotTaken) {
			continue
		}
		if err != nil {
			log.Printf("premade: claim simple: %v", err)
			ephemeral(s, i, "Something went wrong claiming a slot. Please try again.")
			return
		}
		if err := b.premade.LeaveWaitlist(ctx, run.ID, user.ID); err != nil {
			log.Printf("premade: claim simple clear waitlist: %v", err)
		}
		b.renderPremadeUpdate(ctx, s, i, run)
		return
	}
	ephemeral(s, i, "Couldn't grab a slot for that role — it just filled up. Try another role.")
}

// firstOpenSlotForRole returns the lowest-numbered slot for role that isn't
// already taken.
func firstOpenSlotForRole(team *models.Team, taken map[int]bool, role string) (int, bool) {
	best := 0
	for _, p := range team.Players {
		if p.Role != role || taken[p.Slot] {
			continue
		}
		if best == 0 || p.Slot < best {
			best = p.Slot
		}
	}
	if best == 0 {
		return 0, false
	}
	return best, true
}

// handlePremadeLeave releases the presser's claimed slot and/or waitlist entry.
// When a claimed slot is freed and the team's waitlist is on, the head of that
// slot's role waitlist is auto-promoted into it and DM'd.
func (b *bot) handlePremadeLeave(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil || i.Message == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	ctx, cancel := handlerContext()
	defer cancel()

	run, ok := b.premadeRunForMessage(ctx, s, i)
	if !ok {
		return
	}
	team, _, _, _, err := b.loadTeamData(ctx, run.TeamID)
	if err != nil {
		log.Printf("premade: leave load team: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	// Note the slot/role being freed (if any) before releasing it, so we can
	// promote from that role's waitlist afterward.
	freedSlot, freedRole, held := 0, "", false
	if signups, err := b.premade.ListSignups(ctx, run.ID); err != nil {
		log.Printf("premade: leave list signups: %v", err)
	} else {
		for _, sg := range signups {
			if sg.DiscordUserID == user.ID {
				freedSlot, freedRole, held = sg.Slot, roleForSlot(team, sg.Slot), true
				break
			}
		}
	}

	if err := b.premade.LeaveSlot(ctx, run.ID, user.ID); err != nil {
		log.Printf("premade: leave: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if err := b.premade.LeaveWaitlist(ctx, run.ID, user.ID); err != nil {
		log.Printf("premade: leave waitlist: %v", err)
	}

	if held && team.WaitlistEnabled {
		if entry, promoted, err := b.premade.PromoteToSlot(ctx, run.ID, freedSlot, freedRole); err != nil {
			log.Printf("premade: promote: %v", err)
		} else if promoted {
			b.dmPromoted(s, entry, run, team, freedSlot)
		}
	}

	b.renderPremadeUpdate(ctx, s, i, run)
}

// handlePremadeWaitlist adds the presser to the run's waitlist for the chosen
// role. Players who already hold a slot are told to leave it first.
func (b *bot) handlePremadeWaitlist(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil || i.Message == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	role := values[0]

	ctx, cancel := handlerContext()
	defer cancel()

	run, ok := b.premadeRunForMessage(ctx, s, i)
	if !ok {
		return
	}
	team, _, _, _, err := b.loadTeamData(ctx, run.TeamID)
	if err != nil {
		log.Printf("premade: waitlist load team: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if !team.WaitlistEnabled {
		ephemeral(s, i, "The waitlist isn't enabled for this run.")
		return
	}
	if signups, err := b.premade.ListSignups(ctx, run.ID); err == nil {
		for _, sg := range signups {
			if sg.DiscordUserID == user.ID {
				ephemeral(s, i, "You already have a slot. Leave it first if you'd rather wait for another role.")
				return
			}
		}
	}
	if err := b.premade.JoinWaitlist(ctx, run.ID, role, user.ID, displayName(user)); err != nil {
		log.Printf("premade: join waitlist: %v", err)
		ephemeral(s, i, "Something went wrong joining the waitlist. Please try again.")
		return
	}
	b.renderPremadeUpdate(ctx, s, i, run)
}

// roleForSlot returns the role of the given roster slot, or "" if not found.
func roleForSlot(team *models.Team, slot int) string {
	for _, p := range team.Players {
		if p.Slot == slot {
			return p.Role
		}
	}
	return ""
}

// dmPromoted notifies a user that they were auto-promoted off the waitlist into
// an open slot. Failures are logged, not surfaced.
func (b *bot) dmPromoted(s *discordgo.Session, entry *models.PremadeWaitlistEntry, run *models.PremadeRun, team *models.Team, slot int) {
	if entry == nil {
		return
	}
	dm, err := s.UserChannelCreate(entry.DiscordUserID)
	if err != nil {
		log.Printf("premade: promote dm channel: %v", err)
		return
	}
	role := esoref.RoleLabel(entry.Role)
	if role == "" {
		role = entry.Role
	}
	title := run.Title
	if strings.TrimSpace(title) == "" {
		title = team.Name
	}
	msg := fmt.Sprintf("You're off the waitlist! A %s slot opened up for **%s** (<t:%d:F>) and you've been moved into slot %d.", role, title, run.ScheduledAt.Unix(), slot)
	if _, err := s.ChannelMessageSend(dm.ID, msg); err != nil {
		log.Printf("premade: promote dm send: %v", err)
	}
}

// handlePremadeDetails DMs the build details for the selected slot.
func (b *bot) handlePremadeDetails(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil || i.Message == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
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

	ctx, cancel := handlerContext()
	defer cancel()

	run, ok := b.premadeRunForMessage(ctx, s, i)
	if !ok {
		return
	}
	team, encs, _, _, err := b.loadTeamData(ctx, run.TeamID)
	if err != nil {
		log.Printf("premade: details load: %v", err)
		ephemeral(s, i, "Could not load the team. Please try again.")
		return
	}
	var player *models.Player
	for idx := range team.Players {
		if team.Players[idx].Slot == slot {
			player = &team.Players[idx]
			break
		}
	}
	if player == nil {
		ephemeral(s, i, "That slot doesn't exist on this team.")
		return
	}

	title, desc := discordfmt.PlayerDetail(team, *player, encs)
	embed := &discordgo.MessageEmbed{
		Title:       truncate(title, embedTitleLimit),
		Description: truncate(desc, embedDescriptionLimit),
		Color:       embedColor,
	}
	if dm, err := s.UserChannelCreate(user.ID); err == nil {
		if _, err := s.ChannelMessageSendEmbed(dm.ID, embed); err == nil {
			ephemeral(s, i, "Sent that slot's build details via DM.")
			return
		}
	}
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "I couldn't DM you (your DMs may be closed). Here are the details:",
			Embeds:  []*discordgo.MessageEmbed{embed},
		},
	})
	if err != nil {
		log.Printf("premade: details ephemeral: %v", err)
	}
}

// premadeRunForMessage resolves the run posted as the interacted message,
// responding ephemerally and returning ok=false when it's gone.
func (b *bot) premadeRunForMessage(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) (*models.PremadeRun, bool) {
	run, err := b.premade.GetRunByMessage(ctx, i.Message.ID)
	if errors.Is(err, models.ErrPremadeRunNotFound) {
		ephemeral(s, i, "This run is no longer active.")
		return nil, false
	}
	if err != nil {
		log.Printf("premade: get run by message: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return nil, false
	}
	return run, true
}

// renderPremadeUpdate re-renders the run post in place with the current signups.
func (b *bot) renderPremadeUpdate(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, run *models.PremadeRun) {
	team, _, primary, _, err := b.loadTeamData(ctx, run.TeamID)
	if err != nil {
		log.Printf("premade: render load team: %v", err)
		ephemeral(s, i, "Saved, but couldn't refresh the post.")
		return
	}
	signups, err := b.premade.ListSignups(ctx, run.ID)
	if err != nil {
		log.Printf("premade: render list signups: %v", err)
		ephemeral(s, i, "Saved, but couldn't refresh the post.")
		return
	}
	waitlist, err := b.premade.ListWaitlist(ctx, run.ID)
	if err != nil {
		log.Printf("premade: render list waitlist: %v", err)
		ephemeral(s, i, "Saved, but couldn't refresh the post.")
		return
	}
	embed := b.premadeEmbed(team, run, primary, signups, waitlist)
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: premadeComponents(team, signups),
		},
	})
	if err != nil {
		log.Printf("premade: render respond: %v", err)
	}
}

// premadeEmbed builds the run announcement embed from team data, the current
// signups, and the current per-role waitlist.
func (b *bot) premadeEmbed(team *models.Team, run *models.PremadeRun, primary *models.Encounter, signups []models.PremadeSignup, waitlist []models.PremadeWaitlistEntry) *discordgo.MessageEmbed {
	claimants := map[int]string{}
	for _, sg := range signups {
		claimants[sg.Slot] = sg.DiscordUserID
	}
	title, desc := discordfmt.BuildPremadePost(team, run.Title, run.ScheduledAt.Unix(), primary, claimants, waitlist)
	return &discordgo.MessageEmbed{
		Title:       truncate(title, embedTitleLimit),
		Description: truncate(desc, embedDescriptionLimit),
		Color:       embedColor,
	}
}

// premadeComponents builds the post's control rows. In specific signup mode:
// a "claim an open slot" select, a "get a slot's details" select, and a "leave
// my slot" button. In simple signup mode: a role select (claiming takes the
// first open slot matching that role) and a "leave my slot" button — no
// per-slot details select.
func premadeComponents(team *models.Team, signups []models.PremadeSignup) []discordgo.MessageComponent {
	taken := map[int]bool{}
	for _, sg := range signups {
		taken[sg.Slot] = true
	}

	if team.SimpleSignup {
		return premadeSimpleComponents(team, taken)
	}

	players := append([]models.Player{}, team.Players...)
	// team.Players is already slot-ordered from the store, but be defensive.
	allOpts := make([]discordgo.SelectMenuOption, 0, len(players))
	openOpts := make([]discordgo.SelectMenuOption, 0, len(players))
	for _, p := range players {
		label := slotOptionLabel(p)
		opt := discordgo.SelectMenuOption{Label: truncate(label, 100), Value: strconv.Itoa(p.Slot)}
		allOpts = append(allOpts, opt)
		if !taken[p.Slot] {
			openOpts = append(openOpts, opt)
		}
	}

	claimRow := discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.SelectMenu{
			CustomID:    premadeClaimID,
			Placeholder: "Sign up for a slot",
			Options:     openOpts,
		},
	}}
	if len(openOpts) == 0 {
		// A select must carry at least one option; show a disabled "full" menu.
		claimRow = discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    premadeClaimID,
				Placeholder: "All slots are taken",
				Disabled:    true,
				Options:     []discordgo.SelectMenuOption{{Label: "All slots taken", Value: "none"}},
			},
		}}
	}

	rows := []discordgo.MessageComponent{claimRow}
	if len(allOpts) > 0 {
		rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    premadeDetailsID,
				Placeholder: "Get build details for a slot",
				Options:     allOpts,
			},
		}})
	}
	if waitRow, ok := premadeWaitlistRow(team, taken); ok {
		rows = append(rows, waitRow)
	}
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    "Leave my slot",
			Style:    discordgo.SecondaryButton,
			CustomID: premadeLeaveID,
		},
	}})
	return rows
}

// premadeSimpleComponents builds the simple-signup controls: a role select
// (whose open count is shown per role) and a "leave my slot" button. Claiming a
// role takes the first open slot matching it, handled in handlePremadeClaim.
func premadeSimpleComponents(team *models.Team, taken map[int]bool) []discordgo.MessageComponent {
	openByRole := map[string]int{}
	for _, p := range team.Players {
		if p.Role == "" {
			continue
		}
		if !taken[p.Slot] {
			openByRole[p.Role]++
		}
	}

	seen := map[string]bool{}
	opts := make([]discordgo.SelectMenuOption, 0, 8)
	addRole := func(value, label string) {
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		if openByRole[value] <= 0 {
			return
		}
		if label == "" {
			label = value
		}
		opts = append(opts, discordgo.SelectMenuOption{
			Label: truncate(fmt.Sprintf("%s (%d open)", label, openByRole[value]), 100),
			Value: value,
		})
	}
	// Standard roles first, in a sensible order, then any other roles present.
	for _, r := range []string{"tank", "healer", "support_dps", "dps"} {
		addRole(r, esoref.RoleLabel(r))
	}
	for _, p := range team.Players {
		addRole(p.Role, esoref.RoleLabel(p.Role))
	}

	claimRow := discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.SelectMenu{
			CustomID:    premadeClaimID,
			Placeholder: "Sign up for a role",
			Options:     opts,
		},
	}}
	if len(opts) == 0 {
		claimRow = discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    premadeClaimID,
				Placeholder: "All slots are taken",
				Disabled:    true,
				Options:     []discordgo.SelectMenuOption{{Label: "All slots taken", Value: "none"}},
			},
		}}
	}

	rows := []discordgo.MessageComponent{claimRow}
	if waitRow, ok := premadeWaitlistRow(team, taken); ok {
		rows = append(rows, waitRow)
	}
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    "Leave my slot",
			Style:    discordgo.SecondaryButton,
			CustomID: premadeLeaveID,
		},
	}})
	return rows
}

// premadeWaitlistRow builds a "join a waitlist" select listing roles that are
// currently full (no open slots). Returns ok=false when the team's waitlist is
// off or no role is full.
func premadeWaitlistRow(team *models.Team, taken map[int]bool) (discordgo.MessageComponent, bool) {
	if !team.WaitlistEnabled {
		return nil, false
	}
	total := map[string]int{}
	open := map[string]int{}
	for _, p := range team.Players {
		if p.Role == "" {
			continue
		}
		total[p.Role]++
		if !taken[p.Slot] {
			open[p.Role]++
		}
	}

	seen := map[string]bool{}
	opts := make([]discordgo.SelectMenuOption, 0, 4)
	add := func(role string) {
		if role == "" || seen[role] {
			return
		}
		seen[role] = true
		if total[role] == 0 || open[role] > 0 {
			return // only offer waitlist for full roles
		}
		label := esoref.RoleLabel(role)
		if label == "" {
			label = role
		}
		opts = append(opts, discordgo.SelectMenuOption{
			Label: truncate(fmt.Sprintf("%s waitlist", label), 100),
			Value: role,
		})
	}
	for _, r := range []string{"tank", "healer", "support_dps", "dps"} {
		add(r)
	}
	for _, p := range team.Players {
		add(p.Role)
	}
	if len(opts) == 0 {
		return nil, false
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.SelectMenu{
			CustomID:    premadeWaitID,
			Placeholder: "Join a waitlist (role is full)",
			Options:     opts,
		},
	}}, true
}

// slotOptionLabel renders a slot's picker label, e.g. "Slot 3 · Tank · Dragonknight".
func slotOptionLabel(p models.Player) string {
	role := esoref.RoleLabel(p.Role)
	if role == "" {
		role = "—"
	}
	class := "—"
	if p.Class != "" {
		class = esoref.ClassLabel(p.Class)
	}
	return "Slot " + strconv.Itoa(p.Slot) + " · " + role + " · " + class
}
