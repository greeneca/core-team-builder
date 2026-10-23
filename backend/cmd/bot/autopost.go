package main

import (
	"context"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/discordfmt"
	"github.com/core-team-builder/backend/internal/models"
)

// autoPostRetryDelay is how long the scheduler waits before trying a failed auto
// post again (typically a missing Send Messages permission), so a broken channel
// is not hit on every 60-second tick.
const autoPostRetryDelay = 5 * time.Minute

// autoPostFooter labels a post the bot made on its own.
const autoPostFooter = postedByPrefix + "auto post"

// autoPostDue reports whether a team's auto post should go out now: the next run
// is known, we are inside the lead window before it (but before it starts), and
// that run has not been posted already. last is the run instant of the previous
// auto post; anything at or after the upcoming run means it is already done.
func autoPostDue(now time.Time, runUnix int64, lead time.Duration, last *time.Time) bool {
	if runUnix <= 0 {
		return false
	}
	run := time.Unix(runUnix, 0)
	if last != nil && !run.After(*last) {
		return false
	}
	return !now.Before(run.Add(-lead)) && now.Before(run)
}

// autoPostTick posts every team whose auto post is due. Each team is handled
// independently so one failing channel never blocks the others.
func (b *bot) autoPostTick(ctx context.Context, session *discordgo.Session, now time.Time) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	teams, err := b.autoPost.ListAutoPostEnabled(c)
	cancel()
	if err != nil {
		log.Printf("scheduler: list auto posts: %v", err)
		return
	}
	for _, t := range teams {
		if until, ok := b.autoPostRetryAt[t.TeamID]; ok && now.Before(until) {
			continue
		}
		b.autoPostTeam(ctx, session, now, t)
	}
}

func (b *bot) autoPostTeam(ctx context.Context, session *discordgo.Session, now time.Time, due models.AutoPostDue) {
	team, _, primary, gr, err := b.loadTeamData(ctx, due.TeamID)
	if err != nil {
		log.Printf("scheduler: auto post load team %d: %v", due.TeamID, err)
		return
	}
	runUnix, ok := discordfmt.NextRunUnix(team.ScheduleDays, team.ScheduleTime)
	if !ok || !autoPostDue(now, runUnix, time.Duration(due.LeadMinutes)*time.Minute, due.LastPostedAt) {
		return
	}

	names := b.resolveRosterNames(session, due.GuildID, team)
	embed := buildPostEmbed(team, primary, gr, nil, nil, names, autoPostFooter, runUnix)
	msg, err := session.ChannelMessageSendComplex(due.ChannelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: postComponents(team, nil, nil, postLocked(runUnix)),
	})
	if err != nil {
		log.Printf("scheduler: auto post team %d: %v", due.TeamID, err)
		if b.autoPostRetryAt == nil {
			b.autoPostRetryAt = map[int64]time.Time{}
		}
		b.autoPostRetryAt[due.TeamID] = now.Add(autoPostRetryDelay)
		return
	}
	delete(b.autoPostRetryAt, due.TeamID)

	// Mark the run as posted before opening the thread, so a failure after this
	// point can never cause a second post for the same run.
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := b.autoPost.MarkAutoPosted(c, due.TeamID, time.Unix(runUnix, 0).UTC()); err != nil {
		log.Printf("scheduler: auto post mark team %d: %v", due.TeamID, err)
	}
	cancel()
	b.trackPostThread(ctx, session, due.ChannelID, msg.ID, team, runUnix)
}

// autoDeleteTick deletes posts (and their discussion threads) whose team has
// auto delete on and whose delay after the run has passed. A post is marked done
// even when Discord refuses (usually a missing Manage Messages / Manage Threads
// permission), so it is not retried every tick; the failure is logged.
func (b *bot) autoDeleteTick(ctx context.Context, session *discordgo.Session, now time.Time) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	posts, err := b.autoPost.DueAutoDeletes(c, now)
	cancel()
	if err != nil {
		log.Printf("scheduler: due auto deletes: %v", err)
		return
	}
	for _, p := range posts {
		// A thread started from a message shares its id, so fall back to the
		// message id when the thread id was never recorded; a 404 is tolerated.
		threadID := p.ThreadID
		if threadID == "" {
			threadID = p.MessageID
		}
		if _, err := session.ChannelDelete(threadID); err != nil && discordStatus(err) != 404 {
			log.Printf("scheduler: auto delete thread (%s): %v", p.MessageID, err)
		}
		if err := session.ChannelMessageDelete(p.ChannelID, p.MessageID); err != nil && discordStatus(err) != 404 {
			log.Printf("scheduler: auto delete post (%s): %v", p.MessageID, err)
		}
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := b.autoPost.MarkPostAutoDeleted(c, p.MessageID); err != nil {
			log.Printf("scheduler: auto delete mark (%s): %v", p.MessageID, err)
		}
		// Stop any ping/reminder still pending on the now-removed post.
		if err := b.discord.MarkPostGone(c, p.MessageID); err != nil {
			log.Printf("scheduler: auto delete mark gone (%s): %v", p.MessageID, err)
		}
		cancel()
	}
}

// backfillChannelNames records the display name of bound channels that have none
// yet (bindings made before names were stored). Best effort; run once at start.
func (b *bot) backfillChannelNames(ctx context.Context, session *discordgo.Session) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	ids, err := b.autoPost.UnnamedChannels(c)
	cancel()
	if err != nil {
		log.Printf("scheduler: unnamed channels: %v", err)
		return
	}
	for _, id := range ids {
		ch, err := session.Channel(id)
		if err != nil || ch.Name == "" {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := b.autoPost.SetChannelName(c, id, ch.Name); err != nil {
			log.Printf("scheduler: set channel name (%s): %v", id, err)
		}
		cancel()
	}
}

// recordChannelName stores the name of the channel a command ran in, so the web
// app's channel dropdown can show it. Best effort.
func (b *bot) recordChannelName(ctx context.Context, session *discordgo.Session, channelID string) {
	ch, err := session.Channel(channelID)
	if err != nil || ch.Name == "" {
		return
	}
	if err := b.autoPost.SetChannelName(ctx, channelID, ch.Name); err != nil {
		log.Printf("record channel name (%s): %v", channelID, err)
	}
}

// refreshRetryDelay is how long a failed post refresh waits before retrying.
const refreshRetryDelay = 5 * time.Minute

// refreshPostsTick re-renders upcoming posts whose team changed its active
// roster, so the post shows the lineup that will actually run. The refresh edits
// the message in place (keeping RSVPs, fills, and the original footer). A post
// that was deleted on Discord is marked gone instead of retried; any other
// failure is retried after refreshRetryDelay.
func (b *bot) refreshPostsTick(ctx context.Context, session *discordgo.Session, now time.Time) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	posts, err := b.discord.DuePostRefreshes(c, now)
	cancel()
	if err != nil {
		log.Printf("scheduler: due post refreshes: %v", err)
		return
	}
	for _, p := range posts {
		if until, ok := b.refreshRetryAt[p.MessageID]; ok && now.Before(until) {
			continue
		}
		rc, rcancel := context.WithTimeout(ctx, 15*time.Second)
		err := b.refreshPostMessage(rc, session, p.GuildID, p.ChannelID, p.MessageID)
		rcancel()
		switch {
		case err == nil:
			delete(b.refreshRetryAt, p.MessageID)
		case discordStatus(err) == 404:
			log.Printf("scheduler: post %s removed; skipping roster refresh", p.MessageID)
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			if gerr := b.discord.MarkPostGone(c, p.MessageID); gerr != nil {
				log.Printf("scheduler: refresh mark gone (%s): %v", p.MessageID, gerr)
			}
			cancel()
			delete(b.refreshRetryAt, p.MessageID)
		default:
			log.Printf("scheduler: refresh post %s: %v", p.MessageID, err)
			if b.refreshRetryAt == nil {
				b.refreshRetryAt = map[string]time.Time{}
			}
			b.refreshRetryAt[p.MessageID] = now.Add(refreshRetryDelay)
			continue
		}
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := b.discord.ClearPostRefresh(c, p.MessageID, p.RequestedAt); err != nil {
			log.Printf("scheduler: clear refresh (%s): %v", p.MessageID, err)
		}
		cancel()
	}
}
