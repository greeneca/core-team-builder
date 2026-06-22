package main

import (
	"context"
	"log"
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
		b.cleanupRun(ctx, session, run)
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
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := b.premade.SetRunThread(c, run.ID, thread.ID); err != nil {
		log.Printf("premade: set thread id (run %d): %v", run.ID, err)
	}
	cancel()
	run.ThreadID = thread.ID
	return thread.ID
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

	content := "Starting soon — no one has signed up yet."
	if len(signups) > 0 {
		mentions := make([]string, 0, len(signups))
		for _, sg := range signups {
			mentions = append(mentions, "<@"+sg.DiscordUserID+">")
		}
		content = "Starting soon — " + strings.Join(mentions, " ")
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
// cleanup, or "" when no thread should exist. A thread started from a message
// shares that message's id, so when thread_id wasn't recorded we fall back to
// the message id. This covers threads that exist out-of-band from our stored
// id — e.g. the bot restarted between creating the thread and persisting its id
// (the retry then fails and blanks thread_id), or the channel auto-creates
// threads so our explicit start failed. The fallback is gated on a thread start
// having been attempted (thread_started_at set) so runs that never had a thread
// don't get a spurious delete.
func threadCleanupID(run *models.PremadeRun) string {
	if run.ThreadID != "" {
		return run.ThreadID
	}
	if run.ThreadStartedAt != nil {
		return run.MessageID
	}
	return ""
}

// cleanupRun deletes the run's thread and post, then records that cleanup ran.
// Missing-message/thread errors are tolerated (manual deletion) so the run is
// still marked done.
func (b *bot) cleanupRun(ctx context.Context, session *discordgo.Session, run models.PremadeRun) {
	if threadID := threadCleanupID(&run); threadID != "" {
		if _, err := session.ChannelDelete(threadID); err != nil {
			log.Printf("scheduler: delete thread (run %d): %v", run.ID, err)
		}
	}
	if run.MessageID != "" {
		if err := session.ChannelMessageDelete(run.ChannelID, run.MessageID); err != nil {
			log.Printf("scheduler: delete post (run %d): %v", run.ID, err)
		}
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := b.premade.MarkCleanedUp(c, run.ID); err != nil {
		log.Printf("scheduler: mark cleaned up (run %d): %v", run.ID, err)
	}
	cancel()
}
