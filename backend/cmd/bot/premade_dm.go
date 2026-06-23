package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/models"
	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/common"
	"github.com/olebedev/when/rules/en"
)

// The /coreteam signup creation flow is a free-text DM conversation. The slash
// command (handlePremade) only validates the runner and opens a DM; everything
// else happens here, driven by gateway DM messages (onMessageCreate) plus one
// component select for the one-time timezone pick (handlePremadeDMTimezone).
//
// Conversation state lives in premade_signup_sessions (one row per Discord user)
// so a half-finished signup survives a bot restart. The session's Step field
// names the question we're waiting on:
//
//	team   → reply with a number choosing among multiple runnable templates
//	tz     → pick a timezone from the select menu (only when not yet remembered)
//	title  → free-text run title
//	when   → free-text date/time ("tomorrow at 10pm", "Friday at 2100")
//	confirm→ "yes" to accept the parsed time, or send a new one
//	body   → free-text post-body override, or "skip" for the template default
const (
	premadeStepTeam     = "team"
	premadeStepTimezone = "tz"
	premadeStepTitle    = "title"
	premadeStepWhen     = "when"
	premadeStepConfirm  = "confirm"
	premadeStepBody     = "body"
)

// startPremadeDM opens a DM with the runner and seeds a conversation session.
// It always asks the user to pick which template to run (by number), even when
// only one is runnable, so the choice is explicit. Called from handlePremade.
func (b *bot) startPremadeDM(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, user *discordgo.User, appUserID int64, teams []models.Team) {
	dm, err := s.UserChannelCreate(user.ID)
	if err != nil {
		log.Printf("premade dm: open dm: %v", err)
		ephemeral(s, i, "I couldn't DM you (your DMs may be closed). Enable DMs from server members and run /coreteam signup again.")
		return
	}

	sess := &models.PremadeSession{
		DiscordUserID: user.ID,
		AppUserID:     appUserID,
		GuildID:       i.GuildID,
		ChannelID:     i.ChannelID,
		DMChannelID:   dm.ID,
	}

	sess.Step = premadeStepTeam
	if _, err := s.ChannelMessageSend(dm.ID, premadeTeamListMessage(teams)); err != nil {
		log.Printf("premade dm: team list: %v", err)
		ephemeral(s, i, "I couldn't DM you (your DMs may be closed). Enable DMs from server members and run /coreteam signup again.")
		return
	}

	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade dm: upsert session: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	ephemeral(s, i, "Check your DMs — I'll walk you through posting the run there.")
}

// promptAfterTeam moves the conversation past team selection: if the user has no
// remembered timezone it sends the one-time timezone picker, otherwise it asks
// for the run title. It mutates sess.Step but leaves persistence to the caller.
func (b *bot) promptAfterTeam(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, teamName string) error {
	tz, err := b.discord.GetUserTimezone(ctx, sess.AppUserID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tz) == "" {
		sess.Step = premadeStepTimezone
		opts := make([]discordgo.SelectMenuOption, 0, len(signupTimezones))
		for _, z := range signupTimezones {
			opts = append(opts, discordgo.SelectMenuOption{Label: tzOffsetLabel(z), Value: z})
		}
		_, err := s.ChannelMessageSendComplex(sess.DMChannelID, &discordgo.MessageSend{
			Content:    fmt.Sprintf("Let's set up a run for **%s**.\n\nFirst, what's your timezone? I'll remember it so I don't have to ask again.", teamName),
			Components: selectRow(premadeDMTimezoneID, "Select your timezone", 1, 1, opts),
		})
		return err
	}
	sess.Step = premadeStepTitle
	_, err = s.ChannelMessageSend(sess.DMChannelID, fmt.Sprintf("Let's set up a run for **%s**.\n\nWhat's the **title** of this run? (e.g. \"Saturday vAA Carry\")", teamName))
	return err
}

// handleTimezone lets a user set or change their remembered timezone at any time
// (independent of an in-progress signup), so a wrong pick can be corrected. It
// shows an ephemeral select; the choice is saved by handleTimezoneSelect.
func (b *bot) handleTimezone(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		ephemeral(s, i, "Link your account first with /coreteam link.")
		return
	}
	if err != nil {
		log.Printf("timezone: get user: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	current, err := b.discord.GetUserTimezone(ctx, appUserID)
	if err != nil {
		log.Printf("timezone: get timezone: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	prompt := "Pick your timezone. I'll remember it for scheduling signups."
	if strings.TrimSpace(current) != "" {
		prompt = fmt.Sprintf("Your timezone is currently **%s**. Pick a new one below to change it.", tzOffsetLabel(current))
	}

	opts := make([]discordgo.SelectMenuOption, 0, len(signupTimezones))
	for _, z := range signupTimezones {
		opts = append(opts, discordgo.SelectMenuOption{Label: tzOffsetLabel(z), Value: z})
	}
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsEphemeral,
			Content:    prompt,
			Components: selectRow("timezone_select", "Select your timezone", 1, 1, opts),
		},
	})
	if err != nil {
		log.Printf("timezone: respond: %v", err)
	}
}

// handleTimezoneSelect stores the timezone chosen via /coreteam timezone.
func (b *bot) handleTimezoneSelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	tz := values[0]

	ctx, cancel := handlerContext()
	defer cancel()

	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		updateEphemeral(s, i, "Link your account first with /coreteam link.")
		return
	}
	if err != nil {
		log.Printf("timezone select: get user: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if err := b.discord.SetUserTimezone(ctx, appUserID, tz); err != nil {
		log.Printf("timezone select: set timezone: %v", err)
		updateEphemeral(s, i, "Something went wrong saving your timezone. Please try again.")
		return
	}
	updateEphemeral(s, i, fmt.Sprintf("Timezone set to **%s** — I'll use it for scheduling your signups.", tzOffsetLabel(tz)))
}

// handlePremadeDMTimezone stores the picked timezone, then asks for the title.
func (b *bot) handlePremadeDMTimezone(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	tz := values[0]

	ctx, cancel := handlerContext()
	defer cancel()

	sess, err := b.premade.GetSession(ctx, user.ID)
	if errors.Is(err, models.ErrPremadeSessionNotFound) {
		updateEphemeral(s, i, "This signup expired. Run /coreteam signup again to start over.")
		return
	}
	if err != nil {
		log.Printf("premade dm: tz get session: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please run /coreteam signup again.")
		return
	}
	if err := b.discord.SetUserTimezone(ctx, sess.AppUserID, tz); err != nil {
		log.Printf("premade dm: set timezone: %v", err)
		updateEphemeral(s, i, "Something went wrong saving your timezone. Please try again.")
		return
	}
	sess.Step = premadeStepTitle
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade dm: tz upsert session: %v", err)
	}
	updateEphemeral(s, i, fmt.Sprintf("Timezone set to **%s** — I'll remember it.\n\nWhat's the **title** of this run? (e.g. \"Saturday vAA Carry\")", tzOffsetLabel(tz)))
}

// onMessageCreate routes free-text DM messages into the active signup
// conversation. Non-DM messages, bot messages, and users without an active
// session are ignored.
func (b *bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}
	if s.State != nil && s.State.User != nil && m.Author.ID == s.State.User.ID {
		return
	}
	// Only direct messages drive the conversation (DMs carry no guild id).
	if m.GuildID != "" {
		return
	}
	content := strings.TrimSpace(m.Content)
	if content == "" {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	sess, err := b.premade.GetSession(ctx, m.Author.ID)
	if errors.Is(err, models.ErrPremadeSessionNotFound) {
		return
	}
	if err != nil {
		log.Printf("premade dm: get session: %v", err)
		return
	}

	// A cancel word aborts whatever conversation (create or edit) is active.
	if isCancel(content) {
		_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
		if sess.Mode == premadeModeEdit {
			b.dmSend(s, sess.DMChannelID, "Edit cancelled — nothing was changed. Press **Edit run** on the post to start again.")
		} else {
			b.dmSend(s, sess.DMChannelID, "Signup cancelled — nothing was posted. Run /coreteam signup to start again.")
		}
		return
	}

	switch sess.Step {
	case premadeStepTeam:
		b.premadeDMTeam(ctx, s, sess, content)
	case premadeStepTitle:
		b.premadeDMTitle(ctx, s, sess, content)
	case premadeStepWhen:
		b.premadeDMWhen(ctx, s, sess, content)
	case premadeStepConfirm:
		b.premadeDMConfirm(ctx, s, sess, content)
	case premadeStepBody:
		b.premadeDMBody(ctx, s, sess, content)
	case premadeStepTimezone:
		b.dmSend(s, sess.DMChannelID, "Please pick your timezone from the menu above first.")
	case premadeStepEditField:
		b.dmSend(s, sess.DMChannelID, "Please use the menu above to pick what to edit.")
	case premadeStepEditTitle:
		b.premadeEditTitle(ctx, s, sess, content)
	case premadeStepEditWhen:
		b.premadeEditWhen(ctx, s, sess, content)
	case premadeStepEditBody:
		b.premadeEditBody(ctx, s, sess, content)
	case premadeStepEditSignupName:
		b.premadeEditSignupSearch(ctx, s, sess, content)
	case premadeStepEditSignupPick:
		b.dmSend(s, sess.DMChannelID, "Please pick a player from the menu above.")
	case premadeStepEditSignupSlot:
		b.dmSend(s, sess.DMChannelID, "Please pick a slot from the menu above.")
	}
}

// premadeDMTeam resolves the numbered team choice and advances the conversation.
func (b *bot) premadeDMTeam(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, content string) {
	teams, err := b.listRunnablePremadeTeams(ctx, sess.AppUserID, sess.GuildID)
	if err != nil {
		log.Printf("premade dm: team list: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong loading your templates. Please run /coreteam signup again.")
		return
	}
	if n := len(teams); n > 25 {
		teams = teams[:25]
	}
	if len(teams) == 0 {
		b.dmSend(s, sess.DMChannelID, "You don't have any runnable signup templates anymore.")
		_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
		return
	}
	n, perr := strconv.Atoi(strings.TrimSpace(content))
	if perr != nil || n < 1 || n > len(teams) {
		b.dmSend(s, sess.DMChannelID, fmt.Sprintf("Please reply with a number between 1 and %d.", len(teams)))
		return
	}
	t := teams[n-1]
	sess.TeamID = &t.ID
	if err := b.promptAfterTeam(ctx, s, sess, t.Name); err != nil {
		log.Printf("premade dm: prompt after team: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong. Please run /coreteam signup again.")
		return
	}
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade dm: team upsert: %v", err)
	}
}

// premadeDMTitle records the run title and asks for the date/time.
func (b *bot) premadeDMTitle(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, content string) {
	sess.Title = truncate(content, 100)
	sess.Step = premadeStepWhen
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade dm: title upsert: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong. Please try again.")
		return
	}
	b.dmSend(s, sess.DMChannelID, "Got it. **When** is the run? You can type things like \"tomorrow at 10pm\", \"Friday at 2100\", or \"July 4 at 8:30pm\".")
}

// premadeDMWhen parses the natural-language date/time in the user's timezone and
// asks them to confirm the result.
func (b *bot) premadeDMWhen(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, content string) {
	loc := time.UTC
	if tz, err := b.discord.GetUserTimezone(ctx, sess.AppUserID); err == nil && strings.TrimSpace(tz) != "" {
		if l, lerr := time.LoadLocation(tz); lerr == nil {
			loc = l
		}
	}
	parsed, ok := parseWhen(content, loc)
	if !ok {
		b.dmSend(s, sess.DMChannelID, "I couldn't read that date/time. Try something like \"tomorrow at 10pm\" or \"Friday at 2100\".")
		return
	}
	utc := parsed.UTC()
	sess.ScheduledAt = &utc
	sess.Step = premadeStepConfirm
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade dm: when upsert: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong. Please try again.")
		return
	}
	b.dmSend(s, sess.DMChannelID, fmt.Sprintf("I read that as <t:%d:F> (<t:%d:R>). Reply **yes** to confirm, or send a different date/time.", utc.Unix(), utc.Unix()))
}

// premadeDMConfirm accepts the parsed time on "yes"; otherwise it treats the
// message as a fresh date/time to re-parse.
func (b *bot) premadeDMConfirm(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, content string) {
	if isAffirmative(content) {
		sess.Step = premadeStepBody
		if err := b.premade.UpsertSession(ctx, sess); err != nil {
			log.Printf("premade dm: confirm upsert: %v", err)
			b.dmSend(s, sess.DMChannelID, "Something went wrong. Please try again.")
			return
		}
		b.dmSend(s, sess.DMChannelID, "Last step: send a **custom post body** to override the default for this run, or reply **skip** to use the template's default body.")
		return
	}
	// Anything else is treated as a corrected date/time.
	b.premadeDMWhen(ctx, s, sess, content)
}

// premadeDMBody records the optional post-body override and posts the run.
func (b *bot) premadeDMBody(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, content string) {
	if !isSkip(content) {
		sess.PostOverride = content
	} else {
		sess.PostOverride = ""
	}
	b.finishPremadeDM(ctx, s, sess)
}

// finishPremadeDM re-checks permissions, creates the run, posts the public
// announcement in the original channel, and clears the conversation session.
func (b *bot) finishPremadeDM(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession) {
	if sess.TeamID == nil || sess.ScheduledAt == nil {
		b.dmSend(s, sess.DMChannelID, "Something went wrong — please run /coreteam signup again.")
		_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
		return
	}
	teamID := *sess.TeamID

	_, role, err := b.teams.Access(ctx, teamID, sess.AppUserID)
	if err != nil {
		log.Printf("premade dm: finish access: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong. Please run /coreteam signup again.")
		return
	}
	if role != models.RoleOwner && role != models.RoleEditor {
		// Not an owner/editor — allow only if the template is published to the
		// guild the signup was started in.
		published, perr := b.teams.IsTemplatePublishedToGuild(ctx, teamID, sess.GuildID)
		if perr != nil {
			log.Printf("premade dm: finish published check: %v", perr)
			b.dmSend(s, sess.DMChannelID, "Something went wrong. Please run /coreteam signup again.")
			return
		}
		if !published {
			b.dmSend(s, sess.DMChannelID, "You don't have permission to run that team anymore.")
			_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
			return
		}
	}

	team, _, primary, _, err := b.loadTeamData(ctx, teamID)
	if err != nil {
		log.Printf("premade dm: finish load team: %v", err)
		b.dmSend(s, sess.DMChannelID, "Could not load that team. Please run /coreteam signup again.")
		return
	}
	if !team.PreMade {
		b.dmSend(s, sess.DMChannelID, "That team isn't a signup template anymore.")
		_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
		return
	}

	run, err := b.premade.CreateRun(ctx, teamID, sess.GuildID, sess.ChannelID, sess.Title, sess.PostOverride, *sess.ScheduledAt, sess.AppUserID)
	if err != nil {
		log.Printf("premade dm: create run: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong creating the run. Please try again.")
		return
	}

	embed := b.premadeEmbed(ctx, team, run, primary, nil, nil)
	msg, err := s.ChannelMessageSendComplex(sess.ChannelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: premadeComponents(team, nil),
	})
	if err != nil {
		log.Printf("premade dm: post run: %v", err)
		b.dmSend(s, sess.DMChannelID, "I couldn't post the run in that channel. Check my permissions there and run /coreteam signup again.")
		_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
		return
	}
	if err := b.premade.SetRunMessage(ctx, run.ID, msg.ID); err != nil {
		log.Printf("premade dm: set message id: %v", err)
	}
	// Create the discussion thread now so players can talk about the trial right
	// away; the signup ping is still sent ~15 min before start (tagRunSignups).
	run.MessageID = msg.ID
	b.createRunThread(ctx, s, run)
	_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
	done := fmt.Sprintf("Posted **%s** for <t:%d:F>. Players can claim slots from the post now, and discuss in its thread.", run.Title, run.ScheduledAt.Unix())
	done += postLinkSuffix(messageURL(sess.GuildID, sess.ChannelID, msg.ID))
	b.dmSend(s, sess.DMChannelID, done)
}

// dmSend sends a plain DM message, logging (not surfacing) any failure.
func (b *bot) dmSend(s *discordgo.Session, channelID, content string) {
	if _, err := s.ChannelMessageSend(channelID, content); err != nil {
		log.Printf("premade dm: send: %v", err)
	}
}

// premadeTeamListMessage renders the numbered template picker (capped at 25).
func premadeTeamListMessage(teams []models.Team) string {
	var b strings.Builder
	b.WriteString("Which signup template would you like to run? Reply with a number (or type **cancel** anytime to stop):")
	limit := len(teams)
	if limit > 25 {
		limit = 25
	}
	for idx := 0; idx < limit; idx++ {
		b.WriteString(fmt.Sprintf("\n**%d.** %s", idx+1, teams[idx].Name))
	}
	return b.String()
}

// whenParser parses natural-language English dates/times. Built once; the rule
// sets are stateless so it is safe to share across goroutines.
var whenParser = func() *when.Parser {
	w := when.New(nil)
	w.Add(en.All...)
	w.Add(common.All...)
	return w
}()

// militaryTimeRe matches a 3-4 digit military time following "at"/"@" (e.g.
// "Friday at 2100"), which the NLP rules don't recognize on their own. The
// "at"/"@" anchor avoids rewriting bare years like "2026".
var militaryTimeRe = regexp.MustCompile(`(?i)\b(at|@)\s+(\d{3,4})\b`)

// normalizeMilitaryTime rewrites "<at|@> HHMM" into "<at|@> HH:MM" so the parser
// can read 24-hour times. Invalid clock values are left untouched.
func normalizeMilitaryTime(text string) string {
	return militaryTimeRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := militaryTimeRe.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		digits := sub[2]
		if len(digits) == 3 {
			digits = "0" + digits
		}
		hh, _ := strconv.Atoi(digits[:2])
		mm, _ := strconv.Atoi(digits[2:])
		if hh > 23 || mm > 59 {
			return match
		}
		return fmt.Sprintf("%s %02d:%02d", sub[1], hh, mm)
	})
}

// parseWhen resolves a natural-language date/time relative to now in loc. It
// returns ok=false when nothing parseable is found or the result is in the past
// only because no time component was given is not treated specially (the confirm
// step lets the user correct it).
func parseWhen(text string, loc *time.Location) (time.Time, bool) {
	r, err := whenParser.Parse(normalizeMilitaryTime(text), time.Now().In(loc))
	if err != nil || r == nil {
		return time.Time{}, false
	}
	return r.Time, true
}

// isAffirmative reports whether a reply means "yes, confirm".
func isAffirmative(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "y", "yeah", "yep", "yup", "confirm", "ok", "okay", "sure", "correct":
		return true
	}
	return false
}

// isSkip reports whether a reply means "use the default post body".
func isSkip(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "skip", "default", "none", "no":
		return true
	}
	return false
}

// isCancel reports whether a reply means "abort this DM conversation". Checked
// before any step handler so it works from every step of both the create and
// edit flows.
func isCancel(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cancel", "stop", "quit", "abort", "exit", "nevermind", "never mind":
		return true
	}
	return false
}

// premadeEditSignupSearch handles a free-text name entry in the "sign up a
// player" sub-flow. It searches the guild for members whose name starts with the
// query and presents a picker select. The typed text is stored in
// SignupUserName so the editor can choose "use as-is" without a matched account.
func (b *bot) premadeEditSignupSearch(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		b.dmSend(s, sess.DMChannelID, "Please type a name to search for.")
		return
	}

	// Park the typed text so the "raw" option in the picker can refer back to it.
	sess.SignupUserName = truncate(query, 100)
	sess.SignupUserID = ""
	sess.Step = premadeStepEditSignupPick
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade edit: signup search persist: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong. Please try again.")
		return
	}

	// Search the guild for members whose display name or username starts with
	// the query (up to 9 results — leave the last slot for the "as-is" option).
	opts := make([]discordgo.SelectMenuOption, 0, 10)
	if sess.GuildID != "" {
		members, err := s.GuildMembersSearch(sess.GuildID, query, 9)
		if err != nil {
			log.Printf("premade edit: guild member search: %v", err)
		}
		for _, m := range members {
			if m.User == nil {
				continue
			}
			name := memberDisplayName(m)
			if name == "" {
				name = displayName(m.User)
			}
			label := name
			if m.User.Username != name {
				label += " (@" + m.User.Username + ")"
			}
			opts = append(opts, discordgo.SelectMenuOption{
				Label:       truncate(label, 100),
				Value:       m.User.ID,
				Description: "Discord member",
			})
		}
	}

	// Always offer an "add as-is" option so the editor isn't blocked when the
	// target isn't in the guild member list (e.g. a friend who hasn't joined yet).
	rawLabel := fmt.Sprintf("Add \"%s\" as-is (no Discord match)", query)
	opts = append(opts, discordgo.SelectMenuOption{
		Label:       truncate(rawLabel, 100),
		Value:       "raw",
		Description: "Sign up without a matched Discord account",
	})

	msg := "Pick a matching member, or choose the last option to use the name as typed:"
	if len(opts) == 1 {
		msg = fmt.Sprintf("No guild members matched \"%s\". Pick the option below to add them by name anyway:", query)
	}
	if _, err := s.ChannelMessageSendComplex(sess.DMChannelID, &discordgo.MessageSend{
		Content:    msg,
		Components: selectRow(premadeEditSignupPickID, "Pick a player", 1, 1, opts),
	}); err != nil {
		log.Printf("premade edit: signup search send: %v", err)
	}
}
