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
// is the bot's only time-based worker: it creates the run thread ~15 minutes
// before the scheduled start (tagging everyone who signed up) and deletes the
// post + thread ~2 hours after. Both actions are tracked in the DB so they fire
// exactly once and are caught up after a restart.
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

	threads, err := b.withTimeout(ctx, func(c context.Context) ([]models.PremadeRun, error) {
		return b.premade.DueThreadRuns(c, now)
	})
	if err != nil {
		log.Printf("scheduler: due threads: %v", err)
	}
	for _, run := range threads {
		b.startRunThread(ctx, session, run)
	}
}

// withTimeout runs a DB query with a bounded context derived from the loop ctx,
// so a slow query can't stall the scheduler past one interval.
func (b *bot) withTimeout(ctx context.Context, fn func(context.Context) ([]models.PremadeRun, error)) ([]models.PremadeRun, error) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return fn(c)
}

// startRunThread creates the run's thread, tags everyone who signed up, and
// records that it ran.
func (b *bot) startRunThread(ctx context.Context, session *discordgo.Session, run models.PremadeRun) {
	name := strings.TrimSpace(run.Title)
	if name == "" {
		name = "Trial run"
	}
	thread, err := session.MessageThreadStartComplex(run.ChannelID, run.MessageID, &discordgo.ThreadStart{
		Name:                truncate(name, 100),
		AutoArchiveDuration: premadeThreadAutoArchive,
	})
	if err != nil {
		// Most likely the post was deleted manually; don't retry forever.
		log.Printf("scheduler: start thread (run %d): %v", run.ID, err)
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := b.premade.MarkThreadStarted(c, run.ID, ""); err != nil {
			log.Printf("scheduler: mark thread started (run %d): %v", run.ID, err)
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

	content := "Starting soon!"
	if len(signups) > 0 {
		mentions := make([]string, 0, len(signups))
		for _, sg := range signups {
			mentions = append(mentions, "<@"+sg.DiscordUserID+">")
		}
		content = "Starting soon — " + strings.Join(mentions, " ")
	} else {
		content = "Starting soon — no one has signed up yet."
	}
	if _, err := session.ChannelMessageSend(thread.ID, content); err != nil {
		log.Printf("scheduler: thread tag (run %d): %v", run.ID, err)
	}

	c, cancel = context.WithTimeout(ctx, 10*time.Second)
	if err := b.premade.MarkThreadStarted(c, run.ID, thread.ID); err != nil {
		log.Printf("scheduler: mark thread started (run %d): %v", run.ID, err)
	}
	cancel()
}

// cleanupRun deletes the run's thread and post, then records that cleanup ran.
// Missing-message/thread errors are tolerated (manual deletion) so the run is
// still marked done.
func (b *bot) cleanupRun(ctx context.Context, session *discordgo.Session, run models.PremadeRun) {
	if run.ThreadID != "" {
		if _, err := session.ChannelDelete(run.ThreadID); err != nil {
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
