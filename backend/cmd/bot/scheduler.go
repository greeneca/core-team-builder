package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/models"
)

// schedulerInterval is how often the background loop scans for due pre-made-run
// actions. The triggers (15 min before, 2 h after) tolerate up to this much
// jitter, which is fine for a trial reminder.
const schedulerInterval = 60 * time.Second

// premadeThreadAutoArchive is the thread's inactivity auto-archive window
// (minutes). 1440 = 24h, comfortably past the 2-hour cleanup.
const premadeThreadAutoArchive = 1440

// Cleanup retry policy. When a run's thread can't be deleted (almost always a
// missing Manage Threads permission), the scheduler retries with exponential
// backoff — cleanupBaseBackoff doubled per prior failure, capped at
// cleanupMaxBackoff — rather than hammering Discord every tick. After
// cleanupMaxAttempts failures it gives up and marks the run cleanup-failed so it
// stops being revisited (instead of retrying forever).
const (
	cleanupMaxAttempts = 10
	cleanupBaseBackoff = 5 * time.Minute
	cleanupMaxBackoff  = 6 * time.Hour
)

// cleanupBackoff returns the delay before the given attempt (1-based: attempt 1
// is the first retry after the initial failure), using exponential growth capped
// at cleanupMaxBackoff. The bit-shift guard also caps on overflow.
func cleanupBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	backoff := cleanupBaseBackoff << (attempt - 1)
	if backoff <= 0 || backoff > cleanupMaxBackoff {
		return cleanupMaxBackoff
	}
	return backoff
}

// runScheduler runs the pre-made-run background loop until ctx is cancelled. It
// is the bot's only time-based worker: it pings everyone who signed up in the
// run's discussion thread ~15 minutes before the scheduled start, and deletes
// the post + thread ~2 hours after. (The thread itself is created up front when
// the run is posted — see createRunThread.) Both actions are tracked in the DB
// so they fire exactly once and are caught up after a restart.
func (b *bot) runScheduler(ctx context.Context, session *discordgo.Session) {
	// Run an immediate pass on startup so anything that came due while the bot
	// was offline is handled right away, then on each tick.
	b.schedulerTick(ctx, session)
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.schedulerTick(ctx, session)
		}
	}
}

func (b *bot) schedulerTick(ctx context.Context, session *discordgo.Session) {
	now := time.Now().UTC()

	// Cleanups first so a long-offline, already-finished run is removed rather
	// than getting a late thread.
	cleanups, err := b.withTimeout(ctx, func(c context.Context) ([]models.PremadeRun, error) {
		return b.premade.DueCleanupRuns(c, now)
	})
	if err != nil {
		log.Printf("scheduler: due cleanups: %v", err)
	}
	for _, run := range cleanups {
		// Delete the post + thread, then mark the run done. If the thread could
		// not be removed (almost always a missing Manage Threads permission), we
		// leave cleaned_up_at NULL and schedule a backed-off retry so cleanup
		// self-heals the moment an admin grants the permission — but only up to a
		// cap, after which the run is marked cleanup-failed and left alone rather
		// than retried forever.
		if err := b.cleanupRun(ctx, session, run); err != nil {
			b.recordCleanupFailure(ctx, run)
			continue
		}
		b.markRunCleanedUp(ctx, run.ID)
	}

	tags, err := b.withTimeout(ctx, func(c context.Context) ([]models.PremadeRun, error) {
		return b.premade.DueThreadRuns(c, now)
	})
	if err != nil {
		log.Printf("scheduler: due signup tags: %v", err)
	}
	for _, run := range tags {
		b.tagRunSignups(ctx, session, run)
	}

	// Pre-run pings for recurring /coreteam post overviews (15 min before start).
	pc, pcancel := context.WithTimeout(ctx, 10*time.Second)
	posts, err := b.discord.DuePostPings(pc, now)
	pcancel()
	if err != nil {
		log.Printf("scheduler: due post pings: %v", err)
	}
	for _, p := range posts {
		b.pingPostAttendees(ctx, session, p)
	}

	// RSVP reminders for recurring /coreteam post overviews (48 h before start):
	// nudge assigned roster members who still haven't responded.
	rc, rcancel := context.WithTimeout(ctx, 10*time.Second)
	reminders, err := b.discord.DueReminders(rc, now)
	rcancel()
	if err != nil {
		log.Printf("scheduler: due post reminders: %v", err)
	}
	for _, p := range reminders {
		b.remindPostNonResponders(ctx, session, p)
	}
}

// pingPostAttendees posts the pre-run heads-up in a /coreteam post's discussion
// thread ~15 minutes before the run, mentioning everyone who RSVP'd "yes" or
// signed up to fill, then records the ping so it fires once. Best effort: send
// failures are logged but the ping is still marked done to avoid retry storms.
func (b *bot) pingPostAttendees(ctx context.Context, session *discordgo.Session, p models.Post) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	rsvps, err := b.discord.ListRSVPs(c, p.MessageID)
	cancel()
	if err != nil {
		log.Printf("scheduler: post ping list rsvps (%s): %v", p.MessageID, err)
	}
	c, cancel = context.WithTimeout(ctx, 10*time.Second)
	fills, err := b.discord.ListFills(c, p.MessageID)
	cancel()
	if err != nil {
		log.Printf("scheduler: post ping list fills (%s): %v", p.MessageID, err)
	}

	// Dedup attendee user ids: anyone coming (RSVP yes) or signed up to fill.
	seen := map[string]bool{}
	var mentions []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		mentions = append(mentions, "<@"+id+">")
	}
	for _, r := range rsvps {
		if r.Status == models.RSVPYes {
			add(r.DiscordUserID)
		}
	}
	for _, f := range fills {
		add(f.DiscordUserID)
	}

	content := "⏰ Starting in ~15 minutes!"
	if len(mentions) > 0 {
		content = "⏰ Starting in ~15 minutes — " + strings.Join(mentions, " ")
	}
	if _, err := session.ChannelMessageSend(p.ThreadID, content); err != nil {
		log.Printf("scheduler: post ping send (%s): %v", p.MessageID, err)
	}

	c, cancel = context.WithTimeout(ctx, 10*time.Second)
	if err := b.discord.MarkPostPinged(c, p.MessageID); err != nil {
		log.Printf("scheduler: post ping mark (%s): %v", p.MessageID, err)
	}
	cancel()
}

// remindPostNonResponders nudges the assigned roster members on a /coreteam post
// who haven't RSVP'd yet, ~48 hours before the run, then records the reminder so
// it fires exactly once. It mentions everyone whose stored handle resolves to a
// Discord user (so they're pinged) and names anyone else so it's still clear
// who's outstanding. The reminder lands in the post's discussion thread when
// there is one, else the post's channel. Best effort: it's marked done even on
// failure (so it can't retry-storm), and skipped — but still marked — when the
// roster can't be determined or everyone has already responded.
func (b *bot) remindPostNonResponders(ctx context.Context, session *discordgo.Session, p models.Post) {
	// Always mark reminded on the way out: a reminder is a one-shot nudge, so a
	// transient failure shouldn't cause it to fire repeatedly on later ticks.
	defer func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := b.discord.MarkPostReminded(c, p.MessageID); err != nil {
			log.Printf("scheduler: post reminder mark (%s): %v", p.MessageID, err)
		}
		cancel()
	}()

	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	teamID, err := b.discord.GetChannelTeam(c, p.ChannelID)
	cancel()
	if err != nil {
		// Channel unbound or rebound since posting — we can't know the roster.
		log.Printf("scheduler: post reminder team (%s): %v", p.MessageID, err)
		return
	}

	c, cancel = context.WithTimeout(ctx, 10*time.Second)
	team, _, _, _, err := b.loadTeamData(c, teamID)
	cancel()
	if err != nil {
		log.Printf("scheduler: post reminder load team (%s): %v", p.MessageID, err)
		return
	}

	c, cancel = context.WithTimeout(ctx, 10*time.Second)
	rsvps, err := b.discord.ListRSVPs(c, p.MessageID)
	cancel()
	if err != nil {
		log.Printf("scheduler: post reminder list rsvps (%s): %v", p.MessageID, err)
	}
	responded := rsvpMarks(team, rsvps)

	guildID := postGuildID(session, p.ChannelID)
	var mentions []string
	for _, pl := range team.Players {
		handle := strings.TrimSpace(pl.DiscordHandle)
		if handle == "" {
			continue // open slot: nobody assigned to remind
		}
		if responded[pl.Slot] != "" {
			continue // already RSVP'd (coming or not coming)
		}
		if id := b.resolveHandleID(session, guildID, handle); id != "" {
			mentions = append(mentions, "<@"+id+">")
			continue
		}
		// No resolvable Discord user (e.g. an unknown "@username"): name them so
		// it's still clear who we're waiting on, even without a live ping.
		name := strings.TrimSpace(pl.Name)
		if name == "" {
			name = strings.TrimPrefix(handle, "@")
		}
		mentions = append(mentions, name)
	}
	if len(mentions) == 0 {
		return // everyone assigned has responded (or the roster is all open)
	}

	target := p.ThreadID
	if target == "" {
		target = p.ChannelID
	}
	content := "📋 RSVP reminder — this run is about 48 hours away. Please RSVP if you haven't yet: " + strings.Join(mentions, " ")
	if url := messageURL(guildID, p.ChannelID, p.MessageID); url != "" {
		content += "\n" + url
	}
	if _, err := session.ChannelMessageSend(target, content); err != nil {
		log.Printf("scheduler: post reminder send (%s): %v", p.MessageID, err)
	}
}

// postGuildID returns the guild a tracked post's channel belongs to (checking
// the session's state cache first, then REST), or "" when it can't be resolved.
// Needed to mention "@username" roster handles, which requires a guild lookup.
func postGuildID(session *discordgo.Session, channelID string) string {
	ch, err := session.Channel(channelID)
	if err != nil || ch == nil {
		return ""
	}
	return ch.GuildID
}

// withTimeout runs a DB query with a bounded context derived from the loop ctx,
// so a slow query can't stall the scheduler past one interval.
func (b *bot) withTimeout(ctx context.Context, fn func(context.Context) ([]models.PremadeRun, error)) ([]models.PremadeRun, error) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return fn(c)
}

// premadeThreadIntro is posted in a run's discussion thread when the run is
// first posted, inviting players to chat about the trial. The signup ping is a
// separate message sent ~15 minutes before the start (see tagRunSignups).
const premadeThreadIntro = "🗣️ Discuss this trial here — strategy, builds, swaps, and questions. Everyone who signed up will be pinged here about 15 minutes before it starts."

// createRunThread starts the run's discussion thread off its post and posts the
// intro message, then records the thread id. It's called when the run is first
// posted so the thread exists immediately; the signup ping happens later via
// tagRunSignups. Returns the thread id, or "" if the thread couldn't be created.
func (b *bot) createRunThread(ctx context.Context, session *discordgo.Session, run *models.PremadeRun) string {
	name := strings.TrimSpace(run.Title)
	if name == "" {
		name = "Trial run"
	}
	thread, err := session.MessageThreadStartComplex(run.ChannelID, run.MessageID, &discordgo.ThreadStart{
		Name:                truncate(name, 100),
		AutoArchiveDuration: premadeThreadAutoArchive,
	})
	if err != nil {
		log.Printf("premade: create thread (run %d): %v", run.ID, err)
		return ""
	}
	if _, err := session.ChannelMessageSend(thread.ID, premadeThreadIntro); err != nil {
		log.Printf("premade: thread intro (run %d): %v", run.ID, err)
	}
	// Post the active roster's fight-positioning images so players can see where
	// to stand. Best-effort; failures are logged and skipped.
	b.postPositioningImages(ctx, session, thread.ID, run.TeamID, fmt.Sprintf("run %d", run.ID))
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := b.premade.SetRunThread(c, run.ID, thread.ID); err != nil {
		log.Printf("premade: set thread id (run %d): %v", run.ID, err)
	}
	cancel()
	run.ThreadID = thread.ID
	return thread.ID
}

// positioningImageExtensions maps an image MIME type to a file extension for the
// Discord upload filename.
var positioningImageExtensions = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// postPositioningImages uploads the team's active-roster positioning images into
// the given thread (with their captions), so players know where to stand during
// fights. Shared by the pre-made-run thread (createRunThread) and the /coreteam
// post overview thread (startPostThread); logLabel identifies the caller in log
// lines. Best-effort: a failure to load or send any image is logged and the rest
// continue.
func (b *bot) postPositioningImages(ctx context.Context, session *discordgo.Session, threadID string, teamID int64, logLabel string) {
	if b.rosterImages == nil {
		return
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	images, err := b.rosterImages.ListDataForActiveRoster(c, teamID)
	cancel()
	if err != nil {
		log.Printf("premade: load positioning images (%s): %v", logLabel, err)
		return
	}
	for _, img := range images {
		ext := positioningImageExtensions[img.ContentType]
		if ext == "" {
			ext = "png"
		}
		send := &discordgo.MessageSend{
			Files: []*discordgo.File{{
				Name:        fmt.Sprintf("positioning-%d.%s", img.ID, ext),
				ContentType: img.ContentType,
				Reader:      bytes.NewReader(img.Data),
			}},
		}
		if caption := strings.TrimSpace(img.Caption); caption != "" {
			send.Content = "📍 " + caption
		}
		if _, err := session.ChannelMessageSendComplex(threadID, send); err != nil {
			log.Printf("premade: post positioning image %d (%s): %v", img.ID, logLabel, err)
		}
	}
}

// tagRunSignups pings everyone who signed up, in the run's discussion thread,
// ~15 minutes before the scheduled start, and records that the ping ran. The
// thread is normally created up front when the run is posted (createRunThread);
// if it's missing here (an older run, or post-time creation failed) we create it
// now as a fallback so the ping always lands somewhere.
func (b *bot) tagRunSignups(ctx context.Context, session *discordgo.Session, run models.PremadeRun) {
	threadID := run.ThreadID
	if threadID == "" {
		threadID = b.createRunThread(ctx, session, &run)
	}
	if threadID == "" {
		// No thread available (e.g. the post was deleted); don't retry forever.
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := b.premade.MarkThreadStarted(c, run.ID, ""); err != nil {
			log.Printf("scheduler: mark signups tagged (run %d): %v", run.ID, err)
		}
		cancel()
		return
	}

	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	signups, err := b.premade.ListSignups(c, run.ID)
	cancel()
	if err != nil {
		log.Printf("scheduler: list signups (run %d): %v", run.ID, err)
	}

	c, cancel = context.WithTimeout(ctx, 10*time.Second)
	tentative, err := b.premade.ListTentative(c, run.ID)
	cancel()
	if err != nil {
		log.Printf("scheduler: list tentative (run %d): %v", run.ID, err)
	}

	content := "Starting soon — no one has signed up yet."
	if len(signups) > 0 {
		mentions := make([]string, 0, len(signups))
		for _, sg := range signups {
			mentions = append(mentions, "<@"+sg.DiscordUserID+">")
		}
		content = "Starting soon — " + strings.Join(mentions, " ")
	}
	// Tentative ("maybe") players are pinged too, flagged as such.
	if len(tentative) > 0 {
		maybes := make([]string, 0, len(tentative))
		for _, t := range tentative {
			maybes = append(maybes, "<@"+t.DiscordUserID+">")
		}
		content += "\nTentative (maybe): " + strings.Join(maybes, " ")
	}
	if _, err := session.ChannelMessageSend(threadID, content); err != nil {
		log.Printf("scheduler: thread tag (run %d): %v", run.ID, err)
	}

	c, cancel = context.WithTimeout(ctx, 10*time.Second)
	if err := b.premade.MarkThreadStarted(c, run.ID, threadID); err != nil {
		log.Printf("scheduler: mark signups tagged (run %d): %v", run.ID, err)
	}
	cancel()
}

// threadCleanupID returns the channel id of the post's thread to delete during
// cleanup, or "" when the run never had a post to anchor a thread on. A thread
// started from a message shares that message's id, and every run gets its
// discussion thread created up front when it's posted (createRunThread), so we
// fall back to the message id whenever thread_id wasn't recorded — e.g. the bot
// restarted between creating the thread and persisting its id. Deleting an id
// that turns out not to be a thread is a tolerated 404 (see cleanupRun), so the
// fallback is safe even for the rare run whose thread creation failed outright.
func threadCleanupID(run *models.PremadeRun) string {
	if run.ThreadID != "" {
		return run.ThreadID
	}
	return run.MessageID
}

// discordStatus returns the HTTP status code carried by a discordgo REST error,
// or 0 when err is nil or isn't a REST error.
func discordStatus(err error) int {
	var rerr *discordgo.RESTError
	if errors.As(err, &rerr) && rerr.Response != nil {
		return rerr.Response.StatusCode
	}
	return 0
}

// cleanupRun deletes the run's thread and post. It returns a non-nil error only
// when a thread that should exist could not be deleted for a reason other than
// "already gone" — almost always a 403 because the bot lacks the Manage Threads
// permission (deleting a thread requires it; deleting the parent message does
// NOT remove the thread, and the thread shares the starter message's id). A
// missing thread/message (404) is tolerated and reported as success.
//
// cleanupRun does NOT record the run as cleaned up — the caller decides whether
// to mark it via markRunCleanedUp. The scheduler only marks runs whose thread
// was actually removed, so an un-deletable thread keeps the run "due" and is
// retried (self-healing once Manage Threads is granted). User-initiated deletes
// mark the run regardless and warn that the thread is now orphaned.
func (b *bot) cleanupRun(ctx context.Context, session *discordgo.Session, run models.PremadeRun) error {
	var threadErr error
	if threadID := threadCleanupID(&run); threadID != "" {
		if _, err := session.ChannelDelete(threadID); err != nil {
			// A 404 just means there's no such thread (already deleted, or the id
			// we fell back to was a plain message with no thread) — success for
			// our purposes. Anything else is a real failure worth surfacing.
			if discordStatus(err) != http.StatusNotFound {
				threadErr = err
			}
			log.Printf("scheduler: delete thread (run %d): %v", run.ID, err)
		}
	}
	if run.MessageID != "" {
		if err := session.ChannelMessageDelete(run.ChannelID, run.MessageID); err != nil {
			log.Printf("scheduler: delete post (run %d): %v", run.ID, err)
		}
	}
	return threadErr
}

// markRunCleanedUp records that a run's post and thread have been cleaned up so
// the scheduler stops revisiting it. Best effort: a failure is logged, leaving
// the run "due" for a later retry.
func (b *bot) markRunCleanedUp(ctx context.Context, runID int64) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := b.premade.MarkCleanedUp(c, runID); err != nil {
		log.Printf("scheduler: mark cleaned up (run %d): %v", runID, err)
	}
	cancel()
}

// recordCleanupFailure handles a failed cleanup attempt: it schedules the next
// retry with exponential backoff, or — once the attempt cap is reached — marks
// the run cleanup-failed so a permanently un-deletable thread (e.g. the bot is
// never granted Manage Threads) stops being retried on every tick.
func (b *bot) recordCleanupFailure(ctx context.Context, run models.PremadeRun) {
	attempts := run.CleanupAttempts + 1
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if attempts >= cleanupMaxAttempts {
		log.Printf("scheduler: giving up on cleanup (run %d) after %d attempts", run.ID, attempts)
		if err := b.premade.MarkCleanupFailed(c, run.ID, attempts); err != nil {
			log.Printf("scheduler: mark cleanup failed (run %d): %v", run.ID, err)
		}
		return
	}
	nextAt := time.Now().UTC().Add(cleanupBackoff(attempts))
	if err := b.premade.RecordCleanupFailure(c, run.ID, attempts, nextAt); err != nil {
		log.Printf("scheduler: record cleanup failure (run %d): %v", run.ID, err)
	}
}
