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
	"github.com/core-team-builder/backend/internal/models"
)

// The "Edit run" button on a posted signup starts a DM conversation that lets
// the run's creator (or the team's owner/editor) change the run's title, time,
// or description. It reuses the per-user premade_signup_sessions row in "edit"
// mode (run_id set); applying a field updates premade_runs and re-renders the
// posted announcement in place. See premade_dm.go for the create flow.
const (
	premadeModeEdit = "edit"

	premadeStepEditField = "edit_field"
	premadeStepEditTitle = "edit_title"
	premadeStepEditWhen  = "edit_when"
	premadeStepEditBody  = "edit_body"

	// "Sign up a player" sub-conversation: ask for a name, pick a matched member
	// (or use the typed text), then pick the slot/role to put them in.
	premadeStepEditSignupName = "edit_signup_name"
	premadeStepEditSignupPick = "edit_signup_pick"
	premadeStepEditSignupSlot = "edit_signup_slot"

	// "Remove a signup" sub-conversation: pick which current claimant to release.
	premadeStepEditRemovePick = "edit_remove_pick"
)

// handlePremadeEdit starts the edit DM. It resolves the run from the pressed
// message, checks the presser may edit it, opens a DM, and shows the field menu.
func (b *bot) handlePremadeEdit(s *discordgo.Session, i *discordgo.InteractionCreate) {
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

	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		ephemeral(s, i, "Link your account first with /coreteam link to edit a run.")
		return
	}
	if err != nil {
		log.Printf("premade edit: get user: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	allowed, err := b.canEditRun(ctx, run, appUserID)
	if err != nil {
		log.Printf("premade edit: permission: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if !allowed {
		ephemeral(s, i, "Only the person who created this run (or a team owner/editor) can edit it.")
		return
	}

	dm, err := s.UserChannelCreate(user.ID)
	if err != nil {
		log.Printf("premade edit: open dm: %v", err)
		ephemeral(s, i, "I couldn't DM you (your DMs may be closed). Enable DMs from server members and try again.")
		return
	}

	runID := run.ID
	sess := &models.PremadeSession{
		DiscordUserID: user.ID,
		AppUserID:     appUserID,
		TeamID:        &run.TeamID,
		GuildID:       run.GuildID,
		ChannelID:     run.ChannelID,
		DMChannelID:   dm.ID,
		Step:          premadeStepEditField,
		Mode:          premadeModeEdit,
		RunID:         &runID,
	}
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade edit: upsert session: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if err := b.sendEditFieldMenu(s, dm.ID, run, "You're editing this run. What would you like to change? (Type **cancel** anytime to stop.)"); err != nil {
		log.Printf("premade edit: send menu: %v", err)
		ephemeral(s, i, "I couldn't DM you (your DMs may be closed). Enable DMs from server members and try again.")
		return
	}
	ephemeral(s, i, "Check your DMs — I'll help you edit this run there.")
}

// handlePremadeDelete deletes a posted run. New posts no longer carry a Delete
// button (deleting now lives behind the "Edit run" DM menu, see
// handlePremadeEditFieldSelect), but this remains so the button on posts made
// before that change keeps working. Like the edit button it's gated to the run's
// creator or a team owner/editor (Discord can't hide the button per-user, so
// non-editors get an ephemeral rejection). It tears down the post (and thread,
// if any) and marks the run cleaned up so it's no longer active.
func (b *bot) handlePremadeDelete(s *discordgo.Session, i *discordgo.InteractionCreate) {
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

	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		ephemeral(s, i, "Link your account first with /coreteam link to delete a run.")
		return
	}
	if err != nil {
		log.Printf("premade delete: get user: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	allowed, err := b.canEditRun(ctx, run, appUserID)
	if err != nil {
		log.Printf("premade delete: permission: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if !allowed {
		ephemeral(s, i, "Only the person who created this run (or a team owner/editor) can delete it.")
		return
	}

	// Acknowledge privately first (the message this interaction came from is
	// about to be deleted), then tear down the post/thread and mark it done. If
	// the thread couldn't be removed (usually a missing Manage Threads
	// permission), follow up so the user knows it's now orphaned.
	ephemeral(s, i, "Deleted this run.")
	if err := b.cleanupRun(ctx, s, *run); err != nil {
		warnThreadCleanupFailed(s, i)
	}
}

// warnThreadCleanupFailed sends an ephemeral follow-up after a run was deleted
// but its discussion thread couldn't be removed — almost always because the bot
// lacks the Manage Threads permission in that channel (deleting the post does
// not delete the thread). It points the user at the fix so the orphaned thread
// doesn't linger silently.
func warnThreadCleanupFailed(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Flags:   discordgo.MessageFlagsEphemeral,
		Content: "Heads up: I removed the run's post but couldn't delete its discussion thread. I likely need the **Manage Threads** permission in that channel — grant it (then delete the thread), or remove the thread manually.",
	})
	if err != nil {
		log.Printf("premade delete: thread cleanup warning: %v", err)
	}
}

// canEditRun reports whether the app user may edit the run: the original creator,
// or an owner/editor of the run's team.
func (b *bot) canEditRun(ctx context.Context, run *models.PremadeRun, appUserID int64) (bool, error) {
	if run.CreatedBy != nil && *run.CreatedBy == appUserID {
		return true, nil
	}
	_, role, err := b.teams.Access(ctx, run.TeamID, appUserID)
	if err != nil {
		return false, err
	}
	return role == models.RoleOwner || role == models.RoleEditor, nil
}

// sendEditFieldMenu sends the "what to edit" select menu, prefixed with a status
// line that summarizes the run's current title/time/body.
func (b *bot) sendEditFieldMenu(s *discordgo.Session, dmChannelID string, run *models.PremadeRun, prefix string) error {
	body := "the template default"
	if strings.TrimSpace(run.PostOverride) != "" {
		body = "custom"
	}
	content := fmt.Sprintf("%s\n\n**Title:** %s\n**Time:** <t:%d:F>\n**Description:** %s",
		prefix, run.Title, run.ScheduledAt.Unix(), body)
	opts := []discordgo.SelectMenuOption{
		{Label: "Title", Value: "title", Description: "Change the run's title"},
		{Label: "Date & time", Value: "when", Description: "Reschedule the run"},
		{Label: "Description", Value: "body", Description: "Change or clear the post body"},
		{Label: "Sign up a player", Value: "signup", Description: "Add someone else to a slot"},
		{Label: "Remove a signup", Value: "remove", Description: "Release a claimed slot"},
		{Label: "Delete run", Value: "delete", Description: "Delete this run and its post"},
		{Label: "Done", Value: "done", Description: "Finish editing"},
	}
	_, err := s.ChannelMessageSendComplex(dmChannelID, &discordgo.MessageSend{
		Content:    content,
		Components: selectRow(premadeEditFieldID, "Choose what to edit", 1, 1, opts),
	})
	return err
}

// handlePremadeEditFieldSelect routes the chosen field to its prompt, or ends the
// edit session on "done".
func (b *bot) handlePremadeEditFieldSelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	sess, err := b.premade.GetSession(ctx, user.ID)
	if errors.Is(err, models.ErrPremadeSessionNotFound) {
		updateEphemeral(s, i, "This edit expired. Press **Edit run** on the post to start again.")
		return
	}
	if err != nil {
		log.Printf("premade edit: field get session: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if sess.Mode != premadeModeEdit || sess.RunID == nil {
		updateEphemeral(s, i, "This edit expired. Press **Edit run** on the post to start again.")
		return
	}

	switch values[0] {
	case "done":
		_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
		updateEphemeral(s, i, "Done editing this run.")
		return
	case "title":
		sess.Step = premadeStepEditTitle
		b.persistAndPrompt(ctx, s, i, sess, "Send the **new title** for this run.")
	case "when":
		sess.Step = premadeStepEditWhen
		b.persistAndPrompt(ctx, s, i, sess, "Send the **new date/time**. You can type things like \"tomorrow at 10pm\" or \"Friday at 2100\".")
	case "body":
		sess.Step = premadeStepEditBody
		b.persistAndPrompt(ctx, s, i, sess, "Send the **new description** to override the default, or reply **clear** to use the template's default body.")
	case "signup":
		sess.Step = premadeStepEditSignupName
		sess.SignupUserID = ""
		sess.SignupUserName = ""
		b.persistAndPrompt(ctx, s, i, sess, "Who would you like to sign up? Type a Discord name to search for them, or just type any name to add as-is.")
	case "remove":
		b.promptRemoveSignup(ctx, s, i, sess)
		return
	case "delete":
		run, ok := b.editTargetRun(ctx, s, sess)
		if !ok {
			updateEphemeral(s, i, "That run is no longer active.")
			return
		}
		threadErr := b.cleanupRun(ctx, s, *run)
		_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
		msg := "Deleted this run and its post."
		if threadErr != nil {
			msg = "Deleted this run and its post, but I couldn't delete its discussion thread — I likely need the **Manage Threads** permission in that channel. Please remove the thread manually."
		}
		updateEphemeral(s, i, msg)
		return
	default:
		updateEphemeral(s, i, "That selection was invalid.")
	}
}

// persistAndPrompt saves the session's new step and replaces the menu message
// with the prompt for the awaited value.
func (b *bot) persistAndPrompt(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, sess *models.PremadeSession, prompt string) {
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade edit: persist step: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	updateEphemeral(s, i, prompt)
}

// premadeEditTitle applies a new title, refreshes the post, and re-shows the menu.
func (b *bot) premadeEditTitle(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, content string) {
	run, ok := b.editTargetRun(ctx, s, sess)
	if !ok {
		return
	}
	updated, err := b.premade.UpdateRun(ctx, run.ID, truncate(content, 100), run.PostOverride, run.ScheduledAt)
	if err != nil {
		log.Printf("premade edit: update title: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong saving the title. Please try again.")
		return
	}
	b.afterEditApplied(ctx, s, sess, updated, "Title updated.")
}

// premadeEditWhen parses and applies a new time in the user's timezone.
func (b *bot) premadeEditWhen(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, content string) {
	run, ok := b.editTargetRun(ctx, s, sess)
	if !ok {
		return
	}
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
	updated, err := b.premade.UpdateRun(ctx, run.ID, run.Title, run.PostOverride, parsed.UTC())
	if err != nil {
		log.Printf("premade edit: update when: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong saving the time. Please try again.")
		return
	}
	b.afterEditApplied(ctx, s, sess, updated, fmt.Sprintf("Time updated to <t:%d:F>.", updated.ScheduledAt.Unix()))
}

// premadeEditBody applies a new (or cleared) post-body override.
func (b *bot) premadeEditBody(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, content string) {
	run, ok := b.editTargetRun(ctx, s, sess)
	if !ok {
		return
	}
	body := content
	if isClear(content) {
		body = ""
	}
	updated, err := b.premade.UpdateRun(ctx, run.ID, run.Title, body, run.ScheduledAt)
	if err != nil {
		log.Printf("premade edit: update body: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong saving the description. Please try again.")
		return
	}
	msg := "Description updated."
	if body == "" {
		msg = "Description reset to the template default."
	}
	b.afterEditApplied(ctx, s, sess, updated, msg)
}

// editTargetRun loads the run the edit session targets, ending the session and
// notifying the user when it is gone.
func (b *bot) editTargetRun(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession) (*models.PremadeRun, bool) {
	if sess.RunID == nil {
		_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
		b.dmSend(s, sess.DMChannelID, "This edit expired. Press **Edit run** on the post to start again.")
		return nil, false
	}
	run, err := b.premade.GetRun(ctx, *sess.RunID)
	if errors.Is(err, models.ErrPremadeRunNotFound) {
		_ = b.premade.DeleteSession(ctx, sess.DiscordUserID)
		b.dmSend(s, sess.DMChannelID, "That run is no longer active, so there's nothing to edit.")
		return nil, false
	}
	if err != nil {
		log.Printf("premade edit: get run: %v", err)
		b.dmSend(s, sess.DMChannelID, "Something went wrong. Please try again.")
		return nil, false
	}
	return run, true
}

// afterEditApplied refreshes the posted announcement, confirms the change, and
// returns the conversation to the field menu for further edits.
func (b *bot) afterEditApplied(ctx context.Context, s *discordgo.Session, sess *models.PremadeSession, run *models.PremadeRun, confirm string) {
	if err := b.refreshPremadePostMessage(ctx, s, run); err != nil {
		log.Printf("premade edit: refresh post: %v", err)
		b.dmSend(s, sess.DMChannelID, confirm+" (I couldn't refresh the post automatically.)")
	}
	sess.Step = premadeStepEditField
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade edit: reset step: %v", err)
	}
	if err := b.sendEditFieldMenu(s, sess.DMChannelID, run, confirm+" Edit something else, or choose **Done**."); err != nil {
		log.Printf("premade edit: re-send menu: %v", err)
	}
}

// isClear reports whether a reply means "reset to the template default body".
func isClear(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "clear", "default", "reset", "none", "skip":
		return true
	}
	return false
}

// handlePremadeEditSignupPick resolves the chosen guild member (or the "raw"
// typed-name option) in the "sign up a player" sub-flow, then presents a
// slot/role picker so the editor can choose where to put them.
func (b *bot) handlePremadeEditSignupPick(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	sess, err := b.premade.GetSession(ctx, user.ID)
	if errors.Is(err, models.ErrPremadeSessionNotFound) {
		updateEphemeral(s, i, "This edit expired. Press **Edit run** on the post to start again.")
		return
	}
	if err != nil {
		log.Printf("premade edit: signup pick get session: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if sess.Mode != premadeModeEdit || sess.RunID == nil {
		updateEphemeral(s, i, "This edit expired. Press **Edit run** on the post to start again.")
		return
	}

	picked := values[0]
	if picked == "raw" {
		// Keep sess.SignupUserName (already saved from the search step).
		sess.SignupUserID = ""
	} else {
		// Resolve the picked member's display name from the guild or API.
		sess.SignupUserID = picked
		name := b.resolveMemberName(s, sess.GuildID, picked)
		if name == "" {
			if u, uerr := s.User(picked); uerr == nil {
				name = displayName(u)
			}
		}
		if name == "" {
			name = picked
		}
		sess.SignupUserName = name
	}
	sess.Step = premadeStepEditSignupSlot
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade edit: signup pick persist: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	run, ok := b.editTargetRun(ctx, s, sess)
	if !ok {
		updateEphemeral(s, i, "That run is no longer active.")
		return
	}
	team, _, _, _, err := b.loadTeamData(ctx, run.TeamID)
	if err != nil {
		log.Printf("premade edit: signup pick load team: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	signups, err := b.premade.ListSignups(ctx, run.ID)
	if err != nil {
		log.Printf("premade edit: signup pick list signups: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	opts := signupSlotOptions(team, signups)
	if len(opts) == 0 {
		updateEphemeral(s, i, "There are no open slots on this run right now.")
		return
	}

	placeholder := "Choose a slot"
	if team.SimpleSignup {
		placeholder = "Choose a role"
	}
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("Signing up **%s** — which slot?", sess.SignupUserName),
			Components: selectRow(premadeEditSignupSlotID, placeholder, 1, 1, opts),
		},
	})
	if err != nil {
		log.Printf("premade edit: signup pick respond: %v", err)
	}
}

// handlePremadeEditSignupSlot claims the chosen slot (or first open slot for the
// chosen role in simple mode) on behalf of the target stored in the session,
// refreshes the post, and returns to the field menu.
func (b *bot) handlePremadeEditSignupSlot(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	sess, err := b.premade.GetSession(ctx, user.ID)
	if errors.Is(err, models.ErrPremadeSessionNotFound) {
		updateEphemeral(s, i, "This edit expired. Press **Edit run** on the post to start again.")
		return
	}
	if err != nil {
		log.Printf("premade edit: signup slot get session: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if sess.Mode != premadeModeEdit || sess.RunID == nil {
		updateEphemeral(s, i, "This edit expired. Press **Edit run** on the post to start again.")
		return
	}

	run, ok := b.editTargetRun(ctx, s, sess)
	if !ok {
		updateEphemeral(s, i, "That run is no longer active.")
		return
	}
	team, _, _, _, err := b.loadTeamData(ctx, run.TeamID)
	if err != nil {
		log.Printf("premade edit: signup slot load team: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	// Derive a stable identifier for non-Discord signups so ClaimSlot can
	// release any prior claim by the same "person" (all real Discord user IDs
	// are numeric; the "n:" prefix keeps them apart).
	targetID := sess.SignupUserID
	targetName := sess.SignupUserName
	if targetID == "" {
		targetID = "n:" + strings.ToLower(strings.TrimSpace(targetName))
	}

	var slot int
	if team.SimpleSignup {
		role := values[0]
		signups, serr := b.premade.ListSignups(ctx, run.ID)
		if serr != nil {
			log.Printf("premade edit: signup slot list signups: %v", serr)
			updateEphemeral(s, i, "Something went wrong. Please try again.")
			return
		}
		taken := map[int]bool{}
		for _, sg := range signups {
			taken[sg.Slot] = true
		}
		sl, ok2 := firstOpenSlotForRole(team, taken, role)
		if !ok2 {
			updateEphemeral(s, i, "No open slots for that role right now. Choose another role.")
			return
		}
		slot = sl
	} else {
		slot, err = strconv.Atoi(values[0])
		if err != nil {
			return
		}
	}

	if err := b.premade.ClaimSlot(ctx, run.ID, slot, targetID, targetName); err != nil {
		if errors.Is(err, models.ErrSlotTaken) {
			updateEphemeral(s, i, "That slot was just taken by someone else. Choose another slot.")
			return
		}
		log.Printf("premade edit: signup slot claim: %v", err)
		updateEphemeral(s, i, "Something went wrong claiming that slot. Please try again.")
		return
	}

	// In simple mode, pack remaining claimants up so empty slots stay at the bottom.
	if team.SimpleSignup {
		b.compactSimpleSignups(ctx, run, team)
	}

	// Refresh the posted announcement before sending the field menu.
	if rerr := b.refreshPremadePostMessage(ctx, s, run); rerr != nil {
		log.Printf("premade edit: signup slot refresh post: %v", rerr)
	}

	confirm := fmt.Sprintf("Signed up **%s** for slot %d.", targetName, slot)
	if team.SimpleSignup {
		confirm = fmt.Sprintf("Signed up **%s**.", targetName)
	}

	// Reset the sub-flow and return to the field menu.
	sess.Step = premadeStepEditField
	sess.SignupUserID = ""
	sess.SignupUserName = ""
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade edit: signup slot persist: %v", err)
	}

	// Acknowledge the slot-picker interaction (clears the select UI).
	if aerr := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    confirm,
			Components: []discordgo.MessageComponent{},
		},
	}); aerr != nil {
		log.Printf("premade edit: signup slot ack: %v", aerr)
	}

	// Fetch the latest run state for the refreshed field menu.
	run2, rerr := b.premade.GetRun(ctx, run.ID)
	if rerr != nil {
		run2 = run
	}
	if err := b.sendEditFieldMenu(s, sess.DMChannelID, run2, confirm+" Edit something else, or choose **Done**."); err != nil {
		log.Printf("premade edit: signup slot send menu: %v", err)
	}
}

// signupSlotOptions builds the slot or role options for the "sign up a player"
// slot picker. In specific mode every open slot is listed (taken slots are
// excluded since ClaimSlot would reject them). In simple mode each role with at
// least one open slot is listed.
func signupSlotOptions(team *models.Team, signups []models.PremadeSignup) []discordgo.SelectMenuOption {
	taken := map[int]bool{}
	for _, sg := range signups {
		taken[sg.Slot] = true
	}

	if team.SimpleSignup {
		openByRole := map[string]int{}
		for _, p := range team.Players {
			if p.Role != "" && !taken[p.Slot] {
				openByRole[p.Role]++
			}
		}
		seen := map[string]bool{}
		opts := make([]discordgo.SelectMenuOption, 0, 8)
		addRole := func(role string) {
			if role == "" || seen[role] || openByRole[role] <= 0 {
				return
			}
			seen[role] = true
			opts = append(opts, discordgo.SelectMenuOption{
				Label: truncate(fmt.Sprintf("%s (%d open)", team.RoleLabel(role), openByRole[role]), 100),
				Value: role,
				Emoji: &discordgo.ComponentEmoji{Name: team.RoleEmoji(role)},
			})
		}
		playerRoles := make([]string, 0, len(team.Players))
		for _, p := range team.Players {
			playerRoles = append(playerRoles, p.Role)
		}
		for _, r := range team.OrderedRoleKeys(playerRoles...) {
			addRole(r)
		}
		return opts
	}

	opts := make([]discordgo.SelectMenuOption, 0, len(team.Players))
	for _, p := range team.Players {
		if taken[p.Slot] {
			continue
		}
		opts = append(opts, discordgo.SelectMenuOption{
			Label: truncate(slotOptionLabel(team, p), 100),
			Value: strconv.Itoa(p.Slot),
			Emoji: &discordgo.ComponentEmoji{Name: team.RoleEmoji(p.Role)},
		})
	}
	return opts
}

// promptRemoveSignup lists the run's current claimants so the editor can release
// one. When there are no signups it says so and returns to the field menu.
func (b *bot) promptRemoveSignup(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, sess *models.PremadeSession) {
	run, ok := b.editTargetRun(ctx, s, sess)
	if !ok {
		updateEphemeral(s, i, "That run is no longer active.")
		return
	}
	team, _, _, _, err := b.loadTeamData(ctx, run.TeamID)
	if err != nil {
		log.Printf("premade edit: remove load team: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	signups, err := b.premade.ListSignups(ctx, run.ID)
	if err != nil {
		log.Printf("premade edit: remove list signups: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	opts := removeSignupOptions(team, signups)
	if len(opts) == 0 {
		// Nothing to remove — update the menu message and re-show the field menu
		// so the conversation keeps going.
		updateEphemeral(s, i, "No one has signed up for this run yet, so there's nothing to remove.")
		sess.Step = premadeStepEditField
		if uerr := b.premade.UpsertSession(ctx, sess); uerr != nil {
			log.Printf("premade edit: remove reset step: %v", uerr)
		}
		if merr := b.sendEditFieldMenu(s, sess.DMChannelID, run, "Nothing to remove. Edit something else, or choose **Done**."); merr != nil {
			log.Printf("premade edit: remove re-send menu: %v", merr)
		}
		return
	}

	sess.Step = premadeStepEditRemovePick
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade edit: remove persist step: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "Which signup would you like to remove?",
			Components: selectRow(premadeEditRemoveID, "Choose a signup to remove", 1, 1, opts),
		},
	})
	if err != nil {
		log.Printf("premade edit: remove respond: %v", err)
	}
}

// handlePremadeEditRemoveSignup releases the chosen claimant's slot, promotes a
// waitlister into it when enabled, compacts simple-signup claimants, refreshes
// the post, and returns to the field menu — mirroring a player's own "Leave".
func (b *bot) handlePremadeEditRemoveSignup(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	sess, err := b.premade.GetSession(ctx, user.ID)
	if errors.Is(err, models.ErrPremadeSessionNotFound) {
		updateEphemeral(s, i, "This edit expired. Press **Edit run** on the post to start again.")
		return
	}
	if err != nil {
		log.Printf("premade edit: remove get session: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if sess.Mode != premadeModeEdit || sess.RunID == nil {
		updateEphemeral(s, i, "This edit expired. Press **Edit run** on the post to start again.")
		return
	}

	run, ok := b.editTargetRun(ctx, s, sess)
	if !ok {
		updateEphemeral(s, i, "That run is no longer active.")
		return
	}
	team, _, _, _, err := b.loadTeamData(ctx, run.TeamID)
	if err != nil {
		log.Printf("premade edit: remove slot load team: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	targetID := values[0]

	// Find the slot/role being freed (for waitlist promotion) and a display name
	// for the confirmation, before releasing the claim.
	freedSlot, freedRole, removedName, held := 0, "", "", false
	signups, err := b.premade.ListSignups(ctx, run.ID)
	if err != nil {
		log.Printf("premade edit: remove slot list signups: %v", err)
		updateEphemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	for _, sg := range signups {
		if sg.DiscordUserID == targetID {
			freedSlot, freedRole, held = sg.Slot, roleForSlot(team, sg.Slot), true
			removedName = removeSignupName(sg)
			break
		}
	}
	if !held {
		updateEphemeral(s, i, "That signup was already removed. Go back and pick another.")
		return
	}

	if err := b.premade.LeaveSlot(ctx, run.ID, targetID); err != nil {
		log.Printf("premade edit: remove slot leave: %v", err)
		updateEphemeral(s, i, "Something went wrong removing that signup. Please try again.")
		return
	}
	// Clear any waitlist entry the same person held (harmless no-op otherwise).
	if err := b.premade.LeaveWaitlist(ctx, run.ID, targetID); err != nil {
		log.Printf("premade edit: remove slot leave waitlist: %v", err)
	}

	b.promoteFreedSlot(ctx, s, run, team, freedSlot, freedRole)
	b.compactSimpleSignups(ctx, run, team)

	if rerr := b.refreshPremadePostMessage(ctx, s, run); rerr != nil {
		log.Printf("premade edit: remove slot refresh post: %v", rerr)
	}

	confirm := fmt.Sprintf("Removed **%s** from this run.", removedName)

	sess.Step = premadeStepEditField
	if err := b.premade.UpsertSession(ctx, sess); err != nil {
		log.Printf("premade edit: remove slot persist: %v", err)
	}

	// Acknowledge the picker interaction (clears the select UI).
	if aerr := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    confirm,
			Components: []discordgo.MessageComponent{},
		},
	}); aerr != nil {
		log.Printf("premade edit: remove slot ack: %v", aerr)
	}

	// Fetch the latest run state for the refreshed field menu.
	run2, rerr := b.premade.GetRun(ctx, run.ID)
	if rerr != nil {
		run2 = run
	}
	if err := b.sendEditFieldMenu(s, sess.DMChannelID, run2, confirm+" Edit something else, or choose **Done**."); err != nil {
		log.Printf("premade edit: remove slot send menu: %v", err)
	}
}

// removeSignupOptions builds the picker of current claimants for removal: one
// option per signed-up slot, labeled with the claimant and their slot/role.
func removeSignupOptions(team *models.Team, signups []models.PremadeSignup) []discordgo.SelectMenuOption {
	opts := make([]discordgo.SelectMenuOption, 0, len(signups))
	for _, sg := range signups {
		roleKey := roleForSlot(team, sg.Slot)
		role := team.RoleLabel(roleKey)
		if role == "" {
			role = "—"
		}
		label := fmt.Sprintf("%s · Slot %d · %s", removeSignupName(sg), sg.Slot, role)
		opts = append(opts, discordgo.SelectMenuOption{
			Label: truncate(label, 100),
			Value: sg.DiscordUserID,
			Emoji: &discordgo.ComponentEmoji{Name: team.RoleEmoji(roleKey)},
		})
	}
	return opts
}

// removeSignupName is the display name for a claimant in the removal picker and
// its confirmation: the stored username, or the raw id when it's blank.
func removeSignupName(sg models.PremadeSignup) string {
	if n := strings.TrimSpace(sg.DiscordUsername); n != "" {
		return n
	}
	return sg.DiscordUserID
}
