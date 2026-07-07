-- Migration 053: RSVP reminder for posted trial overviews
--
-- Idempotent. Adds a reminder timestamp to discord_posts so the bot's scheduler
-- can ping assigned roster members who haven't RSVP'd ~48 hours before the run,
-- exactly once. This is separate from pinged_at (the ~15-minute pre-run heads-up
-- for everyone who's coming): the reminder nudges the people we're still waiting
-- to hear from, well ahead of the run.
--
-- reminded_at marks the RSVP reminder as sent so it fires once and is caught up
-- after a restart. Posts with no concrete run time (run_at IS NULL) are never
-- reminded, mirroring the pre-run ping.

ALTER TABLE discord_posts
    ADD COLUMN IF NOT EXISTS reminded_at TIMESTAMPTZ;

-- Drives the scheduler's "due RSVP reminder" scan: un-reminded posts with a run
-- time.
CREATE INDEX IF NOT EXISTS discord_posts_reminder_due_idx
    ON discord_posts (run_at) WHERE reminded_at IS NULL AND run_at IS NOT NULL;
