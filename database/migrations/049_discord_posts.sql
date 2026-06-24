-- Migration 049: posted trial overviews (for the pre-run ping)
--
-- Idempotent. Tracks each /coreteam post overview message so the bot's
-- scheduler can ping attendees in the post's discussion thread ~15 minutes
-- before the run starts, exactly once. Keyed by message_id (mirroring
-- discord_rsvps / discord_post_fills) so re-posting starts fresh.
--
-- run_at is the absolute time of the post's next run, computed from the team's
-- recurring schedule at post time (the same instant the post's dynamic
-- timestamp shows). NULL when the team has no concrete schedule — such posts are
-- never pinged. pinged_at marks the pre-run ping as done so it fires once and is
-- caught up after a restart. thread_id is the discussion thread the ping lands
-- in; an empty thread_id (thread creation failed) is skipped.

CREATE TABLE IF NOT EXISTS discord_posts (
    -- The posted overview message.
    message_id  TEXT        NOT NULL PRIMARY KEY,
    -- The channel the message is in (kept for housekeeping / scoping).
    channel_id  TEXT        NOT NULL,
    -- The discussion thread opened off the post; '' until/unless it's created.
    thread_id   TEXT        NOT NULL DEFAULT '',
    -- Absolute next-run time; NULL when the team has no concrete schedule.
    run_at      TIMESTAMPTZ,
    -- Set when the pre-run ping has been sent (once-only, restart-safe).
    pinged_at   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Drives the scheduler's "due pre-run ping" scan: unpinged posts with a run time.
CREATE INDEX IF NOT EXISTS discord_posts_due_idx
    ON discord_posts (run_at) WHERE pinged_at IS NULL AND run_at IS NOT NULL;
