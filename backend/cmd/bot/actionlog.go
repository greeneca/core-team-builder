package main

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/models"
)

// A server can designate one channel as its action log with /coreteam actionlog
// set. Once set, every signup-related interaction on the bot's posts — signing
// up, switching, un-signing, joining a waitlist, going tentative, RSVPing,
// filling a slot, and the editor actions behind "Edit run" — is mirrored there
// as a short entry naming the post (linked to it), what happened, and who did
// it.
//
// Logging is best effort and always happens *after* the interaction has been
// acknowledged, so a slow or failing log never delays (or breaks) the action
// itself; failures are logged to stderr only.

// actionLogEntry is one line of the action log: what was interacted with
// (Title, hyperlinked by URL), what happened (Action), and who did it (Actor).
// Action reads as the predicate of a sentence starting with the actor, e.g.
// "signed up as **Tank**".
type actionLogEntry struct {
	Title  string
	Action string
	Actor  string
	URL    string
}

// logAction mirrors an entry to the guild's action log channel, doing nothing
// when the guild hasn't designated one. Best effort: lookup and delivery
// failures are logged, never surfaced to the user.
func (b *bot) logAction(ctx context.Context, s *discordgo.Session, guildID string, e actionLogEntry) {
	if guildID == "" || strings.TrimSpace(e.Action) == "" {
		return
	}
	channelID, err := b.discord.GetActionLogChannel(ctx, guildID)
	if err != nil {
		log.Printf("action log: lookup channel (guild %s): %v", guildID, err)
		return
	}
	if channelID == "" {
		return
	}
	_, err = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Embeds:          []*discordgo.MessageEmbed{actionLogEmbed(e)},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		log.Printf("action log: send (guild %s, channel %s): %v", guildID, channelID, err)
	}
}

// logRunAction logs an action taken on a pre-made run's post (/coreteam signup).
// The run carries its own guild/channel/message, so this works from the post and
// from the DM-driven edit flow alike.
func (b *bot) logRunAction(ctx context.Context, s *discordgo.Session, run *models.PremadeRun, team *models.Team, actor, action string) {
	if run == nil {
		return
	}
	b.logAction(ctx, s, run.GuildID, actionLogEntry{
		Title:  runLogTitle(run, team),
		Action: action,
		Actor:  actor,
		URL:    messageURL(run.GuildID, run.ChannelID, run.MessageID),
	})
}

// logPostAction logs an action taken on a trial overview post (/coreteam post).
// The post's title comes off its own embed, so the log names the run the same
// way the post does.
func (b *bot) logPostAction(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, action string) {
	if i.Message == nil {
		return
	}
	b.logAction(ctx, s, i.GuildID, actionLogEntry{
		Title:  existingEmbedTitle(i.Message),
		Action: action,
		Actor:  interactionDisplayName(i),
		URL:    messageURL(i.GuildID, i.ChannelID, i.Message.ID),
	})
}

// runLogTitle names a run for the log: its own title, falling back to the team
// name for runs posted without one.
func runLogTitle(run *models.PremadeRun, team *models.Team) string {
	if run != nil {
		if t := strings.TrimSpace(run.Title); t != "" {
			return t
		}
	}
	if team != nil {
		return team.Name
	}
	return ""
}

// slotLogLabel names a roster slot for the action log, e.g. "slot 3 (Tank)",
// dropping the role when the slot has none.
func slotLogLabel(team *models.Team, slot int) string {
	label := "slot " + strconv.Itoa(slot)
	if team != nil {
		if role := team.RoleLabel(roleForSlot(team, slot)); role != "" {
			label += " (" + role + ")"
		}
	}
	return label
}

// actionLogEmbed renders a log entry: the post's title, hyperlinked to the post
// itself, over a single "**who** did what" line.
func actionLogEmbed(e actionLogEntry) *discordgo.MessageEmbed {
	actor := strings.TrimSpace(e.Actor)
	if actor == "" {
		actor = "Someone"
	}
	title := strings.TrimSpace(e.Title)
	if title == "" {
		title = "Trial run"
	}
	return &discordgo.MessageEmbed{
		Title:       truncate(title, embedTitleLimit),
		URL:         e.URL,
		Description: truncate("**"+actor+"** "+e.Action, embedDescriptionLimit),
		Color:       embedColor,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
}

// --- /coreteam actionlog (designate the log channel) ---

// handleActionLog routes the /coreteam actionlog subcommand group (set / off /
// status). The log channel is per server, and choosing it is a server-admin
// action (Manage Server or Administrator), matching /coreteam permissions.
func (b *bot) handleActionLog(s *discordgo.Session, i *discordgo.InteractionCreate, group *discordgo.ApplicationCommandInteractionDataOption) {
	if i.GuildID == "" {
		ephemeral(s, i, "Run this in a server — the action log is set per server.")
		return
	}
	if !hasManageGuild(i) {
		ephemeral(s, i, "You need the Manage Server (or Administrator) permission to change the action log.")
		return
	}
	if len(group.Options) == 0 {
		return
	}
	sub := group.Options[0]

	ctx, cancel := handlerContext()
	defer cancel()

	switch sub.Name {
	case "set":
		b.handleActionLogSet(ctx, s, i, sub)
	case "off":
		b.handleActionLogOff(ctx, s, i)
	case "status":
		b.handleActionLogStatus(ctx, s, i)
	}
}

// handleActionLogSet designates the chosen channel as this server's action log,
// then posts a confirmation there. That confirmation doubles as a permission
// check: if the bot can't post in the channel the log would be silently empty,
// so the runner is told to fix it rather than left assuming it works.
func (b *bot) handleActionLogSet(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	channelID := channelOptionID(sub, "channel")
	if channelID == "" {
		ephemeral(s, i, "Please pick a channel.")
		return
	}
	setBy := ""
	if user := invokingUser(i); user != nil {
		setBy = user.ID
	}
	if err := b.discord.SetActionLogChannel(ctx, i.GuildID, channelID, setBy); err != nil {
		log.Printf("action log set: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         "\U0001F4CB This channel is now the **action log**. I'll post here whenever someone signs up, switches, un-signs, RSVPs, or is changed by a run editor.", // 📋
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		log.Printf("action log set: confirm in channel %s: %v", channelID, err)
		ephemeralNoMentions(s, i, "Saved "+channelMention(channelID)+" as the action log, but I couldn't post there. Give me **View Channel** and **Send Messages** in that channel, or pick another one with `/coreteam actionlog set`.")
		return
	}
	ephemeralNoMentions(s, i, "Signup activity in this server will now be logged to "+channelMention(channelID)+". Turn it off any time with `/coreteam actionlog off`.")
}

// handleActionLogOff stops logging for this server. Idempotent.
func (b *bot) handleActionLogOff(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := b.discord.ClearActionLogChannel(ctx, i.GuildID); err != nil {
		log.Printf("action log off: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	ephemeral(s, i, "Action logging is off for this server. Set it back up any time with `/coreteam actionlog set`.")
}

// handleActionLogStatus reports which channel (if any) this server logs to.
func (b *bot) handleActionLogStatus(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	channelID, err := b.discord.GetActionLogChannel(ctx, i.GuildID)
	if err != nil {
		log.Printf("action log status: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if channelID == "" {
		ephemeral(s, i, "No action log is set for this server. Designate one with `/coreteam actionlog set channel:#channel`.")
		return
	}
	ephemeralNoMentions(s, i, "Signup activity in this server is logged to "+channelMention(channelID)+".")
}

// channelMention renders a Discord channel mention for a channel ID.
func channelMention(channelID string) string {
	return "<#" + channelID + ">"
}
