package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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

// handlePremadeDelete deletes a posted run. Like the edit button it's gated to
// the run's creator or a team owner/editor (Discord can't hide the button
// per-user, so non-editors get an ephemeral rejection). It tears down the post
// (and thread, if any) and marks the run cleaned up so it's no longer active.
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
	// about to be deleted), then tear down the post/thread and mark it done.
	ephemeral(s, i, "Deleted this run.")
	if threadID := threadCleanupID(run); threadID != "" {
		if _, err := s.ChannelDelete(threadID); err != nil {
			log.Printf("premade delete: delete thread (run %d): %v", run.ID, err)
		}
	}
	if err := s.ChannelMessageDelete(i.ChannelID, i.Message.ID); err != nil {
		log.Printf("premade delete: delete post (run %d): %v", run.ID, err)
	}
	if err := b.premade.MarkCleanedUp(ctx, run.ID); err != nil {
		log.Printf("premade delete: mark cleaned up (run %d): %v", run.ID, err)
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
